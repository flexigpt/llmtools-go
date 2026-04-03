package executil

import (
	"bytes"
	"context"
	"errors"
	"math"
	"os/exec"
	"sync"
	"time"

	"github.com/flexigpt/llmtools-go/internal/logutil"
)

func RunOneShellCommand(
	parent context.Context,
	sel SelectedShell,
	command string,
	workdir string,
	env []string,
	timeout time.Duration,
	maxOut int64,
) (ShellCommandExecResult, error) {
	ctx := parent
	var cancel context.CancelFunc
	if timeout > 0 {
		ctx, cancel = context.WithTimeout(parent, timeout)
		defer cancel()
	}

	shellArgs := deriveExecArgs(sel, command)

	// Determine execution strategy: host-spawn (Flatpak) or direct.
	execArgs := shellArgs
	execEnv := env
	useHostSpawn := HostSpawnAvailable(ctx)
	if useHostSpawn {
		execArgs = buildHostSpawnArgs(shellArgs, filterEnvForHostSpawn(env))
		execEnv = nil // env is forwarded via --env= flags to the host command
	}

	cmd := exec.CommandContext(ctx, execArgs[0], execArgs[1:]...) //nolint:gosec // Exec shell command.

	cmd.Dir = workdir
	cmd.Env = execEnv

	configureProcessGroup(cmd)

	stdoutW := newCappedWriter(maxOut)
	stderrW := newCappedWriter(maxOut)
	cmd.Stdout = stdoutW
	cmd.Stderr = stderrW

	start := time.Now()
	runErr := cmd.Start()
	// Fallback: if host-spawn itself failed to start, retry direct execution.
	if runErr != nil && useHostSpawn {
		logutil.WarnContext(ctx, "executil: host-spawn start failed; retrying with direct execution", "err", runErr)
		cmd = exec.CommandContext(ctx, shellArgs[0], shellArgs[1:]...) //nolint:gosec // Fallback direct.
		cmd.Dir = workdir
		cmd.Env = env
		configureProcessGroup(cmd)
		stdoutW = newCappedWriter(maxOut)
		stderrW = newCappedWriter(maxOut)
		cmd.Stdout = stdoutW
		cmd.Stderr = stderrW
		start = time.Now()
		runErr = cmd.Start()
	}

	if runErr != nil {
		return ShellCommandExecResult{}, runErr
	}

	// Wait in a goroutine so we can react to ctx cancellation/timeouts.
	waitCh := make(chan error, 1)
	go func() {
		waitCh <- cmd.Wait()
	}()

	killedByCtx := false
	var waitErr error

	select {
	case waitErr = <-waitCh:
		// Process completed before context cancellation/timeout.
	case <-ctx.Done():
		// If process already finished, do not kill.
		select {
		case waitErr = <-waitCh:
			// Finished.
		default:
			killedByCtx = true
			killProcessGroup(cmd)
			waitErr = <-waitCh
		}
	}
	dur := time.Since(start)

	// Only mark timed out if we actually killed because ctx fired due to deadline.
	timedOut := killedByCtx && errors.Is(ctx.Err(), context.DeadlineExceeded)

	exitCode := exitCodeFromWait(waitErr, timedOut)

	return ShellCommandExecResult{
		Command:   command,
		WorkDir:   workdir,
		Shell:     sel.Name,
		ShellPath: sel.Path,

		ExitCode:   exitCode,
		TimedOut:   timedOut,
		DurationMS: dur.Milliseconds(),

		Stdout: safeUTF8(stdoutW.Bytes()),
		Stderr: safeUTF8(stderrW.Bytes()),

		StdoutTruncated: stdoutW.Truncated(),
		StderrTruncated: stderrW.Truncated(),
	}, nil
}

func exitCodeFromWait(waitErr error, timedOut bool) int {
	if timedOut && waitErr != nil {
		return 124 // conventional timeout exit code
	}
	if waitErr == nil {
		return 0
	}
	var ee *exec.ExitError
	if errors.As(waitErr, &ee) {
		return exitCodeFromProcessState(ee.ProcessState)
	}

	return 127 // Spawn/other failure
}

type cappedWriter struct {
	mu        sync.Mutex
	capBytes  int
	buf       []byte // fixed size capBytes
	start     int    // ring start
	n         int    // number of valid bytes in ring
	total     int64
	truncated bool
}

func newCappedWriter(capBytes int64) *cappedWriter {
	if capBytes < MinOutputBytes {
		capBytes = MinOutputBytes
	}
	if capBytes > HardMaxOutputBytes {
		capBytes = HardMaxOutputBytes
	}

	// Avoid int overflow / huge allocations even if misconfigured.
	if capBytes > int64(math.MaxInt) {
		capBytes = int64(math.MaxInt)
	}
	cb := int(capBytes)
	return &cappedWriter{
		capBytes: cb,
		buf:      make([]byte, cb),
	}
}

func (w *cappedWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()

	w.total += int64(len(p))

	if len(p) == 0 || w.capBytes <= 0 {
		return len(p), nil
	}

	// Tail-capture semantics:
	// Keep the last capBytes bytes written across all writes.
	if len(p) >= w.capBytes {
		copy(w.buf, p[len(p)-w.capBytes:])
		w.start = 0
		w.n = w.capBytes
		w.truncated = true
		return len(p), nil
	}

	// If we would exceed capacity, drop from the front (advance start).
	overflow := (w.n + len(p)) - w.capBytes
	if overflow > 0 {
		w.start = (w.start + overflow) % w.capBytes
		w.n -= overflow
		w.truncated = true
	}

	// Append at end position.
	end := (w.start + w.n) % w.capBytes
	// Copy with wrap.
	first := min(len(p), w.capBytes-end)
	copy(w.buf[end:end+first], p[:first])
	if first < len(p) {
		copy(w.buf[0:len(p)-first], p[first:])
	}
	w.n += len(p)
	return len(p), nil
}

func (w *cappedWriter) Bytes() []byte {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.n == 0 {
		return nil
	}
	out := make([]byte, w.n)
	if w.start+w.n <= w.capBytes {
		copy(out, w.buf[w.start:w.start+w.n])
		return out
	}
	// Wrapped.
	n1 := w.capBytes - w.start
	copy(out, w.buf[w.start:])
	copy(out[n1:], w.buf[:w.n-n1])
	return out
}

func (w *cappedWriter) TotalBytes() int64 {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.total
}

func (w *cappedWriter) Truncated() bool {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.truncated
}

func safeUTF8(b []byte) string {
	// Replace invalid UTF-8 sequences; avoids breaking JSON / UIs.
	return string(bytes.ToValidUTF8(b, []byte("\uFFFD")))
}

func deriveExecArgs(sel SelectedShell, command string) []string {
	switch sel.Name {
	case ShellNameBash, ShellNameZsh, ShellNameSh, ShellNameDash, ShellNameKsh, ShellNameFish:
		return []string{sel.Path, "-c", command}

	case ShellNamePowershell, ShellNamePwsh:
		// Always deterministic by default: no profile; non-interactive to avoid prompts.
		args := []string{sel.Path, "-NoLogo", "-NonInteractive", "-NoProfile", "-Command", command}
		return args

	case ShellNameCmd:
		// Options: /d disables AutoRun from registry (safer); /s handles quotes; /c runs then exits.
		return []string{sel.Path, "/d", "/s", "/c", command}

	default:

		return []string{sel.Path, "-c", command}
	}
}

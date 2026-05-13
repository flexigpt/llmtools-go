package executil

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os/exec"
	"runtime"
	"sync"
	"time"

	"github.com/flexigpt/llmtools-go/internal/logutil"
	"github.com/flexigpt/llmtools-go/internal/toolutil"
)

const managedCommandWaitDelay = 5 * time.Second

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
		execArgs = buildHostSpawnArgs(shellArgs, filterEnvForHostSpawn(env), workdir)
		execEnv = nil // env is forwarded via --env= flags to the host command
	}

	stdoutW := newCappedWriter(maxOut)
	stderrW := newCappedWriter(maxOut)
	cmd, state, prepErr := prepareManagedCommand(ctx, execArgs, workdir, execEnv, stdoutW, stderrW)
	if prepErr != nil {
		return ShellCommandExecResult{}, prepErr
	}

	start := time.Now()
	runErr := cmd.Start()

	// Fallback: if host-spawn itself failed to start, retry direct execution.
	if runErr != nil && useHostSpawn {
		logutil.WarnContext(ctx, "executil: host-spawn start failed; retrying with direct execution", "err", runErr)

		stdoutW = newCappedWriter(maxOut)
		stderrW = newCappedWriter(maxOut)

		cmd, state, prepErr = prepareManagedCommand(ctx, shellArgs, workdir, env, stdoutW, stderrW)
		if prepErr != nil {
			return ShellCommandExecResult{}, prepErr
		}

		cmd.Stdout = stdoutW
		cmd.Stderr = stderrW
		start = time.Now()
		runErr = cmd.Start()
	}

	if runErr != nil {
		return ShellCommandExecResult{}, runErr
	}

	state.afterStart(ctx, cmd)
	defer state.close()

	waitErr := cmd.Wait()
	dur := time.Since(start)

	timedOut := state.wasCancelled() && errors.Is(ctx.Err(), context.DeadlineExceeded)

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

// RunManagedCombinedOutput is like exec.Cmd.CombinedOutput, but uses the same
// process-tree isolation, hidden-window behavior, cancellation, and WaitDelay
// semantics as RunOneShellCommand.
func RunManagedCombinedOutput(ctx context.Context, args []string, workdir string, env []string) ([]byte, error) {
	var out lockedBuffer
	cmd, state, err := prepareManagedCommand(ctx, args, workdir, env, &out, &out)
	if err != nil {
		return nil, err
	}

	if err := cmd.Start(); err != nil {
		return nil, err
	}

	state.afterStart(ctx, cmd)
	defer state.close()

	err = cmd.Wait()
	return out.Bytes(), err
}

type managedCommandState struct {
	mu        sync.Mutex
	pg        *processGroupHandle
	cancelled bool
}

func prepareManagedCommand(
	ctx context.Context,
	args []string,
	workdir string,
	env []string,
	stdout io.Writer,
	stderr io.Writer,
) (*exec.Cmd, *managedCommandState, error) {
	if len(args) == 0 {
		return nil, nil, errors.New("empty command args")
	}

	//nolint:gosec // The caller intentionally constructs the shell/process invocation.
	cmd := exec.CommandContext(ctx, args[0], args[1:]...)
	cmd.Dir = workdir
	cmd.Env = env
	cmd.Stdout = stdout
	cmd.Stderr = stderr

	configureProcessGroup(cmd)

	state := &managedCommandState{}

	// Important: override CommandContext's default Cancel.
	// The default kills only the immediate shell process. On Windows this can
	// orphan grandchildren such as go.exe/node.exe, and those grandchildren can
	// keep inherited stdout/stderr pipe handles open, making Wait hang.
	cmd.Cancel = func() error {
		return state.cancel(cmd)
	}

	// Bound Wait if descendants keep inherited pipe handles open.
	cmd.WaitDelay = managedCommandWaitDelay

	return cmd, state, nil
}

func (s *managedCommandState) afterStart(ctx context.Context, cmd *exec.Cmd) {
	pg, err := afterProcessStart(cmd)
	if err != nil {
		logutil.WarnContext(
			ctx,
			"executil: process-tree isolation setup failed; using fallback cancellation",
			"err",
			err,
		)
	}
	if pg == nil {
		return
	}

	s.mu.Lock()
	if s.pg != nil {
		s.mu.Unlock()
		pg.close()
		return
	}
	s.pg = pg
	alreadyCancelled := s.cancelled
	s.mu.Unlock()

	if alreadyCancelled {
		_ = s.cancel(cmd)
	}
}

func (s *managedCommandState) cancel(cmd *exec.Cmd) error {
	s.mu.Lock()
	s.cancelled = true
	pg := s.pg
	if pg != nil {
		err := pg.terminate(cmd)
		s.mu.Unlock()
		return err
	}
	s.mu.Unlock()

	killProcessGroup(cmd)
	return nil
}

func (s *managedCommandState) close() {
	s.mu.Lock()
	pg := s.pg
	s.pg = nil
	s.mu.Unlock()

	if pg != nil {
		pg.close()
	}
}

func (s *managedCommandState) wasCancelled() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.cancelled
}

func exitCodeFromWait(waitErr error, timedOut bool) int {
	if timedOut {
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
		args := []string{sel.Path, "-NoLogo", "-NonInteractive", "-NoProfile"}
		if runtime.GOOS == toolutil.GOOSWindows {
			args = append(args, "-ExecutionPolicy", "Bypass")
		}
		args = append(args, "-Command", command)
		return args

	case ShellNameCmd:
		// Options: /d disables AutoRun from registry (safer); /s handles quotes; /c runs then exits.
		return []string{sel.Path, "/d", "/s", "/v:off", "/c", command}

	default:
		return []string{sel.Path, "-c", command}
	}
}

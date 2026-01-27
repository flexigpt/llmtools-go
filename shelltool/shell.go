package shelltool

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/flexigpt/llmtools-go/internal/toolutil"
	"github.com/flexigpt/llmtools-go/spec"
)

const shellCommandFuncID spec.FuncID = "github.com/flexigpt/llmtools-go/shelltool/shell.ShellCommand"

const GOOSWindows = "windows"

var shellToolSpec = spec.Tool{
	SchemaVersion: spec.SchemaVersion,
	ID:            "019bfeda-33f2-7315-9007-de55935d2302",
	Slug:          "shell",
	Version:       "v1.0.0",
	DisplayName:   "Shell",
	Description:   "Execute local shell commands (cross-platform) with timeouts, output caps, and session-like persistence for workdir/env.",
	Tags:          []string{"shell", "exec", "cli"},

	ArgSchema: spec.JSONSchema(`{
		"$schema": "http://json-schema.org/draft-07/schema#",
		"type": "object",
		"properties": {
			"commands": {
				"type": "array",
				"items": { "type": "string" },
				"description": "List of commands to execute sequentially. Prefer setting workdir rather than using 'cd'."
			},
			"workdir": {
				"type": "string",
				"description": "Working directory to execute in. If omitted and sessionID is used, uses the session workdir; otherwise uses current process directory."
			},
			"env": {
				"type": "object",
				"additionalProperties": { "type": "string" },
				"description": "Environment variable overrides (merged into the process env)."
			},
			"shell": {
				"type": "string",
				"enum": ["auto", "bash", "zsh", "sh", "powershell", "cmd"],
				"default": "auto",
				"description": "Which shell to run. 'auto' chooses a safe default per OS."
			},
			"continueOnError": {
				"type": "boolean",
				"default": false,
				"description": "If true, continue executing subsequent commands after a non-zero exit code or timeout."
			},
			"login": {
				"type": "boolean",
				"default": false,
				"description": "If true, run with login/profile semantics (e.g., bash -lc). Default false for determinism & safety."
			},
			"timeoutMS": {
				"type": "integer",
				"minimum": 1,
				"maximum": 600000,
				"default": 60000,
				"description": "Timeout per command in milliseconds."
			},
			"maxOutputLength": {
				"type": "integer",
				"minimum": 1024,
				"maximum": 4194304,
				"default": 262144,
				"description": "Max bytes captured for stdout/stderr each. Output beyond this is discarded (but the process continues)."
			},

			"sessionID": {
				"type": "string",
				"default": "",
				"description": "Optional session identifier. Sessions persist workdir and env across calls (not a persistent shell process)."
			},
			"createSession": {
				"type": "boolean",
				"default": false,
				"description": "If true, create a new session and return its sessionID."
			},
			"restartSession": {
				"type": "boolean",
				"default": false,
				"description": "If true, reset the session state (workdir/env) before running commands."
			},
			"closeSession": {
				"type": "boolean",
				"default": false,
				"description": "If true, delete the session and do not run any commands."
			},

			"notes": {
				"type": "string",
				"description": "Optional audit notes. Not executed."
			}
		},
		"additionalProperties": false
	}`),
	GoImpl: spec.GoToolImpl{FuncID: shellCommandFuncID},

	CreatedAt:  spec.SchemaStartTime,
	ModifiedAt: spec.SchemaStartTime,
}

type ShellName string

const (
	ShellNameAuto       ShellName = "auto"
	ShellNameBash       ShellName = "bash"
	ShellNameZsh        ShellName = "zsh"
	ShellNameSh         ShellName = "sh"
	ShellNamePowershell ShellName = "powershell"
	ShellNameCmd        ShellName = "cmd"
)

type selectedShell struct {
	Name ShellName
	Path string
}

type ShellCommandArgs struct {
	Commands []string `json:"commands,omitempty"`

	Workdir string            `json:"workdir,omitempty"`
	Env     map[string]string `json:"env,omitempty"`

	Shell           ShellName `json:"shell,omitempty"`
	ContinueOnError bool      `json:"continueOnError,omitempty"`
	Login           bool      `json:"login,omitempty"`
	TimeoutMS       *int64    `json:"timeoutMS,omitempty"`

	// In bytes, per stream (stdout and stderr each).
	MaxOutputLength *int64 `json:"maxOutputLength,omitempty"`

	SessionID      string `json:"sessionID,omitempty"`
	CreateSession  bool   `json:"createSession,omitempty"`
	RestartSession bool   `json:"restartSession,omitempty"`
	CloseSession   bool   `json:"closeSession,omitempty"`

	Notes string `json:"notes,omitempty"`
}

type ShellCommandExecResult struct {
	Command   string    `json:"command"`
	Workdir   string    `json:"workdir"`
	Shell     ShellName `json:"shell"`
	ShellPath string    `json:"shellPath"`
	Login     bool      `json:"login"`

	ExitCode   int   `json:"exitCode"`
	TimedOut   bool  `json:"timedOut"`
	DurationMS int64 `json:"durationMS"`

	Stdout string `json:"stdout"`
	Stderr string `json:"stderr"`

	StdoutBytes     int64 `json:"stdoutBytes"`
	StderrBytes     int64 `json:"stderrBytes"`
	StdoutTruncated bool  `json:"stdoutTruncated"`
	StderrTruncated bool  `json:"stderrTruncated"`

	Warnings []string `json:"warnings,omitempty"`
}

type ShellCommandResponse struct {
	SessionID string `json:"sessionID,omitempty"`
	Workdir   string `json:"workdir,omitempty"`

	MaxOutputLength int64 `json:"maxOutputLength"`
	TimeoutMS       int64 `json:"timeoutMS"`

	Results []ShellCommandExecResult `json:"results,omitempty"`
	Message string                   `json:"message,omitempty"`
	Notes   string                   `json:"notes,omitempty"`
}

// ShellCommandPolicy provides policy / hardening knobs (package-level, so host app can tune).
type ShellCommandPolicy struct {
	// If true, skip dangerous-command checks (NOT recommended as default).
	AllowDangerous bool

	// Default caps.
	DefaultTimeout        time.Duration
	DefaultMaxOutputBytes int64

	// Maximum cap allowed even if caller requests more.
	HardMaxOutputBytes   int64
	HardMaxCommands      int
	HardMaxCommandLength int
}

var DefaultShellCommandPolicy = ShellCommandPolicy{
	AllowDangerous:        false,
	DefaultTimeout:        60 * time.Second,
	DefaultMaxOutputBytes: 256 * 1024,      // 256KiB per stream
	HardMaxOutputBytes:    4 * 1024 * 1024, // 4MiB per stream
	HardMaxCommands:       64,
	HardMaxCommandLength:  64 * 1024, // 64KiB
}

// ShellTool is an instance-owned shell tool runner.
// It owns sessions, policy, and environment inheritance settings.
type ShellTool struct {
	mu                  sync.RWMutex
	policy              ShellCommandPolicy
	allowedWorkdirRoots []string // optional; if empty, allow any
	sessions            *sessionStore
}

type ShellToolOption func(*ShellTool) error

func WithShellCommandPolicy(p ShellCommandPolicy) ShellToolOption {
	return func(st *ShellTool) error {
		st.policy = p
		return nil
	}
}

// WithShellAllowedWorkdirRoots restricts workdir to be within one of the provided roots.
// Roots are canonicalized (clean+abs) and must exist as directories.
func WithShellAllowedWorkdirRoots(roots []string) ShellToolOption {
	return func(st *ShellTool) error {
		canon, err := canonicalizeAllowedRoots(roots)
		if err != nil {
			return err
		}
		st.allowedWorkdirRoots = canon
		return nil
	}
}

func NewShellTool(opts ...ShellToolOption) (*ShellTool, error) {
	st := &ShellTool{
		policy:              DefaultShellCommandPolicy,
		allowedWorkdirRoots: nil,
		sessions:            newSessionStore(),
	}
	for _, opt := range opts {
		if opt == nil {
			continue
		}
		if err := opt(st); err != nil {
			return nil, err
		}
	}
	return st, nil
}

func (st *ShellTool) Tool() spec.Tool { return toolutil.CloneTool(shellToolSpec) }

// SetAllowedWorkdirRoots allows changing workdir roots at runtime (best-effort).
// Existing sessions whose workdir falls outside the new roots will fail when used.
func (st *ShellTool) SetAllowedWorkdirRoots(roots []string) error {
	canon, err := canonicalizeAllowedRoots(roots)
	if err != nil {
		return err
	}
	st.mu.Lock()
	st.allowedWorkdirRoots = canon
	st.mu.Unlock()
	return nil
}

func (st *ShellTool) Run(ctx context.Context, args ShellCommandArgs) (out []spec.ToolStoreOutputUnion, err error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	args.SessionID = strings.TrimSpace(args.SessionID)
	// Validate mutually exclusive combinations.
	if args.CreateSession && args.SessionID != "" {
		return nil, errors.New("createSession cannot be used with an explicit sessionID")
	}
	if args.CreateSession && args.CloseSession {
		return nil, errors.New("createSession and closeSession cannot both be true")
	}
	if args.CloseSession && len(args.Commands) != 0 {
		return nil, errors.New("closeSession=true cannot be used with commands")
	}

	st.mu.RLock()
	policy := st.policy
	roots := append([]string(nil), st.allowedWorkdirRoots...)
	st.mu.RUnlock()

	createdSessionID := ""
	defer func() {
		// If we created a session but the call failed, do not leak it.
		if err != nil && createdSessionID != "" {
			st.sessions.delete(createdSessionID)
		}
	}()

	// Handle session lifecycle first.
	var sess *shellSession
	if args.CreateSession {
		sess = st.sessions.newSession()

		args.SessionID = sess.id
		createdSessionID = sess.id

	}
	if args.SessionID != "" && sess == nil {
		var ok bool
		sess, ok = st.sessions.get(args.SessionID)

		if !ok {
			return nil, fmt.Errorf("unknown sessionID: %s", args.SessionID)
		}
		if args.RestartSession {
			st.sessions.reset(sess)
		}
		if args.CloseSession {
			st.sessions.delete(args.SessionID)

			resp := ShellCommandResponse{
				SessionID:       args.SessionID,
				Message:         "session closed",
				Notes:           args.Notes,
				MaxOutputLength: effectiveMaxOutputBytes(policy, args.MaxOutputLength),
				TimeoutMS:       effectiveTimeout(policy, args.TimeoutMS).Milliseconds(),
			}
			out, err = toolJSONText(resp)
			return out, err
		}
	} else if args.CloseSession || args.RestartSession {
		return nil, errors.New("sessionID is required for closeSession/restartSession")
	}

	// Determine commands.
	cmds := normalizedCommandList(args)
	if len(cmds) == 0 {
		return nil, errors.New("commands is required (unless closeSession=true)")
	}

	if policy.HardMaxCommands > 0 && len(cmds) > policy.HardMaxCommands {
		return nil, fmt.Errorf("too many commands: %d (max %d)", len(cmds), policy.HardMaxCommands)
	}
	// Determine effective settings.
	timeout := effectiveTimeout(policy, args.TimeoutMS)
	maxOut := effectiveMaxOutputBytes(policy, args.MaxOutputLength)

	login := args.Login

	stopOnError := !args.ContinueOnError

	// Determine effective workdir (args > session > current).
	workdir, err := effectiveWorkdir(args.Workdir, sess, roots)
	if err != nil {
		return nil, err
	}

	// Determine effective env (process env + session env + args env).
	// Validate env early so we don't:
	//  1) store invalid env into sessions
	//  2) fail later at exec.Start with confusing errors
	if err := validateEnvMap(args.Env); err != nil {
		return nil, err
	}

	// Determine effective env (process env + session env + args env).
	env, err := effectiveEnv(sess, args.Env)
	if err != nil {
		return nil, err
	}

	// Choose shell + path.
	sel, err := selectShell(args.Shell)
	if err != nil {
		return nil, err
	}

	// Persist session defaults if session is used and caller provided values.
	if sess != nil {
		sess.mu.Lock()
		if sess.closed {
			sess.mu.Unlock()
			return nil, errors.New("session is closed")
		}
		if strings.TrimSpace(args.Workdir) != "" {
			sess.workdir = workdir
		}
		if len(args.Env) != 0 {
			if sess.env == nil {
				sess.env = map[string]string{}
			}
			if runtime.GOOS == GOOSWindows {
				// Rebuild session env to canonical keys to avoid case-insensitive duplicates
				// causing nondeterministic behavior.
				canon := make(map[string]string, len(sess.env)+len(args.Env))
				for k, v := range sess.env {
					kk := strings.ToUpper(strings.TrimSpace(k))
					if kk == "" {
						continue
					}
					canon[kk] = v
				}
				for k, v := range args.Env {
					kk := strings.ToUpper(strings.TrimSpace(k))
					if kk == "" {
						continue
					}
					canon[kk] = v
				}
				sess.env = canon
			} else {
				maps.Copy(sess.env, args.Env)
			}
		}
		sess.mu.Unlock()
	}

	results := make([]ShellCommandExecResult, 0, len(cmds))
	for _, one := range cmds {
		if err := ctx.Err(); err != nil {
			return nil, err
		}

		command := strings.TrimSpace(one)
		if command == "" {
			continue
		}
		if policy.HardMaxCommandLength > 0 &&
			len(command) > policy.HardMaxCommandLength {
			return nil, fmt.Errorf(
				"command too long (%d bytes; max %d)",
				len(command),
				policy.HardMaxCommandLength,
			)
		}
		if strings.ContainsRune(command, '\x00') {
			return nil, errors.New("command contains NUL byte")
		}

		warnings := classifyWarnings(command)

		if !policy.AllowDangerous {
			if err := toolutil.RejectDangerousCommand(command, string(sel.Name), sel.Path); err != nil {
				return nil, err
			}
		}

		res, err := runOne(ctx, sel, command, workdir, env, login, timeout, maxOut, warnings)
		if err != nil {
			// We still return structured output when possible.
			// If it's an exec-start failure, include it in stderr-ish form.
			res = ShellCommandExecResult{
				Command:     command,
				Workdir:     workdir,
				Shell:       sel.Name,
				ShellPath:   sel.Path,
				Login:       login,
				ExitCode:    127,
				TimedOut:    false,
				DurationMS:  0,
				Stdout:      "",
				Stderr:      err.Error(),
				StdoutBytes: 0,
				StderrBytes: int64(len(err.Error())),
				Warnings:    warnings,
			}
		}
		results = append(results, res)
		if stopOnError && (res.TimedOut || res.ExitCode != 0) {
			break
		}
	}

	resp := ShellCommandResponse{
		SessionID:       args.SessionID,
		Workdir:         workdir,
		MaxOutputLength: maxOut,
		TimeoutMS:       timeout.Milliseconds(),
		Results:         results,
		Notes:           args.Notes,
	}

	out, err = toolJSONText(resp)
	return out, err
}

func runOne(
	parent context.Context,
	sel selectedShell,
	command string,
	workdir string,
	env []string,
	login bool,
	timeout time.Duration,
	maxOut int64,
	warnings []string,
) (ShellCommandExecResult, error) {
	ctx := parent
	var cancel context.CancelFunc
	if timeout > 0 {
		ctx, cancel = context.WithTimeout(parent, timeout)
		defer cancel()
	}

	args := deriveExecArgs(sel, command, login)

	cmd := exec.CommandContext(ctx, args[0], args[1:]...) //nolint:gosec // Exec shell command.
	cmd.Dir = workdir
	cmd.Env = env

	configureProcessGroup(cmd)

	stdoutW := newCappedWriter(maxOut)
	stderrW := newCappedWriter(maxOut)
	cmd.Stdout = stdoutW
	cmd.Stderr = stderrW

	done := make(chan struct{})

	start := time.Now()
	runErr := cmd.Start()
	if runErr != nil {
		return ShellCommandExecResult{}, runErr
	}

	// Kill process group/tree immediately on ctx cancellation/timeout to avoid orphaned grandchildren.
	go func() {
		select {
		case <-ctx.Done():
			// Double-check completion so we don't kill after the process has already been waited/reaped.
			select {
			case <-done:
				return
			default:
			}
			killProcessGroup(cmd)
		case <-done:
		}
	}()

	waitErr := cmd.Wait()
	close(done)
	dur := time.Since(start)

	ctxErr := ctx.Err()
	timedOut := errors.Is(ctxErr, context.DeadlineExceeded)

	exitCode := exitCodeFromWait(waitErr, timedOut)

	return ShellCommandExecResult{
		Command:   command,
		Workdir:   workdir,
		Shell:     sel.Name,
		ShellPath: sel.Path,
		Login:     login,

		ExitCode:   exitCode,
		TimedOut:   timedOut,
		DurationMS: dur.Milliseconds(),

		Stdout: safeUTF8(stdoutW.Bytes()),
		Stderr: safeUTF8(stderrW.Bytes()),

		StdoutBytes:     stdoutW.TotalBytes(),
		StderrBytes:     stderrW.TotalBytes(),
		StdoutTruncated: stdoutW.Truncated(),
		StderrTruncated: stderrW.Truncated(),

		Warnings: warnings,
	}, nil
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

func toolJSONText(v any) ([]spec.ToolStoreOutputUnion, error) {
	b, err := json.Marshal(v)
	if err != nil {
		return nil, err
	}
	return []spec.ToolStoreOutputUnion{
		{
			Kind:     spec.ToolStoreOutputKindText,
			TextItem: &spec.ToolStoreOutputText{Text: string(b)},
		},
	}, nil
}

func safeUTF8(b []byte) string {
	// Replace invalid UTF-8 sequences; avoids breaking JSON / UIs.
	return string(bytes.ToValidUTF8(b, []byte("\uFFFD")))
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
	if capBytes < 1024 {
		capBytes = 1024
	}
	// "capBytes" is bounded by policy hard-max (<= 4MiB), safe to cast to int.
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

func effectiveTimeout(policy ShellCommandPolicy, ms *int64) time.Duration {
	// Tool schema max is 10 minutes; clamp even if caller bypassed schema validation.
	const hardMax = 10 * time.Minute
	if ms == nil || *ms <= 0 {
		d := max(policy.DefaultTimeout, 0)
		return min(d, hardMax)
	}
	maxMS := int64(hardMax / time.Millisecond)
	if *ms >= maxMS {
		return hardMax
	}
	return time.Duration(*ms) * time.Millisecond
}

func effectiveMaxOutputBytes(policy ShellCommandPolicy, v *int64) int64 {
	hardMax := max(policy.HardMaxOutputBytes, 1024)
	def := max(policy.DefaultMaxOutputBytes, 1024)
	if v == nil || *v <= 0 {
		return min(def, hardMax)
	}
	return min(max(*v, 1024), hardMax)
}

func normalizedCommandList(args ShellCommandArgs) []string {
	if len(args.Commands) != 0 {
		out := make([]string, 0, len(args.Commands))
		for _, c := range args.Commands {
			if strings.TrimSpace(c) != "" {
				out = append(out, c)
			}
		}
		return out
	}
	return nil
}

func effectiveWorkdir(arg string, sess *shellSession, allowedRoots []string) (string, error) {
	if strings.TrimSpace(arg) != "" {
		p, err := canonicalWorkdir(arg)
		if err != nil {
			return "", err
		}
		if err := ensureDirExists(p); err != nil {
			return "", err
		}
		if err := ensureWorkdirAllowed(p, allowedRoots); err != nil {
			return "", err
		}
		return p, nil
	}
	if sess != nil {
		sess.mu.RLock()
		wd := sess.workdir
		closed := sess.closed
		sess.mu.RUnlock()
		if closed {
			return "", errors.New("session is closed")
		}
		if strings.TrimSpace(wd) != "" {
			p, err := canonicalWorkdir(wd)
			if err != nil {
				return "", err
			}
			if err := ensureDirExists(p); err != nil {
				return "", err
			}
			if err := ensureWorkdirAllowed(p, allowedRoots); err != nil {
				return "", err
			}
			return p, nil

		}
	}
	cwd, err := os.Getwd()
	if err != nil {
		return "", err
	}
	if err := ensureWorkdirAllowed(cwd, allowedRoots); err != nil {
		return "", err
	}
	return cwd, nil
}

func canonicalWorkdir(p string) (string, error) {
	if strings.ContainsRune(p, '\x00') {
		return "", errors.New("workdir contains NUL byte")
	}
	cleaned := filepath.Clean(p)
	abs, err := filepath.Abs(cleaned)
	if err != nil {
		return "", err
	}
	return abs, nil
}

func ensureDirExists(p string) error {
	st, err := os.Stat(p)
	if err != nil {
		return err
	}
	if !st.IsDir() {
		return fmt.Errorf("workdir is not a directory: %s", p)
	}
	return nil
}

func canonicalizeAllowedRoots(roots []string) ([]string, error) {
	var out []string
	for _, r := range roots {
		if strings.TrimSpace(r) == "" {
			continue
		}
		cr, err := canonicalWorkdir(r)
		if err != nil {
			return nil, fmt.Errorf("invalid allowed root %q: %w", r, err)
		}
		if err := ensureDirExists(cr); err != nil {
			return nil, fmt.Errorf("invalid allowed root %q: %w", r, err)
		}
		out = append(out, cr)
	}
	return out, nil
}

func ensureWorkdirAllowed(p string, roots []string) error {
	if len(roots) == 0 {
		return nil
	}
	for _, r := range roots {
		ok, err := pathWithinRoot(r, p)
		if err != nil {
			continue
		}
		if ok {
			return nil
		}
	}
	return fmt.Errorf("workdir %q is outside allowed roots", p)
}

func pathWithinRoot(root, p string) (bool, error) {
	rel, err := filepath.Rel(root, p)
	if err != nil {
		return false, err
	}
	rel = filepath.Clean(rel)
	if rel == "." {
		return true, nil
	}
	if rel == ".." || strings.HasPrefix(rel, ".."+string(os.PathSeparator)) {
		return false, nil
	}
	return true, nil
}

type envEntry struct {
	key string
	val string
}

func effectiveEnv(sess *shellSession, overrides map[string]string) ([]string, error) {
	// Start with current process env.
	envMap := map[string]envEntry{}

	for _, kv := range os.Environ() {
		k, v, ok := strings.Cut(kv, "=")
		if ok {
			kk := strings.TrimSpace(k)
			if kk == "" || strings.ContainsRune(kk, '\x00') || strings.ContainsRune(v, '\x00') ||
				strings.Contains(kk, "=") {
				continue
			}
			ck := canonicalEnvKey(kk)
			envMap[ck] = envEntry{key: kk, val: v}
		}
	}

	// Then session env.
	if sess != nil {
		sess.mu.RLock()
		closed := sess.closed
		snap := maps.Clone(sess.env)
		sess.mu.RUnlock()
		if closed {
			return nil, errors.New("session is closed")
		}
		for k, v := range snap {
			if err := validateEnvKV(k, v); err != nil {
				return nil, fmt.Errorf("invalid session env %q: %w", k, err)
			}
			kk := strings.TrimSpace(k)
			ck := canonicalEnvKey(kk)
			envMap[ck] = envEntry{key: kk, val: v}
		}
	}

	// Then per-call overrides.
	if len(overrides) != 0 {
		for k, v := range overrides {
			// Overrides are validated by validateEnvMap in ShellCommand; still normalize key.
			kk := strings.TrimSpace(k)
			if kk == "" {
				continue
			}
			ck := canonicalEnvKey(kk)
			envMap[ck] = envEntry{key: kk, val: v}
		}
	}

	out := make([]string, 0, len(envMap))
	for _, e := range envMap {
		out = append(out, e.key+"="+e.val)
	}
	return out, nil
}

func canonicalEnvKey(k string) string {
	if runtime.GOOS == GOOSWindows {
		return strings.ToUpper(k)
	}
	return k
}

func validateEnvKV(k, v string) error {
	kk := strings.TrimSpace(k)
	if kk == "" {
		return errors.New("empty name")
	}
	if strings.ContainsRune(kk, '\x00') || strings.ContainsRune(v, '\x00') {
		return errors.New("contains NUL byte")
	}
	if strings.Contains(kk, "=") {
		return errors.New("name contains '='")
	}
	return nil
}

func validateEnvMap(m map[string]string) error {
	for k, v := range m {
		if err := validateEnvKV(k, v); err != nil {
			return fmt.Errorf("env %q: %w", k, err)
		}
	}
	return nil
}

func selectShell(requested ShellName) (selectedShell, error) {
	r := strings.ToLower(strings.TrimSpace(string(requested)))
	if r == "" {
		r = "auto"
	}

	if r != "auto" {
		return resolveShell(r)
	}

	// Auto.
	if runtime.GOOS == GOOSWindows {
		// Prefer pwsh, then Windows PowerShell, then cmd.
		if p, _ := exec.LookPath("pwsh"); p != "" {
			return selectedShell{Name: ShellNamePowershell, Path: p}, nil
		}
		if p, _ := exec.LookPath("powershell"); p != "" {
			return selectedShell{Name: ShellNamePowershell, Path: p}, nil
		}
		if p, _ := exec.LookPath("cmd"); p != "" {
			return selectedShell{Name: ShellNameCmd, Path: p}, nil
		}
		return selectedShell{}, errors.New("no suitable shell found on windows (pwsh/powershell/cmd)")
	}

	// Unix-ish: prefer $SHELL if present, else bash/zsh/sh.
	if sh := os.Getenv("SHELL"); sh != "" {
		if p, err := exec.LookPath(sh); err == nil && p != "" {
			// Best-effort: infer by basename.
			base := ShellName(strings.ToLower(filepath.Base(p)))
			switch base {
			case ShellNameBash, ShellNameZsh, ShellNameSh:
				return selectedShell{Name: base, Path: p}, nil
			default:
				// Allow a small set of common shells that typically support "-c" even though
				// they aren't explicitly in our enum. Keep Name=auto for transparency.
				switch strings.ToLower(string(base)) {
				case "dash", "ksh", "fish":
					return selectedShell{Name: ShellNameAuto, Path: p}, nil
				}
			}
		}
	}

	if p, _ := exec.LookPath(string(ShellNameBash)); p != "" {
		return selectedShell{Name: ShellNameBash, Path: p}, nil
	}
	if p, _ := exec.LookPath(string(ShellNameZsh)); p != "" {
		return selectedShell{Name: ShellNameZsh, Path: p}, nil
	}
	if p, _ := exec.LookPath(string(ShellNameSh)); p != "" {
		return selectedShell{Name: ShellNameSh, Path: p}, nil
	}
	return selectedShell{}, errors.New("no suitable shell found (bash/zsh/sh)")
}

func resolveShell(name string) (selectedShell, error) {
	shellName := ShellName(name)
	switch shellName {
	case ShellNameBash, ShellNameZsh, ShellNameSh:
		p, err := exec.LookPath(name)
		if err != nil {
			return selectedShell{}, fmt.Errorf("shell not found: %s", name)
		}
		return selectedShell{Name: shellName, Path: p}, nil
	case ShellNamePowershell:
		// Accept pwsh or powershell as the resolved path.
		if p, _ := exec.LookPath("pwsh"); p != "" {
			return selectedShell{Name: ShellNamePowershell, Path: p}, nil
		}
		p, err := exec.LookPath("powershell")
		if err != nil {
			return selectedShell{}, errors.New("powershell requested but neither pwsh nor powershell found")
		}
		return selectedShell{Name: ShellNamePowershell, Path: p}, nil
	case ShellNameCmd:
		p, err := exec.LookPath("cmd")
		if err != nil {
			return selectedShell{}, errors.New("cmd requested but not found")
		}
		return selectedShell{Name: ShellNameCmd, Path: p}, nil
	default:
		return selectedShell{}, fmt.Errorf("invalid shell: %q", name)
	}
}

func deriveExecArgs(sel selectedShell, command string, login bool) []string {
	switch sel.Name {
	case ShellNameBash, ShellNameZsh, ShellNameSh:
		// Hardened default: NOT login unless explicitly enabled.
		// Login=true runs rc/profile scripts and can execute arbitrary user-defined code.
		flag := "-c"
		if login {
			flag = "-lc"
		}
		return []string{sel.Path, flag, command}

	case ShellNamePowershell:
		// Login=true => allow profile; otherwise no profile for determinism.
		// Add -NonInteractive to avoid prompts.
		args := []string{sel.Path, "-NoLogo", "-NonInteractive"}
		if !login {
			args = append(args, "-NoProfile")
		}
		args = append(args, "-Command", command)
		return args

	case ShellNameCmd:
		// Options: /d disables AutoRun from registry (safer); /s handles quotes; /c runs then exits.
		return []string{sel.Path, "/d", "/s", "/c", command}

	default:

		return []string{sel.Path, "-c", command}
	}
}

func classifyWarnings(cmd string) []string {
	c := strings.ToLower(cmd)
	var w []string
	if strings.Contains(c, "curl ") || strings.Contains(c, "wget ") || strings.Contains(c, "ssh ") {
		w = append(w, "network_access")
	}
	if strings.Contains(c, "git ") {
		w = append(w, "git_operation")
	}
	if strings.Contains(c, "rm ") || strings.Contains(c, "del ") {
		w = append(w, "potentially_destructive")
	}
	return w
}

func newSessionID() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err == nil {
		return "sess_" + hex.EncodeToString(b[:])
	}
	now := time.Now().UTC().UnixNano()
	return fmt.Sprintf("sess_%d_%d", now, os.Getpid())
}

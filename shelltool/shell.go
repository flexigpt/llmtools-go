package shelltool

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"maps"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/flexigpt/llmtools-go/internal/toolutil"
	"github.com/flexigpt/llmtools-go/spec"
)

const shellCommandFuncID spec.FuncID = "github.com/flexigpt/llmtools-go/shelltool/shell.ShellCommand"

// Fixed, package-wide hard limits (single source of truth).
const (
	HardMaxTimeout             = 10 * time.Minute
	HardMaxOutputBytes   int64 = 4 * 1024 * 1024 // per stream
	HardMaxCommands            = 64
	HardMaxCommandLength       = 64 * 1024 // bytes
	MinOutputBytes       int64 = 1024

	DefaultTimeout                = 60 * time.Second
	DefaultMaxOutputBytes   int64 = 256 * 1024
	DefaultMaxCommands            = 64
	DefaultMaxCommandLength       = 64 * 1024
)

var shellToolSpec = spec.Tool{
	SchemaVersion: spec.SchemaVersion,
	ID:            "019bfeda-33f2-7315-9007-de55935d2302",
	Slug:          "shell",
	Version:       "v1.0.0",
	DisplayName:   "Shell",
	Description:   "Execute local shell commands (cross-platform) with session-like persistence for workdir/env.",
	Tags:          []string{"shell", "exec"},

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
		"enum": ["auto", "bash", "zsh", "sh", "dash", "ksh", "fish", "pwsh", "powershell", "cmd"],
		"default": "auto",
		"description": "Which shell to run. 'auto' chooses a safe default per OS."
	},
	"executeParallel": {
		"type": "boolean",
		"default": false,
		"description": "If true, treat commands as independent and parallel executable (do not stop on error)."
	},
	"sessionID": {
		"type": "string",
		"default": "",
		"description": "Optional session identifier. If omitted/empty, a new session is created and returned. Sessions persist workdir and env across calls (not a persistent shell process)."
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
	ShellNameDash       ShellName = "dash"
	ShellNameKsh        ShellName = "ksh"
	ShellNameFish       ShellName = "fish"
	ShellNamePwsh       ShellName = "pwsh"
	ShellNamePowershell ShellName = "powershell"
	ShellNameCmd        ShellName = "cmd"
)

type selectedShell struct {
	Name ShellName
	Path string
}

type ShellCommandArgs struct {
	Commands        []string          `json:"commands,omitempty"`
	Workdir         string            `json:"workdir,omitempty"`
	Env             map[string]string `json:"env,omitempty"`
	Shell           ShellName         `json:"shell,omitempty"`
	ExecuteParallel bool              `json:"executeParallel,omitempty"`
	SessionID       string            `json:"sessionID,omitempty"`
}

type ShellCommandExecResult struct {
	Command   string    `json:"command"`
	Workdir   string    `json:"workdir"`
	Shell     ShellName `json:"shell"`
	ShellPath string    `json:"shellPath"`

	ExitCode   int   `json:"exitCode"`
	TimedOut   bool  `json:"timedOut"`
	DurationMS int64 `json:"durationMS"`

	Stdout string `json:"stdout"`
	Stderr string `json:"stderr"`

	StdoutTruncated bool `json:"stdoutTruncated"`
	StderrTruncated bool `json:"stderrTruncated"`
}

type ShellCommandResponse struct {
	SessionID string                   `json:"sessionID,omitempty"`
	Workdir   string                   `json:"workdir,omitempty"`
	Results   []ShellCommandExecResult `json:"results,omitempty"`
}

// ShellCommandPolicy provides policy / hardening knobs (package-level, so host app can tune).
type ShellCommandPolicy struct {
	// If true, skip heuristic checks (fork-bomb/backgrounding). NOTE: hard-blocked commands are ALWAYS blocked.
	AllowDangerous bool

	// Policy limits (clamped to package hard limits).
	Timeout          time.Duration
	MaxOutputBytes   int64
	MaxCommands      int
	MaxCommandLength int
}

var DefaultShellCommandPolicy = ShellCommandPolicy{
	AllowDangerous:   false,
	Timeout:          DefaultTimeout,
	MaxOutputBytes:   DefaultMaxOutputBytes,
	MaxCommands:      DefaultMaxCommands,
	MaxCommandLength: DefaultMaxCommandLength,
}

// ShellTool is an instance-owned shell tool runner.
// It owns sessions, policy, and environment inheritance settings.
type ShellTool struct {
	mu                  sync.RWMutex
	policy              ShellCommandPolicy
	allowedWorkdirRoots []string            // optional; if empty, allow any
	blockedCommands     map[string]struct{} // instance-owned blocklist (includes non-overridable hard defaults)
	sessions            *sessionStore
}

type ShellToolOption func(*ShellTool) error

func WithShellCommandPolicy(p ShellCommandPolicy) ShellToolOption {
	return func(st *ShellTool) error {
		st.policy = p
		return nil
	}
}

// WithShellBlockedCommands adds additional commands to the instance blocklist.
// These are enforced before execution and cannot override/remove the hard default blocklist.
// Entries must be command names (e.g. "git", "python", "curl"), not full command lines.
func WithShellBlockedCommands(cmds []string) ShellToolOption {
	return func(st *ShellTool) error {
		for _, c := range cmds {
			n, err := normalizeBlockedCommand(c)
			if err != nil {
				return err
			}
			if n == "" {
				continue
			}
			st.blockedCommands[n] = struct{}{}
			// On Windows, also add the no-extension variant if it looks like an executable name.
			if runtime.GOOS == toolutil.GOOSWindows {
				ext := strings.ToLower(filepath.Ext(n))
				switch ext {
				case ".exe", ".com", ".bat", ".cmd":
					st.blockedCommands[strings.TrimSuffix(n, ext)] = struct{}{}
				}
			}
		}
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

// WithShellSessionTTL enables TTL eviction for sessions.
// "ttl<=0" disables TTL eviction (LRU max may still evict).
func WithShellSessionTTL(ttl time.Duration) ShellToolOption {
	return func(st *ShellTool) error {
		st.sessions.setTTL(ttl)
		return nil
	}
}

// WithShellMaxSessions sets an upper bound on concurrent sessions (LRU eviction).
// "max<=0" disables max-session eviction (TTL may still evict).
func WithShellMaxSessions(maxSessions int) ShellToolOption {
	return func(st *ShellTool) error {
		st.sessions.setMaxSessions(maxSessions)
		return nil
	}
}

func NewShellTool(opts ...ShellToolOption) (*ShellTool, error) {
	st := &ShellTool{
		policy:              DefaultShellCommandPolicy,
		allowedWorkdirRoots: nil,
		blockedCommands:     maps.Clone(hardBlockedCommands),
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

func (st *ShellTool) Run(ctx context.Context, args ShellCommandArgs) (out *ShellCommandResponse, err error) {
	return toolutil.WithRecoveryResp(func() (out *ShellCommandResponse, err error) {
		return st.run(ctx, args)
	})
}

func (st *ShellTool) run(ctx context.Context, args ShellCommandArgs) (out *ShellCommandResponse, err error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	args.SessionID = strings.TrimSpace(args.SessionID)

	st.mu.RLock()
	policy := st.policy
	roots := append([]string(nil), st.allowedWorkdirRoots...)
	blocked := st.blockedCommands
	st.mu.RUnlock()

	// Determine commands early (so we don't create sessions for invalid requests).
	cmds := normalizedCommandList(args)
	if len(cmds) == 0 {
		return nil, errors.New("commands is required")
	}
	maxCmds := effectiveMaxCommands(policy)
	if maxCmds > 0 && len(cmds) > maxCmds {
		return nil, fmt.Errorf("too many commands: %d (max %d)", len(cmds), maxCmds)
	}

	createdSessionID := ""
	defer func() {
		// If we created a session but the call failed, do not leak it.
		if err != nil && createdSessionID != "" {
			st.sessions.delete(createdSessionID)
		}
	}()

	// Handle session lifecycle first.
	var sess *shellSession
	// Session semantics:
	// - if sessionID provided: use it
	// - otherwise: create a new session and return it.
	if args.SessionID != "" {
		var ok bool
		sess, ok = st.sessions.get(args.SessionID)
		if !ok {
			return nil, fmt.Errorf("unknown sessionID: %s", args.SessionID)
		}
	} else {
		sess = st.sessions.newSession()
		args.SessionID = sess.id
		createdSessionID = sess.id
	}

	// Determine effective settings (policy-only).
	timeout := effectiveTimeout(policy)
	maxOut := effectiveMaxOutputBytes(policy)
	maxCmdLen := effectiveMaxCommandLength(policy)

	// "executeParallel=true" => treat commands as independent => do not stop on error.
	stopOnError := !args.ExecuteParallel

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
			if runtime.GOOS == toolutil.GOOSWindows {
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
		if maxCmdLen > 0 && len(command) > maxCmdLen {
			return nil, fmt.Errorf(
				"command too long (%d bytes; max %d)",
				len(command), maxCmdLen,
			)
		}
		if strings.ContainsRune(command, '\x00') {
			return nil, errors.New("command contains NUL byte")
		}

		// Always enforce command blocklist. Heuristic checks are optional.
		if err := rejectDangerousCommand(command, sel.Path, sel.Name, blocked, !policy.AllowDangerous); err != nil {
			return nil, err
		}

		res, err := runOne(ctx, sel, command, workdir, env, timeout, maxOut)
		if err != nil {
			// We still return structured output when possible.
			// If it's an exec-start failure, include it in stderr-ish form.
			res = ShellCommandExecResult{
				Command:   command,
				Workdir:   workdir,
				Shell:     sel.Name,
				ShellPath: sel.Path,

				ExitCode:   127,
				TimedOut:   false,
				DurationMS: 0,
				Stdout:     "",
				Stderr:     err.Error(),
			}
		}
		results = append(results, res)
		if stopOnError && (res.TimedOut || res.ExitCode != 0) {
			break
		}
	}

	resp := ShellCommandResponse{
		SessionID: args.SessionID,
		Workdir:   workdir,
		Results:   results,
	}

	return &resp, err
}

func runOne(
	parent context.Context,
	sel selectedShell,
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

	args := deriveExecArgs(sel, command)

	cmd := exec.CommandContext(ctx, args[0], args[1:]...) //nolint:gosec // Exec shell command.
	cmd.Dir = workdir
	cmd.Env = env

	configureProcessGroup(cmd)

	stdoutW := newCappedWriter(maxOut)
	stderrW := newCappedWriter(maxOut)
	cmd.Stdout = stdoutW
	cmd.Stderr = stderrW

	start := time.Now()
	runErr := cmd.Start()
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
		Workdir:   workdir,
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

func ensureDirExists(p string) error {
	st, err := os.Stat(p)
	if err != nil {
		return errors.Join(err, errors.New("no such dir"))
	}
	if !st.IsDir() {
		return fmt.Errorf("workdir is not a directory: %s", p)
	}
	return nil
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
	// Best-effort: resolve symlinks to avoid platform-dependent aliases
	// (e.g. macOS /var -> /private/var) and to harden allowed-root checks.
	// If resolution fails (odd FS / permissions), keep the absolute path.
	if resolved, rerr := filepath.EvalSymlinks(abs); rerr == nil && resolved != "" {
		abs = resolved
	}
	return abs, nil
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

	// Stable ordering (deterministic across runs).
	entries := make([]envEntry, 0, len(envMap))
	for _, e := range envMap {
		entries = append(entries, e)
	}
	sort.Slice(entries, func(i, j int) bool {
		// Compare canonical keys for stable ordering across Windows/unix.
		return canonicalEnvKey(entries[i].key) < canonicalEnvKey(entries[j].key)
	})
	out := make([]string, 0, len(entries))
	for _, e := range entries {
		out = append(out, e.key+"="+e.val)
	}
	return out, nil
}

func canonicalEnvKey(k string) string {
	if runtime.GOOS == toolutil.GOOSWindows {
		return strings.ToUpper(k)
	}
	return k
}

func validateEnvMap(m map[string]string) error {
	for k, v := range m {
		if err := validateEnvKV(k, v); err != nil {
			return fmt.Errorf("env %q: %w", k, err)
		}
	}
	return nil
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

func selectShell(requested ShellName) (selectedShell, error) {
	r := strings.ToLower(strings.TrimSpace(string(requested)))
	if r == "" {
		r = "auto"
	}

	if r != "auto" {
		return resolveShell(r)
	}

	// Auto.
	if runtime.GOOS == toolutil.GOOSWindows {
		// Prefer pwsh, then Windows PowerShell, then cmd.
		if p, _ := exec.LookPath("pwsh"); p != "" {
			return selectedShell{Name: ShellNamePwsh, Path: p}, nil
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
			case ShellNameBash, ShellNameZsh, ShellNameSh, ShellNameDash, ShellNameKsh, ShellNameFish:
				return selectedShell{Name: base, Path: p}, nil
			default:
				// Need to explicitly try what we support.
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
	if p, _ := exec.LookPath(string(ShellNameDash)); p != "" {
		return selectedShell{Name: ShellNameDash, Path: p}, nil
	}
	if p, _ := exec.LookPath(string(ShellNameKsh)); p != "" {
		return selectedShell{Name: ShellNameKsh, Path: p}, nil
	}
	if p, _ := exec.LookPath(string(ShellNameFish)); p != "" {
		return selectedShell{Name: ShellNameFish, Path: p}, nil
	}
	return selectedShell{}, errors.New("no suitable shell found (bash/zsh/sh)")
}

func resolveShell(name string) (selectedShell, error) {
	shellName := ShellName(name)
	switch shellName {
	case ShellNameBash, ShellNameZsh, ShellNameSh, ShellNameDash, ShellNameKsh, ShellNameFish:
		p, err := exec.LookPath(name)
		if err != nil {
			return selectedShell{}, fmt.Errorf("shell not found: %s", name)
		}
		return selectedShell{Name: shellName, Path: p}, nil
	case ShellNamePwsh:
		p, err := exec.LookPath("pwsh")
		if err != nil {
			return selectedShell{}, errors.New("pwsh requested but not found")
		}
		return selectedShell{Name: ShellNamePwsh, Path: p}, nil
	case ShellNamePowershell:
		// Accept pwsh or powershell as the resolved path.
		if p, _ := exec.LookPath("pwsh"); p != "" {
			return selectedShell{Name: ShellNamePwsh, Path: p}, nil
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

func deriveExecArgs(sel selectedShell, command string) []string {
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

func newSessionID() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err == nil {
		return "sess_" + hex.EncodeToString(b[:])
	}
	now := time.Now().UTC().UnixNano()
	return fmt.Sprintf("sess_%d_%d", now, os.Getpid())
}

func effectiveTimeout(policy ShellCommandPolicy) time.Duration {
	d := policy.Timeout
	if d <= 0 {
		d = DefaultTimeout
	}
	if d > HardMaxTimeout {
		d = HardMaxTimeout
	}
	return d
}

func effectiveMaxOutputBytes(policy ShellCommandPolicy) int64 {
	v := policy.MaxOutputBytes
	if v <= 0 {
		v = DefaultMaxOutputBytes
	}
	v = max(v, MinOutputBytes)
	v = min(v, HardMaxOutputBytes)
	// Also prevent int overflow in cappedWriter allocation.
	v = min(v, int64(math.MaxInt))
	return v
}

func effectiveMaxCommands(policy ShellCommandPolicy) int {
	v := policy.MaxCommands
	if v <= 0 {
		v = DefaultMaxCommands
	}
	v = max(v, 1)
	v = min(v, HardMaxCommands)
	return v
}

func effectiveMaxCommandLength(policy ShellCommandPolicy) int {
	v := policy.MaxCommandLength
	if v <= 0 {
		v = DefaultMaxCommandLength
	}
	v = max(v, 1)
	v = min(v, HardMaxCommandLength)
	return v
}

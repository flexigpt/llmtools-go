package shelltool

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/flexigpt/llmtools-go/spec"
)

func TestShellCommand_ArgValidation_MutualExclusionAndRequirements(t *testing.T) {
	st := newTestShellTool(t)

	_, err := st.Run(t.Context(), ShellCommandArgs{
		CreateSession: true,
		SessionID:     "sess_abc",
		Commands:      []string{"echo hi"},
	})
	if err == nil || !strings.Contains(err.Error(), "createSession cannot be used") {
		t.Fatalf("expected createSession+sessionID error, got: %v", err)
	}

	_, err = st.Run(t.Context(), ShellCommandArgs{
		CreateSession: true,
		CloseSession:  true,
		Commands:      []string{"echo hi"},
	})
	if err == nil || !strings.Contains(err.Error(), "cannot both be true") {
		t.Fatalf("expected createSession+closeSession error, got: %v", err)
	}

	_, err = st.Run(t.Context(), ShellCommandArgs{
		CloseSession: true,
		Commands:     []string{"echo hi"},
	})
	if err == nil || !strings.Contains(err.Error(), "cannot be used with commands") {
		t.Fatalf("expected closeSession+commands error, got: %v", err)
	}

	_, err = st.Run(t.Context(), ShellCommandArgs{
		RestartSession: true,
		Commands:       []string{"echo hi"},
	})
	if err == nil || !strings.Contains(err.Error(), "sessionID is required") {
		t.Fatalf("expected restartSession without sessionID error, got: %v", err)
	}

	_, err = st.Run(t.Context(), ShellCommandArgs{
		CloseSession: true,
	})
	if err == nil || !strings.Contains(err.Error(), "sessionID is required") {
		t.Fatalf("expected closeSession without sessionID error, got: %v", err)
	}

	_, err = st.Run(t.Context(), ShellCommandArgs{
		SessionID: "sess_unknown",
		Commands:  []string{"echo hi"},
	})
	if err == nil || !strings.Contains(err.Error(), "unknown sessionID") {
		t.Fatalf("expected unknown sessionID error, got: %v", err)
	}

	_, err = st.Run(t.Context(), ShellCommandArgs{})
	if err == nil || !strings.Contains(err.Error(), "commands is required") {
		t.Fatalf("expected commands required error, got: %v", err)
	}
}

func TestShellCommand_CreateSession_CleansUpOnLaterError(t *testing.T) {
	st := newTestShellTool(t)

	// Force an error after session creation by omitting commands.
	_, err := st.Run(t.Context(), ShellCommandArgs{
		CreateSession: true,
		Commands:      nil,
	})
	if err == nil {
		t.Fatalf("expected error")
	}

	// Ensure no leaked session.
	st.sessions.mu.RLock()
	defer st.sessions.mu.RUnlock()
	if len(st.sessions.m) != 0 {
		t.Fatalf("expected no sessions left, found %d", len(st.sessions.m))
	}
}

func TestNormalizedCommandList(t *testing.T) {
	args := ShellCommandArgs{
		Commands: []string{"", "  ", "\n", "echo a", " echo b "},
	}
	got := normalizedCommandList(args)
	if len(got) != 2 {
		t.Fatalf("expected 2 commands, got %d: %#v", len(got), got)
	}
	if got[0] != "echo a" || got[1] != " echo b " {
		t.Fatalf("unexpected command list: %#v", got)
	}
}

func TestEffectiveTimeout_ClampsAndDefaults(t *testing.T) {
	p := DefaultShellCommandPolicy
	p.DefaultTimeout = 1500 * time.Millisecond

	if got := effectiveTimeout(p, nil); got != 1500*time.Millisecond {
		t.Fatalf("expected default timeout 1500ms, got %v", got)
	}

	neg := int64(-5)
	if got := effectiveTimeout(p, &neg); got != 1500*time.Millisecond {
		t.Fatalf("expected default timeout for negative, got %v", got)
	}

	// Hard max is 10 min.
	big := (11 * time.Minute).Milliseconds()
	if got := effectiveTimeout(p, &big); got != 10*time.Minute {
		t.Fatalf("expected hard max 10m, got %v", got)
	}
}

func TestEffectiveMaxOutputBytes_ClampsAndDefaults(t *testing.T) {
	p := DefaultShellCommandPolicy
	p.DefaultMaxOutputBytes = 2048
	p.HardMaxOutputBytes = 4096

	if got := effectiveMaxOutputBytes(p, nil); got != 2048 {
		t.Fatalf("expected default 2048, got %d", got)
	}

	zero := int64(0)
	if got := effectiveMaxOutputBytes(p, &zero); got != 2048 {
		t.Fatalf("expected default for 0, got %d", got)
	}

	tooSmall := int64(1)
	if got := effectiveMaxOutputBytes(p, &tooSmall); got != 1024 {
		t.Fatalf("expected min clamp 1024, got %d", got)
	}

	tooBig := int64(999999)
	if got := effectiveMaxOutputBytes(p, &tooBig); got != 4096 {
		t.Fatalf("expected hard max clamp 4096, got %d", got)
	}
}

func TestCanonicalWorkdir_AndEnsureDirExists(t *testing.T) {
	td := t.TempDir()

	got, err := canonicalWorkdir(td)
	if err != nil {
		t.Fatalf("canonicalWorkdir error: %v", err)
	}
	if !filepath.IsAbs(got) {
		t.Fatalf("expected abs path, got: %q", got)
	}
	if err := ensureDirExists(got); err != nil {
		t.Fatalf("ensureDirExists error: %v", err)
	}

	// Not a directory.
	f := filepath.Join(td, "f")
	if err := os.WriteFile(f, []byte("x"), 0o600); err != nil {
		t.Fatalf("write file: %v", err)
	}
	_, err = effectiveWorkdir(f, nil, nil)
	if err == nil || !strings.Contains(err.Error(), "not a directory") {
		t.Fatalf("expected not-a-directory error, got: %v", err)
	}

	// NUL check.
	_, err = canonicalWorkdir("bad\x00path")
	if err == nil || !strings.Contains(err.Error(), "NUL") {
		t.Fatalf("expected NUL error, got: %v", err)
	}
}

func TestValidateEnvMap(t *testing.T) {
	if err := validateEnvMap(map[string]string{"OK": "1"}); err != nil {
		t.Fatalf("expected ok, got: %v", err)
	}

	if err := validateEnvMap(map[string]string{"": "1"}); err == nil {
		t.Fatalf("expected error for empty key")
	}

	if err := validateEnvMap(map[string]string{"A=B": "1"}); err == nil {
		t.Fatalf("expected error for key containing '='")
	}

	if err := validateEnvMap(map[string]string{"A\x00": "1"}); err == nil {
		t.Fatalf("expected error for NUL in key")
	}

	if err := validateEnvMap(map[string]string{"A": "1\x00"}); err == nil {
		t.Fatalf("expected error for NUL in value")
	}
}

func TestSelectShell_ResolveAndAuto(t *testing.T) {
	if runtime.GOOS == GOOSWindows {
		t.Skip("unix-specific expectations")
	}

	shPath := mustLookPath(t, "sh")

	// Explicit resolution.
	sel, err := selectShell(ShellNameSh)
	if err != nil {
		t.Fatalf("selectShell(sh) error: %v", err)
	}
	if sel.Name != ShellNameSh {
		t.Fatalf("expected name=sh, got %q", sel.Name)
	}
	if sel.Path == "" {
		t.Fatalf("expected path for sh")
	}

	// Auto via $SHELL should pick sh when basename is sh.
	t.Setenv("SHELL", shPath)
	sel, err = selectShell(ShellNameAuto)
	if err != nil {
		t.Fatalf("selectShell(auto) error: %v", err)
	}
	if sel.Path == "" {
		t.Fatalf("expected auto-selected path")
	}

	// Invalid.
	_, err = selectShell(ShellName("nope"))
	if err == nil || !strings.Contains(err.Error(), "invalid shell") {
		t.Fatalf("expected invalid shell error, got: %v", err)
	}
}

func TestDeriveExecArgs_UnixLoginFlag(t *testing.T) {
	if runtime.GOOS == GOOSWindows {
		t.Skip("unix-specific")
	}
	shPath := mustLookPath(t, "sh")

	sel := selectedShell{Name: ShellNameSh, Path: shPath}
	a := deriveExecArgs(sel, "echo hi", false)
	if len(a) < 3 || a[0] != shPath || a[1] != "-c" {
		t.Fatalf("unexpected args for login=false: %#v", a)
	}
	a = deriveExecArgs(sel, "echo hi", true)
	if len(a) < 3 || a[0] != shPath || a[1] != "-lc" {
		t.Fatalf("unexpected args for login=true: %#v", a)
	}
}

func TestClassifyWarnings(t *testing.T) {
	w := classifyWarnings("curl https://example.com")
	if !contains(w, "network_access") {
		t.Fatalf("expected network_access warning, got %#v", w)
	}

	w = classifyWarnings("git status")
	if !contains(w, "git_operation") {
		t.Fatalf("expected git_operation warning, got %#v", w)
	}

	w = classifyWarnings("rm -rf ./tmp")
	if !contains(w, "potentially_destructive") {
		t.Fatalf("expected potentially_destructive warning, got %#v", w)
	}
}

func TestCappedWriter_TruncatesAndCounts(t *testing.T) {
	w := newCappedWriter(1024)

	_, _ = w.Write([]byte(strings.Repeat("a", 600)))
	_, _ = w.Write([]byte(strings.Repeat("b", 600))) // total 1200 > 1024

	if w.TotalBytes() != 1200 {
		t.Fatalf("expected totalBytes 1200, got %d", w.TotalBytes())
	}
	if !w.Truncated() {
		t.Fatalf("expected truncated=true")
	}
	if got := len(w.Bytes()); got != 1024 {
		t.Fatalf("expected stored bytes len 1024, got %d", got)
	}
	b := w.Bytes()
	if len(b) == 0 || b[len(b)-1] != 'b' {
		t.Fatalf("expected tail capture ending with 'b'")
	}
}

func TestSafeUTF8_ReplacesInvalid(t *testing.T) {
	s := safeUTF8([]byte{0xff, 0xfe, 'a'})
	if !strings.Contains(s, "\uFFFD") {
		t.Fatalf("expected replacement char in %q", s)
	}
	if !strings.Contains(s, "a") {
		t.Fatalf("expected 'a' preserved in %q", s)
	}
}

func TestShellCommand_Run_CapturesStdoutStderr_ExitCode(t *testing.T) {
	if runtime.GOOS == GOOSWindows {
		t.Skip("unix command expectations")
	}
	st := newTestShellTool(t)

	out, err := st.Run(t.Context(), ShellCommandArgs{
		Shell:    ShellNameSh,
		Commands: []string{`printf '%s' hello; printf '%s' err_msg 1>&2`},
	})
	if err != nil {
		t.Fatalf("ShellCommand error: %v", err)
	}
	resp := decodeShellResponse(t, out)
	if len(resp.Results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(resp.Results))
	}
	r := resp.Results[0]
	if r.ExitCode != 0 {
		t.Fatalf("expected exitCode=0, got %d (stderr=%q)", r.ExitCode, r.Stderr)
	}
	if r.Stdout != "hello" {
		t.Fatalf("expected stdout=hello, got %q", r.Stdout)
	}
	if r.Stderr != "err_msg" {
		t.Fatalf("expected stderr=err_msg, got %q", r.Stderr)
	}
	if r.Login != false {
		t.Fatalf("expected login default false, got %v", r.Login)
	}
	if strings.TrimSpace(r.ShellPath) == "" {
		t.Fatalf("expected shellPath set")
	}
}

func TestShellCommand_ExitCode_NonZeroAndSignaled(t *testing.T) {
	if runtime.GOOS == GOOSWindows {
		t.Skip("unix-specific exit/signal expectations")
	}
	st := newTestShellTool(t)

	// Exit with explicit code.
	out, err := st.Run(t.Context(), ShellCommandArgs{
		Shell:    ShellNameSh,
		Commands: []string{`exit 7`},
	})
	if err != nil {
		t.Fatalf("ShellCommand error: %v", err)
	}
	resp := decodeShellResponse(t, out)
	if resp.Results[0].ExitCode != 7 {
		t.Fatalf("expected exitCode=7, got %d", resp.Results[0].ExitCode)
	}

	// Signal self with SIGKILL; expect 128+9=137 per unix convention in exitCodeFromProcessState.
	out, err = st.Run(t.Context(), ShellCommandArgs{
		Shell:    ShellNameSh,
		Commands: []string{`kill -9 $$`},
	})
	if err != nil {
		t.Fatalf("ShellCommand error: %v", err)
	}
	resp = decodeShellResponse(t, out)
	if resp.Results[0].ExitCode != 137 {
		t.Fatalf("expected exitCode=137, got %d (stderr=%q)", resp.Results[0].ExitCode, resp.Results[0].Stderr)
	}
}

func TestShellCommand_Timeout_SetsTimedOutAnd124(t *testing.T) {
	if runtime.GOOS == GOOSWindows {
		t.Skip("unix-specific sleep/timeout expectations")
	}
	st := newTestShellTool(t)

	to := int64(150) // ms
	out, err := st.Run(t.Context(), ShellCommandArgs{
		Shell:        ShellNameSh,
		TimeoutMS:    &to,
		Commands:     []string{`sleep 2`},
		Notes:        "timeout test",
		Login:        false,
		SessionID:    "",
		Workdir:      "",
		Env:          nil,
		CloseSession: false,
	})
	if err != nil {
		t.Fatalf("ShellCommand error: %v", err)
	}
	resp := decodeShellResponse(t, out)
	if len(resp.Results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(resp.Results))
	}
	r := resp.Results[0]
	if !r.TimedOut {
		t.Fatalf("expected TimedOut=true, got false (exitCode=%d, stderr=%q)", r.ExitCode, r.Stderr)
	}
	if r.ExitCode != 124 {
		t.Fatalf("expected exitCode=124 on timeout, got %d", r.ExitCode)
	}
}

func TestShellCommand_MaxOutput_Truncates(t *testing.T) {
	if runtime.GOOS == GOOSWindows {
		t.Skip("unix-specific sh loop expectations")
	}
	st := newTestShellTool(t)

	maxOut := int64(1024)
	out, err := st.Run(t.Context(), ShellCommandArgs{
		Shell:           ShellNameSh,
		MaxOutputLength: &maxOut,
		Commands: []string{
			// Print 3000 'a' characters using POSIX sh arithmetic.
			`i=0; while [ $i -lt 3000 ]; do printf a; i=$((i+1)); done`,
		},
	})
	if err != nil {
		t.Fatalf("ShellCommand error: %v", err)
	}
	resp := decodeShellResponse(t, out)
	r := resp.Results[0]

	if !r.StdoutTruncated {
		t.Fatalf("expected stdout truncated")
	}
	if got := int64(len(r.Stdout)); got != maxOut {
		t.Fatalf("expected captured stdout len=%d, got %d", maxOut, got)
	}
	if r.StdoutBytes <= maxOut {
		t.Fatalf("expected stdoutBytes > maxOut (%d), got %d", maxOut, r.StdoutBytes)
	}
}

func TestShellCommand_DangerousRejected_BeforeExec(t *testing.T) {
	if runtime.GOOS == GOOSWindows {
		t.Skip("unix-specific dangerous patterns")
	}
	st := newTestShellTool(t)

	_, err := st.Run(t.Context(), ShellCommandArgs{
		Shell:    ShellNameSh,
		Commands: []string{`rm -rf /`},
	})
	if err == nil {
		t.Fatalf("expected error")
	}
	if !strings.Contains(strings.ToLower(err.Error()), "blocked") {
		t.Fatalf("expected blocked error, got: %v", err)
	}
}

func TestShellCommand_Session_PersistsWorkdirAndEnv_UpdateRestartClose(t *testing.T) {
	if runtime.GOOS == GOOSWindows {
		t.Skip("unix-specific")
	}
	st := newTestShellTool(t)

	td := t.TempDir()

	// 1) Create session, set workdir and env, and run "pwd".
	out, err := st.Run(t.Context(), ShellCommandArgs{
		CreateSession: true,
		Shell:         ShellNameSh,
		Workdir:       td,
		Env:           map[string]string{"FOO": "bar"},
		Commands:      []string{"pwd"},
	})
	if err != nil {
		t.Fatalf("ShellCommand(createSession) error: %v", err)
	}
	resp := decodeShellResponse(t, out)

	if resp.SessionID == "" {
		t.Fatalf("expected sessionID returned")
	}
	if filepath.Clean(resp.Workdir) != filepath.Clean(td) {
		t.Fatalf("expected resp.Workdir=%q, got %q", td, resp.Workdir)
	}
	if len(resp.Results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(resp.Results))
	}
	r0 := resp.Results[0]
	if filepath.Clean(strings.TrimSpace(r0.Stdout)) != filepath.Clean(td) {
		t.Fatalf("expected pwd=%q, got %q", td, r0.Stdout)
	}

	sid := resp.SessionID

	// 2) Verify env persists without passing Env.
	out, err = st.Run(t.Context(), ShellCommandArgs{
		SessionID: sid,
		Shell:     ShellNameSh,
		Commands:  []string{`printf '%s' "$FOO"`},
	})
	if err != nil {
		t.Fatalf("ShellCommand(session reuse) error: %v", err)
	}
	resp = decodeShellResponse(t, out)
	if resp.Results[0].Stdout != "bar" {
		t.Fatalf("expected FOO=bar, got %q (stderr=%q)", resp.Results[0].Stdout, resp.Results[0].Stderr)
	}
	if filepath.Clean(resp.Workdir) != filepath.Clean(td) {
		t.Fatalf("expected workdir persisted as %q, got %q", td, resp.Workdir)
	}

	// 3) Update session env by providing Env; should persist for subsequent calls.
	out, err = st.Run(t.Context(), ShellCommandArgs{
		SessionID: sid,
		Shell:     ShellNameSh,
		Env:       map[string]string{"FOO": "baz"},
		Commands:  []string{`printf '%s' "$FOO"`},
	})
	if err != nil {
		t.Fatalf("ShellCommand(session update env) error: %v", err)
	}
	resp = decodeShellResponse(t, out)
	if resp.Results[0].Stdout != "baz" {
		t.Fatalf("expected FOO=baz, got %q", resp.Results[0].Stdout)
	}

	out, err = st.Run(t.Context(), ShellCommandArgs{
		SessionID: sid,
		Shell:     ShellNameSh,
		Commands:  []string{`printf '%s' "$FOO"`},
	})
	if err != nil {
		t.Fatalf("ShellCommand(session verify env persisted) error: %v", err)
	}
	resp = decodeShellResponse(t, out)
	if resp.Results[0].Stdout != "baz" {
		t.Fatalf("expected FOO=baz persisted, got %q", resp.Results[0].Stdout)
	}

	// 4) Restart session: clears workdir/env; should fall back to process cwd.
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatalf("Getwd: %v", err)
	}
	out, err = st.Run(t.Context(), ShellCommandArgs{
		SessionID:      sid,
		RestartSession: true,
		Shell:          ShellNameSh,
		Commands:       []string{"pwd; printf '%s' \"$FOO\""},
	})
	if err != nil {
		t.Fatalf("ShellCommand(restartSession) error: %v", err)
	}
	resp = decodeShellResponse(t, out)
	if filepath.Clean(resp.Workdir) != filepath.Clean(cwd) {
		t.Fatalf("expected workdir reset to cwd=%q, got %q", cwd, resp.Workdir)
	}
	// After restart, FOO should be empty (unless inherited from process env; to avoid flake,
	// assert only that it is not "baz").
	if strings.Contains(resp.Results[0].Stdout, "baz") {
		t.Fatalf("expected FOO cleared (not baz), got stdout=%q", resp.Results[0].Stdout)
	}

	// 5) Close session; should return message and then be unusable.
	out, err = st.Run(t.Context(), ShellCommandArgs{
		SessionID:      sid,
		CloseSession:   true,
		Commands:       nil,
		Shell:          ShellNameSh,
		RestartSession: false,
	})
	if err != nil {
		t.Fatalf("ShellCommand(closeSession) error: %v", err)
	}
	resp = decodeShellResponse(t, out)
	if !strings.Contains(resp.Message, "session closed") {
		t.Fatalf("expected session closed message, got %q", resp.Message)
	}
	if len(resp.Results) != 0 {
		t.Fatalf("expected no results on closeSession, got %d", len(resp.Results))
	}

	_, err = st.Run(t.Context(), ShellCommandArgs{
		SessionID: sid,
		Shell:     ShellNameSh,
		Commands:  []string{"echo hi"},
	})
	if err == nil || !strings.Contains(err.Error(), "unknown sessionID") {
		t.Fatalf("expected unknown sessionID after close, got: %v", err)
	}
}

func TestUnixSpecific_ProcessGroupAndExitCodeHelpers(t *testing.T) {
	if runtime.GOOS == GOOSWindows {
		t.Skip("unix-specific")
	}

	// "configureProcessGroup" should set Setpgid=true.
	cmd := exec.CommandContext(t.Context(), "sh", "-c", "exit 0")
	configureProcessGroup(cmd)
	if cmd.SysProcAttr == nil {
		t.Fatalf("expected SysProcAttr set")
	}

	// "exitCodeFromProcessState" should reflect normal exit and signaled exit.
	c := exec.CommandContext(t.Context(), "sh", "-c", "exit 9")
	if err := c.Run(); err == nil {
		t.Fatalf("expected non-zero exit")
	}
	if got := exitCodeFromProcessState(c.ProcessState); got != 9 {
		t.Fatalf("expected exit code 9, got %d", got)
	}

	c = exec.CommandContext(t.Context(), "sh", "-c", "kill -9 $$")
	_ = c.Run() // expect error
	if got := exitCodeFromProcessState(c.ProcessState); got != 137 {
		t.Fatalf("expected signaled exit code 137, got %d", got)
	}

	// "killProcessGroup" should be safe on nils.
	killProcessGroup(nil)
	killProcessGroup(&exec.Cmd{})
}

func TestToolJSONText_ProducesTextUnion(t *testing.T) {
	type X struct {
		A string `json:"a"`
	}
	out, err := toolJSONText(X{A: "b"})
	if err != nil {
		t.Fatalf("toolJSONText error: %v", err)
	}
	if len(out) != 1 {
		t.Fatalf("expected 1 output, got %d", len(out))
	}
	if out[0].Kind != spec.ToolStoreOutputKindText || out[0].TextItem == nil {
		t.Fatalf("expected text output union")
	}
	var x X
	if err := json.Unmarshal([]byte(out[0].TextItem.Text), &x); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if x.A != "b" {
		t.Fatalf("expected a=b, got %q", x.A)
	}
}

func TestShellCommand_ContextCanceledEarly(t *testing.T) {
	st := newTestShellTool(t)

	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	_, err := st.Run(ctx, ShellCommandArgs{
		Commands: []string{"echo hi"},
	})
	if err == nil {
		t.Fatalf("expected context error")
	}
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("expected context.Canceled, got %v", err)
	}
}

func newTestShellTool(t *testing.T, opts ...ShellToolOption) *ShellTool {
	t.Helper()

	st, err := NewShellTool(opts...)
	if err != nil {
		t.Fatalf("NewShellTool: %v", err)
	}
	return st
}

func decodeShellResponse(t *testing.T, out []spec.ToolStoreOutputUnion) ShellCommandResponse {
	t.Helper()

	if len(out) != 1 {
		t.Fatalf("expected 1 output item, got %d", len(out))
	}
	if out[0].Kind != spec.ToolStoreOutputKindText || out[0].TextItem == nil {
		t.Fatalf("expected text output, got kind=%q textItem=%v", out[0].Kind, out[0].TextItem)
	}

	var resp ShellCommandResponse
	if err := json.Unmarshal([]byte(out[0].TextItem.Text), &resp); err != nil {
		t.Fatalf("failed to unmarshal response JSON: %v\njson=%s", err, out[0].TextItem.Text)
	}
	return resp
}

func mustLookPath(t *testing.T, name string) string {
	t.Helper()
	p, err := exec.LookPath(name)
	if err != nil || strings.TrimSpace(p) == "" {
		t.Skipf("missing dependency on PATH: %s (%v)", name, err)
	}
	return p
}

func contains(ss []string, want string) bool {
	return slices.Contains(ss, want)
}

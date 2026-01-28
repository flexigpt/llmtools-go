package shelltool

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func TestShellCommand_AutoSession_DoesNotLeakOnEarlyError(t *testing.T) {
	st := newTestShellTool(t)

	// Force an early validation error by omitting commands.
	_, err := st.Run(t.Context(), ShellCommandArgs{
		Commands: nil,
	})
	if err == nil {
		t.Fatalf("expected error")
	}

	// Ensure no leaked session.
	if got := st.sessions.sizeForTest(); got != 0 {
		t.Fatalf("expected no sessions left, found %d", got)
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

func TestPolicy_EffectiveTimeout_UsesDefaultAndClampsToHardMax(t *testing.T) {
	p := ShellCommandPolicy{}
	if got := effectiveTimeout(p); got != DefaultTimeout {
		t.Fatalf("expected DefaultTimeout=%v got %v", DefaultTimeout, got)
	}

	p.Timeout = 999 * time.Hour
	if got := effectiveTimeout(p); got != HardMaxTimeout {
		t.Fatalf("expected clamp to HardMaxTimeout=%v got %v", HardMaxTimeout, got)
	}
}

func TestPolicy_EffectiveMaxOutputBytes_UsesDefaultAndClamps(t *testing.T) {
	p := ShellCommandPolicy{}
	if got := effectiveMaxOutputBytes(p); got != DefaultMaxOutputBytes {
		t.Fatalf("expected DefaultMaxOutputBytes=%d got %d", DefaultMaxOutputBytes, got)
	}

	p.MaxOutputBytes = 1
	if got := effectiveMaxOutputBytes(p); got != MinOutputBytes {
		t.Fatalf("expected clamp to MinOutputBytes=%d got %d", MinOutputBytes, got)
	}

	p.MaxOutputBytes = 1 << 62
	if got := effectiveMaxOutputBytes(p); got != min(HardMaxOutputBytes, int64(^uint(0)>>1)) {
		// The implementation clamps to HardMaxOutputBytes and also to MaxInt.
		t.Fatalf("expected clamp to hard max, got %d", got)
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

func TestEffectiveEnv_OrderIsDeterministicNonDecreasingByCanonicalKey(t *testing.T) {
	// We can't control all of os.Environ(), but we can assert monotonic ordering.
	t.Setenv("ZZZ_TEST_ENV", "1")
	t.Setenv("AAA_TEST_ENV", "2")

	env, err := effectiveEnv(nil, map[string]string{"MMM_TEST_ENV": "3"})
	if err != nil {
		t.Fatalf("effectiveEnv error: %v", err)
	}
	var prev string
	for i, kv := range env {
		k, _, ok := strings.Cut(kv, "=")
		if !ok {
			t.Fatalf("bad env entry %q", kv)
		}
		ck := canonicalEnvKey(k)
		if i > 0 && ck < prev {
			t.Fatalf("env not sorted at %d: %q < %q", i, ck, prev)
		}
		prev = ck
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
	resp := out
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
	resp := out
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
	resp = out
	if resp.Results[0].ExitCode != 137 {
		t.Fatalf("expected exitCode=137, got %d (stderr=%q)", resp.Results[0].ExitCode, resp.Results[0].Stderr)
	}
}

func TestShellCommand_Timeout_SetsTimedOutAnd124(t *testing.T) {
	if runtime.GOOS == GOOSWindows {
		t.Skip("unix-specific sleep/timeout expectations")
	}
	p := DefaultShellCommandPolicy
	p.Timeout = 150 * time.Millisecond
	st := newTestShellTool(t, WithShellCommandPolicy(p))

	out, err := st.Run(t.Context(), ShellCommandArgs{
		Shell:     ShellNameSh,
		Commands:  []string{`sleep 2`},
		SessionID: "",
		Workdir:   "",
		Env:       nil,
	})
	if err != nil {
		t.Fatalf("ShellCommand error: %v", err)
	}
	resp := out
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
	p := DefaultShellCommandPolicy
	p.MaxOutputBytes = 1024
	st := newTestShellTool(t, WithShellCommandPolicy(p))

	out, err := st.Run(t.Context(), ShellCommandArgs{
		Shell: ShellNameSh,

		Commands: []string{
			// Print 3000 'a' characters using POSIX sh arithmetic.
			`i=0; while [ $i -lt 3000 ]; do printf a; i=$((i+1)); done`,
		},
	})
	if err != nil {
		t.Fatalf("ShellCommand error: %v", err)
	}
	resp := out
	r := resp.Results[0]

	if !r.StdoutTruncated {
		t.Fatalf("expected stdout truncated")
	}
	if got := int64(len(r.Stdout)); got != 1024 {
		t.Fatalf("expected captured stdout len=1024, got %d", got)
	}
}

func TestShellCommand_ExecuteParallelly_False_StopsOnFirstError(t *testing.T) {
	if runtime.GOOS == GOOSWindows {
		t.Skip("unix-specific")
	}
	st := newTestShellTool(t)

	out, err := st.Run(t.Context(), ShellCommandArgs{
		Shell: ShellNameSh,
		Commands: []string{
			`exit 7`,
			`echo should_not_run`,
		},
		ExecuteParallel: false,
	})
	if err != nil {
		t.Fatalf("Run error: %v", err)
	}
	resp := out
	if len(resp.Results) != 1 {
		t.Fatalf("expected 1 result due to stop-on-error, got %d", len(resp.Results))
	}
	if resp.Results[0].ExitCode != 7 {
		t.Fatalf("expected exitCode=7 got %d", resp.Results[0].ExitCode)
	}
}

func TestShellCommand_ExecuteParallelly_True_RunsAllCommandsEvenIfError(t *testing.T) {
	if runtime.GOOS == GOOSWindows {
		t.Skip("unix-specific")
	}
	st := newTestShellTool(t)

	out, err := st.Run(t.Context(), ShellCommandArgs{
		Shell: ShellNameSh,
		Commands: []string{
			`exit 7`,
			`echo ok`,
		},
		ExecuteParallel: true,
	})
	if err != nil {
		t.Fatalf("Run error: %v", err)
	}
	resp := out
	if len(resp.Results) != 2 {
		t.Fatalf("expected 2 results, got %d", len(resp.Results))
	}
	if resp.Results[0].ExitCode != 7 {
		t.Fatalf("expected first exitCode=7 got %d", resp.Results[0].ExitCode)
	}
	if strings.TrimSpace(resp.Results[1].Stdout) != "ok" {
		t.Fatalf("expected second stdout=ok, got %q", resp.Results[1].Stdout)
	}
}

func TestShellCommand_RejectsNULInCommand(t *testing.T) {
	st := newTestShellTool(t)
	_, err := st.Run(t.Context(), ShellCommandArgs{
		Commands: []string{"echo hi\x00there"},
	})
	if err == nil || !strings.Contains(strings.ToLower(err.Error()), "nul") {
		t.Fatalf("expected NUL error, got %v", err)
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

func TestShellCommand_MaxCommands_PolicyLimit(t *testing.T) {
	st := newTestShellTool(t, WithShellCommandPolicy(ShellCommandPolicy{
		MaxCommands: 1,
	}))
	_, err := st.Run(t.Context(), ShellCommandArgs{
		Commands: []string{"echo a", "echo b"},
	})
	if err == nil || !strings.Contains(err.Error(), "too many commands") {
		t.Fatalf("expected too many commands error, got %v", err)
	}
}

func TestShellCommand_MaxCommandLength_PolicyLimit(t *testing.T) {
	st := newTestShellTool(t, WithShellCommandPolicy(ShellCommandPolicy{
		MaxCommandLength: 5,
	}))
	_, err := st.Run(t.Context(), ShellCommandArgs{
		Commands: []string{"echo_12345"},
	})
	if err == nil || !strings.Contains(err.Error(), "command too long") {
		t.Fatalf("expected command too long error, got %v", err)
	}
}

func TestSessions_TTL_Evicts(t *testing.T) {
	if runtime.GOOS == GOOSWindows {
		t.Skip("timing tests may be flaky on some windows CI")
	}
	st := newTestShellTool(t,
		WithShellSessionTTL(150*time.Millisecond),
		WithShellMaxSessions(100),
	)

	out, err := st.Run(t.Context(), ShellCommandArgs{
		Shell:    ShellNameSh,
		Commands: []string{"echo hi"},
	})
	if err != nil {
		t.Fatalf("Run error: %v", err)
	}
	resp := out
	if resp.SessionID == "" {
		t.Fatalf("expected session id")
	}

	time.Sleep(250 * time.Millisecond)

	_, err = st.Run(t.Context(), ShellCommandArgs{
		SessionID: resp.SessionID,
		Shell:     ShellNameSh,
		Commands:  []string{"echo hi"},
	})
	if err == nil || !strings.Contains(err.Error(), "unknown sessionID") {
		t.Fatalf("expected unknown sessionID after TTL eviction, got %v", err)
	}
}

func TestSessions_LRU_MaxSessions_EvictsOldest(t *testing.T) {
	st := newTestShellTool(t, WithShellMaxSessions(1))

	out1, err := st.Run(t.Context(), ShellCommandArgs{Commands: []string{"echo a"}})
	if err != nil {
		t.Fatalf("Run1 error: %v", err)
	}
	sid1 := out1.SessionID

	_, err = st.Run(t.Context(), ShellCommandArgs{Commands: []string{"echo b"}})
	if err != nil {
		t.Fatalf("Run2 error: %v", err)
	}

	_, err = st.Run(t.Context(), ShellCommandArgs{
		SessionID: sid1,
		Commands:  []string{"echo should_fail"},
	})
	if err == nil || !strings.Contains(err.Error(), "unknown sessionID") {
		t.Fatalf("expected unknown sessionID after LRU eviction, got %v", err)
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
		Shell:    ShellNameSh,
		Workdir:  td,
		Env:      map[string]string{"FOO": "bar"},
		Commands: []string{"pwd"},
	})
	if err != nil {
		t.Fatalf("ShellCommand(auto session) error: %v", err)
	}
	resp := out

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
	resp = out
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
	resp = out
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
	resp = out
	if resp.Results[0].Stdout != "baz" {
		t.Fatalf("expected FOO=baz persisted, got %q", resp.Results[0].Stdout)
	}

	// 4) Start a NEW session by omitting sessionID; should not inherit prior session's workdir/env.
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatalf("Getwd: %v", err)
	}
	out, err = st.Run(t.Context(), ShellCommandArgs{
		Shell:    ShellNameSh,
		Commands: []string{"pwd; printf '%s' \"$FOO\""},
	})
	if err != nil {
		t.Fatalf("ShellCommand(new session) error: %v", err)
	}
	resp = out
	if filepath.Clean(resp.Workdir) != filepath.Clean(cwd) {
		t.Fatalf("expected new session workdir reset to cwd=%q, got %q", cwd, resp.Workdir)
	}
	// After restart, FOO should be empty (unless inherited from process env; to avoid flake,
	// assert only that it is not "baz").
	if strings.Contains(resp.Results[0].Stdout, "baz") {
		t.Fatalf("expected new session not to have baz, got stdout=%q", resp.Results[0].Stdout)
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

func TestShellCommand_Blocklist_DefaultBlocksRMAndCurl(t *testing.T) {
	st := newTestShellTool(t)

	_, err := st.Run(t.Context(), ShellCommandArgs{
		Commands: []string{`rm foo`},
	})
	if err == nil || !strings.Contains(strings.ToLower(err.Error()), "blocked") {
		t.Fatalf("expected rm to be blocked, got %v", err)
	}

	_, err = st.Run(t.Context(), ShellCommandArgs{
		Commands: []string{`curl https://example.com`},
	})
	if err == nil || !strings.Contains(strings.ToLower(err.Error()), "blocked") {
		t.Fatalf("expected curl to be blocked, got %v", err)
	}
}

func TestShellCommand_Blocklist_NotOverridableByAllowDangerous(t *testing.T) {
	p := DefaultShellCommandPolicy
	p.AllowDangerous = true
	st := newTestShellTool(t, WithShellCommandPolicy(p))

	_, err := st.Run(t.Context(), ShellCommandArgs{
		Commands: []string{`rm foo`},
	})
	if err == nil || !strings.Contains(strings.ToLower(err.Error()), "blocked") {
		t.Fatalf("expected rm to be blocked even with AllowDangerous=true, got %v", err)
	}
}

func TestShellCommand_Blocklist_AdditionalBlocks(t *testing.T) {
	st := newTestShellTool(t, WithShellBlockedCommands([]string{"echo"}))

	_, err := st.Run(t.Context(), ShellCommandArgs{
		Commands: []string{`echo hi`},
	})
	if err == nil || !strings.Contains(strings.ToLower(err.Error()), "blocked") {
		t.Fatalf("expected echo to be blocked via additional blocklist, got %v", err)
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

func mustLookPath(t *testing.T, name string) string {
	t.Helper()
	p, err := exec.LookPath(name)
	if err != nil || strings.TrimSpace(p) == "" {
		t.Skipf("missing dependency on PATH: %s (%v)", name, err)
	}
	return p
}

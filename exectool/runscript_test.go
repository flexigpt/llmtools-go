package exectool

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/flexigpt/llmtools-go/internal/toolutil"
)

func TestExtAllowed(t *testing.T) {
	cases := []struct {
		name    string
		ext     string
		allowed []string
		want    bool
	}{
		{name: "empty_allowed_false", ext: ".sh", allowed: nil, want: false},
		{name: "match_exact", ext: ".sh", allowed: []string{".sh"}, want: true},
		{name: "match_case_insensitive_and_trim", ext: ".Sh", allowed: []string{"  .sH "}, want: true},
		{name: "no_match", ext: ".py", allowed: []string{".sh"}, want: false},
		{name: "ext_empty_allowed_only_if_empty_entry_present", ext: "", allowed: []string{".sh", ""}, want: true},
		{name: "ext_empty_not_allowed_without_empty_entry", ext: "", allowed: []string{".sh"}, want: false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := extAllowed(tc.ext, tc.allowed); got != tc.want {
				t.Fatalf("got %v want %v", got, tc.want)
			}
		})
	}
}

func TestLookupInterpreter(t *testing.T) {
	pol := RunScriptPolicy{
		InterpreterByExtension: map[string]RunScriptInterpreter{
			".sh": {Shell: ShellNameSh, Mode: RunScriptModeShell},
			"":    {Shell: ShellNameSh, Mode: RunScriptModeShell},
		},
	}

	cases := []struct {
		name    string
		pol     RunScriptPolicy
		ext     string
		wantOK  bool
		wantMod RunScriptMode
	}{
		{name: "nil_map_no", pol: RunScriptPolicy{InterpreterByExtension: nil}, ext: ".sh", wantOK: false},
		{name: "exact_match_lowercase", pol: pol, ext: ".sh", wantOK: true, wantMod: RunScriptModeShell},
		{name: "exact_match_casefold", pol: pol, ext: ".SH", wantOK: true, wantMod: RunScriptModeShell},
		{name: "fallback_empty_key", pol: pol, ext: ".unknown", wantOK: true, wantMod: RunScriptModeShell},
		{
			name: "no_match_no_fallback",
			pol: RunScriptPolicy{
				InterpreterByExtension: map[string]RunScriptInterpreter{
					".sh": {Shell: ShellNameSh, Mode: RunScriptModeShell},
				},
			},
			ext:    ".py",
			wantOK: false,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := lookupInterpreter(tc.pol, tc.ext)
			if ok != tc.wantOK {
				t.Fatalf("ok got %v want %v (interp=%+v)", ok, tc.wantOK, got)
			}
			if ok && got.Mode != tc.wantMod {
				t.Fatalf("mode got %q want %q", got.Mode, tc.wantMod)
			}
		})
	}
}

func TestRunScript_ValidationsAndResolution(t *testing.T) {
	td := t.TempDir()
	outside := t.TempDir()

	// Create a simple shell script.
	scriptDir := filepath.Join(td, "scripts")
	if err := os.MkdirAll(scriptDir, 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	scriptPath := filepath.Join(scriptDir, "hello.sh")
	if err := os.WriteFile(scriptPath, []byte("#!/bin/sh\nprintf '%s' \"hello\"\n"), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	// Another script for direct execution with execute bit.
	execScriptPath := filepath.Join(scriptDir, "exec_direct.sh")
	if err := os.WriteFile( //nolint:gosec // Execution script.
		execScriptPath,
		[]byte("#!/bin/sh\nprintf '%s' \"direct\"\n"),
		0o700,
	); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	// A script that outputs lots.
	verbosePath := filepath.Join(scriptDir, "verbose.sh")
	if err := os.WriteFile(
		verbosePath,
		[]byte("#!/bin/sh\ni=0; while [ $i -lt 3000 ]; do printf a; i=$((i+1)); done\n"),
		0o600,
	); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	// A script that sleeps.
	sleepPath := filepath.Join(scriptDir, "sleep.sh")
	if err := os.WriteFile(sleepPath, []byte("#!/bin/sh\nsleep 2\n"), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	// Ensure we can pick some shell on this platform for cases that reach execution.
	requireShell := func(t *testing.T) {
		t.Helper()
		if _, err := selectShell(t.Context(), ShellNameAuto); err != nil {
			t.Skipf("no suitable shell found: %v", err)
		}
	}

	cases := []struct {
		name        string
		opts        []ExecToolOption
		args        RunScriptArgs
		needShell   bool
		wantErrSubs []string
		wantOK      bool

		wantStdout       string
		wantExitCode     int
		wantTimedOut     bool
		wantTrunc        bool
		wantPathEndsWith string
	}{
		{
			name:        "missing_path",
			args:        RunScriptArgs{Path: "   "},
			wantErrSubs: []string{"path is required"},
		},
		{
			name:        "workdir_outside_allowed_roots",
			opts:        []ExecToolOption{WithAllowedRoots([]string{td})},
			args:        RunScriptArgs{Path: scriptPath, WorkDir: outside},
			wantErrSubs: []string{"outside allowed roots"},
		},
		{
			name: "disallowed_extension",
			opts: []ExecToolOption{
				WithAllowedRoots([]string{td}),
				WithRunScriptPolicy(RunScriptPolicy{
					AllowedExtensions: []string{".sh"},
					InterpreterByExtension: map[string]RunScriptInterpreter{
						".sh": {Shell: ShellNameSh, Mode: RunScriptModeShell},
					},
				}),
			},
			args:        RunScriptArgs{Path: filepath.Join(scriptDir, "nope.txt")},
			wantErrSubs: []string{"stat", "file"}, // RequireExistingRegularFileNoSymlink likely fails first.
		},
		{
			name: "extension_not_allowed_even_if_mapping_exists",
			opts: []ExecToolOption{
				WithAllowedRoots([]string{td}),
				WithRunScriptPolicy(RunScriptPolicy{
					AllowedExtensions: []string{".py"}, // not .sh
					InterpreterByExtension: map[string]RunScriptInterpreter{
						".sh": {Shell: ShellNameSh, Mode: RunScriptModeShell},
					},
				}),
			},
			args:        RunScriptArgs{Path: scriptPath},
			wantErrSubs: []string{"extension", "not allowed"},
		},
		{
			name: "no_interpreter_mapping",
			opts: []ExecToolOption{
				WithAllowedRoots([]string{td}),
				WithRunScriptPolicy(RunScriptPolicy{
					AllowedExtensions:      []string{".sh"},
					InterpreterByExtension: map[string]RunScriptInterpreter{
						// "empty" -> missing .sh mapping.
					},
				}),
			},
			args:        RunScriptArgs{Path: scriptPath},
			wantErrSubs: []string{"no interpreter mapping"},
		},
		{
			name: "invalid_env",
			opts: []ExecToolOption{
				WithAllowedRoots([]string{td}),
			},
			args:        RunScriptArgs{Path: scriptPath, Env: map[string]string{"": "1"}},
			wantErrSubs: []string{"env"},
		},
		{
			name: "too_many_args",
			opts: []ExecToolOption{
				WithAllowedRoots([]string{td}),
				WithRunScriptPolicy(RunScriptPolicy{
					AllowedExtensions: []string{".sh"},
					InterpreterByExtension: map[string]RunScriptInterpreter{
						".sh": {Shell: ShellNameSh, Mode: RunScriptModeShell},
					},
					MaxArgs: 1,
				}),
			},
			args:        RunScriptArgs{Path: scriptPath, Args: []string{"a", "b"}},
			wantErrSubs: []string{"too many args"},
		},
		{
			name: "arg_contains_nul",
			opts: []ExecToolOption{
				WithAllowedRoots([]string{td}),
			},
			args:        RunScriptArgs{Path: scriptPath, Args: []string{"a\x00b"}},
			wantErrSubs: []string{"nul"},
		},
		{
			name: "arg_too_long",
			opts: []ExecToolOption{
				WithAllowedRoots([]string{td}),
				WithRunScriptPolicy(RunScriptPolicy{
					AllowedExtensions: []string{".sh"},
					InterpreterByExtension: map[string]RunScriptInterpreter{
						".sh": {Shell: ShellNameSh, Mode: RunScriptModeShell},
					},
					MaxArgBytes: 3,
				}),
			},
			args:        RunScriptArgs{Path: scriptPath, Args: []string{"toolong"}},
			wantErrSubs: []string{"too long"},
		},
		{
			name: "relative_script_resolves_against_workdir_when_workdir_provided",
			opts: []ExecToolOption{
				WithAllowedRoots([]string{td}),
				WithWorkBaseDir(td),
				WithRunScriptPolicy(RunScriptPolicy{
					AllowedExtensions: []string{".sh"},
					InterpreterByExtension: map[string]RunScriptInterpreter{
						".sh": {Shell: ShellNameSh, Mode: RunScriptModeShell},
					},
				}),
			},
			args: RunScriptArgs{
				Path:    "hello.sh",
				WorkDir: scriptDir,
			},
			needShell:        true,
			wantOK:           true,
			wantStdout:       "hello",
			wantExitCode:     0,
			wantPathEndsWith: string(filepath.Separator) + "hello.sh",
		},
		{
			name: "invalid_interpreter_mode",
			opts: []ExecToolOption{
				WithAllowedRoots([]string{td}),
				WithRunScriptPolicy(RunScriptPolicy{
					AllowedExtensions: []string{".sh"},
					InterpreterByExtension: map[string]RunScriptInterpreter{
						".sh": {Shell: ShellNameSh, Mode: RunScriptMode("nope")},
					},
				}),
			},
			args:        RunScriptArgs{Path: scriptPath},
			wantErrSubs: []string{"invalid mode"},
		},
		{
			name: "interpreter_mode_requires_command",
			opts: []ExecToolOption{
				WithAllowedRoots([]string{td}),
				WithRunScriptPolicy(RunScriptPolicy{
					AllowedExtensions: []string{".sh"},
					InterpreterByExtension: map[string]RunScriptInterpreter{
						".sh": {Shell: ShellNameSh, Mode: RunScriptModeInterpreter, Command: "   "},
					},
				}),
			},
			args:        RunScriptArgs{Path: scriptPath},
			wantErrSubs: []string{"empty command"},
		},

		// Successful execution (Unix-focused semantics).
		{
			name: "mode_shell_happy_path",
			opts: []ExecToolOption{
				WithAllowedRoots([]string{td}),
				WithRunScriptPolicy(RunScriptPolicy{
					AllowedExtensions: []string{".sh"},
					InterpreterByExtension: map[string]RunScriptInterpreter{
						".sh": {Shell: ShellNameSh, Mode: RunScriptModeShell},
					},
				}),
			},
			args:             RunScriptArgs{Path: scriptPath, WorkDir: scriptDir},
			needShell:        true,
			wantOK:           true,
			wantStdout:       "hello",
			wantExitCode:     0,
			wantPathEndsWith: string(filepath.Separator) + "hello.sh",
		},
		{
			name: "mode_direct_requires_executable_script",
			opts: []ExecToolOption{
				WithAllowedRoots([]string{td}),
				WithRunScriptPolicy(RunScriptPolicy{
					AllowedExtensions: []string{".sh"},
					InterpreterByExtension: map[string]RunScriptInterpreter{
						".sh": {Shell: ShellNameSh, Mode: RunScriptModeDirect},
					},
				}),
			},
			args:             RunScriptArgs{Path: execScriptPath, WorkDir: scriptDir},
			needShell:        true,
			wantOK:           true,
			wantStdout:       "direct",
			wantExitCode:     0,
			wantPathEndsWith: string(filepath.Separator) + "exec_direct.sh",
		},
		{
			name: "max_output_truncates_stdout",
			opts: []ExecToolOption{
				WithAllowedRoots([]string{td}),
				WithRunScriptPolicy(RunScriptPolicy{
					AllowedExtensions: []string{".sh"},
					InterpreterByExtension: map[string]RunScriptInterpreter{
						".sh": {Shell: ShellNameSh, Mode: RunScriptModeShell},
					},
					ExecutionPolicy: ExecutionPolicy{MaxOutputBytes: 1024},
				}),
			},
			args:             RunScriptArgs{Path: verbosePath, WorkDir: scriptDir},
			needShell:        true,
			wantOK:           true,
			wantExitCode:     0,
			wantTrunc:        true,
			wantPathEndsWith: string(filepath.Separator) + "verbose.sh",
		},
		{
			name: "timeout_sets_timed_out_and_124",
			opts: []ExecToolOption{
				WithAllowedRoots([]string{td}),
				WithRunScriptPolicy(RunScriptPolicy{
					AllowedExtensions: []string{".sh"},
					InterpreterByExtension: map[string]RunScriptInterpreter{
						".sh": {Shell: ShellNameSh, Mode: RunScriptModeShell},
					},
					ExecutionPolicy: ExecutionPolicy{Timeout: 150 * time.Millisecond},
				}),
			},
			args:             RunScriptArgs{Path: sleepPath, WorkDir: scriptDir},
			needShell:        true,
			wantOK:           true,
			wantExitCode:     124,
			wantTimedOut:     true,
			wantPathEndsWith: string(filepath.Separator) + "sleep.sh",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			et, err := NewExecTool(tc.opts...)
			if err != nil {
				if len(tc.wantErrSubs) > 0 {
					low := strings.ToLower(err.Error())
					for _, sub := range tc.wantErrSubs {
						if !strings.Contains(low, strings.ToLower(sub)) {
							t.Fatalf("error %q does not contain %q", err.Error(), sub)
						}
					}
					return
				}
				t.Fatalf("NewExecTool: %v", err)
			}

			if tc.needShell {
				requireShell(t)

				// Skip direct-exec case on Windows unless you specifically adapt to PS1.
				if runtime.GOOS == toolutil.GOOSWindows && strings.Contains(tc.name, "mode_direct") {
					t.Skip("direct shell-script execution test is unix-focused")
				}
			}

			got, err := et.RunScript(t.Context(), tc.args)

			if len(tc.wantErrSubs) > 0 {
				if err == nil {
					t.Fatalf("expected error")
				}
				low := strings.ToLower(err.Error())
				for _, sub := range tc.wantErrSubs {
					if !strings.Contains(low, strings.ToLower(sub)) {
						t.Fatalf("error %q does not contain %q", err.Error(), sub)
					}
				}
				return
			}

			if err != nil {
				t.Fatalf("RunScript error: %v", err)
			}
			if !tc.wantOK {
				t.Fatalf("expected not-ok case to have errors configured")
			}
			if got == nil {
				t.Fatalf("expected result")
			}
			if tc.wantPathEndsWith != "" && !strings.HasSuffix(got.Path, tc.wantPathEndsWith) {
				t.Fatalf("Path got %q want suffix %q", got.Path, tc.wantPathEndsWith)
			}
			if got.ExitCode != tc.wantExitCode {
				t.Fatalf("ExitCode got %d want %d (stderr=%q)", got.ExitCode, tc.wantExitCode, got.Stderr)
			}
			if tc.wantStdout != "" && got.Stdout != tc.wantStdout {
				t.Fatalf("Stdout got %q want %q", got.Stdout, tc.wantStdout)
			}
			if got.TimedOut != tc.wantTimedOut {
				t.Fatalf(
					"TimedOut got %v want %v (exitCode=%d stderr=%q)",
					got.TimedOut,
					tc.wantTimedOut,
					got.ExitCode,
					got.Stderr,
				)
			}
			if tc.wantTrunc && !got.StdoutTruncated && !got.StderrTruncated {
				t.Fatalf(
					"expected some truncation, got stdoutTrunc=%v stderrTrunc=%v",
					got.StdoutTruncated,
					got.StderrTruncated,
				)
			}
		})
	}
}

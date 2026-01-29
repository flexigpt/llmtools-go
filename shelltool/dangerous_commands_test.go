package shelltool

import (
	"runtime"
	"strings"
	"testing"

	"github.com/flexigpt/llmtools-go/internal/toolutil"
)

type rejectTC struct {
	name       string
	cmd        string
	shellName  string
	shellPath  string
	wantErr    bool
	wantSubstr string // optional substring to assert in error message
}

func TestRejectDangerous_EmptyAndWhitespace(t *testing.T) {
	runRejectCases(t, []rejectTC{
		{name: "empty", cmd: "", wantErr: false},
		{name: "spaces", cmd: "   ", wantErr: false},
		{name: "tabs_newlines", cmd: "\n\t  \n", wantErr: false},
	})
}

func TestRejectDangerous_Unix_SafeInputs(t *testing.T) {
	if runtime.GOOS == toolutil.GOOSWindows {
		t.Skip("unix-focused expectations")
	}

	runRejectCases(t, []rejectTC{
		// Basic "boundary" false positives we want to avoid.
		{name: "echo_sudo_argument", cmd: "echo sudo", wantErr: false},
		{name: "printf_sudo_argument", cmd: "printf %s sudo", wantErr: false},
		{name: "quoted_sudo", cmd: `echo "sudo"`, wantErr: false},
		{name: "single_quoted_sudo", cmd: `echo 'sudo'`, wantErr: false},

		// Separators inside quotes should not split commands.
		{name: "sudo_inside_quotes_semicolon", cmd: `echo "x; sudo ls"`, wantErr: false},
		{name: "sudo_inside_quotes_andand", cmd: `echo "x && sudo ls"`, wantErr: false},
		{name: "sudo_inside_quotes_pipe", cmd: `echo "x | sudo ls"`, wantErr: false},
		{name: "hash_inside_quotes_not_comment", cmd: `echo "# sudo ls"`, wantErr: false},

		// Comments: sudo is in comment => should not be rejected.
		{name: "sudo_in_comment", cmd: "echo hi # sudo ls", wantErr: false},
		{name: "hash_not_comment_when_not_preceded_by_space", cmd: "echo hi#sudo", wantErr: false},

		// "&"" handling: should not reject redirections or escaped ampersand.
		{name: "allow_andand", cmd: "echo a && echo b", wantErr: false},
		{name: "escaped_ampersand", cmd: `echo hi \&`, wantErr: false},
		{name: "amp_redir_stdout", cmd: `echo hi &>out.txt`, wantErr: false},
		{name: "amp_redir_stderr_to_stdout", cmd: `echo hi 2>&1`, wantErr: false},
		{name: "pipe_stderr_too", cmd: `echo hi |& tee out.txt`, wantErr: false},

		// Blocklist should not false-positive on arguments.
		{name: "echo_rm_argument", cmd: "echo rm", wantErr: false},
		{name: "echo_curl_argument", cmd: "echo curl", wantErr: false},

		// "mkfs / shutdown / editors" as arguments (not commands) should be allowed.
		{name: "echo_mkfs_argument", cmd: "echo mkfs /dev/sda", wantErr: false},
		{name: "echo_shutdown_argument", cmd: "echo shutdown -h now", wantErr: false},
		{name: "echo_vim_argument", cmd: "echo vim foo.txt", wantErr: false},
	})
}

func TestRejectDangerous_Unix_RejectedInputs(t *testing.T) {
	if runtime.GOOS == toolutil.GOOSWindows {
		t.Skip("unix-focused expectations")
	}

	runRejectCases(t, []rejectTC{
		// Sudo/su.
		{name: "sudo_simple", cmd: "sudo ls", wantErr: true, wantSubstr: "blocked command"},
		{name: "su_simple", cmd: "su -c id", wantErr: true, wantSubstr: "blocked command"},
		{name: "sudo_after_semicolon", cmd: "echo x; sudo ls", wantErr: true, wantSubstr: "sudo"},
		{name: "sudo_after_andand", cmd: "echo x && sudo ls", wantErr: true, wantSubstr: "sudo"},
		{name: "sudo_after_oror", cmd: "echo x || sudo ls", wantErr: true, wantSubstr: "sudo"},
		{name: "sudo_after_pipe", cmd: "echo x | sudo ls", wantErr: true, wantSubstr: "sudo"},
		{name: "sudo_in_parens", cmd: "(sudo ls)", wantErr: true, wantSubstr: "sudo"},
		{name: "sudo_with_path", cmd: "/usr/bin/sudo ls", wantErr: true, wantSubstr: "sudo"},

		// Fork bomb (whitespace variations).
		{name: "fork_bomb_classic", cmd: ":(){ :|:& };:", wantErr: true, wantSubstr: "fork bomb"},
		{name: "fork_bomb_extra_spaces", cmd: ": ( ) {  : | : & } ; :", wantErr: true, wantSubstr: "fork bomb"},
		{name: "fork_bomb_with_newlines", cmd: ":\n(){\n:|:&\n};:\n", wantErr: true, wantSubstr: "fork bomb"},

		// "rm" is ALWAYS blocked  (any target).
		{name: "rm_simple", cmd: "rm foo", wantErr: true, wantSubstr: "blocked command"},
		{name: "rm_rf_root", cmd: "rm -rf /", wantErr: true, wantSubstr: "blocked command"},
		{name: "rm_fr_root_glob", cmd: "rm -fr /*", wantErr: true, wantSubstr: "rm"},
		{name: "rm_r_root_no_force", cmd: "rm -r /", wantErr: true, wantSubstr: "rm"},
		{name: "rm_R_root", cmd: "rm -R /", wantErr: true, wantSubstr: "rm"},
		{name: "rm_long_recursive_root", cmd: "rm --recursive /", wantErr: true, wantSubstr: "rm"},
		{name: "rm_recursive_with_double_dash", cmd: "rm -r -- /", wantErr: true, wantSubstr: "rm"},
		{name: "rm_recursive_root_quoted", cmd: `rm -r "/"`, wantErr: true, wantSubstr: "rm"},
		{name: "rm_recursive_root_glob_quoted", cmd: `rm -r '/*'`, wantErr: true, wantSubstr: "rm"},
		{name: "rm_recursive_root_via_clean_dot", cmd: `rm -r /./`, wantErr: true, wantSubstr: "rm"},
		{name: "rm_recursive_root_via_clean_double_slash", cmd: `rm -r //`, wantErr: true, wantSubstr: "rm"},
		{name: "rm_recursive_root_via_clean_parent", cmd: `rm -r /tmp/..`, wantErr: true, wantSubstr: "rm"},
		{name: "rm_recursive_root_via_clean_parent2", cmd: `rm -R /../`, wantErr: true, wantSubstr: "rm"},

		// Mkfs.
		{name: "mkfs_plain", cmd: "mkfs /dev/sda", wantErr: true, wantSubstr: "blocked command"},
		{name: "mkfs_variant_ext4", cmd: "mkfs.ext4 /dev/sda1", wantErr: true, wantSubstr: "blocked command"},

		// Network tools.
		{name: "curl_blocked", cmd: "curl https://example.com", wantErr: true, wantSubstr: "blocked command"},

		// Shutdown/reboot/halt/poweroff.
		{name: "shutdown", cmd: "shutdown -h now", wantErr: true, wantSubstr: "blocked command"},
		{name: "reboot", cmd: "reboot", wantErr: true, wantSubstr: "blocked command"},
		{name: "halt", cmd: "halt", wantErr: true, wantSubstr: "blocked command"},
		{name: "poweroff", cmd: "poweroff", wantErr: true, wantSubstr: "blocked command"},

		// Interactive tools.
		{name: "vim", cmd: "vim foo.txt", wantErr: true, wantSubstr: "blocked command"},
		{name: "less", cmd: "less /var/log/syslog", wantErr: true, wantSubstr: "blocked command"},
		{name: "top", cmd: "top", wantErr: true, wantSubstr: "blocked command"},
		{name: "pipeline_into_less", cmd: "echo hi | less", wantErr: true, wantSubstr: "blocked command"},

		// Backgrounding/chaining with "&".
		{name: "background_trailing", cmd: "sleep 1 &", wantErr: true, wantSubstr: "background"},
		{name: "background_no_space", cmd: "sleep 1&", wantErr: true, wantSubstr: "background"},
		{name: "chaining_single_ampersand", cmd: "echo a & echo b", wantErr: true, wantSubstr: "background"},
	})
}

func TestRejectDangerous_Unix_WrappersAndAssignments(t *testing.T) {
	if runtime.GOOS == toolutil.GOOSWindows {
		t.Skip("unix-focused expectations")
	}

	runRejectCases(t, []rejectTC{
		// Env assignments should be skipped to find the real command.
		{name: "assignment_then_sudo", cmd: "X=1 sudo ls", wantErr: true, wantSubstr: "sudo"},
		{name: "multiple_assignments_then_su", cmd: "A=1 B=2 su -c id", wantErr: true, wantSubstr: "su"},
		{name: "assignment_then_echo_sudo_is_safe", cmd: "X=1 echo sudo", wantErr: false},

		// Unwrap env/command/builtin wrappers.
		{name: "env_wraps_sudo", cmd: "env sudo ls", wantErr: true, wantSubstr: "sudo"},
		{name: "env_with_flags_wraps_sudo", cmd: "env -i FOO=bar sudo ls", wantErr: true, wantSubstr: "sudo"},
		{name: "command_wraps_sudo", cmd: "command sudo ls", wantErr: true, wantSubstr: "sudo"},
		{name: "builtin_wraps_sudo", cmd: "builtin sudo ls", wantErr: true, wantSubstr: "sudo"},
	})
}

func TestRejectDangerous_Windows_Patterns(t *testing.T) {
	if runtime.GOOS != toolutil.GOOSWindows {
		t.Skip("windows-only patterns")
	}

	runRejectCases(t, []rejectTC{
		// Diskpart anywhere on Windows.
		{name: "diskpart", cmd: "diskpart", wantErr: true, wantSubstr: "blocked command"},
		{name: "diskpart_exe", cmd: "diskpart.exe", wantErr: true, wantSubstr: "blocked command"},
		{name: "diskpart_full_path", cmd: `C:\Windows\System32\diskpart.exe`, wantErr: true, wantSubstr: "diskpart"},
		{name: "diskpart_after_andand", cmd: "echo hi && diskpart", wantErr: true, wantSubstr: "diskpart"},
		{name: "echo_diskpart_argument_safe", cmd: "echo diskpart", wantErr: false},

		// Format.com anywhere.
		{name: "format_com", cmd: "format.com C:", wantErr: true, wantSubstr: "blocked command"},

		// Format only when using cmd.exe shellName.
		{
			name:       "format_cmd_shell_reject",
			cmd:        "format C:",
			shellName:  string(ShellNameCmd),
			wantErr:    true,
			wantSubstr: "blocked command",
		},
		{
			name:       "format_exe_cmd_shell_reject",
			cmd:        "format.exe C:",
			shellName:  string(ShellNameCmd),
			wantErr:    true,
			wantSubstr: "format",
		},
		{name: "format_powershell_allow", cmd: "format C:", shellName: "powershell", wantErr: false},

		// Ensure we don't accidentally block "Format-Table" style cmdlets by prefix.
		{name: "format_table_allowed", cmd: "Format-Table", shellName: "powershell", wantErr: false},

		// Cross-platform blocks still apply on Windows too.
		{name: "shutdown_blocked_on_windows", cmd: "shutdown /s /t 0", wantErr: true, wantSubstr: "shutdown"},
		{name: "sudo_blocked_on_windows_too", cmd: "sudo whoami", wantErr: true, wantSubstr: "sudo"},
		{name: "mkfs_blocked_on_windows_too", cmd: "mkfs.ext4 /dev/sda1", wantErr: true, wantSubstr: "mkfs"},
	})
}

func runRejectCases(t *testing.T, cases []rejectTC) {
	t.Helper()
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Helper()
			if tc.shellName == "" {
				if runtime.GOOS == toolutil.GOOSWindows {
					tc.shellName = string(ShellNameCmd)
				} else {
					tc.shellName = string(ShellNameSh)
				}
			}
			if tc.shellPath == "" {
				if runtime.GOOS == toolutil.GOOSWindows {
					tc.shellPath = `C:\Windows\System32\cmd.exe`
				} else {
					tc.shellPath = "/bin/sh"
				}
			}

			err := rejectDangerousCommand(tc.cmd, tc.shellPath, ShellName(tc.shellName), hardBlockedCommands, true)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("expected reject for %q", tc.cmd)
				}
				if tc.wantSubstr != "" &&
					!strings.Contains(strings.ToLower(err.Error()), strings.ToLower(tc.wantSubstr)) {
					t.Fatalf("error %q does not contain %q (cmd=%q)", err.Error(), tc.wantSubstr, tc.cmd)
				}
			} else if err != nil {
				t.Fatalf("did not expect reject for %q: %v", tc.cmd, err)
			}
		})
	}
}

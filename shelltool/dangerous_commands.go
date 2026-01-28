package shelltool

import (
	"errors"
	"path/filepath"
	"runtime"
	"strings"
	"unicode"
)

var hardBlockedCommands = func() map[string]struct{} {
	// Non-overridable baseline. These are blocked regardless of AllowDangerous.
	hard := []string{
		// Privilege escalation / destructive.
		"sudo", "su",
		"rm",
		"mkfs",
		"shutdown", "reboot", "halt", "poweroff",

		// Interactive/TUI editors (not useful in non-interactive tool).
		"vim", "vi", "nano", "emacs", "less", "more", "top", "htop",

		// Network/communication tools.
		"curl", "wget",
		"nc", "netcat", "ncat", "socat",
		"ssh", "scp", "sftp",
		"ftp", "tftp", "telnet",

		// PowerShell network cmdlets/aliases (relevant when shell is pwsh/powershell).
		"invoke-webrequest", "iwr",
		"invoke-restmethod", "irm",

		// Windows destructive / deletion equivalents (also harmless to block on unix).
		"diskpart",
		"format.com",
		"del", "erase", "rmdir", "rd",
		"remove-item", "ri",
	}

	m := make(map[string]struct{}, len(hard))
	for _, c := range hard {
		m[c] = struct{}{}
	}
	return m
}()

func rejectDangerousCommand(
	cmd, shellPath string, shellName ShellName,
	blockedCommands map[string]struct{},
	enableHeuristicChecks bool,
) error {
	c := strings.TrimSpace(cmd)
	if c == "" {
		return nil
	}
	if len(blockedCommands) == 0 {
		blockedCommands = map[string]struct{}{}
	}

	// Cheap whole-input checks first.
	if enableHeuristicChecks {
		if looksLikeForkBomb(c) {
			return errors.New("blocked dangerous command pattern (fork bomb)")
		}
		if hasBackgroundAmpersand(c) {
			return errors.New("blocked backgrounding with '&' (leaks processes)")
		}
	}

	// Scan each "command segment" separated by ";, &&, ||, |, &, newline, (, )".
	return forEachSegment(c, func(seg string) error {
		toks := shellFields(seg, runtime.GOOS == GOOSWindows)
		if len(toks) == 0 {
			return nil
		}

		name, args := unwrapCommand(toks)
		if name == "" {
			return nil
		}
		_ = args // args intentionally unused; we now block by command name, not by argument heuristics.

		// Block mkfs variants like mkfs.ext4 if mkfs is blocked.
		if strings.HasPrefix(name, "mkfs.") {
			if _, ok := blockedCommands["mkfs"]; ok {
				return errors.New("blocked command: " + name)
			}
		}

		// Block by exact command name (plus a Windows no-extension variant).
		if isBlockedName(name, blockedCommands) {
			return errors.New("blocked command: " + name)
		}

		// Windows-only: block "format" when using cmd (but do not block PowerShell's formatting cmdlets/aliases).
		if runtime.GOOS == GOOSWindows && shellName == ShellNameCmd {
			// Name may be "format" or "format.exe"; treat both as blocked in cmd.
			if isBlockedName("format", map[string]struct{}{"format": {}}) &&
				isBlockedName(name, map[string]struct{}{"format": {}}) {
				return errors.New("blocked command: " + name)
			}
			if strings.EqualFold(name, "format") || strings.EqualFold(name, "format.exe") {
				return errors.New("blocked command: " + name)
			}
		}

		return nil
	})
}

func isBlockedName(name string, blocked map[string]struct{}) bool {
	if blocked == nil {
		return false
	}
	// Exact match first.
	if _, ok := blocked[name]; ok {
		return true
	}
	// On Windows, also match by stripping a common executable extension.
	if runtime.GOOS == GOOSWindows {
		ext := strings.ToLower(filepath.Ext(name))
		switch ext {
		case ".exe", ".com", ".bat", ".cmd":
			if _, ok := blocked[strings.TrimSuffix(name, ext)]; ok {
				return true
			}
		}
	}
	return false
}

func normalizeBlockedCommand(s string) (string, error) {
	x := strings.TrimSpace(s)
	if x == "" {
		return "", nil
	}
	if strings.ContainsRune(x, '\x00') {
		return "", errors.New("blocked command contains NUL byte")
	}
	if strings.IndexFunc(x, unicode.IsSpace) >= 0 {
		return "", errors.New("blocked command must be a single command name (no whitespace)")
	}
	// Allow passing "/bin/rm" etc; we only store the basename.
	return strings.ToLower(filepath.Base(x)), nil
}

func forEachSegment(s string, fn func(seg string) error) error {
	inS, inD := false, false
	esc := false
	start := 0

	emit := func(end int) error {
		seg := strings.TrimSpace(s[start:end])
		start = end
		if seg == "" {
			return nil
		}
		return fn(seg)
	}

	for i := 0; i < len(s); i++ {
		ch := s[i]

		if esc {
			esc = false
			continue
		}
		if !inS && ch == '\\' {
			esc = true
			continue
		}
		if !inD && ch == '\'' {
			inS = !inS
			continue
		}
		if !inS && ch == '"' {
			inD = !inD
			continue
		}
		if inS || inD {
			continue
		}

		// Comment start: " #...".
		if ch == '#' && (i == 0 || unicode.IsSpace(rune(s[i-1]))) {
			if err := emit(i); err != nil {
				return err
			}
			return nil
		}

		// Separators.
		if ch == ';' || ch == '\n' || ch == '(' || ch == ')' {
			if err := emit(i); err != nil {
				return err
			}
			start = i + 1
			continue
		}

		// "|| and |".
		if ch == '|' {
			if err := emit(i); err != nil {
				return err
			}
			if i+1 < len(s) && s[i+1] == '|' { //nolint:gocritic // No switch here.
				start = i + 2
				i++
			} else if i+1 < len(s) && s[i+1] == '&' { // |&
				start = i + 2
				i++
			} else {
				start = i + 1
			}
			continue
		}

		// "&&"" (single & is handled by hasBackgroundAmpersand before we get here).
		if ch == '&' && i+1 < len(s) && s[i+1] == '&' {
			if err := emit(i); err != nil {
				return err
			}
			start = i + 2
			i++
			continue
		}
	}

	return emit(len(s))
}

func shellFields(s string, windowsMode bool) []string {
	var out []string
	var b strings.Builder
	inS, inD := false, false
	esc := false

	flush := func() {
		if b.Len() == 0 {
			return
		}
		out = append(out, b.String())
		b.Reset()
	}

	for i := 0; i < len(s); i++ {
		ch := s[i]

		if esc {
			b.WriteByte(ch)
			esc = false
			continue
		}
		if !windowsMode && !inS && ch == '\\' {
			esc = true
			continue
		}
		if !inD && ch == '\'' {
			inS = !inS
			continue
		}
		if !inS && ch == '"' {
			inD = !inD
			continue
		}

		if !inS && !inD && unicode.IsSpace(rune(ch)) {
			flush()
			continue
		}
		b.WriteByte(ch)
	}
	flush()
	return out
}

func unwrapCommand(tokens []string) (name string, args []string) {
	i := 0
	for i < len(tokens) && isEnvAssignment(tokens[i]) {
		i++
	}
	if i >= len(tokens) {
		return "", nil
	}

	cmd := canonicalCmd(tokens[i])
	rest := tokens[i+1:]

	for {
		switch cmd {
		case "env":
			j := 0
			for j < len(rest) {
				a := rest[j]
				if a == "-i" || a == "--ignore-environment" || isEnvAssignment(a) || strings.HasPrefix(a, "-") {
					j++
					continue
				}
				break
			}
			if j >= len(rest) {
				return cmd, rest
			}
			cmd = canonicalCmd(rest[j])
			rest = rest[j+1:]
			continue

		case "command", "builtin":
			j := 0
			for j < len(rest) && strings.HasPrefix(rest[j], "-") {
				j++
			}
			if j >= len(rest) {
				return cmd, rest
			}
			cmd = canonicalCmd(rest[j])
			rest = rest[j+1:]
			continue
		}
		break
	}

	return cmd, rest
}

func canonicalCmd(tok string) string {
	return strings.ToLower(filepath.Base(strings.TrimSpace(tok)))
}

func isEnvAssignment(tok string) bool {
	eq := strings.IndexByte(tok, '=')
	if eq <= 0 {
		return false
	}
	for i := range eq {
		c := tok[i]
		if c != '_' &&
			(c < 'a' || c > 'z') &&
			(c < 'A' || c > 'Z') &&
			(i <= 0 || c < '0' || c > '9') {
			return false
		}
	}
	return true
}

func looksLikeForkBomb(s string) bool {
	b := make([]byte, 0, len(s))
	for i := 0; i < len(s); i++ {
		if !unicode.IsSpace(rune(s[i])) {
			b = append(b, s[i])
		}
	}
	return strings.Contains(string(b), ":(){:|:&};:")
}

// Only reject actual backgrounding/chaining '&'. Avoid "2>&1, &>file, |&".
func hasBackgroundAmpersand(s string) bool {
	inS, inD := false, false
	esc := false

	for i := 0; i < len(s); i++ {
		ch := s[i]

		if esc {
			esc = false
			continue
		}
		if !inS && ch == '\\' {
			esc = true
			continue
		}
		if !inD && ch == '\'' {
			inS = !inS
			continue
		}
		if !inS && ch == '"' {
			inD = !inD
			continue
		}
		if inS || inD {
			continue
		}

		if ch != '&' {
			continue
		}
		// Allow "&&".
		if i+1 < len(s) && s[i+1] == '&' {
			i++
			continue
		}
		// Allow redirections: "&>file, 2>&1, |&".
		if (i+1 < len(s) && s[i+1] == '>') || (i > 0 && (s[i-1] == '>' || s[i-1] == '<' || s[i-1] == '|')) {
			continue
		}
		return true
	}
	return false
}

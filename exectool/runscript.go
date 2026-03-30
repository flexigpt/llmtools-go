package exectool

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"path/filepath"
	"runtime"
	"slices"
	"strings"

	"github.com/flexigpt/llmtools-go/internal/executil"
	"github.com/flexigpt/llmtools-go/internal/toolutil"
	"github.com/flexigpt/llmtools-go/spec"
)

const runScriptFuncID spec.FuncID = "github.com/flexigpt/llmtools-go/exectool/runscript.RunScript"

var runScriptToolSpec = spec.Tool{
	SchemaVersion: spec.SchemaVersion,
	ID:            "019c3df0-e332-717f-85d1-3d752f9f6046",
	Slug:          "runscript",
	Version:       "v1.0.0",
	DisplayName:   "Run Script",
	Description:   "Run a pre-existing script from disk.",
	Tags:          []string{"exec"},

	ArgSchema: spec.JSONSchema(`{
"$schema": "http://json-schema.org/draft-07/schema#",
"type": "object",
"properties": {
	"path": {
		"type": "string",
		"description": "Path to the script. Can be absolute or relative. If relative and workdir is provided, resolves against workdir; otherwise resolves against the tool workBaseDir."
	},
	"args": {
		"type": "array",
		"items": { "type": "string" },
		"description": "Arguments passed to the script."
	},
	"env": {
		"type": "object",
		"additionalProperties": { "type": "string" },
		"description": "Environment variable overrides (merged into the process env + tool base env)."
	},
	"workDir": {
		"type": "string",
		"description": "Working directory. Can be absolute or relative to workBaseDir."
	}
},
"required": ["path"],
"additionalProperties": false
}`),
	GoImpl: spec.GoToolImpl{FuncID: runScriptFuncID},

	CreatedAt:  spec.SchemaStartTime,
	ModifiedAt: spec.SchemaStartTime,
}

type RunScriptArgs struct {
	Path    string            `json:"path"`
	Args    []string          `json:"args,omitempty"`
	Env     map[string]string `json:"env,omitempty"`
	WorkDir string            `json:"workDir,omitempty"`
}

type RunScriptOut struct {
	Path       string `json:"path"`
	ExitCode   int    `json:"exitCode"`
	Stdout     string `json:"stdout,omitempty"`
	Stderr     string `json:"stderr,omitempty"`
	TimedOut   bool   `json:"timedOut,omitempty"`
	DurationMS int64  `json:"durationMS,omitempty"`

	StdoutTruncated bool `json:"stdoutTruncated,omitempty"`
	StderrTruncated bool `json:"stderrTruncated,omitempty"`
}

type RunScriptMode string

const (
	// RunScriptModeDirect executes the script path directly (as the "command").
	// This is appropriate for PowerShell scripts executed via "& 'script.ps1' ...".
	RunScriptModeDirect RunScriptMode = "direct"

	// RunScriptModeShell executes the script by invoking the selected wrapper shell as an interpreter:
	//   <shellPath> <script> <args...>
	// This avoids requiring execute bits on Unix shell scripts.
	RunScriptModeShell RunScriptMode = "shell"

	// RunScriptModeInterpreter executes the script via an explicit interpreter command:
	//   <command> <commandArgs...> <script> <args...>
	RunScriptModeInterpreter RunScriptMode = "interpreter"
)

type RunScriptInterpreter struct {
	// Shell selects the wrapper shell used to run the *constructed command string*.
	// This affects quoting dialect and which binary is used for "shell -c" / "-Command".
	Shell ShellName

	Mode RunScriptMode

	// Command is required for ModeInterpreter.
	// Examples: "python3", "python", "node", "ruby".
	Command string
	Args    []string
}

type RunScriptPolicy struct {
	// AllowedExtensions is an optional lowercase allowlist (e.g. [".sh", ".ps1", ".py"]).
	// If empty/nil, extension is allowed iff InterpreterByExtension has a match (or a "" fallback is configured).
	AllowedExtensions []string

	// InterpreterByExtension controls how scripts are executed based on extension.
	// Keys should be lowercase and include the leading dot (".sh", ".ps1", ".py").
	//
	// If no mapping exists for the script extension:
	//   - if a mapping for "" exists, it is used as a fallback
	//   - otherwise runscript fails with "no interpreter mapping for extension"
	InterpreterByExtension map[string]RunScriptInterpreter

	// ExecutionPolicy overrides the ExecTool-wide defaults for runscript.
	// If left zero-valued, ExecTool.execPolicy is used.
	ExecutionPolicy ExecutionPolicy

	// Arg limits (defense-in-depth).
	MaxArgs     int
	MaxArgBytes int
}

// Clone returns an independent copy of the RunScriptPolicy.
// It deep-copies reference fields (slice/map). ExecutionPolicy is value-copied.
//
// Note: Map values (RunScriptInterpreter) are copied as-is. If RunScriptInterpreter
// contains reference types that must be independent, add a Clone() for it and
// use that in the map copy loop.
func (p *RunScriptPolicy) Clone() *RunScriptPolicy {
	if p == nil {
		return nil
	}

	cp := new(RunScriptPolicy)
	*cp = *p // copies scalar fields + slice/map headers (we fix those below)

	if p.AllowedExtensions != nil {
		cp.AllowedExtensions = make([]string, len(p.AllowedExtensions))
		copy(cp.AllowedExtensions, p.AllowedExtensions)
	} else {
		cp.AllowedExtensions = nil
	}

	if p.InterpreterByExtension != nil {
		cp.InterpreterByExtension = make(map[string]RunScriptInterpreter, len(p.InterpreterByExtension))
		maps.Copy(cp.InterpreterByExtension, p.InterpreterByExtension)
	} else {
		cp.InterpreterByExtension = nil
	}

	cp.ExecutionPolicy = *p.ExecutionPolicy.Clone()

	return cp
}

func DefaultRunScriptPolicy() RunScriptPolicy {
	pyCmd := "python3"
	pyShell := ShellNameSh
	if runtime.GOOS == toolutil.GOOSWindows {
		pyCmd = "python"
		pyShell = ShellNamePowershell
	}
	return RunScriptPolicy{
		AllowedExtensions: []string{".sh", ".bash", ".zsh", ".ksh", ".dash", ".ps1", ".py"},
		InterpreterByExtension: map[string]RunScriptInterpreter{
			// Shell scripts: run via the wrapper shell path as interpreter.
			".sh":   {Shell: ShellNameSh, Mode: RunScriptModeShell},
			".bash": {Shell: ShellNameBash, Mode: RunScriptModeShell},
			".zsh":  {Shell: ShellNameZsh, Mode: RunScriptModeShell},
			".ksh":  {Shell: ShellNameKsh, Mode: RunScriptModeShell},
			".dash": {Shell: ShellNameDash, Mode: RunScriptModeShell},

			// PowerShell: execute the script directly via PowerShell dialect ("& 'script.ps1' ...").
			".ps1": {Shell: ShellNamePowershell, Mode: RunScriptModeDirect},

			// Python: interpreter-based.
			".py": {Shell: pyShell, Mode: RunScriptModeInterpreter, Command: pyCmd},
		},
		ExecutionPolicy: ExecutionPolicy{}, // inherit from ExecTool by default
		MaxArgs:         256,
		MaxArgBytes:     16 * 1024,
	}
}

// NormalizeRunScriptPolicy deep-clones and normalizes a RunScriptPolicy.
// This is defensive: it prevents shared map/slice backing storage and makes
// policy behavior deterministic (lowercased extensions, leading dots, etc).
func NormalizeRunScriptPolicy(in RunScriptPolicy) (RunScriptPolicy, error) {
	out := in

	// Normalize numeric limits.
	if out.MaxArgs < 0 {
		return RunScriptPolicy{}, errors.New("runscript policy: MaxArgs must be >= 0")
	}
	if out.MaxArgBytes < 0 {
		return RunScriptPolicy{}, errors.New("runscript policy: MaxArgBytes must be >= 0")
	}

	// AllowedExtensions: clone + normalize + stable-dedup (preserve order).
	if out.AllowedExtensions != nil {
		seen := map[string]struct{}{}
		norm := make([]string, 0, len(out.AllowedExtensions))
		for _, e := range out.AllowedExtensions {
			x := strings.ToLower(strings.TrimSpace(e))
			if strings.ContainsRune(x, '\x00') {
				return RunScriptPolicy{}, errors.New("runscript policy: AllowedExtensions contains NUL byte")
			}
			if x != "" && !strings.HasPrefix(x, ".") {
				x = "." + x
			}
			if _, ok := seen[x]; ok {
				continue
			}
			seen[x] = struct{}{}
			norm = append(norm, x)
		}
		out.AllowedExtensions = norm
	}

	// InterpreterByExtension: deep clone + normalize keys.
	if out.InterpreterByExtension != nil {
		m := make(map[string]RunScriptInterpreter, len(out.InterpreterByExtension))
		for k, v := range out.InterpreterByExtension {
			key := strings.ToLower(strings.TrimSpace(k))
			if strings.ContainsRune(key, '\x00') {
				return RunScriptPolicy{}, errors.New("runscript policy: InterpreterByExtension key contains NUL byte")
			}
			if key != "" && !strings.HasPrefix(key, ".") {
				key = "." + key
			}

			// Defensive clone of args slice (RunScriptInterpreter contains []string).
			v.Args = slices.Clone(v.Args)

			// Validate mapping is internally consistent.
			switch v.Mode {
			case RunScriptModeDirect, RunScriptModeShell, RunScriptModeInterpreter:
			default:
				return RunScriptPolicy{}, fmt.Errorf("runscript policy: invalid mode for %q: %q", key, v.Mode)
			}
			if v.Mode == RunScriptModeInterpreter && strings.TrimSpace(v.Command) == "" {
				return RunScriptPolicy{}, fmt.Errorf(
					"runscript policy: interpreter mapping for %q has empty Command",
					key,
				)
			}
			m[key] = v
		}
		out.InterpreterByExtension = m
	}

	return out, nil
}

func runScript(
	ctx context.Context,
	args RunScriptArgs,
	tp execToolPolicy,
) (*RunScriptOut, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	fsPol := tp.fsPolicy
	defaultExecPol := tp.executionPolicy
	blocked := tp.blockedCommands
	pol := tp.runScriptPolicy

	reqPath := strings.TrimSpace(args.Path)
	if reqPath == "" {
		return nil, errors.New("path is required")
	}
	// WorkDir: absolute or relative; default to policy work base dir.
	workdirAbs, err := fsPol.ResolvePath(args.WorkDir, fsPol.WorkBaseDir())
	if err != nil {
		return nil, err
	}
	if err := fsPol.VerifyDirResolved(workdirAbs); err != nil {
		return nil, err
	}

	// Resolve script path:
	// - relative => workBaseDir
	// - absolute => must still be within allowedRoots (if configured).
	// If relative and workdir provided, resolve relative to workdir.
	scriptInput := reqPath
	if strings.TrimSpace(args.WorkDir) != "" && !filepath.IsAbs(reqPath) {
		scriptInput = filepath.Join(workdirAbs, reqPath)
	}
	scriptAbs, err := fsPol.ResolvePath(scriptInput, "")
	if err != nil {
		return nil, err
	}

	// Require existing, regular, non-symlink file and refuse symlink traversal in parents.
	if _, err := fsPol.RequireExistingRegularFileResolved(scriptAbs); err != nil {
		return nil, err
	}

	ext := strings.ToLower(filepath.Ext(scriptAbs))
	if len(pol.AllowedExtensions) != 0 && !extAllowed(ext, pol.AllowedExtensions) {
		return nil, fmt.Errorf("script extension %q is not allowed", ext)
	}

	// Validate env + args.
	if err := executil.ValidateEnvMap(args.Env); err != nil {
		return nil, err
	}
	if pol.MaxArgs > 0 && len(args.Args) > pol.MaxArgs {
		return nil, fmt.Errorf("too many args: %d (max %d)", len(args.Args), pol.MaxArgs)
	}
	maxArgBytes := pol.MaxArgBytes
	if maxArgBytes <= 0 {
		maxArgBytes = 16 * 1024
	}
	for i, a := range args.Args {
		if strings.ContainsRune(a, '\x00') {
			return nil, fmt.Errorf("args[%d] contains NUL byte", i)
		}
		if len(a) > maxArgBytes {
			return nil, fmt.Errorf("args[%d] too long", i)
		}
	}

	interp, ok := lookupInterpreter(pol, ext)
	if !ok {
		return nil, fmt.Errorf("no interpreter mapping for extension %q", ext)
	}

	// Select wrapper shell (concrete shell needed for quoting + execution).
	sel, err := selectShell(ctx, interp.Shell)
	if err != nil {
		return nil, err
	}

	// Build argv based on mode.
	var argv []string
	switch interp.Mode {
	case RunScriptModeDirect:
		// Execute script path directly.
		argv = append([]string{scriptAbs}, args.Args...)
	case RunScriptModeShell:
		// Use the wrapper shell binary as the interpreter.
		argv = append([]string{sel.Path, scriptAbs}, args.Args...)
	case RunScriptModeInterpreter:
		cmd := strings.TrimSpace(interp.Command)
		if cmd == "" {
			return nil, errors.New("invalid interpreter mapping: empty command")
		}
		argv = append(argv, cmd)
		argv = append(argv, interp.Args...)
		argv = append(argv, scriptAbs)
		argv = append(argv, args.Args...)
	default:
		return nil, fmt.Errorf("invalid interpreter mode: %q", interp.Mode)
	}

	// Convert argv into a safely-quoted command string for the selected wrapper shell.
	cmdStr, err := executil.CommandFromArgv(sel.Name, argv)
	if err != nil {
		return nil, err
	}
	cmdStrExec, cmdStrCheck := cmdStr, cmdStr

	// PowerShell/Pwsh: when running external commands or scripts via "-Command",
	// use the call operator (&). Without it, script execution often fails, and
	// executable paths with spaces can fail.
	//
	// But for safety checks (blocklist/heuristics), we want the command string
	// without the leading '&' so parsers don't treat '&' as the "command".
	if sel.Name == ShellNamePwsh || sel.Name == ShellNamePowershell {
		trimmed := strings.TrimSpace(cmdStr)
		if !strings.HasPrefix(trimmed, "&") {
			cmdStrExec = "& " + trimmed
			cmdStrCheck = trimmed
		} else {
			cmdStrExec = trimmed
			cmdStrCheck = strings.TrimSpace(strings.TrimPrefix(trimmed, "&"))
			if cmdStrCheck == "" {
				cmdStrCheck = trimmed
			}
		}
	}

	// Effective execution policy: runscript overrides or inherit ExecTool default.
	execPol := pol.ExecutionPolicy
	if execPol.Timeout == 0 && execPol.MaxOutputBytes == 0 && execPol.MaxCommands == 0 &&
		execPol.MaxCommandLength == 0 &&
		!execPol.AllowDangerous {
		execPol = defaultExecPol
	}

	timeout := effectiveTimeout(execPol)
	maxOut := effectiveMaxOutputBytes(execPol)
	maxCmdLen := effectiveMaxCommandLength(execPol)

	// Defense-in-depth: bound constructed command length (similar to shellcommand).
	if maxCmdLen > 0 && (len(cmdStrExec) > maxCmdLen || len(cmdStrCheck) > maxCmdLen) {
		return nil, fmt.Errorf(
			"constructed command too long (%d bytes; max %d)",
			max(len(cmdStrExec), len(cmdStrCheck)),
			maxCmdLen,
		)
	}

	// Merge env like shellcommand does (process env + tool base env + overrides), but no session.
	env, err := executil.EffectiveEnvWithBase(tp.baseEnv, args.Env)
	if err != nil {
		return nil, err
	}

	// Apply the same outer-command checks (blocklist always, heuristics optional).
	if err := executil.RejectDangerousCommand(
		cmdStrCheck,
		sel.Path,
		sel.Name,
		blocked,
		!execPol.AllowDangerous,
	); err != nil {
		return nil, err
	}

	res, runErr := executil.RunOneShellCommand(ctx, sel, cmdStrExec, workdirAbs, env, timeout, maxOut)
	if runErr != nil {
		return &RunScriptOut{ //nolint:nilerr // For shell exec, we return a exit code on err.
			Path:     scriptAbs,
			ExitCode: 127,
			Stderr:   runErr.Error(),
		}, nil
	}

	return &RunScriptOut{
		Path:       scriptAbs,
		ExitCode:   res.ExitCode,
		Stdout:     res.Stdout,
		Stderr:     res.Stderr,
		TimedOut:   res.TimedOut,
		DurationMS: res.DurationMS,

		StdoutTruncated: res.StdoutTruncated,
		StderrTruncated: res.StderrTruncated,
	}, nil
}

func extAllowed(ext string, allowed []string) bool {
	if len(allowed) == 0 {
		return false
	}
	// Allow extension-less scripts if explicitly configured.
	if strings.TrimSpace(ext) == "" {
		for _, a := range allowed {
			if strings.TrimSpace(strings.ToLower(a)) == "" {
				return true
			}
		}
		return false
	}

	x := strings.ToLower(ext)
	for _, a := range allowed {
		if strings.ToLower(strings.TrimSpace(a)) == x {
			return true
		}
	}
	return false
}

func lookupInterpreter(pol RunScriptPolicy, ext string) (RunScriptInterpreter, bool) {
	if pol.InterpreterByExtension == nil {
		return RunScriptInterpreter{}, false
	}
	// Exact match.
	if v, ok := pol.InterpreterByExtension[strings.ToLower(ext)]; ok {
		return v, true
	}
	// Optional fallback for extension-less scripts / shebang-style files.
	if v, ok := pol.InterpreterByExtension[""]; ok {
		return v, true
	}
	return RunScriptInterpreter{}, false
}

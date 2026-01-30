//go:build windows

package shelltool

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"strconv"
	"syscall"
	"time"
)

func configureProcessGroup(cmd *exec.Cmd) {
	// Best-effort isolation: create a new process group.
	cmd.SysProcAttr = &syscall.SysProcAttr{
		CreationFlags: syscall.CREATE_NEW_PROCESS_GROUP,
	}
}

func killProcessGroup(cmd *exec.Cmd) {
	if cmd == nil || cmd.Process == nil {
		return
	}
	// Best-effort: kill process tree. Taskkill is available on Windows.
	pid := strconv.Itoa(cmd.Process.Pid)
	runTaskkill := func(args ...string) error {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		return exec.CommandContext(ctx, "taskkill", args...).Run()
	}

	// Try a soft termination first (no /F), then force.
	if err := runTaskkill("/T", "/PID", pid); err != nil {
		// If taskkill is missing or fails, at least kill the direct process.
		if errors.Is(err, exec.ErrNotFound) {
			_ = cmd.Process.Kill()
			return
		}
	}
	time.Sleep(250 * time.Millisecond)
	if err := runTaskkill("/T", "/F", "/PID", pid); err != nil {
		_ = cmd.Process.Kill()
	}
}

func exitCodeFromProcessState(ps *os.ProcessState) int {
	if ps == nil {
		return -1
	}
	// Go sets ExitCode() appropriately on Windows.
	return ps.ExitCode()
}

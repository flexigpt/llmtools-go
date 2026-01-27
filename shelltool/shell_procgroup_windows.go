//go:build windows

package shelltool

import (
	"context"
	"os"
	"os/exec"
	"strconv"
	"syscall"
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
	_ = exec.CommandContext(context.Background(), "taskkill", "/T", "/F", "/PID", strconv.Itoa(cmd.Process.Pid)).Run()
}

func exitCodeFromProcessState(ps *os.ProcessState) int {
	if ps == nil {
		return -1
	}
	// Go sets ExitCode() appropriately on Windows.
	return ps.ExitCode()
}

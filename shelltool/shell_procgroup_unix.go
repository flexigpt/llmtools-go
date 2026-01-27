//go:build !windows

package shelltool

import (
	"os"
	"os/exec"
	"syscall"
)

func configureProcessGroup(cmd *exec.Cmd) {
	// Put the child in its own process group so we can kill the whole tree.
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
}

func killProcessGroup(cmd *exec.Cmd) {
	if cmd == nil || cmd.Process == nil {
		return
	}
	// Negative PID targets the process group.
	_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
}

func exitCodeFromProcessState(ps *os.ProcessState) int {
	if ps == nil {
		return -1
	}
	// On Unix, ps.Sys() is syscall.WaitStatus.
	if ws, ok := ps.Sys().(syscall.WaitStatus); ok {
		if ws.Signaled() {
			// Conventional: 128 + signal.
			return 128 + int(ws.Signal())
		}
		return ws.ExitStatus()
	}
	// Fallback.
	return ps.ExitCode()
}

//go:build !windows

package executil

import (
	"errors"
	"os"
	"os/exec"
	"syscall"
	"time"
)

type processGroupHandle struct{}

func afterProcessStart(cmd *exec.Cmd) (*processGroupHandle, error) {
	return &processGroupHandle{}, nil
}

func (h *processGroupHandle) terminate(cmd *exec.Cmd) error {
	killProcessGroup(cmd)
	return nil
}

func (h *processGroupHandle) close() {
}

func configureProcessGroup(cmd *exec.Cmd) {
	// Put the child in its own process group so we can kill the whole tree.
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
}

func killProcessGroup(cmd *exec.Cmd) {
	if cmd == nil || cmd.Process == nil {
		return
	}
	// Negative PID targets the process group.

	pgid := -cmd.Process.Pid

	// Best-effort graceful shutdown first.
	_ = syscall.Kill(pgid, syscall.SIGTERM)

	// Short grace period, then SIGKILL if still alive.
	deadline := time.Now().Add(500 * time.Millisecond)
	for time.Now().Before(deadline) {
		// Signal 0: check existence.
		err := syscall.Kill(pgid, 0)
		if err == nil {
			time.Sleep(50 * time.Millisecond)
			continue
		}
		// ESRCH => no such process group => already gone.
		if errors.Is(err, syscall.ESRCH) {
			return
		}
		// Any other error: break and try SIGKILL.
		break
	}
	_ = syscall.Kill(pgid, syscall.SIGKILL)
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

//go:build !windows

package unity

import (
	"os/exec"
	"syscall"
)

// setSysProcAttr places the Unity process in its own process group (Setpgid)
// and overrides cmd.Cancel to kill the entire group when the context fires.
// This prevents orphan child processes (e.g. PlayMode network servers) from
// surviving after a timeout or signal interruption.
func setSysProcAttr(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.WaitDelay = waitDelayAfterKill
	cmd.Cancel = func() error {
		if cmd.Process == nil {
			return nil
		}
		// Negative PID targets the entire process group.
		return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
	}
}

//go:build !windows

package unity

import (
	"os/exec"
	"syscall"
	"time"
)

// setSysProcAttr places the Unity process in its own process group (Setpgid)
// and overrides cmd.Cancel to kill the entire group when the context fires.
// This prevents orphan child processes (e.g. PlayMode network servers) from
// surviving after a timeout or signal interruption.
// waitDelayAfterKill is how long cmd.Run waits for the process group to exit
// after SIGKILL before giving up. SIGKILL is nearly instantaneous, but a small
// delay prevents cmd.Wait from returning before the kernel has cleaned up.
const waitDelayAfterKill = 5 * time.Second

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

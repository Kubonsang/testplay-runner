//go:build windows

package unity

import (
	"fmt"
	"os/exec"
	"syscall"
)

// setSysProcAttr places the Unity process in a new process group and
// overrides cmd.Cancel to kill the entire process tree when the context
// fires. Uses taskkill /F /T which terminates all child processes.
func setSysProcAttr(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{
		CreationFlags: syscall.CREATE_NEW_PROCESS_GROUP,
	}
	cmd.WaitDelay = waitDelayAfterKill
	cmd.Cancel = func() error {
		if cmd.Process == nil {
			return nil
		}
		// /F = force, /T = terminate child processes (tree kill)
		return exec.Command("taskkill", "/F", "/T", "/PID",
			fmt.Sprintf("%d", cmd.Process.Pid)).Run()
	}
}

//go:build windows

package unity

import "os/exec"

// setSysProcAttr is a no-op on Windows: process groups work differently and
// PlayMode network tests are not yet supported on that platform.
func setSysProcAttr(_ *exec.Cmd) {}

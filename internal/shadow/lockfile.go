package shadow

import (
	"os"
	"path/filepath"
)

// IsLocked returns true if the Unity Editor currently has the project open.
// It checks for Temp/UnityLockfile, which Unity creates on startup and
// removes on clean exit.
func IsLocked(projectPath string) bool {
	_, err := os.Stat(filepath.Join(projectPath, "Temp", "UnityLockfile"))
	return err == nil
}

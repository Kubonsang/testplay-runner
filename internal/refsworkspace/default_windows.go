//go:build windows

package refsworkspace

import (
	"fmt"
	"os"
	"path/filepath"
)

func DefaultConfig() (Config, error) {
	root := os.Getenv("TESTPLAY_REFS_POOL_FILE")
	poolFile := root
	if root != "" {
		root = filepath.Dir(root)
	} else {
		local := os.Getenv("LOCALAPPDATA")
		if local == "" {
			return Config{}, fmt.Errorf("LOCALAPPDATA is not set")
		}
		root = filepath.Join(local, "TestPlay", "Storage")
	}
	config := Config{Root: root, VHDXPath: poolFile, MountRoot: os.Getenv("TESTPLAY_REFS_MOUNT_ROOT")}
	if value := os.Getenv("TESTPLAY_REFS_MAX_BYTES"); value != "" {
		var parsed int64
		if _, err := fmt.Sscan(value, &parsed); err != nil || parsed <= 0 {
			return Config{}, fmt.Errorf("invalid TESTPLAY_REFS_MAX_BYTES")
		}
		config.MaximumBytes = parsed
	}
	return config, nil
}

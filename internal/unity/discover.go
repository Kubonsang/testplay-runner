package unity

import (
	"errors"
	"fmt"
	"os"

	"github.com/fastplay/runner/internal/config"
)

// ErrUnityNotFound is returned when Unity binary cannot be located.
var ErrUnityNotFound = errors.New("Unity binary not found: set unity_path in fastplay.json or UNITY_PATH env var")

// DiscoverUnityPath returns the Unity binary path from cfg or the UNITY_PATH env var.
func DiscoverUnityPath(cfg *config.Config) (string, error) {
	if cfg.UnityPath != "" {
		return cfg.UnityPath, nil
	}
	if p := os.Getenv("UNITY_PATH"); p != "" {
		return p, nil
	}
	return "", fmt.Errorf("%w", ErrUnityNotFound)
}

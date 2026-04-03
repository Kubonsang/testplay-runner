package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

var (
	ErrConfigNotFound  = errors.New("testplay.json not found")
	ErrConfigInvalid   = errors.New("testplay.json is invalid")
	ErrUnityPathMissing = errors.New("unity_path not set and UNITY_PATH env var not found")
)

// RetentionConfig controls automatic cleanup of old run results and artifacts.
type RetentionConfig struct {
	MaxRuns int `json:"max_runs"` // max recent runs to keep; default 30
}

type Config struct {
	SchemaVersion string          `json:"schema_version"`
	UnityPath     string          `json:"unity_path"`
	ProjectPath   string          `json:"project_path"`
	Timeout       Timeouts        `json:"timeout"`
	ResultDir     string          `json:"result_dir"`
	TestPlatform  string          `json:"test_platform"` // "edit_mode" (default) | "play_mode"
	Retention     RetentionConfig `json:"retention"`
	configDir     string          // unexported: directory containing testplay.json
}

// Timeouts holds timeout configuration for a testplay run.
// When both CompileMs and TestMs are > 0, two-phase execution is enabled:
// Unity runs compile-only first (CompileMs deadline), then runs tests
// (TestMs deadline). TotalMs remains as an outer safety-net context.
// When only TotalMs is set, single-phase execution is used.
type Timeouts struct {
	CompileMs int64 `json:"compile_ms"` // compile-only phase deadline; two-phase when > 0 with TestMs
	TestMs    int64 `json:"test_ms"`    // test phase deadline; two-phase when > 0 with CompileMs
	TotalMs   int64 `json:"total_ms"`   // outer deadline; default 300000
}

// Validate fills in default values and validates required fields.
// It mutates the Config in place.
// When requireUnity is true, unity_path (or UNITY_PATH env var) must be present.
// When requireUnity is false, the unity_path check is skipped (used by list/result).
func (c *Config) Validate(requireUnity bool) error {
	if requireUnity {
		// Unity path: config field → env var
		if c.UnityPath == "" {
			c.UnityPath = os.Getenv("UNITY_PATH")
		}
		if c.UnityPath == "" {
			return fmt.Errorf("%w", ErrUnityPathMissing)
		}
	}

	// Project path: default to directory containing config file
	if c.ProjectPath == "" {
		if c.configDir != "" {
			c.ProjectPath = c.configDir
		}
	}

	// Default result dir
	if c.ResultDir == "" {
		c.ResultDir = ".testplay/results"
	}

	// Default total timeout
	if c.Timeout.TotalMs == 0 {
		c.Timeout.TotalMs = 300000
	}

	// Reject negative total timeout (checked after default so zero → default → positive is valid)
	if c.Timeout.TotalMs < 0 {
		return fmt.Errorf("%w: total_ms must be positive", ErrConfigInvalid)
	}

	// Reject negative phase timeouts.
	if c.Timeout.CompileMs < 0 || c.Timeout.TestMs < 0 {
		return fmt.Errorf("%w: compile_ms and test_ms must be non-negative", ErrConfigInvalid)
	}

	// Require both phase timeouts when either is set — partial config silently falls
	// back to single-phase, which is almost certainly not what the user intended.
	if (c.Timeout.CompileMs > 0) != (c.Timeout.TestMs > 0) {
		return fmt.Errorf("%w: compile_ms and test_ms must both be set to enable two-phase execution", ErrConfigInvalid)
	}

	// Validate and default test_platform
	switch c.TestPlatform {
	case "", "edit_mode":
		c.TestPlatform = "edit_mode"
	case "play_mode":
		// valid
	default:
		return fmt.Errorf("%w: test_platform must be \"edit_mode\" or \"play_mode\"", ErrConfigInvalid)
	}

	// Default retention
	if c.Retention.MaxRuns == 0 {
		c.Retention.MaxRuns = 30
	}
	if c.Retention.MaxRuns < 0 {
		return fmt.Errorf("%w: retention.max_runs must be non-negative", ErrConfigInvalid)
	}

	return nil
}

func Load(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, fmt.Errorf("%w: %s", ErrConfigNotFound, path)
		}
		return nil, fmt.Errorf("%w: %v", ErrConfigInvalid, err)
	}

	var cfg Config
	if err := json.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrConfigInvalid, err)
	}

	if cfg.SchemaVersion == "" {
		return nil, fmt.Errorf("%w: schema_version is required", ErrConfigInvalid)
	}

	// Store the directory containing the config file
	cfg.configDir = filepath.Dir(filepath.Clean(path))

	return &cfg, nil
}

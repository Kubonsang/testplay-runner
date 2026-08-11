package config

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

var (
	ErrConfigNotFound   = errors.New("testplay.json not found")
	ErrConfigInvalid    = errors.New("testplay.json is invalid")
	ErrUnityPathMissing = errors.New("unity_path not set and UNITY_PATH env var not found")
)

// RetentionConfig controls automatic cleanup of old run results and artifacts.
type RetentionConfig struct {
	MaxRuns *int `json:"max_runs,omitempty"` // max recent runs to keep; nil → default 30; 0 = disable pruning
}

// BridgeConfig controls the warm-editor bridge backend (tier-1 selection).
// When absent or Enabled is nil, the bridge is allowed (default true) but is
// still only selected when a live, compatible bridge handshake is present and
// the Pristine Gate passes — otherwise execution falls back to shadow/process.
// Set Enabled to false to forbid the warm bridge entirely (guaranteed cold
// hermeticity, e.g. for conservative CI).
type BridgeConfig struct {
	Enabled *bool `json:"enabled,omitempty"` // nil → default true; false disables tier-1 bridge selection
}

const (
	DefaultWorkspaceStoreMaxAllocatedBytes int64 = 32 << 30
	DefaultWorkspaceMinimumHostFreeBytes   int64 = 20 << 30
)

// WorkspaceConfig selects the isolated workspace storage provider. It is
// additive to schema 1 so existing configuration files retain their meaning.
// An empty Backend preserves the historical selection rules.
type WorkspaceConfig struct {
	Backend                string            `json:"backend,omitempty"`
	StoreRoot              string            `json:"store_root,omitempty"`
	StoreMaxAllocatedBytes int64             `json:"store_max_allocated_bytes,omitempty"`
	MinimumHostFreeBytes   int64             `json:"minimum_host_free_bytes,omitempty"`
	LocalPackageOverrides  map[string]string `json:"local_package_overrides,omitempty"`
}

type Config struct {
	SchemaVersion string           `json:"schema_version"`
	UnityPath     string           `json:"unity_path"`
	ProjectPath   string           `json:"project_path"`
	Timeout       Timeouts         `json:"timeout"`
	ResultDir     string           `json:"result_dir"`
	TestPlatform  string           `json:"test_platform"` // "edit_mode" (default) | "play_mode"
	Retention     RetentionConfig  `json:"retention"`
	Bridge        *BridgeConfig    `json:"bridge,omitempty"` // warm-editor bridge backend; nil → enabled by default
	Workspace     *WorkspaceConfig `json:"workspace,omitempty"`
	configDir     string           // unexported: directory containing testplay.json
}

// BridgeEnabled reports whether the warm-editor bridge backend may be selected.
// Defaults to true when the bridge config block (or its enabled field) is absent.
func (c *Config) BridgeEnabled() bool {
	if c.Bridge == nil || c.Bridge.Enabled == nil {
		return true
	}
	return *c.Bridge.Enabled
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

	// Project path: default to directory containing config file.
	// A relative project_path anchors to the config file's directory, not the
	// process cwd — otherwise --config from another directory silently points
	// at the wrong project.
	if c.ProjectPath == "" {
		if c.configDir != "" {
			c.ProjectPath = c.configDir
		}
	} else if !filepath.IsAbs(c.ProjectPath) && c.configDir != "" {
		c.ProjectPath = filepath.Join(c.configDir, c.ProjectPath)
	}

	// Result dir: default, then anchor a relative path to the project.
	// Anchoring to cwd would split history from artifacts (project-anchored)
	// and make scenario instances share one mixed cwd-relative store.
	if c.ResultDir == "" {
		c.ResultDir = filepath.Join(".testplay", "results")
	}
	if !filepath.IsAbs(c.ResultDir) && c.ProjectPath != "" {
		c.ResultDir = filepath.Join(c.ProjectPath, c.ResultDir)
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

	// Default retention: nil (omitted) → 30; explicit 0 → disable pruning
	if c.Retention.MaxRuns == nil {
		defaultRuns := 30
		c.Retention.MaxRuns = &defaultRuns
	}
	if *c.Retention.MaxRuns < 0 {
		return fmt.Errorf("%w: retention.max_runs must be non-negative", ErrConfigInvalid)
	}

	if c.Workspace != nil {
		switch c.Workspace.Backend {
		case "", "legacy", "image", "vhdx-diff", "auto":
		default:
			return fmt.Errorf("%w: workspace.backend must be legacy, image, vhdx-diff, or auto", ErrConfigInvalid)
		}
		if c.Workspace.StoreRoot != "" && !filepath.IsAbs(c.Workspace.StoreRoot) {
			return fmt.Errorf("%w: workspace.store_root must be an absolute path", ErrConfigInvalid)
		}
		if c.Workspace.StoreMaxAllocatedBytes == 0 {
			c.Workspace.StoreMaxAllocatedBytes = DefaultWorkspaceStoreMaxAllocatedBytes
		}
		if c.Workspace.MinimumHostFreeBytes == 0 {
			c.Workspace.MinimumHostFreeBytes = DefaultWorkspaceMinimumHostFreeBytes
		}
		if c.Workspace.StoreMaxAllocatedBytes < 0 || c.Workspace.MinimumHostFreeBytes < 0 {
			return fmt.Errorf("%w: workspace storage limits must be non-negative", ErrConfigInvalid)
		}
		for name, path := range c.Workspace.LocalPackageOverrides {
			if strings.TrimSpace(name) == "" {
				return fmt.Errorf("%w: workspace.local_package_overrides contains an empty package name", ErrConfigInvalid)
			}
			if !filepath.IsAbs(path) {
				return fmt.Errorf("%w: workspace.local_package_overrides[%q] must be an absolute path", ErrConfigInvalid, name)
			}
		}
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

	// Strict decode: an unknown (typo'd) key silently falling back to a
	// default is the most expensive failure class for an automated caller.
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	var cfg Config
	if err := dec.Decode(&cfg); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrConfigInvalid, err)
	}
	// Decode exactly one JSON value. Decoder.Decode accepts a valid object
	// followed by another value unless callers explicitly require EOF; without
	// this check a generated config such as `{} {}` is only partially read.
	if err := dec.Decode(&struct{}{}); err != io.EOF {
		if err == nil {
			err = errors.New("multiple JSON values")
		}
		return nil, fmt.Errorf("%w: trailing data after config object: %v", ErrConfigInvalid, err)
	}

	if cfg.SchemaVersion == "" {
		return nil, fmt.Errorf("%w: schema_version is required", ErrConfigInvalid)
	}
	if cfg.SchemaVersion != "1" {
		return nil, fmt.Errorf("%w: unsupported schema_version %q (this testplay understands \"1\")", ErrConfigInvalid, cfg.SchemaVersion)
	}

	// Store the directory containing the config file
	cfg.configDir = filepath.Dir(filepath.Clean(path))

	return &cfg, nil
}

package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

var (
	ErrConfigNotFound = errors.New("fastplay.json not found")
	ErrConfigInvalid  = errors.New("fastplay.json is invalid")
)

type Config struct {
	SchemaVersion string   `json:"schema_version"`
	UnityPath     string   `json:"unity_path"`
	ProjectPath   string   `json:"project_path"`
	Timeout       Timeouts `json:"timeout"`
	ResultDir     string   `json:"result_dir"`
	configDir     string   // unexported: directory containing fastplay.json
}

type Timeouts struct {
	CompileMs int64 `json:"compile_ms"`
	TestMs    int64 `json:"test_ms"`
	TotalMs   int64 `json:"total_ms"`
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

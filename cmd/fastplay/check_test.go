package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"testing"

	"github.com/Kubonsang/testplay-runner/internal/config"
)

func TestCheckCmd_ReadyTrue(t *testing.T) {
	cfg := &config.Config{
		SchemaVersion: "1",
		UnityPath:     "/fake/unity",
		ProjectPath:   "/fake/project",
	}
	var buf bytes.Buffer
	code := runCheck(&buf, checkDeps{
		loadConfig: func(string) (*config.Config, error) { return cfg, nil },
		fileExists: func(string) bool { return true },
		configPath: "fastplay.json",
	})
	if code != 0 {
		t.Errorf("expected exit 0, got %d", code)
	}
	var out map[string]any
	json.Unmarshal(buf.Bytes(), &out)
	if out["ready"] != true {
		t.Errorf("expected ready:true, got %v", out["ready"])
	}
}

func TestCheckCmd_ConfigMissing_Exit5(t *testing.T) {
	var buf bytes.Buffer
	code := runCheck(&buf, checkDeps{
		loadConfig: func(string) (*config.Config, error) {
			return nil, fmt.Errorf("%w", config.ErrConfigNotFound)
		},
		fileExists: func(string) bool { return false },
		configPath: "fastplay.json",
	})
	if code != 5 {
		t.Errorf("expected exit 5, got %d", code)
	}
	var out map[string]any
	json.Unmarshal(buf.Bytes(), &out)
	if out["ready"] != false {
		t.Error("ready should be false")
	}
}

func TestCheckCmd_UnityNotFound_Exit1WithHint(t *testing.T) {
	cfg := &config.Config{
		SchemaVersion: "1",
		UnityPath:     "/fake/unity",
		ProjectPath:   "/fake/project",
	}
	var buf bytes.Buffer
	code := runCheck(&buf, checkDeps{
		loadConfig: func(string) (*config.Config, error) { return cfg, nil },
		fileExists: func(path string) bool {
			return path != "/fake/unity" // Unity binary missing
		},
		configPath: "fastplay.json",
	})
	if code != 1 {
		t.Errorf("expected exit 1, got %d", code)
	}
	var out map[string]any
	json.Unmarshal(buf.Bytes(), &out)
	if out["hint"] == nil {
		t.Error("hint field required on exit 1")
	}
	if out["ready"] != false {
		t.Error("ready should be false")
	}
}

func TestCheckCmd_InvalidConfig_Exit5(t *testing.T) {
	// compile_ms set without test_ms → validation error → exit 5
	cfg := &config.Config{
		SchemaVersion: "1",
		UnityPath:     "/fake/unity",
		ProjectPath:   "/fake/project",
		Timeout:       config.Timeouts{CompileMs: 1000}, // test_ms missing → invalid
	}
	var buf bytes.Buffer
	code := runCheck(&buf, checkDeps{
		loadConfig: func(string) (*config.Config, error) { return cfg, nil },
		fileExists: func(string) bool { return true },
		configPath: "fastplay.json",
	})
	if code != 5 {
		t.Errorf("expected exit 5, got %d", code)
	}
	var out map[string]any
	json.Unmarshal(buf.Bytes(), &out)
	if out["ready"] != false {
		t.Error("ready should be false")
	}
	if out["hint"] != nil {
		t.Error("hint must not be present on exit 5 (config error)")
	}
}

func TestCheckCmd_ProjectDirMissing_Exit1WithHint(t *testing.T) {
	cfg := &config.Config{
		SchemaVersion: "1",
		UnityPath:     "/fake/unity",
		ProjectPath:   "/fake/project",
	}
	var buf bytes.Buffer
	code := runCheck(&buf, checkDeps{
		loadConfig: func(string) (*config.Config, error) { return cfg, nil },
		fileExists: func(path string) bool {
			return path != "/fake/project" // project dir missing
		},
		configPath: "fastplay.json",
	})
	if code != 1 {
		t.Errorf("expected exit 1, got %d", code)
	}
	var out map[string]any
	json.Unmarshal(buf.Bytes(), &out)
	if out["hint"] == nil {
		t.Error("hint field required on exit 1")
	}
}

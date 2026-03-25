package config_test

import (
	"errors"
	"testing"

	"github.com/Kubonsang/testplay-runner/internal/config"
)

func TestLoad_ValidConfig(t *testing.T) {
	cfg, err := config.Load("testdata/valid.json")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.SchemaVersion != "1" {
		t.Errorf("got schema_version %q, want %q", cfg.SchemaVersion, "1")
	}
	if cfg.Timeout.CompileMs != 120000 {
		t.Errorf("got compile_ms %d, want 120000", cfg.Timeout.CompileMs)
	}
}

func TestLoad_FileNotFound(t *testing.T) {
	_, err := config.Load("testdata/nonexistent.json")
	if !errors.Is(err, config.ErrConfigNotFound) {
		t.Errorf("got %v, want ErrConfigNotFound", err)
	}
}

func TestLoad_InvalidJSON(t *testing.T) {
	_, err := config.Load("testdata/invalid_json.json")
	if !errors.Is(err, config.ErrConfigInvalid) {
		t.Errorf("got %v, want ErrConfigInvalid", err)
	}
}

func TestLoad_MissingSchemaVersion(t *testing.T) {
	_, err := config.Load("testdata/missing_schema.json")
	if !errors.Is(err, config.ErrConfigInvalid) {
		t.Errorf("got %v, want ErrConfigInvalid", err)
	}
}

func TestValidate_UnityPathFallsBackToEnv(t *testing.T) {
	t.Setenv("UNITY_PATH", "/fake/unity")
	cfg := &config.Config{SchemaVersion: "1", ProjectPath: "/tmp/proj"}
	if err := cfg.Validate(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.UnityPath != "/fake/unity" {
		t.Errorf("expected unity path from env, got %q", cfg.UnityPath)
	}
}

func TestValidate_MissingUnityPath(t *testing.T) {
	t.Setenv("UNITY_PATH", "")
	cfg := &config.Config{SchemaVersion: "1", ProjectPath: "/tmp/proj"}
	err := cfg.Validate()
	if !errors.Is(err, config.ErrUnityPathMissing) {
		t.Errorf("got %v, want ErrUnityPathMissing", err)
	}
}

func TestValidate_DefaultResultDir(t *testing.T) {
	t.Setenv("UNITY_PATH", "/fake/unity")
	cfg := &config.Config{SchemaVersion: "1", ProjectPath: "/tmp/proj"}
	_ = cfg.Validate()
	if cfg.ResultDir != ".fastplay/results" {
		t.Errorf("expected default result_dir, got %q", cfg.ResultDir)
	}
}

func TestValidate_NegativeTimeout_ReturnsError(t *testing.T) {
	t.Setenv("UNITY_PATH", "/fake/unity")
	cfg := &config.Config{
		SchemaVersion: "1",
		ProjectPath:   "/tmp/proj",
		Timeout:       config.Timeouts{TotalMs: -1},
	}
	err := cfg.Validate()
	if !errors.Is(err, config.ErrConfigInvalid) {
		t.Errorf("got %v, want ErrConfigInvalid", err)
	}
}

func TestValidate_DefaultTotalMs(t *testing.T) {
	t.Setenv("UNITY_PATH", "/fake/unity")
	cfg := &config.Config{SchemaVersion: "1", ProjectPath: "/tmp/proj"}
	if err := cfg.Validate(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.Timeout.TotalMs != 300000 {
		t.Errorf("TotalMs default: got %d, want 300000", cfg.Timeout.TotalMs)
	}
	// compile_ms and test_ms stay at zero — they are reserved fields
	if cfg.Timeout.CompileMs != 0 {
		t.Errorf("CompileMs should remain 0 (reserved), got %d", cfg.Timeout.CompileMs)
	}
	if cfg.Timeout.TestMs != 0 {
		t.Errorf("TestMs should remain 0 (reserved), got %d", cfg.Timeout.TestMs)
	}
}

func TestValidate_NegativeCompileMs_IsAccepted(t *testing.T) {
	// compile_ms has no runtime effect; negative values are tolerated
	t.Setenv("UNITY_PATH", "/fake/unity")
	cfg := &config.Config{
		SchemaVersion: "1",
		ProjectPath:   "/tmp/proj",
		Timeout:       config.Timeouts{CompileMs: -1, TotalMs: 300000},
	}
	if err := cfg.Validate(); err != nil {
		t.Errorf("expected no error for negative compile_ms (reserved field), got %v", err)
	}
}

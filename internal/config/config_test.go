package config_test

import (
	"errors"
	"testing"

	"github.com/fastplay/runner/internal/config"
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

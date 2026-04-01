package scenario_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/Kubonsang/testplay-runner/internal/scenario"
)

func TestLoad_ValidFile(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	content := `{
		"schema_version": "1",
		"instances": [
			{"role": "Host",   "config": "./host/fastplay.json"},
			{"role": "Client", "config": "./client/fastplay.json"}
		]
	}`
	path := filepath.Join(dir, "scenario.json")
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}

	sf, err := scenario.Load(path)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(sf.Instances) != 2 {
		t.Fatalf("expected 2 instances, got %d", len(sf.Instances))
	}
	if sf.Instances[0].Role != "Host" {
		t.Errorf("expected role 'Host', got %q", sf.Instances[0].Role)
	}
	// Config path should resolve relative to the scenario file's directory.
	got := sf.ConfigPath(sf.Instances[0])
	want := filepath.Join(dir, "host", "fastplay.json")
	if got != want {
		t.Errorf("ConfigPath: got %q, want %q", got, want)
	}
}

func TestLoad_MissingFile(t *testing.T) {
	t.Parallel()
	_, err := scenario.Load("/nonexistent/scenario.json")
	if err == nil {
		t.Fatal("expected error for missing file, got nil")
	}
}

func TestLoad_EmptyInstances(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "scenario.json")
	_ = os.WriteFile(path, []byte(`{"schema_version":"1","instances":[]}`), 0644)

	_, err := scenario.Load(path)
	if err == nil {
		t.Fatal("expected error for empty instances, got nil")
	}
}

func TestLoad_MissingSchemaVersion(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "scenario.json")
	_ = os.WriteFile(path, []byte(`{"instances":[{"role":"Host","config":"./f.json"}]}`), 0644)

	_, err := scenario.Load(path)
	if err == nil {
		t.Fatal("expected error for missing schema_version, got nil")
	}
}

func TestLoad_MissingRole(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "scenario.json")
	_ = os.WriteFile(path, []byte(`{"schema_version":"1","instances":[{"config":"./f.json"}]}`), 0644)

	_, err := scenario.Load(path)
	if err == nil {
		t.Fatal("expected error for missing role, got nil")
	}
}

func TestLoad_AbsoluteConfigPath(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	absConfig := filepath.Join(dir, "absolute", "fastplay.json")
	content := `{"schema_version":"1","instances":[{"role":"Host","config":"` + absConfig + `"}]}`
	path := filepath.Join(dir, "scenario.json")
	_ = os.WriteFile(path, []byte(content), 0644)

	sf, _ := scenario.Load(path)
	if sf.ConfigPath(sf.Instances[0]) != absConfig {
		t.Errorf("absolute config path should not be joined: got %q", sf.ConfigPath(sf.Instances[0]))
	}
}

func TestLoad_MissingConfig(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "scenario.json")
	_ = os.WriteFile(path, []byte(`{"schema_version":"1","instances":[{"role":"Host"}]}`), 0644)

	_, err := scenario.Load(path)
	if err == nil {
		t.Fatal("expected error for missing config, got nil")
	}
}

package scenario_test

import (
	"errors"
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
			{"role": "Host",   "config": "./host/testplay.json"},
			{"role": "Client", "config": "./client/testplay.json"}
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
	want := filepath.Join(dir, "host", "testplay.json")
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
	absConfig := filepath.Join(dir, "absolute", "testplay.json")
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

func TestLoad_DependsOn_ValidReference(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "scenario.json")
	content := `{
		"schema_version": "1",
		"instances": [
			{"role": "host",   "config": "host.json"},
			{"role": "client", "config": "client.json", "depends_on": "host", "ready_timeout_ms": 5000}
		]
	}`
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
	sf, err := scenario.Load(path)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if sf.Instances[1].DependsOn != "host" {
		t.Errorf("expected depends_on=host, got %q", sf.Instances[1].DependsOn)
	}
	if sf.Instances[1].ReadyTimeoutMs != 5000 {
		t.Errorf("expected ready_timeout_ms=5000, got %d", sf.Instances[1].ReadyTimeoutMs)
	}
}

func TestLoad_DependsOn_InvalidReference(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "scenario.json")
	content := `{
		"schema_version": "1",
		"instances": [
			{"role": "client", "config": "client.json", "depends_on": "nonexistent"}
		]
	}`
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
	_, err := scenario.Load(path)
	if err == nil {
		t.Fatal("expected error for invalid depends_on")
	}
}

func TestLoad_DuplicateRoles(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "scenario.json")
	content := `{
		"schema_version": "1",
		"instances": [
			{"role": "host", "config": "a.json"},
			{"role": "host", "config": "b.json"}
		]
	}`
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
	_, err := scenario.Load(path)
	if err == nil {
		t.Fatal("expected error for duplicate roles")
	}
}

func TestInstanceSpec_EffectiveReadyPhase_Default(t *testing.T) {
	inst := scenario.InstanceSpec{Role: "host", Config: "host.json"}
	if inst.EffectiveReadyPhase() != "compiling" {
		t.Errorf("expected default ready phase 'compiling', got %q", inst.EffectiveReadyPhase())
	}
}

func TestInstanceSpec_EffectiveReadyPhase_Custom(t *testing.T) {
	inst := scenario.InstanceSpec{Role: "host", Config: "host.json", ReadyPhase: "running"}
	if inst.EffectiveReadyPhase() != "running" {
		t.Errorf("expected 'running', got %q", inst.EffectiveReadyPhase())
	}
}

func TestInstanceSpec_EffectiveReadyTimeoutMs_Default(t *testing.T) {
	inst := scenario.InstanceSpec{Role: "host", Config: "host.json"}
	if inst.EffectiveReadyTimeoutMs() != 30000 {
		t.Errorf("expected default timeout 30000, got %d", inst.EffectiveReadyTimeoutMs())
	}
}

func TestLoad_CircularDependency_TwoNodes(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "scenario.json")
	content := `{
		"schema_version": "1",
		"instances": [
			{"role": "host",   "config": "host.json", "depends_on": "client"},
			{"role": "client", "config": "client.json", "depends_on": "host"}
		]
	}`
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
	_, err := scenario.Load(path)
	if err == nil {
		t.Fatal("expected error for circular dependency")
	}
}

func TestLoad_CircularDependency_ThreeNodes(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "scenario.json")
	content := `{
		"schema_version": "1",
		"instances": [
			{"role": "a", "config": "a.json", "depends_on": "c"},
			{"role": "b", "config": "b.json", "depends_on": "a"},
			{"role": "c", "config": "c.json", "depends_on": "b"}
		]
	}`
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
	_, err := scenario.Load(path)
	if err == nil {
		t.Fatal("expected error for circular dependency (3-node cycle)")
	}
}

func TestLoad_LinearDependency_NoCycle(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "scenario.json")
	content := `{
		"schema_version": "1",
		"instances": [
			{"role": "host",    "config": "host.json"},
			{"role": "client1", "config": "c1.json", "depends_on": "host"},
			{"role": "client2", "config": "c2.json", "depends_on": "host"}
		]
	}`
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
	sf, err := scenario.Load(path)
	if err != nil {
		t.Fatalf("unexpected error for valid linear dependency: %v", err)
	}
	if len(sf.Instances) != 3 {
		t.Errorf("expected 3 instances, got %d", len(sf.Instances))
	}
}

func TestLoad_EnvField_Parsed(t *testing.T) {
	dir := t.TempDir()
	content := `{
		"schema_version": "1",
		"instances": [
			{"role": "host", "config": "host.json", "env": {"PORT": "7777", "ROLE": "HOST"}},
			{"role": "client", "config": "client.json", "env": {"PORT": "7778", "ROLE": "CLIENT"}}
		]
	}`
	path := filepath.Join(dir, "scenario.json")
	os.WriteFile(path, []byte(content), 0644)
	sf, err := scenario.Load(path)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(sf.Instances[0].Env) != 2 {
		t.Errorf("host env: got %d entries, want 2", len(sf.Instances[0].Env))
	}
	if sf.Instances[0].Env["PORT"] != "7777" {
		t.Errorf("host PORT = %q, want %q", sf.Instances[0].Env["PORT"], "7777")
	}
	if sf.Instances[1].Env["ROLE"] != "CLIENT" {
		t.Errorf("client ROLE = %q, want %q", sf.Instances[1].Env["ROLE"], "CLIENT")
	}
}

func TestLoad_EnvEmptyKey_Rejected(t *testing.T) {
	dir := t.TempDir()
	content := `{
		"schema_version": "1",
		"instances": [
			{"role": "host", "config": "host.json", "env": {"": "value"}}
		]
	}`
	path := filepath.Join(dir, "scenario.json")
	os.WriteFile(path, []byte(content), 0644)
	_, err := scenario.Load(path)
	if err == nil {
		t.Fatal("expected error for empty env key")
	}
	if !errors.Is(err, scenario.ErrScenarioInvalid) {
		t.Errorf("expected ErrScenarioInvalid, got %v", err)
	}
}

func TestLoad_NoEnvField_Valid(t *testing.T) {
	dir := t.TempDir()
	content := `{
		"schema_version": "1",
		"instances": [
			{"role": "host", "config": "host.json"}
		]
	}`
	path := filepath.Join(dir, "scenario.json")
	os.WriteFile(path, []byte(content), 0644)
	sf, err := scenario.Load(path)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if sf.Instances[0].Env != nil {
		t.Errorf("expected nil env, got %v", sf.Instances[0].Env)
	}
}

func TestLoad_EnvKeyWithEquals_Rejected(t *testing.T) {
	dir := t.TempDir()
	content := `{
		"schema_version": "1",
		"instances": [
			{"role": "host", "config": "host.json", "env": {"KEY=BAD": "value"}}
		]
	}`
	path := filepath.Join(dir, "scenario.json")
	os.WriteFile(path, []byte(content), 0644)
	_, err := scenario.Load(path)
	if err == nil {
		t.Fatal("expected error for env key containing '='")
	}
	if !errors.Is(err, scenario.ErrScenarioInvalid) {
		t.Errorf("expected ErrScenarioInvalid, got %v", err)
	}
}

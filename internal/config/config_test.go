package config_test

import (
	"errors"
	"testing"

	"github.com/Kubonsang/testplay-runner/internal/config"
)

func TestLoad_ValidConfig(t *testing.T) {
	t.Setenv("UNITY_PATH", "/fake/unity")
	cfg, err := config.Load("testdata/valid.json")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.SchemaVersion != "1" {
		t.Errorf("got schema_version %q, want %q", cfg.SchemaVersion, "1")
	}
	// Verify a loaded config can be validated successfully
	if err := cfg.Validate(true); err != nil {
		t.Errorf("loaded config failed Validate(): %v", err)
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
	if err := cfg.Validate(true); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.UnityPath != "/fake/unity" {
		t.Errorf("expected unity path from env, got %q", cfg.UnityPath)
	}
}

func TestValidate_MissingUnityPath(t *testing.T) {
	t.Setenv("UNITY_PATH", "")
	cfg := &config.Config{SchemaVersion: "1", ProjectPath: "/tmp/proj"}
	err := cfg.Validate(true)
	if !errors.Is(err, config.ErrUnityPathMissing) {
		t.Errorf("got %v, want ErrUnityPathMissing", err)
	}
}

func TestValidate_DefaultResultDir(t *testing.T) {
	t.Setenv("UNITY_PATH", "/fake/unity")
	cfg := &config.Config{SchemaVersion: "1", ProjectPath: "/tmp/proj"}
	_ = cfg.Validate(true)
	if cfg.ResultDir != ".testplay/results" {
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
	err := cfg.Validate(true)
	if !errors.Is(err, config.ErrConfigInvalid) {
		t.Errorf("got %v, want ErrConfigInvalid", err)
	}
}

func TestValidate_DefaultTotalMs(t *testing.T) {
	t.Setenv("UNITY_PATH", "/fake/unity")
	cfg := &config.Config{SchemaVersion: "1", ProjectPath: "/tmp/proj"}
	if err := cfg.Validate(true); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.Timeout.TotalMs != 300000 {
		t.Errorf("TotalMs default: got %d, want 300000", cfg.Timeout.TotalMs)
	}
	// compile_ms and test_ms stay at zero when not set — no default applied
	if cfg.Timeout.CompileMs != 0 {
		t.Errorf("CompileMs should remain 0 when unset, got %d", cfg.Timeout.CompileMs)
	}
	if cfg.Timeout.TestMs != 0 {
		t.Errorf("TestMs should remain 0 when unset, got %d", cfg.Timeout.TestMs)
	}
}

func TestValidate_NegativeCompileMs_ReturnsError(t *testing.T) {
	t.Setenv("UNITY_PATH", "/fake/unity")
	cfg := &config.Config{
		SchemaVersion: "1",
		ProjectPath:   "/tmp/proj",
		Timeout:       config.Timeouts{CompileMs: -1, TestMs: 5000, TotalMs: 300000},
	}
	err := cfg.Validate(true)
	if !errors.Is(err, config.ErrConfigInvalid) {
		t.Errorf("expected ErrConfigInvalid for negative compile_ms, got %v", err)
	}
}

func TestValidate_NegativeTestMs_ReturnsError(t *testing.T) {
	t.Setenv("UNITY_PATH", "/fake/unity")
	cfg := &config.Config{
		SchemaVersion: "1",
		ProjectPath:   "/tmp/proj",
		Timeout:       config.Timeouts{CompileMs: 5000, TestMs: -1, TotalMs: 300000},
	}
	err := cfg.Validate(true)
	if !errors.Is(err, config.ErrConfigInvalid) {
		t.Errorf("expected ErrConfigInvalid for negative test_ms, got %v", err)
	}
}

func TestValidate_OnlyCompileMsSet_ReturnsError(t *testing.T) {
	t.Setenv("UNITY_PATH", "/fake/unity")
	cfg := &config.Config{
		SchemaVersion: "1",
		ProjectPath:   "/tmp/proj",
		Timeout:       config.Timeouts{CompileMs: 5000, TotalMs: 300000},
	}
	err := cfg.Validate(true)
	if !errors.Is(err, config.ErrConfigInvalid) {
		t.Errorf("expected ErrConfigInvalid when only compile_ms is set, got %v", err)
	}
}

func TestValidate_OnlyTestMsSet_ReturnsError(t *testing.T) {
	t.Setenv("UNITY_PATH", "/fake/unity")
	cfg := &config.Config{
		SchemaVersion: "1",
		ProjectPath:   "/tmp/proj",
		Timeout:       config.Timeouts{TestMs: 5000, TotalMs: 300000},
	}
	err := cfg.Validate(true)
	if !errors.Is(err, config.ErrConfigInvalid) {
		t.Errorf("expected ErrConfigInvalid when only test_ms is set, got %v", err)
	}
}

func TestValidate_BothCompileAndTestMsSet_IsAccepted(t *testing.T) {
	t.Setenv("UNITY_PATH", "/fake/unity")
	cfg := &config.Config{
		SchemaVersion: "1",
		ProjectPath:   "/tmp/proj",
		Timeout:       config.Timeouts{CompileMs: 30000, TestMs: 120000, TotalMs: 300000},
	}
	if err := cfg.Validate(true); err != nil {
		t.Errorf("expected no error when both compile_ms and test_ms are set, got %v", err)
	}
}

func TestValidate_RequireUnityFalse_SkipsUnityCheck(t *testing.T) {
	// No UNITY_PATH env, no unity_path in config — should not error when requireUnity=false
	t.Setenv("UNITY_PATH", "")
	cfg := &config.Config{SchemaVersion: "1", ProjectPath: "/tmp/proj"}
	if err := cfg.Validate(false); err != nil {
		t.Errorf("expected no error with requireUnity=false, got %v", err)
	}
}

func TestValidate_PlayMode_IsAccepted(t *testing.T) {
	t.Setenv("UNITY_PATH", "/fake/unity")
	cfg := &config.Config{
		SchemaVersion: "1",
		ProjectPath:   "/tmp/proj",
		TestPlatform:  "play_mode",
	}
	if err := cfg.Validate(true); err != nil {
		t.Fatalf("expected no error for play_mode, got %v", err)
	}
	if cfg.TestPlatform != "play_mode" {
		t.Errorf("expected test_platform 'play_mode', got %q", cfg.TestPlatform)
	}
}

func TestValidate_EmptyTestPlatform_DefaultsToEditMode(t *testing.T) {
	t.Setenv("UNITY_PATH", "/fake/unity")
	cfg := &config.Config{SchemaVersion: "1", ProjectPath: "/tmp/proj"}
	if err := cfg.Validate(true); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.TestPlatform != "edit_mode" {
		t.Errorf("expected default 'edit_mode', got %q", cfg.TestPlatform)
	}
}

func TestValidate_InvalidTestPlatform_ReturnsError(t *testing.T) {
	t.Setenv("UNITY_PATH", "/fake/unity")
	cfg := &config.Config{
		SchemaVersion: "1",
		ProjectPath:   "/tmp/proj",
		TestPlatform:  "web_gl",
	}
	err := cfg.Validate(true)
	if !errors.Is(err, config.ErrConfigInvalid) {
		t.Errorf("got %v, want ErrConfigInvalid for invalid test_platform", err)
	}
}

func intPtr(v int) *int { return &v }

func TestValidate_RetentionDefaults(t *testing.T) {
	cfg := &config.Config{
		SchemaVersion: "1",
		UnityPath:     "/fake/unity",
	}
	if err := cfg.Validate(true); err != nil {
		t.Fatal(err)
	}
	if cfg.Retention.MaxRuns == nil || *cfg.Retention.MaxRuns != 30 {
		t.Errorf("Retention.MaxRuns = %v, want 30 (default)", cfg.Retention.MaxRuns)
	}
}

func TestValidate_RetentionExplicit(t *testing.T) {
	cfg := &config.Config{
		SchemaVersion: "1",
		UnityPath:     "/fake/unity",
		Retention:     config.RetentionConfig{MaxRuns: intPtr(50)},
	}
	if err := cfg.Validate(true); err != nil {
		t.Fatal(err)
	}
	if *cfg.Retention.MaxRuns != 50 {
		t.Errorf("Retention.MaxRuns = %d, want 50", *cfg.Retention.MaxRuns)
	}
}

func TestValidate_RetentionZero_DisablesPruning(t *testing.T) {
	cfg := &config.Config{
		SchemaVersion: "1",
		UnityPath:     "/fake/unity",
		Retention:     config.RetentionConfig{MaxRuns: intPtr(0)},
	}
	if err := cfg.Validate(true); err != nil {
		t.Fatal(err)
	}
	if *cfg.Retention.MaxRuns != 0 {
		t.Errorf("Retention.MaxRuns = %d, want 0 (pruning disabled)", *cfg.Retention.MaxRuns)
	}
}

func TestValidate_RetentionNegative_Rejected(t *testing.T) {
	cfg := &config.Config{
		SchemaVersion: "1",
		UnityPath:     "/fake/unity",
		Retention:     config.RetentionConfig{MaxRuns: intPtr(-1)},
	}
	err := cfg.Validate(true)
	if err == nil {
		t.Fatal("expected error for negative max_runs")
	}
	if !errors.Is(err, config.ErrConfigInvalid) {
		t.Errorf("expected ErrConfigInvalid, got %v", err)
	}
}

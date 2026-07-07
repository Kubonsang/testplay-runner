package config_test

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
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
	want := filepath.Join("/tmp/proj", ".testplay", "results")
	if cfg.ResultDir != want {
		t.Errorf("expected default result_dir anchored to project_path (%q), got %q", want, cfg.ResultDir)
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

func TestLoad_UnknownTopLevelKey_Rejected(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "testplay.json")
	// "test_platfrom" is a typo of test_platform — must not be silently dropped.
	body := `{"schema_version":"1","unity_path":"/u","test_platfrom":"play_mode"}`
	if err := os.WriteFile(path, []byte(body), 0644); err != nil {
		t.Fatal(err)
	}
	_, err := config.Load(path)
	if err == nil {
		t.Fatal("expected error for unknown key, got nil (typo silently dropped)")
	}
	if !errors.Is(err, config.ErrConfigInvalid) {
		t.Errorf("expected ErrConfigInvalid, got %v", err)
	}
	if !strings.Contains(err.Error(), "test_platfrom") {
		t.Errorf("error should name the offending key, got: %v", err)
	}
}

func TestLoad_UnknownNestedKey_Rejected(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "testplay.json")
	// bridge.enable is a typo of bridge.enabled — silently dropping it would
	// defeat a user's attempt at guaranteed cold hermeticity.
	body := `{"schema_version":"1","unity_path":"/u","bridge":{"enable":false}}`
	if err := os.WriteFile(path, []byte(body), 0644); err != nil {
		t.Fatal(err)
	}
	_, err := config.Load(path)
	if err == nil {
		t.Fatal("expected error for unknown nested key, got nil")
	}
	if !errors.Is(err, config.ErrConfigInvalid) {
		t.Errorf("expected ErrConfigInvalid, got %v", err)
	}
}

func TestLoad_UnsupportedSchemaVersion_Rejected(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "testplay.json")
	body := `{"schema_version":"2","unity_path":"/u"}`
	if err := os.WriteFile(path, []byte(body), 0644); err != nil {
		t.Fatal(err)
	}
	_, err := config.Load(path)
	if err == nil {
		t.Fatal("expected error for unsupported schema_version, got nil")
	}
	if !errors.Is(err, config.ErrConfigInvalid) {
		t.Errorf("expected ErrConfigInvalid, got %v", err)
	}
}

func TestLoad_AllKnownKeys_Accepted(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "testplay.json")
	body := `{
		"schema_version": "1",
		"unity_path": "/u",
		"project_path": "/p",
		"test_platform": "play_mode",
		"timeout": {"total_ms": 60000, "compile_ms": 10000, "test_ms": 20000},
		"result_dir": ".testplay/results",
		"retention": {"max_runs": 5},
		"bridge": {"enabled": false}
	}`
	if err := os.WriteFile(path, []byte(body), 0644); err != nil {
		t.Fatal(err)
	}
	cfg, err := config.Load(path)
	if err != nil {
		t.Fatalf("full valid config must load, got: %v", err)
	}
	if cfg.BridgeEnabled() {
		t.Error("bridge.enabled=false must parse")
	}
}

func TestValidate_RelativeProjectPath_AnchoredToConfigDir(t *testing.T) {
	dir := t.TempDir()
	sub := filepath.Join(dir, "UnityProj")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "testplay.json")
	body := `{"schema_version":"1","unity_path":"/u","project_path":"UnityProj"}`
	if err := os.WriteFile(path, []byte(body), 0644); err != nil {
		t.Fatal(err)
	}
	cfg, err := config.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := cfg.Validate(true); err != nil {
		t.Fatal(err)
	}
	if cfg.ProjectPath != sub {
		t.Errorf("relative project_path must anchor to the config file's directory:\ngot  %q\nwant %q", cfg.ProjectPath, sub)
	}
}

func TestValidate_RelativeResultDir_AnchoredToProjectPath(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "testplay.json")
	body := `{"schema_version":"1","unity_path":"/u","result_dir":"custom/results"}`
	if err := os.WriteFile(path, []byte(body), 0644); err != nil {
		t.Fatal(err)
	}
	cfg, err := config.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := cfg.Validate(true); err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(dir, "custom", "results")
	if cfg.ResultDir != want {
		t.Errorf("relative result_dir must anchor to project_path:\ngot  %q\nwant %q", cfg.ResultDir, want)
	}
}

func TestValidate_DefaultResultDir_AnchoredToProjectPath(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "testplay.json")
	body := `{"schema_version":"1","unity_path":"/u"}`
	if err := os.WriteFile(path, []byte(body), 0644); err != nil {
		t.Fatal(err)
	}
	cfg, err := config.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := cfg.Validate(true); err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(dir, ".testplay", "results")
	if cfg.ResultDir != want {
		t.Errorf("default result_dir must anchor to project_path:\ngot  %q\nwant %q", cfg.ResultDir, want)
	}
}

func TestValidate_AbsolutePaths_Unchanged(t *testing.T) {
	dir := t.TempDir()
	proj := filepath.Join(dir, "proj")
	res := filepath.Join(dir, "elsewhere", "results")
	cfg := &config.Config{
		SchemaVersion: "1",
		UnityPath:     "/u",
		ProjectPath:   proj,
		ResultDir:     res,
	}
	if err := cfg.Validate(true); err != nil {
		t.Fatal(err)
	}
	if cfg.ProjectPath != proj || cfg.ResultDir != res {
		t.Errorf("absolute paths must pass through unchanged, got %q %q", cfg.ProjectPath, cfg.ResultDir)
	}
}

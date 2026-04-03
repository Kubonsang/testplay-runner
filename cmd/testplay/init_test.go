package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestInitCmd_CreatesConfigFile(t *testing.T) {
	dir := t.TempDir()
	outPath := filepath.Join(dir, "testplay.json")

	var buf bytes.Buffer
	code := runInit(&buf, initDeps{
		unityPath:  "/fake/unity",
		projectDir: dir,
		outputPath: outPath,
		fileExists: func(string) bool { return false },
	})
	if code != 0 {
		t.Fatalf("expected exit 0, got %d; output: %s", code, buf.String())
	}

	data, err := os.ReadFile(outPath)
	if err != nil {
		t.Fatalf("config file not created: %v", err)
	}

	var cfg map[string]any
	if err := json.Unmarshal(data, &cfg); err != nil {
		t.Fatalf("config file is not valid JSON: %v", err)
	}
	if cfg["schema_version"] != "1" {
		t.Errorf("expected schema_version 1, got %v", cfg["schema_version"])
	}
	if cfg["unity_path"] != "/fake/unity" {
		t.Errorf("expected unity_path /fake/unity, got %v", cfg["unity_path"])
	}
	if cfg["project_path"] != dir {
		t.Errorf("expected project_path %s, got %v", dir, cfg["project_path"])
	}

	var out map[string]any
	if err := json.Unmarshal(buf.Bytes(), &out); err != nil {
		t.Fatalf("stdout not valid JSON: %v", err)
	}
	if out["created"] != outPath {
		t.Errorf("expected created=%s, got %v", outPath, out["created"])
	}
}

func TestInitCmd_RefusesOverwrite(t *testing.T) {
	dir := t.TempDir()
	outPath := filepath.Join(dir, "testplay.json")
	os.WriteFile(outPath, []byte(`{}`), 0644)

	var buf bytes.Buffer
	code := runInit(&buf, initDeps{
		unityPath:  "/fake/unity",
		projectDir: dir,
		outputPath: outPath,
		fileExists: func(path string) bool {
			_, err := os.Stat(path)
			return err == nil
		},
	})
	if code != 5 {
		t.Errorf("expected exit 5 (file exists), got %d", code)
	}
}

func TestInitCmd_ForceOverwrites(t *testing.T) {
	dir := t.TempDir()
	outPath := filepath.Join(dir, "testplay.json")
	os.WriteFile(outPath, []byte(`{"old": true}`), 0644)

	var buf bytes.Buffer
	code := runInit(&buf, initDeps{
		unityPath:  "/fake/unity",
		projectDir: dir,
		outputPath: outPath,
		force:      true,
		fileExists: func(path string) bool {
			_, err := os.Stat(path)
			return err == nil
		},
	})
	if code != 0 {
		t.Errorf("expected exit 0 with --force, got %d", code)
	}

	data, _ := os.ReadFile(outPath)
	var cfg map[string]any
	json.Unmarshal(data, &cfg)
	if cfg["schema_version"] != "1" {
		t.Error("expected overwritten config to have schema_version 1")
	}
}

func TestInitCmd_UnityPathFromEnv(t *testing.T) {
	dir := t.TempDir()
	outPath := filepath.Join(dir, "testplay.json")

	var buf bytes.Buffer
	code := runInit(&buf, initDeps{
		unityPath:  "",
		envLookup:  func(string) string { return "/env/unity" },
		projectDir: dir,
		outputPath: outPath,
		fileExists: func(string) bool { return false },
	})
	if code != 0 {
		t.Fatalf("expected exit 0, got %d", code)
	}

	data, _ := os.ReadFile(outPath)
	var cfg map[string]any
	json.Unmarshal(data, &cfg)
	if cfg["unity_path"] != "/env/unity" {
		t.Errorf("expected unity_path from env, got %v", cfg["unity_path"])
	}
}

func TestInitCmd_EmptyUnityPath_StillCreates(t *testing.T) {
	dir := t.TempDir()
	outPath := filepath.Join(dir, "testplay.json")

	var buf bytes.Buffer
	code := runInit(&buf, initDeps{
		unityPath:  "",
		envLookup:  func(string) string { return "" },
		projectDir: dir,
		outputPath: outPath,
		fileExists: func(string) bool { return false },
	})
	if code != 0 {
		t.Fatalf("expected exit 0 even without unity path, got %d", code)
	}

	data, _ := os.ReadFile(outPath)
	var cfg map[string]any
	json.Unmarshal(data, &cfg)
	if cfg["unity_path"] != "" {
		t.Errorf("expected empty unity_path, got %v", cfg["unity_path"])
	}

	// Verify warnings field in stdout JSON
	var out map[string]any
	json.Unmarshal(buf.Bytes(), &out)
	warnings, ok := out["warnings"].([]any)
	if !ok || len(warnings) == 0 {
		t.Error("expected warnings field in stdout when unity_path is empty")
	}
}

func TestInitCmd_TestPlatformFlag(t *testing.T) {
	dir := t.TempDir()
	outPath := filepath.Join(dir, "testplay.json")

	var buf bytes.Buffer
	code := runInit(&buf, initDeps{
		unityPath:    "/fake/unity",
		projectDir:   dir,
		outputPath:   outPath,
		testPlatform: "play_mode",
		fileExists:   func(string) bool { return false },
	})
	if code != 0 {
		t.Fatalf("expected exit 0, got %d", code)
	}

	data, _ := os.ReadFile(outPath)
	var cfg map[string]any
	json.Unmarshal(data, &cfg)
	if cfg["test_platform"] != "play_mode" {
		t.Errorf("expected test_platform play_mode, got %v", cfg["test_platform"])
	}
}

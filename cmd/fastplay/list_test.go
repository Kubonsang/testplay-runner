package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestListCmd_FindsTestMethods(t *testing.T) {
	// Create a fake Unity project structure
	dir := t.TempDir()
	testDir := filepath.Join(dir, "Assets", "Tests")
	if err := os.MkdirAll(testDir, 0755); err != nil {
		t.Fatal(err)
	}
	testFile := filepath.Join(testDir, "MyTest.cs")
	content := `using NUnit.Framework;
public class MyTest {
    [Test]
    public void TestAdd() {}
    [Test]
    public void TestSub() {}
    public void NotATest() {}
}
`
	if err := os.WriteFile(testFile, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}

	var buf bytes.Buffer
	code := runList(&buf, listDeps{projectPath: dir})
	if code != 0 {
		t.Errorf("expected exit 0, got %d", code)
	}

	var out struct {
		Tests []string `json:"tests"`
	}
	if err := json.Unmarshal(buf.Bytes(), &out); err != nil {
		t.Fatalf("invalid JSON: %v\noutput: %s", err, buf.String())
	}
	if len(out.Tests) < 2 {
		t.Errorf("expected at least 2 tests, got %d: %v", len(out.Tests), out.Tests)
	}
}

func TestListCmd_EmptyProject_ReturnsEmptyArray(t *testing.T) {
	dir := t.TempDir()
	var buf bytes.Buffer
	code := runList(&buf, listDeps{projectPath: dir})
	if code != 0 {
		t.Errorf("expected exit 0, got %d", code)
	}

	var out map[string]any
	if err := json.Unmarshal(buf.Bytes(), &out); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	tests, ok := out["tests"]
	if !ok {
		t.Error("tests field missing")
	}
	// Must be [] not null
	if tests == nil {
		t.Error("tests must be an empty array, not null")
	}
	// As JSON array, an empty array is represented as []interface{}
	if arr, ok := tests.([]interface{}); ok {
		if len(arr) != 0 {
			t.Errorf("expected empty array, got %v", arr)
		}
	}
}

func TestListCmd_SchemaVersionPresent(t *testing.T) {
	dir := t.TempDir()
	var buf bytes.Buffer
	runList(&buf, listDeps{projectPath: dir})

	var out map[string]any
	json.Unmarshal(buf.Bytes(), &out)
	if out["schema_version"] == nil {
		t.Error("schema_version must be present in list output")
	}
}

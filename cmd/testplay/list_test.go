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

func TestListCmd_NoUnityPath_StillScansFiles(t *testing.T) {
	dir := t.TempDir()
	csContent := `using NUnit.Framework;
public class MyTests {
    [Test]
    public void TestFoo() {}
}`
	if err := os.WriteFile(filepath.Join(dir, "MyTests.cs"), []byte(csContent), 0644); err != nil {
		t.Fatal(err)
	}

	var buf bytes.Buffer
	code := runList(&buf, listDeps{projectPath: dir})
	if code != 0 {
		t.Errorf("expected exit 0, got %d\noutput: %s", code, buf.String())
	}
	var out map[string]any
	if err := json.Unmarshal(buf.Bytes(), &out); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	tests, ok := out["tests"].([]any)
	if !ok || len(tests) == 0 {
		t.Errorf("expected at least one test, got: %v", out["tests"])
	}
}

func TestListCmd_EmptyProjectPath_OutputsJSON(t *testing.T) {
	// projectPath that doesn't exist — should still return valid JSON with empty tests
	var buf bytes.Buffer
	code := runList(&buf, listDeps{projectPath: "/nonexistent/path/that/does/not/exist"})
	if code != 0 {
		t.Errorf("expected exit 0 for missing dir (returns empty), got %d\noutput: %s", code, buf.String())
	}
	var out map[string]any
	if err := json.Unmarshal(buf.Bytes(), &out); err != nil {
		t.Fatalf("output must be valid JSON: %v\n%s", err, buf.String())
	}
	if out["schema_version"] == nil {
		t.Error("schema_version must be present in all JSON responses")
	}
}

func TestListCmd_FindsUnityTestMethods(t *testing.T) {
	dir := t.TempDir()
	testFile := filepath.Join(dir, "PlayTests.cs")
	content := `using NUnit.Framework;
using UnityEngine.TestTools;
using System.Collections;
public class PlayTests {
    [UnityTest]
    public IEnumerator TestSpawn() { yield return null; }
    [UnityTest]
    public IEnumerator TestNetwork() { yield return null; }
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
		t.Errorf("expected at least 2 [UnityTest] methods, got %d: %v", len(out.Tests), out.Tests)
	}
	for _, name := range out.Tests {
		if name != "PlayTests.TestSpawn" && name != "PlayTests.TestNetwork" {
			t.Errorf("unexpected test name: %q", name)
		}
	}
}

func TestListCmd_MultipleAttributes_ExtractsCorrectMethodName(t *testing.T) {
	// [UnityTest] followed by additional attributes like [Category] and [Timeout]
	// should still extract the correct method name, not the attribute name.
	dir := t.TempDir()
	testFile := filepath.Join(dir, "MultiAttr.cs")
	content := `using NUnit.Framework;
using UnityEngine.TestTools;
public class MultiAttr {
    [UnityTest]
    [Category("Network")]
    [Timeout(30000)]
    public System.Collections.IEnumerator TestSpawnWithCategory() { yield return null; }
    [Test]
    [Category("Smoke")]
    public void EditModeWithCategory() {}
}
`
	if err := os.WriteFile(testFile, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}

	var buf bytes.Buffer
	code := runList(&buf, listDeps{projectPath: dir})
	if code != 0 {
		t.Errorf("expected exit 0, got %d\noutput: %s", code, buf.String())
	}

	var out struct {
		Tests []string `json:"tests"`
	}
	if err := json.Unmarshal(buf.Bytes(), &out); err != nil {
		t.Fatalf("invalid JSON: %v\noutput: %s", err, buf.String())
	}
	if len(out.Tests) != 2 {
		t.Errorf("expected 2 tests, got %d: %v", len(out.Tests), out.Tests)
	}
	for _, name := range out.Tests {
		if name != "MultiAttr.TestSpawnWithCategory" && name != "MultiAttr.EditModeWithCategory" {
			t.Errorf("unexpected test name %q — attribute line mis-parsed as method name", name)
		}
	}
}

func TestListCmd_MixedTestAndUnityTest(t *testing.T) {
	dir := t.TempDir()
	testFile := filepath.Join(dir, "Mixed.cs")
	content := `using NUnit.Framework;
public class Mixed {
    [Test]
    public void EditModeTest() {}
    [UnityTest]
    public System.Collections.IEnumerator PlayModeTest() { yield return null; }
}
`
	if err := os.WriteFile(testFile, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}

	var buf bytes.Buffer
	runList(&buf, listDeps{projectPath: dir})

	var out struct {
		Tests []string `json:"tests"`
	}
	if err := json.Unmarshal(buf.Bytes(), &out); err != nil {
		t.Fatalf("invalid JSON: %v\noutput: %s", err, buf.String())
	}
	if len(out.Tests) != 2 {
		t.Errorf("expected 2 tests ([Test] + [UnityTest]), got %d: %v", len(out.Tests), out.Tests)
	}
}

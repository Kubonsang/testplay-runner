package unityvhdxfixture

import (
	"os"
	"path/filepath"
	"testing"
)

func TestParsePlatformResultIgnoresDurationAndSorts(t *testing.T) {
	dir := t.TempDir()
	first := filepath.Join(dir, "first.xml")
	second := filepath.Join(dir, "second.xml")
	writeXML := func(path, duration, cases string) {
		t.Helper()
		data := `<?xml version="1.0"?><test-run total="2" passed="2" failed="0" skipped="0" duration="` + duration + `"><test-suite type="Assembly" name="Tests">` + cases + `</test-suite></test-run>`
		if err := os.WriteFile(path, []byte(data), 0600); err != nil {
			t.Fatal(err)
		}
	}
	writeXML(first, "1.0", `<test-case fullname="Fixture.B" result="Passed" duration="0.8"/><test-case fullname="Fixture.A" result="Passed" duration="0.2"/>`)
	writeXML(second, "9.0", `<test-case fullname="Fixture.A" result="Passed" duration="4.0"/><test-case fullname="Fixture.B" result="Passed" duration="5.0"/>`)
	if err := os.WriteFile(filepath.Join(dir, "a.log"), nil, 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "b.log"), nil, 0600); err != nil {
		t.Fatal(err)
	}
	a, err := ParsePlatformResult(PlatformEditMode, 0, first, filepath.Join(dir, "a.log"), 10)
	if err != nil {
		t.Fatal(err)
	}
	b, err := ParsePlatformResult(PlatformEditMode, 0, second, filepath.Join(dir, "b.log"), 20)
	if err != nil {
		t.Fatal(err)
	}
	if a.SemanticDigest != b.SemanticDigest || a.Tests[0].FullName != "Fixture.A" {
		t.Fatalf("a=%#v b=%#v", a, b)
	}
}

func TestRequireExpectedTestsRejectsNameMismatch(t *testing.T) {
	result := passing(PlatformEditMode, "digest")
	result.Tests = []SemanticTest{{FullName: "Fixture.Actual", Outcome: "Passed"}}
	result.Total, result.Passed = 1, 1
	if ErrorCode(RequireExpectedTests(result, []string{"Fixture.Expected"})) != CodeUnityRunFailed {
		t.Fatal("expected exact test-set rejection")
	}
}

func TestParsePlatformResultRejectsCompileErrors(t *testing.T) {
	dir := t.TempDir()
	xmlPath := filepath.Join(dir, "result.xml")
	logPath := filepath.Join(dir, "editor.log")
	if err := os.WriteFile(xmlPath, []byte(`<?xml version="1.0"?><test-run total="1" passed="1" failed="0" skipped="0"><test-case fullname="Fixture.Pass" result="Passed"/></test-run>`), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(logPath, []byte(`C:\\one\\Assets\\Broken.cs(1,2): error CS1002: ; expected`), 0600); err != nil {
		t.Fatal(err)
	}
	result, err := ParsePlatformResult(PlatformEditMode, 0, xmlPath, logPath, 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.CompileErrors) != 1 || ErrorCode(RequirePassing(result)) != CodeUnityRunFailed {
		t.Fatalf("result=%#v", result)
	}
}

func TestCompareSemanticDoesNotDropOutcome(t *testing.T) {
	a := passing(PlatformPlayMode, "one")
	b := a
	b.SemanticDigest = "two"
	if ErrorCode(CompareSemantic(a, b)) != CodeSemanticParityFailed {
		t.Fatal("expected parity failure")
	}
}

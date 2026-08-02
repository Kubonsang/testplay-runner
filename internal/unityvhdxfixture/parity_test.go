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
	a, err := ParsePlatformResult(PlatformEditMode, 0, first, "a.log", 10)
	if err != nil {
		t.Fatal(err)
	}
	b, err := ParsePlatformResult(PlatformEditMode, 0, second, "b.log", 20)
	if err != nil {
		t.Fatal(err)
	}
	if a.SemanticDigest != b.SemanticDigest || a.Tests[0].FullName != "Fixture.A" {
		t.Fatalf("a=%#v b=%#v", a, b)
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

package unity_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/Kubonsang/fastplay-runner/internal/status"
	"github.com/Kubonsang/fastplay-runner/internal/unity"
)

func mustReadFixture(t *testing.T, path string) []byte {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("failed to read fixture %s: %v", path, err)
	}
	return data
}

func TestExecute_AllTestsPass_Returns0(t *testing.T) {
	dir := t.TempDir()
	xmlData := mustReadFixture(t, "../parser/testdata/passing.xml")
	fake := &fakeRunner{resultsXML: xmlData, exitCode: 0}
	sw := status.NewWriter(filepath.Join(dir, "status.json"))

	result, code := unity.Execute(context.Background(), fake, unity.ExecuteOptions{
		ProjectPath:  dir,
		ResultsFile:  filepath.Join(dir, "results.xml"),
		StatusWriter: sw,
	})
	if code != 0 {
		t.Errorf("expected exit 0, got %d", code)
	}
	if result.Failed != 0 {
		t.Errorf("expected 0 failures, got %d", result.Failed)
	}
}

func TestExecute_TestFailure_Returns3(t *testing.T) {
	dir := t.TempDir()
	xmlData := mustReadFixture(t, "../parser/testdata/one_failure.xml")
	fake := &fakeRunner{resultsXML: xmlData, exitCode: 0}
	sw := status.NewWriter(filepath.Join(dir, "status.json"))

	_, code := unity.Execute(context.Background(), fake, unity.ExecuteOptions{
		ProjectPath:  dir,
		ResultsFile:  filepath.Join(dir, "results.xml"),
		StatusWriter: sw,
	})
	if code != 3 {
		t.Errorf("expected exit 3, got %d", code)
	}
}

func TestExecute_NoXMLFile_Returns2(t *testing.T) {
	dir := t.TempDir()
	// fakeRunner doesn't write XML — simulates compile failure
	fake := &fakeRunner{
		exitCode: 0,
		stderr:   []byte(`Assets/Foo.cs(1,1): error CS0246: Type 'Bar' not found`),
	}
	sw := status.NewWriter(filepath.Join(dir, "status.json"))

	_, code := unity.Execute(context.Background(), fake, unity.ExecuteOptions{
		ProjectPath:  dir,
		ResultsFile:  filepath.Join(dir, "results.xml"), // file won't exist
		StatusWriter: sw,
	})
	if code != 2 {
		t.Errorf("expected exit 2 (compile failure), got %d", code)
	}
}

func TestExecute_ContextCancelled_Returns4(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // cancel immediately

	dir := t.TempDir()
	fake := &fakeRunner{err: context.Canceled}
	sw := status.NewWriter(filepath.Join(dir, "status.json"))

	result, code := unity.Execute(ctx, fake, unity.ExecuteOptions{
		ProjectPath:  dir,
		ResultsFile:  filepath.Join(dir, "results.xml"),
		StatusWriter: sw,
		TimeoutType:  "total",
	})
	if code != 4 {
		t.Errorf("expected exit 4, got %d", code)
	}
	if result.TimeoutType != "total" {
		t.Errorf("expected timeout_type 'total', got %q", result.TimeoutType)
	}
}

type spyWriter struct {
	phases []status.Phase
}

func (s *spyWriter) Write(st status.Status) error {
	s.phases = append(s.phases, st.Phase)
	return nil
}

func containsPhase(phases []status.Phase, p status.Phase) bool {
	for _, ph := range phases {
		if ph == p {
			return true
		}
	}
	return false
}

func TestExecute_WritesStatusPhases(t *testing.T) {
	dir := t.TempDir()
	xmlData := mustReadFixture(t, "../parser/testdata/passing.xml")
	fake := &fakeRunner{resultsXML: xmlData, exitCode: 0}
	spy := &spyWriter{}

	unity.Execute(context.Background(), fake, unity.ExecuteOptions{
		ProjectPath:  dir,
		ResultsFile:  filepath.Join(dir, "results.xml"),
		StatusWriter: spy,
	})

	if !containsPhase(spy.phases, status.PhaseCompiling) {
		t.Error("expected PhaseCompiling to be written")
	}
	if !containsPhase(spy.phases, status.PhaseDone) {
		t.Error("expected PhaseDone to be written")
	}
}

func TestExecute_CompileErrorsInStderr_Returns2(t *testing.T) {
	dir := t.TempDir()
	// fakeRunner writes an empty XML but also has compile errors in stderr
	emptyXML := []byte(`<?xml version="1.0"?><test-run total="0" passed="0" failed="0" skipped="0" duration="0" result="Passed"></test-run>`)
	fake := &fakeRunner{
		resultsXML: emptyXML,
		stderr:     []byte(`Assets/Foo.cs(1,1): error CS0246: Type 'Bar' not found`),
		exitCode:   0,
	}
	sw := status.NewWriter(filepath.Join(dir, "status.json"))

	_, code := unity.Execute(context.Background(), fake, unity.ExecuteOptions{
		ProjectPath:  dir,
		ResultsFile:  filepath.Join(dir, "results.xml"),
		StatusWriter: sw,
	})
	if code != 2 {
		t.Errorf("expected exit 2 when compile errors in stderr, got %d", code)
	}
}

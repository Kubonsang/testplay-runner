package unity_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/fastplay/runner/internal/status"
	"github.com/fastplay/runner/internal/unity"
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

func TestExecute_WritesStatusPhases(t *testing.T) {
	dir := t.TempDir()
	xmlData := mustReadFixture(t, "../parser/testdata/passing.xml")
	fake := &fakeRunner{resultsXML: xmlData, exitCode: 0}

	sw := status.NewWriter(filepath.Join(dir, "status.json"))

	unity.Execute(context.Background(), fake, unity.ExecuteOptions{
		ProjectPath:  dir,
		ResultsFile:  filepath.Join(dir, "results.xml"),
		StatusWriter: sw,
	})

	// Read the final status file and verify it's "done"
	data, err := os.ReadFile(filepath.Join(dir, "status.json"))
	if err != nil {
		t.Fatalf("status file not written: %v", err)
	}
	var finalStatus status.Status
	if err := json.Unmarshal(data, &finalStatus); err != nil {
		t.Fatalf("invalid status JSON: %v", err)
	}
	if finalStatus.Phase != status.PhaseDone {
		t.Errorf("expected final phase 'done', got %q", finalStatus.Phase)
	}
}

package unity_test

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/Kubonsang/testplay-runner/internal/status"
	"github.com/Kubonsang/testplay-runner/internal/unity"
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

	expected := []status.Phase{status.PhaseCompiling, status.PhaseRunning, status.PhaseDone}
	if !reflect.DeepEqual(spy.phases, expected) {
		t.Errorf("expected phase sequence %v, got %v", expected, spy.phases)
	}
}

func TestExecute_TimeoutType_AlwaysPropagated(t *testing.T) {
	// opts.TimeoutType is always propagated to the result.
	dir := t.TempDir()
	blockingRunner := &funcRunner{
		run: func(ctx context.Context, args []string) ([]byte, []byte, int, error) {
			<-ctx.Done()
			return nil, nil, -1, ctx.Err()
		},
	}
	sw := status.NewWriter(filepath.Join(dir, "status.json"))

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	result, code := unity.Execute(ctx, blockingRunner, unity.ExecuteOptions{
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

// funcRunner implements Runner via a pluggable function, for tests that need
// custom blocking or error behaviour that fakeRunner cannot provide.
type funcRunner struct {
	run func(ctx context.Context, args []string) ([]byte, []byte, int, error)
}

func (f *funcRunner) Run(ctx context.Context, args []string) ([]byte, []byte, int, error) {
	return f.run(ctx, args)
}

func TestExecute_PlayMode_PassesPlayModeToRunner(t *testing.T) {
	dir := t.TempDir()
	var capturedArgs []string
	capturingRunner := &funcRunner{
		run: func(ctx context.Context, args []string) ([]byte, []byte, int, error) {
			capturedArgs = args
			return nil, nil, -1, context.Canceled
		},
	}

	unity.Execute(context.Background(), capturingRunner, unity.ExecuteOptions{
		ProjectPath:  dir,
		ResultsFile:  filepath.Join(dir, "results.xml"),
		TestPlatform: "play_mode",
	})

	idx := indexOf(capturedArgs, "-testPlatform")
	if idx == -1 || idx+1 >= len(capturedArgs) {
		t.Fatal("-testPlatform not found in args")
	}
	if capturedArgs[idx+1] != "PlayMode" {
		t.Errorf("expected PlayMode, got %q", capturedArgs[idx+1])
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

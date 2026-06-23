package unity

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/Kubonsang/testplay-runner/internal/bridge"
	"github.com/Kubonsang/testplay-runner/internal/history"
	"github.com/Kubonsang/testplay-runner/internal/status"
)

// fakeBridgeClient returns a preset outcome/error and optionally writes a
// results.xml to the request's ResultsXML path to simulate the bridge.
type fakeBridgeClient struct {
	outcome    bridge.RunOutcome
	err        error
	writeXML   string // when non-empty, written to req.ResultsXML before returning
	gotRequest bridge.RunRequest
}

func (f *fakeBridgeClient) Run(_ context.Context, req bridge.RunRequest, sw status.WriterInterface) (bridge.RunOutcome, error) {
	f.gotRequest = req
	if f.err != nil {
		return bridge.RunOutcome{}, f.err
	}
	if f.writeXML != "" {
		_ = os.WriteFile(req.ResultsXML, []byte(f.writeXML), 0o644)
	}
	// Simulate a running-phase progress write so callers can observe streaming.
	_ = sw.Write(status.Status{Phase: status.PhaseRunning})
	return f.outcome, nil
}

const passingXML = `<?xml version="1.0" encoding="utf-8"?>
<test-run total="1" passed="1" failed="0" skipped="0" duration="0.01">
  <test-suite type="TestFixture" name="F" fullname="F">
    <test-case name="T" fullname="F.T" result="Passed" duration="0.01" />
  </test-suite>
</test-run>`

const failingXML = `<?xml version="1.0" encoding="utf-8"?>
<test-run total="1" passed="0" failed="1" skipped="0" duration="0.02">
  <test-suite type="TestFixture" name="F" fullname="F">
    <test-case name="T" fullname="F.T" result="Failed" duration="0.02">
      <failure><message>boom</message><stack-trace>at F.T () (at /proj/Assets/F.cs:7)</stack-trace></failure>
    </test-case>
  </test-suite>
</test-run>`

func baseOpts(t *testing.T) ExecuteOptions {
	t.Helper()
	return ExecuteOptions{
		ProjectPath:  t.TempDir(),
		ResultsFile:  filepath.Join(t.TempDir(), "results.xml"),
		TestPlatform: "edit_mode",
	}
}

func TestExecuteBridge_CompletedPass(t *testing.T) {
	opts := baseOpts(t)
	client := &fakeBridgeClient{outcome: bridge.RunOutcome{Outcome: bridge.OutcomeCompleted, ResultsXMLWritten: true}, writeXML: passingXML}

	res := ExecuteBridge(context.Background(), client, opts, "20260101-120000-aaaaaaaa", 30000)
	if res.FellBack {
		t.Fatal("unexpected fall-back")
	}
	if res.ExitCode != 0 {
		t.Fatalf("exit code = %d, want 0", res.ExitCode)
	}
	if res.Result.Passed != 1 || len(res.Result.Tests) != 1 {
		t.Fatalf("unexpected result: %+v", res.Result)
	}
	if client.gotRequest.RunID != "20260101-120000-aaaaaaaa" || client.gotRequest.ResultsXML != opts.ResultsFile {
		t.Fatalf("request not wired correctly: %+v", client.gotRequest)
	}
}

func TestExecuteBridge_CompletedFail(t *testing.T) {
	opts := baseOpts(t)
	client := &fakeBridgeClient{outcome: bridge.RunOutcome{Outcome: bridge.OutcomeCompleted, ResultsXMLWritten: true, NonPristine: []string{"editor had unsaved changes"}}, writeXML: failingXML}

	res := ExecuteBridge(context.Background(), client, opts, "20260101-120000-bbbbbbbb", 30000)
	if res.ExitCode != 3 {
		t.Fatalf("exit code = %d, want 3", res.ExitCode)
	}
	if len(res.Warnings) != 1 {
		t.Fatalf("expected 1 non-pristine warning, got %+v", res.Warnings)
	}
}

func TestExecuteBridge_CompletedWithoutResultsXMLFallsBack(t *testing.T) {
	opts := baseOpts(t)
	// Bridge reports completed but did NOT write results.xml.
	client := &fakeBridgeClient{outcome: bridge.RunOutcome{Outcome: bridge.OutcomeCompleted, ResultsXMLWritten: false}}
	res := ExecuteBridge(context.Background(), client, opts, "20260101-120000-11111111", 30000)
	if !res.FellBack {
		t.Fatal("completed outcome with results_xml_written=false must fall back to cold")
	}
}

func TestExecuteBridge_CompletedButUnparseableXMLFallsBack(t *testing.T) {
	opts := baseOpts(t)
	// Claims it wrote results.xml, but the file is absent → parseResults exit 2;
	// must fall back, NOT report a phantom compile failure.
	client := &fakeBridgeClient{outcome: bridge.RunOutcome{Outcome: bridge.OutcomeCompleted, ResultsXMLWritten: true}}
	res := ExecuteBridge(context.Background(), client, opts, "20260101-120000-22222222", 30000)
	if !res.FellBack {
		t.Fatalf("completed+claimed but missing XML must fall back, got exit %d", res.ExitCode)
	}
}

func TestExecuteBridge_CompileFailed(t *testing.T) {
	opts := baseOpts(t)
	errs := []history.CompileError{{File: "Assets/Foo.cs", Line: 1, Message: "CS0246: missing"}}
	client := &fakeBridgeClient{outcome: bridge.RunOutcome{Outcome: bridge.OutcomeCompileFailed, CompileErrors: errs}}

	res := ExecuteBridge(context.Background(), client, opts, "20260101-120000-cccccccc", 30000)
	if res.ExitCode != 2 {
		t.Fatalf("exit code = %d, want 2", res.ExitCode)
	}
	if len(res.Result.Errors) != 1 || res.Result.Errors[0].Message != "CS0246: missing" {
		t.Fatalf("unexpected errors: %+v", res.Result.Errors)
	}
}

func TestExecuteBridge_BuildFailed(t *testing.T) {
	opts := baseOpts(t)
	client := &fakeBridgeClient{outcome: bridge.RunOutcome{Outcome: bridge.OutcomeBuildFailed}}

	res := ExecuteBridge(context.Background(), client, opts, "20260101-120000-dddddddd", 30000)
	if res.ExitCode != 6 {
		t.Fatalf("exit code = %d, want 6", res.ExitCode)
	}
}

func TestExecuteBridge_BusyAndRejectedFallBack(t *testing.T) {
	for _, oc := range []bridge.Outcome{bridge.OutcomeBusy, bridge.OutcomeRejected} {
		opts := baseOpts(t)
		client := &fakeBridgeClient{outcome: bridge.RunOutcome{Outcome: oc}}
		res := ExecuteBridge(context.Background(), client, opts, "20260101-120000-eeeeeeee", 30000)
		if !res.FellBack {
			t.Fatalf("outcome %q should fall back to cold", oc)
		}
	}
}

func TestExecuteBridge_TransportErrorFallsBack(t *testing.T) {
	opts := baseOpts(t)
	client := &fakeBridgeClient{err: os.ErrNotExist}
	res := ExecuteBridge(context.Background(), client, opts, "20260101-120000-ffffffff", 30000)
	if !res.FellBack {
		t.Fatal("transport error should fall back to cold")
	}
}

func TestExecuteBridge_TimeoutMapsToExit4(t *testing.T) {
	opts := baseOpts(t)
	opts.TimeoutType = "total"
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // immediately canceled
	// Simulate the client observing the deadline.
	client := &fakeBridgeClient{err: context.DeadlineExceeded}
	res := ExecuteBridge(ctx, client, opts, "20260101-120000-09090909", 30000)
	if res.FellBack {
		t.Fatal("timeout is a real result, not a fall-back")
	}
	if res.ExitCode != 4 || res.Result.TimeoutType != "total" {
		t.Fatalf("expected exit 4 timeout_type total, got code=%d type=%q", res.ExitCode, res.Result.TimeoutType)
	}
}

func TestExecuteBridge_SignalMapsToExit8(t *testing.T) {
	opts := baseOpts(t)
	ctx, cancel := context.WithCancelCause(context.Background())
	cancel(ErrSignalInterrupt)
	client := &fakeBridgeClient{err: context.Canceled}
	res := ExecuteBridge(ctx, client, opts, "20260101-120000-10101010", 30000)
	if res.ExitCode != 8 {
		t.Fatalf("expected exit 8 on signal interrupt, got %d", res.ExitCode)
	}
}

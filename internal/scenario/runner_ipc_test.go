package scenario_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Kubonsang/testplay-runner/internal/ipc"
	"github.com/Kubonsang/testplay-runner/internal/runsvc"
	"github.com/Kubonsang/testplay-runner/internal/scenario"
)

// touchFile creates an empty file at path, mirroring `touch`.
func touchFile(path string) error {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
	if err != nil {
		return err
	}
	return f.Close()
}

// host writes to bus → client's IpcEvents should include the recv.
func TestRunScenario_IpcMessagesCollected(t *testing.T) {
	dir := t.TempDir()
	busPath := filepath.Join(dir, "bus.ndjson")
	// Pre-create the bus file so reader's ENOENT-wait path is not exercised here.
	if err := touchFile(busPath); err != nil {
		t.Fatal(err)
	}

	spec := &scenario.ScenarioFile{
		Instances: []scenario.InstanceSpec{
			{Role: "host", Config: "./host.json"},
			{Role: "client", Config: "./client.json", DependsOn: "host", ReadyTimeoutMs: 5000},
		},
	}

	run := func(_ context.Context, inst scenario.InstanceSpec, readyCh chan<- struct{}) (runsvc.Response, error) {
		if inst.Role == "host" {
			w, _ := ipc.NewBusWriter(busPath, "host")
			_, _ = w.Append(ipc.Message{To: "*", Kind: "ready"})
			if readyCh != nil {
				close(readyCh)
			}
			// Give the client's reader time to pick up the message before host finishes.
			time.Sleep(150 * time.Millisecond)
			return fakeResult(0), nil
		}
		// client: wait long enough for the reader to observe at least one tick.
		time.Sleep(150 * time.Millisecond)
		return fakeResult(0), nil
	}

	result, err := scenario.RunScenario(context.Background(), spec, run, busPath)
	if err != nil {
		t.Fatalf("RunScenario: %v", err)
	}

	host, client := result.Instances[0], result.Instances[1]
	if len(host.IpcEvents) != 1 || host.IpcEvents[0].Direction != "send" {
		t.Errorf("host events = %+v, want one send", host.IpcEvents)
	}
	if len(client.IpcEvents) != 1 || client.IpcEvents[0].Direction != "recv" {
		t.Errorf("client events = %+v, want one recv", client.IpcEvents)
	}
	if client.IpcEvents[0].Msg.Kind != "ready" {
		t.Errorf("client recv kind = %q, want 'ready'", client.IpcEvents[0].Msg.Kind)
	}
}

// host crashes after sending one message → client never gets ready signal.
// orchestrator_errors should mention what client last saw from host.
func TestRunScenario_HostCrashIncludesLastIpcMessage(t *testing.T) {
	dir := t.TempDir()
	busPath := filepath.Join(dir, "bus.ndjson")
	if err := touchFile(busPath); err != nil {
		t.Fatal(err)
	}

	spec := &scenario.ScenarioFile{
		Instances: []scenario.InstanceSpec{
			{Role: "host", Config: "./host.json"},
			{Role: "client", Config: "./client.json", DependsOn: "host", ReadyTimeoutMs: 5000},
		},
	}

	run := func(_ context.Context, inst scenario.InstanceSpec, readyCh chan<- struct{}) (runsvc.Response, error) {
		if inst.Role == "host" {
			// Send one message, then exit with a non-ready failure.
			w, _ := ipc.NewBusWriter(busPath, "host")
			_, _ = w.Append(ipc.Message{To: "client", Kind: "boot"})
			// Give the client's reader time to pick this up before we exit.
			time.Sleep(200 * time.Millisecond)
			return fakeResult(2), nil // exit 2 = compile error, never reached ready
		}
		// client should never run its body — fast-fail in dep wait
		t.Errorf("client run() should not be invoked; host failed before ready")
		return fakeResult(0), nil
	}

	result, _ := scenario.RunScenario(context.Background(), spec, run, busPath)
	if len(result.OrchestratorErrors) == 0 {
		t.Fatal("expected orchestrator_errors for host crash before ready")
	}
	msg := result.OrchestratorErrors[0]
	if !strings.Contains(msg, `"client" last received from "host"`) {
		t.Errorf("expected last-received correlation in error, got: %s", msg)
	}
	if !strings.Contains(msg, `kind="boot"`) {
		t.Errorf("expected kind=\"boot\" in error, got: %s", msg)
	}
}

// IPC disabled (busPath == "") → IpcEvents must be nil/empty for every instance.
func TestRunScenario_IpcDisabled_NoEventsCaptured(t *testing.T) {
	t.Parallel()
	spec := &scenario.ScenarioFile{
		Instances: []scenario.InstanceSpec{
			{Role: "host", Config: "./host.json"},
		},
	}
	run := func(_ context.Context, inst scenario.InstanceSpec, _ chan<- struct{}) (runsvc.Response, error) {
		return fakeResult(0), nil
	}
	result, _ := scenario.RunScenario(context.Background(), spec, run, "")
	if len(result.Instances[0].IpcEvents) != 0 {
		t.Errorf("expected no IPC events when busPath empty, got %d", len(result.Instances[0].IpcEvents))
	}
}

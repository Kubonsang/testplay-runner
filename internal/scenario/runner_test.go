package scenario_test

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Kubonsang/testplay-runner/internal/history"
	"github.com/Kubonsang/testplay-runner/internal/parser"
	"github.com/Kubonsang/testplay-runner/internal/runsvc"
	"github.com/Kubonsang/testplay-runner/internal/scenario"
)

// fakeResult builds a minimal valid Response.
func fakeResult(exitCode int) runsvc.Response {
	return runsvc.Response{
		RunID:    "20260326-143055-aabbccdd",
		ExitCode: exitCode,
		Result: &history.RunResult{
			SchemaVersion: "1",
			ExitCode:      exitCode,
			Tests:         []parser.TestCase{},
			Errors:        []history.CompileError{},
		},
	}
}

func TestRunScenario_AllInstancesRun(t *testing.T) {
	t.Parallel()
	spec := &scenario.ScenarioFile{
		Instances: []scenario.InstanceSpec{
			{Role: "Host",   Config: "./host.json"},
			{Role: "Client", Config: "./client.json"},
		},
	}

	var ran int32
	run := func(_ context.Context, inst scenario.InstanceSpec, _ chan<- struct{}) (runsvc.Response, error) {
		atomic.AddInt32(&ran, 1)
		return fakeResult(0), nil
	}

	result, err := scenario.RunScenario(context.Background(), spec, run)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if int(ran) != 2 {
		t.Errorf("expected 2 instances to run, got %d", ran)
	}
	if len(result.Instances) != 2 {
		t.Errorf("expected 2 instance results, got %d", len(result.Instances))
	}
	if result.Instances[0].Role != "Host" {
		t.Errorf("expected first role 'Host', got %q", result.Instances[0].Role)
	}
	if result.Instances[1].Role != "Client" {
		t.Errorf("expected second role 'Client', got %q", result.Instances[1].Role)
	}
}

func TestRunScenario_ExitCodeIsMaxOfInstances(t *testing.T) {
	t.Parallel()
	spec := &scenario.ScenarioFile{
		Instances: []scenario.InstanceSpec{
			{Role: "Host",   Config: "./host.json"},
			{Role: "Client", Config: "./client.json"},
		},
	}

	exitCodes := []int{0, 3}
	var idx int32
	run := func(_ context.Context, _ scenario.InstanceSpec, _ chan<- struct{}) (runsvc.Response, error) {
		i := int(atomic.AddInt32(&idx, 1)) - 1
		return fakeResult(exitCodes[i]), nil
	}

	result, _ := scenario.RunScenario(context.Background(), spec, run)
	if result.ExitCode != 3 {
		t.Errorf("expected exit code 3 (max), got %d", result.ExitCode)
	}
}

func TestRunScenario_InfraErrorTreatedAsExit1(t *testing.T) {
	t.Parallel()
	spec := &scenario.ScenarioFile{
		Instances: []scenario.InstanceSpec{
			{Role: "Host", Config: "./host.json"},
		},
	}

	run := func(_ context.Context, _ scenario.InstanceSpec, _ chan<- struct{}) (runsvc.Response, error) {
		return runsvc.Response{}, fmt.Errorf("disk full")
	}

	result, _ := scenario.RunScenario(context.Background(), spec, run)
	if result.ExitCode != 1 {
		t.Errorf("expected exit code 1 for infra error, got %d", result.ExitCode)
	}
	if result.Instances[0].Err == nil {
		t.Error("expected Err to be non-nil for infra error")
	}
}

func TestAggregateExitCode_AllZero(t *testing.T) {
	t.Parallel()
	spec := &scenario.ScenarioFile{
		Instances: []scenario.InstanceSpec{
			{Role: "A", Config: "./a.json"},
			{Role: "B", Config: "./b.json"},
		},
	}
	run := func(_ context.Context, _ scenario.InstanceSpec, _ chan<- struct{}) (runsvc.Response, error) {
		return fakeResult(0), nil
	}
	result, _ := scenario.RunScenario(context.Background(), spec, run)
	if result.ExitCode != 0 {
		t.Errorf("expected exit code 0, got %d", result.ExitCode)
	}
}

func TestRunScenario_ClientStartsAfterHostReady(t *testing.T) {
	t.Parallel()
	spec := &scenario.ScenarioFile{
		Instances: []scenario.InstanceSpec{
			{Role: "host",   Config: "./host.json"},
			{Role: "client", Config: "./client.json", DependsOn: "host"},
		},
	}

	var order []string
	var mu sync.Mutex

	run := func(_ context.Context, inst scenario.InstanceSpec, readyCh chan<- struct{}) (runsvc.Response, error) {
		mu.Lock()
		order = append(order, inst.Role+":start")
		mu.Unlock()

		if inst.Role == "host" && readyCh != nil {
			close(readyCh) // signal ready immediately
		}

		mu.Lock()
		order = append(order, inst.Role+":done")
		mu.Unlock()
		return fakeResult(0), nil
	}

	result, err := scenario.RunScenario(context.Background(), spec, run)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.ExitCode != 0 {
		t.Errorf("expected exit 0, got %d", result.ExitCode)
	}

	mu.Lock()
	defer mu.Unlock()
	hostStartIdx, clientStartIdx := -1, -1
	for i, ev := range order {
		if ev == "host:start" { hostStartIdx = i }
		if ev == "client:start" { clientStartIdx = i }
	}
	if hostStartIdx == -1 || clientStartIdx == -1 {
		t.Fatalf("order incomplete: %v", order)
	}
	if hostStartIdx >= clientStartIdx {
		t.Errorf("client started before host; order=%v", order)
	}
}

func TestRunScenario_ReadyTimeout_ReturnsExit4(t *testing.T) {
	t.Parallel()
	spec := &scenario.ScenarioFile{
		Instances: []scenario.InstanceSpec{
			{Role: "host",   Config: "./host.json"},
			{Role: "client", Config: "./client.json", DependsOn: "host", ReadyTimeoutMs: 50},
		},
	}

	run := func(_ context.Context, inst scenario.InstanceSpec, readyCh chan<- struct{}) (runsvc.Response, error) {
		if inst.Role == "host" {
			// host never signals ready — simulates hang
			time.Sleep(200 * time.Millisecond)
		}
		return fakeResult(0), nil
	}

	result, err := scenario.RunScenario(context.Background(), spec, run)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.ExitCode != 4 {
		t.Errorf("expected exit 4 for ready timeout, got %d", result.ExitCode)
	}
	if len(result.OrchestratorErrors) == 0 {
		t.Error("expected orchestrator_errors to be non-empty")
	}
}

func TestRunScenario_HostCrash_ClientFastFails(t *testing.T) {
	t.Parallel()
	spec := &scenario.ScenarioFile{
		Instances: []scenario.InstanceSpec{
			{Role: "host", Config: "./host.json"},
			{Role: "client", Config: "./client.json", DependsOn: "host", ReadyTimeoutMs: 10000},
		},
	}

	run := func(_ context.Context, inst scenario.InstanceSpec, readyCh chan<- struct{}) (runsvc.Response, error) {
		if inst.Role == "host" {
			// Host crashes immediately without signaling ready
			// (readyCh is NOT closed — simulates crash before reaching ready phase)
			return fakeResult(2), nil // exit 2 = compile failure
		}
		return fakeResult(0), nil
	}

	start := time.Now()
	result, err := scenario.RunScenario(context.Background(), spec, run)
	elapsed := time.Since(start)

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// Client should have fast-failed, NOT waited the full 10s timeout
	if elapsed > 2*time.Second {
		t.Errorf("expected fast-fail but RunScenario took %v (timeout is 10s)", elapsed)
	}
	if result.ExitCode < 2 {
		t.Errorf("expected exit code >= 2, got %d", result.ExitCode)
	}
	if len(result.OrchestratorErrors) == 0 {
		t.Error("expected orchestrator_errors for host crash fast-fail")
	}
}

func TestRunScenario_ContextCancellation_StopsWait(t *testing.T) {
	t.Parallel()
	spec := &scenario.ScenarioFile{
		Instances: []scenario.InstanceSpec{
			{Role: "host",   Config: "./host.json"},
			{Role: "client", Config: "./client.json", DependsOn: "host", ReadyTimeoutMs: 10000},
		},
	}

	ctx, cancel := context.WithCancel(context.Background())

	run := func(_ context.Context, inst scenario.InstanceSpec, readyCh chan<- struct{}) (runsvc.Response, error) {
		if inst.Role == "host" {
			time.Sleep(200 * time.Millisecond) // host is slow
		}
		return fakeResult(0), nil
	}

	// Cancel shortly after start
	go func() {
		time.Sleep(20 * time.Millisecond)
		cancel()
	}()

	result, _ := scenario.RunScenario(ctx, spec, run)
	// key assertion: RunScenario returned (did not hang)
	_ = result
}

func TestRunScenario_HostCrash_ErrorIncludesExitCode(t *testing.T) {
	t.Parallel()
	spec := &scenario.ScenarioFile{
		Instances: []scenario.InstanceSpec{
			{Role: "host", Config: "./host.json"},
			{Role: "client", Config: "./client.json", DependsOn: "host", ReadyTimeoutMs: 10000},
		},
	}

	run := func(_ context.Context, inst scenario.InstanceSpec, readyCh chan<- struct{}) (runsvc.Response, error) {
		if inst.Role == "host" {
			return fakeResult(2), nil
		}
		return fakeResult(0), nil
	}

	result, err := scenario.RunScenario(context.Background(), spec, run)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(result.OrchestratorErrors) == 0 {
		t.Fatal("expected orchestrator errors for host crash")
	}
	msg := result.OrchestratorErrors[0]
	if !strings.Contains(msg, "exit 2") {
		t.Errorf("expected error to contain 'exit 2', got: %s", msg)
	}
	if !strings.Contains(msg, "compile error") {
		t.Errorf("expected error to contain 'compile error', got: %s", msg)
	}
}

func TestRunScenario_HostInfraError_ErrorIncludesDetail(t *testing.T) {
	t.Parallel()
	spec := &scenario.ScenarioFile{
		Instances: []scenario.InstanceSpec{
			{Role: "host", Config: "./host.json"},
			{Role: "client", Config: "./client.json", DependsOn: "host", ReadyTimeoutMs: 10000},
		},
	}

	run := func(_ context.Context, inst scenario.InstanceSpec, readyCh chan<- struct{}) (runsvc.Response, error) {
		if inst.Role == "host" {
			return runsvc.Response{}, fmt.Errorf("disk full")
		}
		return fakeResult(0), nil
	}

	result, err := scenario.RunScenario(context.Background(), spec, run)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(result.OrchestratorErrors) == 0 {
		t.Fatal("expected orchestrator errors for host infra error")
	}
	msg := result.OrchestratorErrors[0]
	if !strings.Contains(msg, "infrastructure error") {
		t.Errorf("expected error to contain 'infrastructure error', got: %s", msg)
	}
}

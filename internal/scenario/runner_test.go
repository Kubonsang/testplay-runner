package scenario_test

import (
	"context"
	"fmt"
	"sync/atomic"
	"testing"

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
	run := func(_ context.Context, inst scenario.InstanceSpec) (runsvc.Response, error) {
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
	run := func(_ context.Context, _ scenario.InstanceSpec) (runsvc.Response, error) {
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

	run := func(_ context.Context, _ scenario.InstanceSpec) (runsvc.Response, error) {
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
	run := func(_ context.Context, _ scenario.InstanceSpec) (runsvc.Response, error) {
		return fakeResult(0), nil
	}
	result, _ := scenario.RunScenario(context.Background(), spec, run)
	if result.ExitCode != 0 {
		t.Errorf("expected exit code 0, got %d", result.ExitCode)
	}
}

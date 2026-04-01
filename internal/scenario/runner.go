package scenario

import (
	"context"
	"sync"

	"github.com/Kubonsang/testplay-runner/internal/runsvc"
)

// InstanceRunner executes a single instance and returns its Response.
// readyCh is closed by the runner when the instance reaches its configured
// ready phase (via ReadyNotifier). Pass nil if this instance does not need
// to signal readiness to any dependent.
// Infrastructure failures are returned as error; Unity-side failures are
// encoded in Response.ExitCode.
type InstanceRunner func(ctx context.Context, spec InstanceSpec, readyCh chan<- struct{}) (runsvc.Response, error)

// InstanceResult holds the outcome of a single instance run.
type InstanceResult struct {
	Role     string
	Response runsvc.Response
	Err      error // non-nil for infrastructure errors only
}

// ScenarioResult aggregates the outcomes of all instances.
type ScenarioResult struct {
	ExitCode           int
	Instances          []InstanceResult
	OrchestratorErrors []string // non-empty when dependency wait fails (timeout or cancellation)
}

// RunScenario runs all instances in spec concurrently, one goroutine per instance.
// All instances run to completion regardless of individual failures.
// RunScenario itself never returns a non-nil error; instance errors are recorded
// in InstanceResult.Err and orchestration errors in ScenarioResult.OrchestratorErrors.
func RunScenario(ctx context.Context, spec *ScenarioFile, run InstanceRunner) (ScenarioResult, error) {
	results := make([]InstanceResult, len(spec.Instances))
	var wg sync.WaitGroup

	for i, inst := range spec.Instances {
		wg.Add(1)
		go func() {
			defer wg.Done()
			// readyCh is nil for now — ordering logic added in Task 5
			resp, err := run(ctx, inst, nil)
			results[i] = InstanceResult{
				Role:     inst.Role,
				Response: resp,
				Err:      err,
			}
		}()
	}

	wg.Wait()

	return ScenarioResult{
		ExitCode:  aggregateExitCode(results),
		Instances: results,
	}, nil
}

// aggregateExitCode returns the maximum exit code across all instance results.
// Infrastructure errors (Err != nil) are treated as exit 1.
func aggregateExitCode(results []InstanceResult) int {
	max := 0
	for _, r := range results {
		code := r.Response.ExitCode
		if r.Err != nil {
			code = 1
		}
		if code > max {
			max = code
		}
	}
	return max
}

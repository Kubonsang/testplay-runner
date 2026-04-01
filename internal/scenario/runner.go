package scenario

import (
	"context"
	"sync"

	"github.com/Kubonsang/testplay-runner/internal/runsvc"
)

// InstanceRunner executes a single instance and returns its Response.
// Infrastructure failures (cannot load config, cannot create artifact dir)
// are returned as error. Unity-side failures (compile error, test failure)
// are encoded in Response.ExitCode — never returned as error.
type InstanceRunner func(ctx context.Context, spec InstanceSpec) (runsvc.Response, error)

// InstanceResult holds the outcome of a single instance run.
type InstanceResult struct {
	Role     string
	Response runsvc.Response
	Err      error // non-nil for infrastructure errors only
}

// ScenarioResult aggregates the outcomes of all instances.
type ScenarioResult struct {
	ExitCode  int
	Instances []InstanceResult
}

// RunScenario runs all instances in spec concurrently, one goroutine per instance.
// All instances run to completion regardless of individual failures — partial
// results are preserved for agent inspection.
// RunScenario itself never returns a non-nil error; instance errors are recorded
// in InstanceResult.Err.
func RunScenario(ctx context.Context, spec *ScenarioFile, run InstanceRunner) (ScenarioResult, error) {
	results := make([]InstanceResult, len(spec.Instances))
	var wg sync.WaitGroup

	for i, inst := range spec.Instances {
		wg.Add(1)
		go func() {
			defer wg.Done()
			resp, err := run(ctx, inst)
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

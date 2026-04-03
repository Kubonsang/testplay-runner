package scenario

import (
	"context"
	"fmt"
	"sync"
	"time"

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
	// Create one ready channel and one done channel per instance.
	// readyCh is closed by the runner (via ReadyNotifier) when the instance
	// reaches its configured ready phase.
	// doneCh is closed by the orchestrator when the instance's goroutine finishes,
	// regardless of success or failure — enabling fast-fail for dependents.
	readyChannels := make(map[string]chan struct{}, len(spec.Instances))
	doneChannels := make(map[string]chan struct{}, len(spec.Instances))
	for _, inst := range spec.Instances {
		readyChannels[inst.Role] = make(chan struct{})
		doneChannels[inst.Role] = make(chan struct{})
	}

	roleIndex := make(map[string]int, len(spec.Instances))
	for i, inst := range spec.Instances {
		roleIndex[inst.Role] = i
	}

	results := make([]InstanceResult, len(spec.Instances))
	var (
		wg       sync.WaitGroup
		orchMu   sync.Mutex
		orchErrs []string
	)

	for i, inst := range spec.Instances {
		wg.Add(1)
		go func() {
			defer wg.Done()
			defer close(doneChannels[inst.Role])

			// If this instance depends on another, wait for its ready signal.
			if inst.DependsOn != "" {
				depReadyCh := readyChannels[inst.DependsOn]
				depDoneCh := doneChannels[inst.DependsOn]
				timeout := time.Duration(inst.EffectiveReadyTimeoutMs()) * time.Millisecond
				select {
				case <-depReadyCh:
					// dependency reached ready phase — proceed
				case <-depDoneCh:
					// dependency goroutine finished — but it may have signaled
					// ready before exiting. Go's select picks randomly when
					// multiple cases are ready, so re-check readyCh.
					select {
					case <-depReadyCh:
						// dependency was ready; proceed normally
					default:
						// dependency truly exited without signaling ready — fast-fail
						depIdx := roleIndex[inst.DependsOn]
						depResult := results[depIdx]
						var msg string
						if depResult.Err != nil {
							msg = fmt.Sprintf("instance %q: dependency %q failed with infrastructure error before reaching phase %q",
								inst.Role, inst.DependsOn, inst.EffectiveReadyPhase())
						} else {
							depExit := depResult.Response.ExitCode
							msg = fmt.Sprintf("instance %q: dependency %q exited with exit %d (%s) before reaching phase %q",
								inst.Role, inst.DependsOn, depExit, exitCodeLabel(depExit), inst.EffectiveReadyPhase())
						}
						orchMu.Lock()
						orchErrs = append(orchErrs, msg)
						orchMu.Unlock()
						results[i] = InstanceResult{
							Role:     inst.Role,
							Response: runsvc.Response{ExitCode: 4},
						}
						return
					}
				case <-time.After(timeout):
					msg := fmt.Sprintf("instance %q timed out waiting for %q to reach phase %q (%dms)",
						inst.Role, inst.DependsOn, inst.EffectiveReadyPhase(), inst.EffectiveReadyTimeoutMs())
					orchMu.Lock()
					orchErrs = append(orchErrs, msg)
					orchMu.Unlock()
					results[i] = InstanceResult{
						Role:     inst.Role,
						Response: runsvc.Response{ExitCode: 4},
					}
					return
				case <-ctx.Done():
					results[i] = InstanceResult{Role: inst.Role, Err: ctx.Err()}
					return
				}
			}

			readyCh := readyChannels[inst.Role]
			resp, err := run(ctx, inst, readyCh)
			results[i] = InstanceResult{Role: inst.Role, Response: resp, Err: err}
		}()
	}

	wg.Wait()

	return ScenarioResult{
		ExitCode:           aggregateExitCode(results),
		Instances:          results,
		OrchestratorErrors: orchErrs,
	}, nil
}

// exitCodeLabel returns a human-readable label for a testplay exit code.
func exitCodeLabel(code int) string {
	switch code {
	case 0:
		return "all passed"
	case 1:
		return "dependency error"
	case 2:
		return "compile error"
	case 3:
		return "test failure"
	case 4:
		return "timeout"
	case 5:
		return "config error"
	case 6:
		return "build error"
	case 7:
		return "permission error"
	case 8:
		return "interrupted"
	case 9:
		return "runner system error"
	default:
		return "unknown"
	}
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

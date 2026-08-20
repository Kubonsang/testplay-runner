package refsworkspace

import (
	"errors"
	"reflect"
	"testing"
)

func durableReleaseStatusFixture() *Result {
	residual := fullyMeasuredResidual()
	residual.OwnedVHDXFiles = measured(1)
	residual.Status = residualStatus(residual)
	return &Result{Status: "READY", Paths: Paths{PoolRoot: `C:\pool\testplay`}, Residual: residual}
}

func TestWorkerReleaseDurabilityOrdering(t *testing.T) {
	var events []string
	appendEvent := func(name string) { events = append(events, name) }
	result, err := runWorkerReleaseDurability(workerReleaseDurabilityOps{
		release: func() (WorkerMetrics, error) { appendEvent("release"); return WorkerMetrics{}, nil },
		measure: func() (Residual, error) {
			appendEvent("measure")
			residual := fullyMeasuredResidual()
			residual.Status = mountedResidualStatus(residual)
			return residual, nil
		},
		writeArtifact: func(workerReleaseArtifact) error { appendEvent("artifact"); return nil },
		flush:         func() error { appendEvent("flush"); return nil },
		beforeDetach:  func() error { appendEvent("mounted-gate"); return nil },
		detach:        func() error { appendEvent("detach"); return nil },
		status:        func() (*Result, error) { appendEvent("status"); return durableReleaseStatusFixture(), nil },
		remove:        func() (*Result, error) { appendEvent("remove"); return &Result{Status: "PASS"}, nil },
	})
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"release", "measure", "artifact", "flush", "artifact", "mounted-gate", "detach", "artifact", "status", "artifact", "remove", "artifact"}
	if !reflect.DeepEqual(events, want) {
		t.Fatalf("events=%v want=%v", events, want)
	}
	if result.MountedResidual.Status != "MOUNTED_MEASURED_ZERO" || result.Durability.DurableResidual == nil || result.Durability.DurableResidual.Status != "MEASURED_ZERO" || !result.Durability.PoolRemoveSucceeded {
		t.Fatalf("result=%+v", result)
	}
}

func TestWorkerReleaseFlushFailurePreservesPrimaryAndSkipsDetach(t *testing.T) {
	primary := errors.New("release failed")
	flushFailure := errors.New("flush failed")
	detached := false
	_, err := runWorkerReleaseDurability(workerReleaseDurabilityOps{
		release:       func() (WorkerMetrics, error) { return WorkerMetrics{}, primary },
		measure:       func() (Residual, error) { return fullyMeasuredResidual(), nil },
		writeArtifact: func(workerReleaseArtifact) error { return nil },
		flush:         func() error { return flushFailure },
		detach:        func() error { detached = true; return nil },
	})
	if !errors.Is(err, primary) || !errors.Is(err, flushFailure) || detached {
		t.Fatalf("err=%v detached=%t", err, detached)
	}
}

func TestWorkerReleaseReattachDetectsReappearedArtifacts(t *testing.T) {
	for _, mutate := range []func(*Residual){
		func(residual *Residual) { residual.ActiveBaselineUses.Count = 1 },
		func(residual *Residual) { residual.WorkerLeaseJournals.Count = 1 },
		func(residual *Residual) { residual.WorkerDirectories.Count = 1 },
	} {
		removed := false
		status := durableReleaseStatusFixture()
		mutate(&status.Residual)
		status.Residual.Status = residualStatus(status.Residual)
		_, err := runWorkerReleaseDurability(workerReleaseDurabilityOps{
			release: func() (WorkerMetrics, error) { return WorkerMetrics{}, nil },
			measure: func() (Residual, error) {
				residual := fullyMeasuredResidual()
				residual.Status = mountedResidualStatus(residual)
				return residual, nil
			},
			writeArtifact: func(workerReleaseArtifact) error { return nil },
			flush:         func() error { return nil },
			detach:        func() error { return nil },
			status:        func() (*Result, error) { return status, nil },
			remove:        func() (*Result, error) { removed = true; return nil, nil },
		})
		if ErrorCode(err) != CodeWorkerReleasePersistenceFailed || removed {
			t.Fatalf("err=%v removed=%t residual=%+v", err, removed, status.Residual)
		}
	}
}

func TestCleanupStateClassification(t *testing.T) {
	if !cleanupWasPreserved(nil) || !cleanupWasPreserved(errorWithCleanupEvidence(errors.New("refused"), "preserved", `C:\pool.vhdx`, true)) {
		t.Fatal("clean detach or preserved refusal was not classified as preserved")
	}
	if cleanupWasPreserved(cleanupFailure("detach", `C:\pool.vhdx`, errors.New("failed"), true)) {
		t.Fatal("detach failure was classified as preserved")
	}
}

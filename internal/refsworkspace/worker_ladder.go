package refsworkspace

import "fmt"

const (
	WorkerLadderSafetyBytes   int64 = 4 << 30
	WorkerLadderMaxSoftBudget int64 = 62 << 30
)

type WorkerLadderSizingEvidence struct {
	WorkerCount               int   `json:"workerCount"`
	UsedAfterBaselineBytes    int64 `json:"usedAfterBaselineBytes"`
	WorkerReserveBytes        int64 `json:"workerReserveBytes"`
	TotalWorkerReserveBytes   int64 `json:"totalWorkerReserveBytes"`
	SafetyBytes               int64 `json:"safetyBytes"`
	CalculatedSoftBudgetBytes int64 `json:"calculatedSoftBudgetBytes"`
	MaximumSoftBudgetBytes    int64 `json:"maximumSoftBudgetBytes"`
	MinimumHostFreeBytes      int64 `json:"minimumHostFreeBytes"`
	VHDXOverheadReserveBytes  int64 `json:"vhdxOverheadReserveBytes"`
	RequiredHostFreeBytes     int64 `json:"requiredHostFreeBytes"`
	PolicyOverrideRequired    bool  `json:"policyOverrideRequired"`
	WithinMaximum             bool  `json:"withinMaximum"`
}

func CalculateWorkerLadderSizing(usedAfterBaseline int64, workerCount int, workerReserve, minimumHostFree, vhdxOverhead int64) (WorkerLadderSizingEvidence, error) {
	evidence := WorkerLadderSizingEvidence{
		WorkerCount: workerCount, UsedAfterBaselineBytes: usedAfterBaseline,
		WorkerReserveBytes: workerReserve, SafetyBytes: WorkerLadderSafetyBytes,
		MaximumSoftBudgetBytes: WorkerLadderMaxSoftBudget,
		MinimumHostFreeBytes:   minimumHostFree, VHDXOverheadReserveBytes: vhdxOverhead,
		PolicyOverrideRequired: true,
	}
	if !validParallelWorkerCount(workerCount) || usedAfterBaseline < 0 || workerReserve <= 0 || minimumHostFree <= 0 || vhdxOverhead < 0 {
		return evidence, newError(CodeInvalidConfiguration, "calculate-worker-ladder-budget", fmt.Sprint(workerCount), fmt.Errorf("invalid sizing input"))
	}
	reserve, ok := checkedMultiplyInt64(int64(workerCount), workerReserve)
	if !ok {
		return evidence, newError(CodeStorageBudgetExceeded, "calculate-worker-ladder-budget", fmt.Sprint(workerCount), fmt.Errorf("worker reserve overflow"))
	}
	evidence.TotalWorkerReserveBytes = reserve
	required, ok := checkedAddInt64(usedAfterBaseline, reserve)
	if ok {
		required, ok = checkedAddInt64(required, WorkerLadderSafetyBytes)
	}
	if !ok {
		return evidence, newError(CodeStorageBudgetExceeded, "calculate-worker-ladder-budget", fmt.Sprint(workerCount), fmt.Errorf("soft budget overflow"))
	}
	evidence.CalculatedSoftBudgetBytes, ok = roundUpGiB(required)
	if !ok {
		return evidence, newError(CodeStorageBudgetExceeded, "calculate-worker-ladder-budget", fmt.Sprint(workerCount), fmt.Errorf("soft budget round overflow"))
	}
	evidence.WithinMaximum = evidence.CalculatedSoftBudgetBytes <= WorkerLadderMaxSoftBudget
	requiredHost, ok := checkedAddInt64(minimumHostFree, vhdxOverhead)
	if ok {
		requiredHost, ok = checkedAddInt64(requiredHost, reserve)
	}
	if !ok {
		return evidence, newError(CodeStorageBudgetExceeded, "calculate-worker-ladder-host-free", fmt.Sprint(workerCount), fmt.Errorf("host-free floor overflow"))
	}
	evidence.RequiredHostFreeBytes = requiredHost
	if !evidence.WithinMaximum {
		return evidence, newError(CodeStorageBudgetExceeded, "calculate-worker-ladder-budget", fmt.Sprint(workerCount), fmt.Errorf("required=%d maximum=%d", evidence.CalculatedSoftBudgetBytes, WorkerLadderMaxSoftBudget))
	}
	return evidence, nil
}

func roundUpGiB(value int64) (int64, bool) {
	if value < 0 {
		return 0, false
	}
	const gib = int64(1 << 30)
	adjusted, ok := checkedAddInt64(value, gib-1)
	if !ok {
		return 0, false
	}
	return (adjusted / gib) * gib, true
}

func checkedMultiplyInt64(left, right int64) (int64, bool) {
	if left < 0 || right < 0 || (left != 0 && right > int64(^uint64(0)>>1)/left) {
		return 0, false
	}
	return left * right, true
}

func workerLadderErrorString(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}

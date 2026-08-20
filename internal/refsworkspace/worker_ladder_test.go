package refsworkspace

import "testing"

func TestCalculateWorkerLadderSizing(t *testing.T) {
	tests := []struct {
		workers int
		used    int64
		want    int64
		host    int64
	}{
		{workers: 2, used: 5 << 30, want: 13 << 30, host: 36 << 30},
		{workers: 4, used: 5 << 30, want: 17 << 30, host: 40 << 30},
		{workers: 8, used: 5 << 30, want: 25 << 30, host: 48 << 30},
		{workers: 8, used: (5 << 30) + 1, want: 26 << 30, host: 48 << 30},
	}
	for _, test := range tests {
		evidence, err := CalculateWorkerLadderSizing(test.used, test.workers, 2<<30, 30<<30, 2<<30)
		if err != nil {
			t.Fatalf("workers=%d: %v", test.workers, err)
		}
		if evidence.CalculatedSoftBudgetBytes != test.want || evidence.RequiredHostFreeBytes != test.host || !evidence.WithinMaximum {
			t.Fatalf("workers=%d evidence=%+v", test.workers, evidence)
		}
	}
}

func TestCalculateWorkerLadderSizingRejectsInvalidAndOverMaximum(t *testing.T) {
	if _, err := CalculateWorkerLadderSizing(1, 3, 2<<30, 30<<30, 2<<30); ErrorCode(err) != CodeInvalidConfiguration {
		t.Fatalf("invalid count err=%v", err)
	}
	evidence, err := CalculateWorkerLadderSizing(43<<30, 8, 2<<30, 30<<30, 2<<30)
	if ErrorCode(err) != CodeStorageBudgetExceeded || evidence.WithinMaximum {
		t.Fatalf("evidence=%+v err=%v", evidence, err)
	}
}

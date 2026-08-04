package refsworkspace

import "testing"

func TestPlanCloneAlignmentAndTail(t *testing.T) {
	const cluster = int64(64 << 10)
	plan, err := PlanClone(9<<30+123, cluster)
	if err != nil {
		t.Fatal(err)
	}
	var cloned int64
	for _, cloneRange := range plan.Ranges {
		if cloneRange.Offset%cluster != 0 || cloneRange.Length%cluster != 0 {
			t.Fatalf("unaligned range: %+v", cloneRange)
		}
		if cloneRange.Length >= fourGiB {
			t.Fatalf("range must be below 4 GiB: %+v", cloneRange)
		}
		cloned += cloneRange.Length
	}
	if cloned+plan.TailBytes != plan.FileSize {
		t.Fatalf("planned bytes=%d tail=%d size=%d", cloned, plan.TailBytes, plan.FileSize)
	}
	if plan.TailBytes != 123 {
		t.Fatalf("tail=%d", plan.TailBytes)
	}
}

func TestPlanCloneSmallFileIsMeasuredTail(t *testing.T) {
	plan, err := PlanClone(31, 4096)
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Ranges) != 0 || plan.TailBytes != 31 || plan.TailOffset != 0 {
		t.Fatalf("unexpected plan: %+v", plan)
	}
}

func TestPlanCloneRejectsInvalidCluster(t *testing.T) {
	for _, cluster := range []int64{0, -1, 3} {
		if _, err := PlanClone(1, cluster); err == nil {
			t.Fatalf("cluster %d unexpectedly accepted", cluster)
		}
	}
}

func TestValidateCloneMetricsForbidsFallback(t *testing.T) {
	valid := CloneMetrics{ClonedBytes: 4096, PhysicalCopiedBytes: 7, TailCopiedBytes: 7}
	if err := ValidateCloneMetrics(valid); err != nil {
		t.Fatal(err)
	}
	valid.FallbackUsed = true
	if code := ErrorCode(ValidateCloneMetrics(valid)); code != CodeBlockCloneUnavailable {
		t.Fatalf("code=%s", code)
	}
	valid.FallbackUsed = false
	valid.PhysicalCopiedBytes++
	if code := ErrorCode(ValidateCloneMetrics(valid)); code != CodeBlockCloneUnavailable {
		t.Fatalf("code=%s", code)
	}
}

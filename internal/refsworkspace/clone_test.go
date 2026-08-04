package refsworkspace

import (
	"errors"
	"testing"
)

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

func TestPlanSparseClonePreservesHolesAndChunksAllocatedExtents(t *testing.T) {
	const cluster = int64(4096)
	fileSize := int64(2<<40) + 123
	allocated := []AllocatedRange{
		{Offset: 0, Length: cluster},
		{Offset: 10 * cluster, Length: 3*cluster + 17},
		{Offset: fileSize - 123, Length: 123},
	}
	plan, err := PlanSparseClone(fileSize, cluster, allocated)
	if err != nil {
		t.Fatal(err)
	}
	var cloned, physical int64
	for _, current := range plan.CloneRanges {
		if current.Offset%cluster != 0 || current.Length%cluster != 0 || current.Length >= fourGiB {
			t.Fatalf("invalid clone range: %+v", current)
		}
		cloned += current.Length
	}
	for _, current := range plan.PhysicalRanges {
		physical += current.Length
	}
	if cloned+physical != plan.AllocatedBytes {
		t.Fatalf("cloned=%d physical=%d allocated=%d", cloned, physical, plan.AllocatedBytes)
	}
	if plan.AllocatedBytes+plan.HoleBytes != fileSize {
		t.Fatalf("allocated=%d holes=%d size=%d", plan.AllocatedBytes, plan.HoleBytes, fileSize)
	}
	if plan.HoleBytes == 0 || physical == 0 {
		t.Fatalf("plan=%+v", plan)
	}
}

func TestPlanSparseCloneCases(t *testing.T) {
	const cluster = int64(4096)
	tests := []struct {
		name                    string
		size                    int64
		ranges                  []AllocatedRange
		wantAllocated, wantHole int64
	}{
		{name: "empty", size: 0},
		{name: "leading hole", size: 4 * cluster, ranges: []AllocatedRange{{Offset: cluster, Length: cluster}}, wantAllocated: cluster, wantHole: 3 * cluster},
		{name: "middle hole", size: 5 * cluster, ranges: []AllocatedRange{{Offset: 0, Length: cluster}, {Offset: 4 * cluster, Length: cluster}}, wantAllocated: 2 * cluster, wantHole: 3 * cluster},
		{name: "unaligned tail", size: cluster + 17, ranges: []AllocatedRange{{Offset: 0, Length: cluster + 17}}, wantAllocated: cluster + 17, wantHole: 0},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			plan, err := PlanSparseClone(test.size, cluster, test.ranges)
			if err != nil {
				t.Fatal(err)
			}
			if plan.AllocatedBytes != test.wantAllocated || plan.HoleBytes != test.wantHole {
				t.Fatalf("plan=%+v", plan)
			}
		})
	}
}

func TestPlanSparseCloneRejectsBadAllocatedRanges(t *testing.T) {
	for _, ranges := range [][]AllocatedRange{
		{{Offset: -1, Length: 1}}, {{Offset: 0, Length: 0}}, {{Offset: 8, Length: 8}, {Offset: 4, Length: 1}}, {{Offset: 0, Length: 11}},
	} {
		if _, err := PlanSparseClone(10, 2, ranges); err == nil {
			t.Fatalf("ranges unexpectedly accepted: %+v", ranges)
		}
	}
}

func TestPlanSparseCloneQueryFailureDoesNotFallback(t *testing.T) {
	want := errors.New("query failed")
	_, err := PlanSparseCloneFromQuery(1<<30, 4096, func() ([]AllocatedRange, error) { return nil, want })
	if !errors.Is(err, want) {
		t.Fatalf("err=%v", err)
	}
}

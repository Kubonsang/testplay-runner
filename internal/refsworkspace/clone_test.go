package refsworkspace

import (
	"errors"
	"math"
	"reflect"
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
		{{Offset: -1, Length: 1}}, {{Offset: 0, Length: -1}}, {{Offset: math.MaxInt64, Length: 1}},
	} {
		if _, err := PlanSparseClone(10, 2, ranges); err == nil {
			t.Fatalf("ranges unexpectedly accepted: %+v", ranges)
		}
	}
}

func TestNormalizeAllocatedRangesClipsSortsAndMerges(t *testing.T) {
	ranges, err := NormalizeAllocatedRanges(100, 20, 60, []AllocatedRange{
		{Offset: 70, Length: 40}, // query and EOF crossing
		{Offset: 10, Length: 20}, // begins before query
		{Offset: 30, Length: 10},
		{Offset: 40, Length: 10}, // adjacent
		{Offset: 35, Length: 20}, // overlap
		{Offset: 30, Length: 10}, // duplicate
		{Offset: 60, Length: 0},
	})
	if err != nil {
		t.Fatal(err)
	}
	want := []AllocatedRange{{Offset: 20, Length: 35}, {Offset: 70, Length: 10}}
	if !reflect.DeepEqual(ranges, want) {
		t.Fatalf("ranges=%+v want=%+v", ranges, want)
	}
}

func TestNormalizeAllocatedRangesAllHoleAndFullFile(t *testing.T) {
	allHole, err := NormalizeAllocatedRanges(32, 0, 32, nil)
	if err != nil || len(allHole) != 0 {
		t.Fatalf("allHole=%v err=%v", allHole, err)
	}
	full, err := NormalizeAllocatedRanges(32, 0, 64, []AllocatedRange{{Offset: 0, Length: 64}})
	if err != nil || !reflect.DeepEqual(full, []AllocatedRange{{Offset: 0, Length: 32}}) {
		t.Fatalf("full=%v err=%v", full, err)
	}
}

func TestNormalizeAllocatedRangesRejectsOverflow(t *testing.T) {
	for _, test := range []struct {
		offset, length int64
		ranges         []AllocatedRange
	}{
		{offset: math.MaxInt64, length: 1},
		{offset: 0, length: 10, ranges: []AllocatedRange{{Offset: math.MaxInt64, Length: 1}}},
	} {
		if _, err := NormalizeAllocatedRanges(math.MaxInt64, test.offset, test.length, test.ranges); err == nil {
			t.Fatal("overflow unexpectedly accepted")
		}
	}
}

func TestCollectAllocatedRangesPagedNormalizesMoreDataBatches(t *testing.T) {
	queries := 0
	ranges, err := collectAllocatedRangesPaged(100, func(offset, length int64) ([]AllocatedRange, bool, error) {
		queries++
		switch queries {
		case 1:
			return []AllocatedRange{{Offset: offset, Length: 40}, {Offset: 30, Length: 20}}, true, nil
		case 2:
			return []AllocatedRange{{Offset: offset - 10, Length: 30}, {Offset: 70, Length: 50}}, true, nil
		default:
			t.Fatalf("unexpected query offset=%d length=%d", offset, length)
			return nil, false, nil
		}
	})
	if err != nil {
		t.Fatal(err)
	}
	if queries != 2 || !reflect.DeepEqual(ranges, []AllocatedRange{{Offset: 0, Length: 100}}) {
		t.Fatalf("queries=%d ranges=%+v", queries, ranges)
	}
}

func TestCollectAllocatedRangesPagedRejectsNoProgress(t *testing.T) {
	_, err := collectAllocatedRangesPaged(100, func(offset, _ int64) ([]AllocatedRange, bool, error) {
		return []AllocatedRange{{Offset: 0, Length: offset}}, true, nil
	})
	if err == nil {
		t.Fatal("no-progress response accepted")
	}
}

func TestPlanSparseCloneQueryFailureDoesNotFallback(t *testing.T) {
	want := errors.New("query failed")
	_, err := PlanSparseCloneFromQuery(1<<30, 4096, func() ([]AllocatedRange, error) { return nil, want })
	if !errors.Is(err, want) {
		t.Fatalf("err=%v", err)
	}
}

func TestAggregateCloneFileMetricsPreservesIOCTLAttempts(t *testing.T) {
	tests := []struct {
		name        string
		files       []CloneMetrics
		wantRegular bool
		wantSparse  bool
	}{
		{name: "regular only", files: []CloneMetrics{{RegularBlockCloneIOCTLAttempted: true}}, wantRegular: true},
		{name: "sparse only", files: []CloneMetrics{{SparseBlockCloneIOCTLAttempted: true}}, wantSparse: true},
		{name: "mixed", files: []CloneMetrics{{RegularBlockCloneIOCTLAttempted: true}, {SparseBlockCloneIOCTLAttempted: true}}, wantRegular: true, wantSparse: true},
		{name: "attempt retained after later file error", files: []CloneMetrics{{RegularBlockCloneIOCTLAttempted: true}, {}}, wantRegular: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var aggregate CloneMetrics
			for _, file := range test.files {
				aggregateCloneFileMetrics(&aggregate, file)
			}
			if aggregate.RegularBlockCloneIOCTLAttempted != test.wantRegular || aggregate.SparseBlockCloneIOCTLAttempted != test.wantSparse {
				t.Fatalf("aggregate=%+v", aggregate)
			}
		})
	}
	fileErr := errors.New("IOCTL failed after attempt")
	var failedAggregate CloneMetrics
	if err := aggregateCloneFileResult(&failedAggregate, CloneMetrics{RegularBlockCloneIOCTLAttempted: true}, fileErr); !errors.Is(err, fileErr) {
		t.Fatalf("returned error=%v", err)
	}
	if !failedAggregate.RegularBlockCloneIOCTLAttempted || failedAggregate.FailedFileCount != 1 {
		t.Fatalf("failed aggregate=%+v", failedAggregate)
	}
}

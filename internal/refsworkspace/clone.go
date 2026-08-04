package refsworkspace

import (
	"context"
	"fmt"
	"sort"
)

const fourGiB = int64(4 << 30)

type CloneRange struct {
	Offset int64 `json:"offset"`
	Length int64 `json:"length"`
}

type ClonePlan struct {
	FileSize    int64        `json:"fileSize"`
	ClusterSize int64        `json:"clusterSize"`
	Ranges      []CloneRange `json:"ranges"`
	TailOffset  int64        `json:"tailOffset"`
	TailBytes   int64        `json:"tailBytes"`
}

type AllocatedRange struct {
	Offset int64 `json:"offset"`
	Length int64 `json:"length"`
}

type SparseClonePlan struct {
	FileSize       int64        `json:"fileSize"`
	ClusterSize    int64        `json:"clusterSize"`
	CloneRanges    []CloneRange `json:"cloneRanges"`
	PhysicalRanges []CloneRange `json:"physicalRanges"`
	AllocatedBytes int64        `json:"allocatedBytes"`
	HoleBytes      int64        `json:"holeBytes"`
}

func PlanClone(fileSize, clusterSize int64) (ClonePlan, error) {
	if fileSize < 0 {
		return ClonePlan{}, fmt.Errorf("file size must not be negative")
	}
	if clusterSize <= 0 || clusterSize&(clusterSize-1) != 0 {
		return ClonePlan{}, fmt.Errorf("cluster size must be a positive power of two")
	}
	maxChunk := ((fourGiB - 1) / clusterSize) * clusterSize
	if maxChunk <= 0 {
		return ClonePlan{}, fmt.Errorf("cluster size is too large")
	}
	alignedBytes := (fileSize / clusterSize) * clusterSize
	plan := ClonePlan{
		FileSize:    fileSize,
		ClusterSize: clusterSize,
		TailOffset:  alignedBytes,
		TailBytes:   fileSize - alignedBytes,
	}
	for offset := int64(0); offset < alignedBytes; {
		length := alignedBytes - offset
		if length > maxChunk {
			length = maxChunk
		}
		plan.Ranges = append(plan.Ranges, CloneRange{Offset: offset, Length: length})
		offset += length
	}
	return plan, nil
}

func PlanSparseClone(fileSize, clusterSize int64, allocated []AllocatedRange) (SparseClonePlan, error) {
	if _, err := PlanClone(fileSize, clusterSize); err != nil {
		return SparseClonePlan{}, err
	}
	allocated, err := NormalizeAllocatedRanges(fileSize, 0, fileSize, allocated)
	if err != nil {
		return SparseClonePlan{}, err
	}
	plan := SparseClonePlan{FileSize: fileSize, ClusterSize: clusterSize}
	maxChunk := ((fourGiB - 1) / clusterSize) * clusterSize
	for _, current := range allocated {
		end := current.Offset + current.Length
		plan.AllocatedBytes += current.Length
		alignedStart := current.Offset
		if remainder := current.Offset % clusterSize; remainder != 0 {
			increment := clusterSize - remainder
			if increment > fileSize-current.Offset {
				alignedStart = end
			} else {
				alignedStart += increment
			}
		}
		if alignedStart > end {
			alignedStart = end
		}
		alignedEnd := (end / clusterSize) * clusterSize
		if alignedEnd < alignedStart {
			alignedEnd = alignedStart
		}
		if current.Offset < alignedStart {
			plan.PhysicalRanges = append(plan.PhysicalRanges, CloneRange{Offset: current.Offset, Length: alignedStart - current.Offset})
		}
		for offset := alignedStart; offset < alignedEnd; {
			length := alignedEnd - offset
			if length > maxChunk {
				length = maxChunk
			}
			plan.CloneRanges = append(plan.CloneRanges, CloneRange{Offset: offset, Length: length})
			offset += length
		}
		if alignedEnd < end {
			plan.PhysicalRanges = append(plan.PhysicalRanges, CloneRange{Offset: alignedEnd, Length: end - alignedEnd})
		}
	}
	plan.HoleBytes = fileSize - plan.AllocatedBytes
	return plan, nil
}

// NormalizeAllocatedRanges converts filesystem range-query output into a
// canonical sorted, clipped, non-overlapping set for the requested file span.
func NormalizeAllocatedRanges(fileSize, queryOffset, queryLength int64, ranges []AllocatedRange) ([]AllocatedRange, error) {
	if fileSize < 0 || queryOffset < 0 || queryLength < 0 {
		return nil, fmt.Errorf("file size and query range must not be negative")
	}
	queryEnd, ok := checkedAddInt64(queryOffset, queryLength)
	if !ok {
		return nil, fmt.Errorf("query range overflows")
	}
	if queryOffset >= fileSize || queryLength == 0 {
		return nil, nil
	}
	if queryEnd > fileSize {
		queryEnd = fileSize
	}
	var normalized []AllocatedRange
	for index, current := range ranges {
		if current.Offset < 0 || current.Length < 0 {
			return nil, fmt.Errorf("allocated range %d must not be negative", index)
		}
		if current.Length == 0 {
			continue
		}
		end, ok := checkedAddInt64(current.Offset, current.Length)
		if !ok {
			return nil, fmt.Errorf("allocated range %d overflows", index)
		}
		start := current.Offset
		if start < queryOffset {
			start = queryOffset
		}
		if end > queryEnd {
			end = queryEnd
		}
		if start >= end {
			continue
		}
		normalized = append(normalized, AllocatedRange{Offset: start, Length: end - start})
	}
	sort.Slice(normalized, func(left, right int) bool {
		if normalized[left].Offset == normalized[right].Offset {
			return normalized[left].Length < normalized[right].Length
		}
		return normalized[left].Offset < normalized[right].Offset
	})
	merged := normalized[:0]
	for _, current := range normalized {
		if len(merged) == 0 {
			merged = append(merged, current)
			continue
		}
		last := &merged[len(merged)-1]
		lastEnd := last.Offset + last.Length
		currentEnd := current.Offset + current.Length
		if current.Offset <= lastEnd {
			if currentEnd > lastEnd {
				last.Length = currentEnd - last.Offset
			}
			continue
		}
		merged = append(merged, current)
	}
	return merged, nil
}

type allocatedRangePageQuery func(offset, length int64) (ranges []AllocatedRange, more bool, err error)

// collectAllocatedRangesPaged handles ERROR_MORE_DATA-style pagination. Every
// batch is clipped to its query, and repeated responses must make progress.
func collectAllocatedRangesPaged(fileSize int64, query allocatedRangePageQuery) ([]AllocatedRange, error) {
	if fileSize < 0 || query == nil {
		return nil, fmt.Errorf("valid file size and allocated range query are required")
	}
	if fileSize == 0 {
		return nil, nil
	}
	var collected []AllocatedRange
	for offset := int64(0); offset < fileSize; {
		ranges, more, err := query(offset, fileSize-offset)
		if err != nil {
			return nil, err
		}
		batch, err := NormalizeAllocatedRanges(fileSize, offset, fileSize-offset, ranges)
		if err != nil {
			return nil, err
		}
		collected = append(collected, batch...)
		if !more {
			break
		}
		if len(batch) == 0 {
			return nil, fmt.Errorf("allocated range query made no progress")
		}
		last := batch[len(batch)-1]
		next := last.Offset + last.Length
		if next <= offset {
			return nil, fmt.Errorf("allocated range query made no progress")
		}
		if next >= fileSize {
			break
		}
		offset = next
	}
	return NormalizeAllocatedRanges(fileSize, 0, fileSize, collected)
}

func PlanSparseCloneFromQuery(fileSize, clusterSize int64, query func() ([]AllocatedRange, error)) (SparseClonePlan, error) {
	if query == nil {
		return SparseClonePlan{}, fmt.Errorf("allocated range query is required")
	}
	ranges, err := query()
	if err != nil {
		return SparseClonePlan{}, fmt.Errorf("query allocated ranges: %w", err)
	}
	return PlanSparseClone(fileSize, clusterSize, ranges)
}

type TreeCloner interface {
	CloneTree(context.Context, string, string, int64) (CloneMetrics, error)
}

func ValidateCloneMetrics(metrics CloneMetrics) error {
	if metrics.FallbackUsed {
		return newError(CodeBlockCloneUnavailable, "validate-clone-metrics", "", fmt.Errorf("silent physical fallback is forbidden"))
	}
	if metrics.FailedFileCount != 0 {
		return newError(CodeCloneFailed, "validate-clone-metrics", "", fmt.Errorf("%d files failed", metrics.FailedFileCount))
	}
	if metrics.ClonedBytes == 0 {
		return newError(CodeBlockCloneUnavailable, "validate-clone-metrics", "", fmt.Errorf("no cluster-aligned bytes were block cloned"))
	}
	if metrics.PhysicalCopiedBytes != metrics.TailCopiedBytes {
		return newError(CodeBlockCloneUnavailable, "validate-clone-metrics", "", fmt.Errorf("physical bytes must be limited to measured unaligned tails"))
	}
	return nil
}

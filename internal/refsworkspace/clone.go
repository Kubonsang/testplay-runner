package refsworkspace

import (
	"context"
	"fmt"
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
	plan := SparseClonePlan{FileSize: fileSize, ClusterSize: clusterSize}
	maxChunk := ((fourGiB - 1) / clusterSize) * clusterSize
	previousEnd := int64(0)
	for index, current := range allocated {
		if current.Offset < 0 || current.Length <= 0 || current.Offset > fileSize || current.Length > fileSize-current.Offset {
			return SparseClonePlan{}, fmt.Errorf("allocated range %d is outside the file", index)
		}
		end := current.Offset + current.Length
		if index != 0 && current.Offset < previousEnd {
			return SparseClonePlan{}, fmt.Errorf("allocated ranges overlap or are unsorted")
		}
		previousEnd = end
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

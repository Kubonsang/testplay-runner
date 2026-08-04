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

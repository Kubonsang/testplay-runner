package librarymaterializer

import (
	"context"
	"fmt"
	"os"
	"time"

	"github.com/Kubonsang/testplay-runner/internal/shadow"
)

const PhysicalCopyID = "physical-copy"

// Request identifies one immutable Library source and its independent writable
// destination. Image identity and lifecycle policy intentionally stay outside
// this package.
type Request struct {
	SourcePath      string
	DestinationPath string
}

// Result describes the files placed by a materializer.
type Result struct {
	MaterializerID string
	LogicalBytes   int64
	FileCount      int64
	Duration       time.Duration
}

// LibraryMaterializer prepares a writable Library from an already validated
// source. The interface stays deliberately narrow until another implementation
// is proven.
type LibraryMaterializer interface {
	ID() string
	Materialize(ctx context.Context, request Request) (*Result, error)
}

// PhysicalCopyMaterializer preserves the original Image backend's full
// physical-copy behavior. It never creates hardlinks.
type PhysicalCopyMaterializer struct{}

func (PhysicalCopyMaterializer) ID() string {
	return PhysicalCopyID
}

func (materializer PhysicalCopyMaterializer) Materialize(
	ctx context.Context,
	request Request,
) (*Result, error) {
	started := time.Now()
	succeeded := false
	defer func() {
		if !succeeded {
			_ = os.RemoveAll(request.DestinationPath)
		}
	}()

	stats, err := shadow.CopyDirParallelWithStats(
		ctx,
		request.SourcePath,
		request.DestinationPath,
		0,
	)
	result := &Result{
		MaterializerID: materializer.ID(),
		LogicalBytes:   stats.LogicalBytes,
		FileCount:      stats.FileCount,
		Duration:       time.Since(started),
	}
	if err != nil {
		return result, fmt.Errorf("%s materializer: %w", materializer.ID(), err)
	}
	succeeded = true
	return result, nil
}

package runsvc

import (
	"bytes"
	"context"
	"crypto/sha256"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/Kubonsang/testplay-runner/internal/history"
	"github.com/Kubonsang/testplay-runner/internal/libraryimage"
	"github.com/Kubonsang/testplay-runner/internal/librarymaterializer"
	"github.com/Kubonsang/testplay-runner/internal/shadow"
	"github.com/Kubonsang/testplay-runner/internal/unity"
)

const (
	WorkspaceBackendLegacy   = "legacy"
	WorkspaceBackendImage    = "image"
	WorkspaceBackendVHDXDiff = "vhdx-diff"
	WorkspaceBackendAuto     = "auto"
)

func ValidWorkspaceBackend(value string) bool {
	return value == "" || value == WorkspaceBackendLegacy || value == WorkspaceBackendImage ||
		value == WorkspaceBackendVHDXDiff || value == WorkspaceBackendAuto
}

func testExecutionMilliseconds(result *history.RunResult) int64 {
	if result == nil {
		return 0
	}
	var total float64
	for _, test := range result.Tests {
		total += test.Duration
	}
	return int64(total*1000 + 0.5)
}

func prepareMetrics(backend string) *history.WorkspaceMetrics {
	imageStatus := "not_selected"
	if backend == WorkspaceBackendImage {
		imageStatus = string(libraryimage.StatusUnsupported)
	}
	return &history.WorkspaceMetrics{
		WorkspaceBackend: backend,
		ImageStatus:      imageStatus,
	}
}

func updateObservedPeak(metrics *history.WorkspaceMetrics, bytes int64) {
	if bytes > metrics.ObservedPeakAdditionalPhysicalBytes {
		metrics.ObservedPeakAdditionalPhysicalBytes = bytes
	}
}

func durationWithoutVerification(
	total time.Duration,
	verification libraryimage.VerificationMetrics,
) time.Duration {
	excluded := verification.MetadataVerify + verification.FullHash
	if excluded >= total {
		return 0
	}
	return total - excluded
}

func updateImageVerificationMetrics(
	metrics *history.WorkspaceMetrics,
	verification libraryimage.VerificationMetrics,
) {
	metrics.ImageMetadataVerifyMs = verification.MetadataVerify.Milliseconds()
	metrics.ImageFullHashMs = verification.FullHash.Milliseconds()
}

func resolveWorkspaceStoreRoot(projectPath, requested string) (string, error) {
	if requested == "" {
		return filepath.Join(projectPath, ".testplay"), nil
	}
	if !filepath.IsAbs(requested) {
		return "", fmt.Errorf("must be an absolute path")
	}
	info, err := os.Stat(requested)
	if err != nil {
		return "", err
	}
	if !info.IsDir() {
		return "", fmt.Errorf("is not a directory")
	}
	resolvedRoot, err := filepath.EvalSymlinks(requested)
	if err != nil {
		return "", err
	}
	resolvedProject, err := filepath.EvalSymlinks(projectPath)
	if err != nil {
		return "", err
	}
	rootWithSeparator := resolvedRoot + string(os.PathSeparator)
	projectWithSeparator := resolvedProject + string(os.PathSeparator)
	if resolvedRoot == resolvedProject ||
		strings.HasPrefix(projectWithSeparator, rootWithSeparator) ||
		strings.HasPrefix(rootWithSeparator, projectWithSeparator) {
		return "", fmt.Errorf("must not contain or be contained by the Unity project")
	}
	digest := sha256.Sum256([]byte(resolvedProject))
	return filepath.Join(resolvedRoot, fmt.Sprintf("%x", digest[:8])), nil
}

func legacyCacheRoot(req Request) string {
	root, _ := resolveWorkspaceStoreRoot(req.Config.ProjectPath, req.WorkspaceStoreRoot)
	if req.WorkspaceStoreRoot == "" {
		return filepath.Join(root, "cache")
	}
	return filepath.Join(root, "legacy-cache")
}

func imageStoreRoot(req Request) string {
	root, _ := resolveWorkspaceStoreRoot(req.Config.ProjectPath, req.WorkspaceStoreRoot)
	return filepath.Join(root, "library-images")
}

func (s *Service) selectedLibraryMaterializer() librarymaterializer.LibraryMaterializer {
	if s.LibraryMaterializer != nil {
		return s.LibraryMaterializer
	}
	return librarymaterializer.PhysicalCopyMaterializer{}
}

func (s *Service) prepareLegacyWorkspace(
	ctx context.Context,
	req Request,
	runID string,
) (*shadow.Workspace, *history.WorkspaceMetrics, error) {
	metrics := prepareMetrics(WorkspaceBackendLegacy)
	cacheRoot := legacyCacheRoot(req)
	opts := shadow.PrepareOptions{LibraryCacheRoot: cacheRoot}
	if !req.ClearCache && shadow.ValidateCacheAt(req.Config.ProjectPath, cacheRoot) {
		opts.LibraryCacheDir = shadow.CacheLibraryDirAt(cacheRoot)
	}

	started := time.Now()
	ws, err := shadow.Prepare(ctx, req.Config.ProjectPath, runID, opts)
	metrics.WorkspacePreparationMs = time.Since(started).Milliseconds()
	if err != nil {
		return nil, metrics, err
	}
	metrics.FileCopyMs = (ws.Metrics.AssetsCopy + ws.Metrics.ProjectSettingsCopy + ws.Metrics.PackagesCopy).Milliseconds()
	metrics.LibraryMaterializationMs = ws.Metrics.LibraryMaterialize.Milliseconds()
	return ws, metrics, nil
}

func (s *Service) prepareImageWorkspace(
	ctx context.Context,
	req Request,
	runID string,
	stdout io.Writer,
	stderr io.Writer,
) (*shadow.Workspace, *history.WorkspaceMetrics, error) {
	metrics := prepareMetrics(WorkspaceBackendImage)
	store := libraryimage.NewStoreAt(imageStoreRoot(req))
	if req.ClearCache {
		if err := store.Clear(); err != nil {
			return nil, metrics, fmt.Errorf("clear Library images: %w", err)
		}
	}

	resolveStarted := time.Now()
	key, err := libraryimage.ComputeKey(req.Config.ProjectPath, req.Config.UnityPath)
	if err != nil {
		return nil, metrics, err
	}
	metrics.ImageKey = key.Digest

	resolution, err := store.Resolve(ctx, key)
	if err != nil {
		return nil, metrics, err
	}
	resolveVerification := store.VerificationMetrics()
	metrics.ImageResolveMs = durationWithoutVerification(
		time.Since(resolveStarted),
		resolveVerification,
	).Milliseconds()
	updateImageVerificationMetrics(metrics, resolveVerification)
	metrics.ImageResolutionStatus = string(resolution.Status)
	if resolution.Status == libraryimage.StatusUnsupported {
		return nil, metrics, fmt.Errorf("Library image unsupported: %s", resolution.Reason)
	}

	image := resolution.Image
	if resolution.Status != libraryimage.StatusValid {
		creationStarted := time.Now()
		var builderUsage shadow.DirectoryUsage
		var lockedStatus libraryimage.Status
		image, lockedStatus, err = store.Ensure(ctx, key, func() (libraryimage.ImageSource, error) {
			builder, err := shadow.Prepare(
				ctx,
				req.Config.ProjectPath,
				runID+"-image-builder",
				shadow.PrepareOptions{CopyPackages: true},
			)
			if err != nil {
				return libraryimage.ImageSource{}, fmt.Errorf("prepare Library image builder: %w", err)
			}

			args := append(unity.BuildCompileArgs(builder.ShadowPath), "-disable-assembly-updater")
			var builderStderr bytes.Buffer
			stderrWriter := io.Writer(&builderStderr)
			if stderr != nil {
				stderrWriter = io.MultiWriter(stderr, &builderStderr)
			}
			exitCode, runErr := s.Runner.Run(ctx, args, stdout, stderrWriter)
			if runErr != nil {
				_ = builder.Cleanup()
				return libraryimage.ImageSource{}, fmt.Errorf("build Library image: launch Unity: %w", runErr)
			}
			if err := ctx.Err(); err != nil {
				_ = builder.Cleanup()
				return libraryimage.ImageSource{}, fmt.Errorf("build Library image: %w", err)
			}
			if exitCode != 0 {
				_ = builder.Cleanup()
				return libraryimage.ImageSource{}, fmt.Errorf(
					"build Library image: Unity exited with code %d: %s",
					exitCode,
					tailText(builderStderr.String(), 4000),
				)
			}
			builderUsage, err = shadow.MeasureDirectoryUsage(builder.ShadowPath)
			if err != nil {
				_ = builder.Cleanup()
				return libraryimage.ImageSource{}, fmt.Errorf("measure Library image builder: %w", err)
			}
			return libraryimage.ImageSource{
				LibraryPath: filepath.Join(builder.ShadowPath, "Library"),
				Release:     func() { _ = builder.Cleanup() },
			}, nil
		})
		if err != nil {
			updateImageVerificationMetrics(metrics, store.VerificationMetrics())
			return nil, metrics, err
		}
		updateImageVerificationMetrics(metrics, store.VerificationMetrics())
		metrics.ImageResolutionStatus = string(lockedStatus)
		if lockedStatus != libraryimage.StatusValid {
			metrics.ImageCreationMs = time.Since(creationStarted).Milliseconds()
			metrics.ImageBuilderLogicalBytes = builderUsage.LogicalBytes
			metrics.ImageBuilderPhysicalBytes = builderUsage.AllocatedBytes
		}
	}
	metrics.ImageStatus = string(libraryimage.StatusValid)
	imageUsage, err := shadow.MeasureDirectoryUsage(image.Path)
	if err != nil {
		return nil, metrics, fmt.Errorf("measure Library base image: %w", err)
	}
	metrics.BaseImageLogicalBytes = imageUsage.LogicalBytes
	metrics.BaseImagePhysicalBytes = imageUsage.AllocatedBytes
	storeUsage, err := shadow.MeasureDirectoryUsage(store.Root())
	if err != nil {
		return nil, metrics, fmt.Errorf("measure Library image store: %w", err)
	}
	metrics.ImageStorePhysicalBytes = storeUsage.AllocatedBytes
	metrics.PhysicalBytesAdded = metrics.BaseImagePhysicalBytes
	updateObservedPeak(
		metrics,
		metrics.ImageStorePhysicalBytes+metrics.ImageBuilderPhysicalBytes,
	)

	preparationStarted := time.Now()
	ws, err := shadow.Prepare(
		ctx,
		req.Config.ProjectPath,
		runID,
		shadow.PrepareOptions{CopyPackages: true},
	)
	if err != nil {
		return nil, metrics, err
	}
	materializer := s.selectedLibraryMaterializer()
	metrics.Materializer = materializer.ID()
	verification, err := store.Verify(ctx, image)
	updateImageVerificationMetrics(metrics, store.VerificationMetrics())
	if err != nil {
		_ = ws.Cleanup()
		return nil, metrics, fmt.Errorf("verify Library image before materialization: %w", err)
	}
	if verification.Status != libraryimage.StatusValid {
		_ = ws.Cleanup()
		return nil, metrics, fmt.Errorf(
			"verify Library image before materialization: %s",
			verification.Reason,
		)
	}

	materialized, err := materializer.Materialize(ctx, librarymaterializer.Request{
		SourcePath:      image.LibraryPath,
		DestinationPath: filepath.Join(ws.ShadowPath, "Library"),
	})
	if materialized != nil {
		metrics.LibraryMaterializeMs = materialized.Duration.Milliseconds()
	}
	if err != nil {
		_ = ws.Cleanup()
		return nil, metrics, fmt.Errorf("materialize Library: %w", err)
	}
	workspaceVerifyStarted := time.Now()
	if materialized.MaterializerID != materializer.ID() ||
		materialized.FileCount != image.Metadata.FileCount ||
		materialized.LogicalBytes != image.Metadata.LogicalBytes {
		metrics.WorkspaceVerifyMs = time.Since(workspaceVerifyStarted).Milliseconds()
		_ = ws.Cleanup()
		return nil, metrics, fmt.Errorf(
			"verify materialized Library: materializer=%q files=%d/%d bytes=%d/%d",
			materialized.MaterializerID,
			materialized.FileCount,
			image.Metadata.FileCount,
			materialized.LogicalBytes,
			image.Metadata.LogicalBytes,
		)
	}
	metrics.WorkspaceVerifyMs = time.Since(workspaceVerifyStarted).Milliseconds()
	metrics.WorkspacePreparationMs = time.Since(preparationStarted).Milliseconds()
	metrics.FileCopyMs = (ws.Metrics.AssetsCopy + ws.Metrics.ProjectSettingsCopy + ws.Metrics.PackagesCopy).Milliseconds()
	metrics.LibraryMaterializationMs = materialized.Duration.Milliseconds()
	return ws, metrics, nil
}

func tailText(value string, limit int) string {
	value = strings.TrimSpace(value)
	if len(value) <= limit {
		return value
	}
	return value[len(value)-limit:]
}

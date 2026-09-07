package runsvc

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/Kubonsang/testplay-runner/internal/contentcache"
	"github.com/Kubonsang/testplay-runner/internal/history"
	"github.com/Kubonsang/testplay-runner/internal/shadow"
)

func sharedCacheEnabled(req Request) bool {
	return req.Config.Workspace != nil && req.Config.Workspace.CacheMode == "shared-content"
}
func sharedCacheStore(req Request) (contentcache.Store, error) {
	root := req.Config.Workspace.CacheRoot
	if root == "" {
		var err error
		root, err = contentcache.DefaultRoot()
		if err != nil {
			return contentcache.Store{}, err
		}
	}
	store := contentcache.New(root)
	if err := store.ValidateLocation(req.Config.ProjectPath); err != nil {
		return contentcache.Store{}, err
	}
	return store, nil
}
func (s *Service) prepareSharedCacheWorkspace(ctx context.Context, req Request, runID string, metrics *history.WorkspaceMetrics) (*shadow.Workspace, *history.WorkspaceMetrics, error) {
	started := time.Now()
	store, err := sharedCacheStore(req)
	if err != nil {
		return nil, metrics, err
	}
	key, err := contentcache.Key(req.Config.ProjectPath, req.Config.UnityPath)
	if err != nil {
		return nil, metrics, err
	}
	ws, err := shadow.Prepare(ctx, req.Config.ProjectPath, runID, shadow.PrepareOptions{SkipLibrary: true})
	if err != nil {
		return nil, metrics, err
	}
	fail := func(err error) (*shadow.Workspace, *history.WorkspaceMetrics, error) {
		return nil, metrics, fmt.Errorf("shared cache: %w", errorsWithCleanup(err, ws.Cleanup()))
	}
	library := filepath.Join(ws.ShadowPath, "Library")
	copiedAt := time.Now()
	hit := false
	if !req.ClearCache {
		hit, err = store.Restore(ctx, req.Config.ProjectPath, key, library)
		if err != nil {
			return fail(err)
		}
	}
	if !hit {
		if err = os.Mkdir(library, 0700); err != nil {
			return fail(err)
		}
	}
	ws.Metrics.LibraryMaterialize = time.Since(copiedAt)
	ws.CacheWriteBack = func(ctx context.Context) error {
		current, err := contentcache.Key(req.Config.ProjectPath, req.Config.UnityPath)
		if err != nil {
			return err
		}
		if current != key {
			return fmt.Errorf("project compatibility changed during run; cache preserved")
		}
		if err := store.Publish(ctx, req.Config.ProjectPath, key, library); err != nil {
			return err
		}
		usage, err := shadow.MeasureDirectoryUsage(store.Root)
		if err != nil {
			return fmt.Errorf("cache published; peak measurement failed: %w", err)
		}
		ws.Metrics.LegacyCacheWritePeakPhysicalBytes = usage.AllocatedBytes
		if _, err := store.Inspect(ctx, true); err != nil {
			return fmt.Errorf("cache published; unreferenced content cleanup deferred: %w", err)
		}
		return nil
	}
	metrics.WorkspacePreparationMs = time.Since(started).Milliseconds()
	metrics.FileCopyMs = (ws.Metrics.AssetsCopy + ws.Metrics.ProjectSettingsCopy + ws.Metrics.PackagesCopy).Milliseconds()
	metrics.LibraryMaterializationMs = ws.Metrics.LibraryMaterialize.Milliseconds()
	return ws, metrics, nil
}
func errorsWithCleanup(primary, cleanup error) error {
	if cleanup != nil {
		return fmt.Errorf("%w; cleanup: %v", primary, cleanup)
	}
	return primary
}

package runsvc

import (
	"context"
	"errors"
	"fmt"
	"io"
	"time"

	"github.com/Kubonsang/testplay-runner/internal/history"
	"github.com/Kubonsang/testplay-runner/internal/shadow"
)

type ReleasePolicy struct{ Keep bool }

const workspaceReleaseTimeout = 60 * time.Second

// WorkspaceProvider owns preparation up to a ready Unity project. Its lease
// owns cleanup ordering, so a mounted child is always released before the
// physical workspace shell can be removed.
type WorkspaceProvider interface {
	Prepare(context.Context, Request, string, io.Writer, io.Writer) (WorkspaceLease, error)
}

type WorkspaceLease interface {
	ProjectPath() string
	Metrics() *history.WorkspaceMetrics
	Release(context.Context, ReleasePolicy) error
}

type serviceWorkspaceProvider struct {
	service *Service
	backend string
}

type serviceWorkspaceLease struct {
	workspace *shadow.Workspace
	metrics   *history.WorkspaceMetrics
	vhdx      *preparedVHDXWorkspace
	released  bool
}

func (provider serviceWorkspaceProvider) Prepare(ctx context.Context, request Request, runID string, stdout, stderr io.Writer) (WorkspaceLease, error) {
	var workspace *shadow.Workspace
	var metrics *history.WorkspaceMetrics
	var vhdx *preparedVHDXWorkspace
	var err error
	switch provider.backend {
	case WorkspaceBackendLegacy:
		workspace, metrics, err = provider.service.prepareLegacyWorkspace(ctx, request, runID)
	case WorkspaceBackendImage:
		workspace, metrics, err = provider.service.prepareImageWorkspace(ctx, request, runID, stdout, stderr)
	case WorkspaceBackendVHDXDiff:
		workspace, metrics, vhdx, err = provider.service.prepareVHDXWorkspace(ctx, request, runID, stdout, stderr)
	case WorkspaceBackendAuto:
		workspace, metrics, vhdx, err = provider.service.prepareVHDXWorkspace(ctx, request, runID, stdout, stderr)
		if err != nil && errors.Is(err, errVHDXPreStartUnavailable) {
			fallbackReason := err.Error()
			workspace, metrics, err = provider.service.prepareLegacyWorkspace(ctx, request, runID)
			if metrics != nil {
				metrics.FallbackUsed = true
				metrics.FallbackReason = fallbackReason
			}
			vhdx = nil
		}
	default:
		err = fmt.Errorf("unknown workspace provider %q", provider.backend)
	}
	if err != nil {
		return nil, err
	}
	return &serviceWorkspaceLease{workspace: workspace, metrics: metrics, vhdx: vhdx}, nil
}

func (lease *serviceWorkspaceLease) ProjectPath() string                { return lease.workspace.ShadowPath }
func (lease *serviceWorkspaceLease) Metrics() *history.WorkspaceMetrics { return lease.metrics }
func (lease *serviceWorkspaceLease) Release(ctx context.Context, policy ReleasePolicy) error {
	if lease == nil || lease.released {
		return nil
	}
	if lease.vhdx != nil {
		if err := lease.vhdx.Release(ctx, policy.Keep); err != nil {
			return err
		}
	}
	if policy.Keep {
		lease.released = true
		return nil
	}
	if err := lease.workspace.Cleanup(); err != nil {
		return err
	}
	lease.released = true
	return nil
}

func (s *Service) workspaceProvider(backend string) WorkspaceProvider {
	return serviceWorkspaceProvider{service: s, backend: backend}
}

func releaseWorkspaceLeaseBounded(lease WorkspaceLease, policy ReleasePolicy) error {
	if lease == nil {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), workspaceReleaseTimeout)
	defer cancel()
	return lease.Release(ctx, policy)
}

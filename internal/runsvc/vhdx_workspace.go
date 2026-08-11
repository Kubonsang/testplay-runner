package runsvc

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/Kubonsang/testplay-runner/internal/config"
	"github.com/Kubonsang/testplay-runner/internal/history"
	"github.com/Kubonsang/testplay-runner/internal/shadow"
	"github.com/Kubonsang/testplay-runner/internal/unity"
	"github.com/Kubonsang/testplay-runner/internal/vhdxworkspace"
)

var errVHDXPreStartUnavailable = errors.New("vhdx-diff unavailable before workspace start")
var vhdxMonitorInterval = 5 * time.Second

type preparedVHDXWorkspace struct {
	client  vhdxworkspace.Client
	leaseID string
	runID   string
	metrics *history.WorkspaceMetrics
	mu      sync.Mutex
}

func (lease *preparedVHDXWorkspace) SetUnityPID(ctx context.Context, pid int) {
	if lease == nil || lease.leaseID == "" || pid <= 0 {
		return
	}
	request := vhdxworkspace.NewRequest(vhdxworkspace.OperationHeartbeat, fmt.Sprintf("unitypid-%s-%d", lease.runID, pid))
	request.LeaseID, request.ClientPID, request.UnityPID = lease.leaseID, os.Getpid(), pid
	response, _ := lease.client.Call(ctx, request)
	lease.apply(response.Metrics)
}

func (lease *preparedVHDXWorkspace) apply(metrics *vhdxworkspace.Metrics) {
	lease.mu.Lock()
	defer lease.mu.Unlock()
	applyVHDXMetrics(lease.metrics, metrics)
}

func (lease *preparedVHDXWorkspace) Monitor(parent context.Context) (context.Context, func() error) {
	ctx, cancel := context.WithCancelCause(parent)
	done := make(chan struct{})
	var monitorErr error
	go func() {
		defer close(done)
		ticker := time.NewTicker(vhdxMonitorInterval)
		defer ticker.Stop()
		failures, sequence := 0, 0
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				sequence++
				heartbeat := vhdxworkspace.NewRequest(vhdxworkspace.OperationHeartbeat, fmt.Sprintf("heartbeat-%s-%d", lease.runID, sequence))
				heartbeat.LeaseID, heartbeat.ClientPID = lease.leaseID, os.Getpid()
				response, err := lease.client.Call(ctx, heartbeat)
				lease.apply(response.Metrics)
				if err != nil {
					failures++
					if failures >= 3 {
						monitorErr = fmt.Errorf("broker heartbeat failed: %w", err)
						cancel(monitorErr)
						return
					}
					continue
				}
				failures = 0
				status := vhdxworkspace.NewRequest(vhdxworkspace.OperationStatus, fmt.Sprintf("capacity-%s-%d", lease.runID, sequence))
				statusResponse, statusErr := lease.client.Call(ctx, status)
				if statusErr != nil {
					continue
				}
				if statusResponse.Status != nil {
					capacity := statusResponse.Status.Capacity
					lease.mu.Lock()
					lease.metrics.StoreAllocatedBytes = capacity.AllocatedBytes
					lease.metrics.HostFreeBytes = capacity.HostFreeBytes
					lease.mu.Unlock()
					if capacity.HostFreeBytes <= vhdxworkspace.SafetyHostFloor {
						monitorErr = fmt.Errorf("host free safety floor reached: free=%d floor=%d", capacity.HostFreeBytes, vhdxworkspace.SafetyHostFloor)
						cancel(monitorErr)
						return
					}
				}
			}
		}
	}()
	return ctx, func() error {
		cancel(nil)
		<-done
		return monitorErr
	}
}

func (lease *preparedVHDXWorkspace) Release(ctx context.Context, retain bool) error {
	if lease == nil || lease.leaseID == "" {
		return nil
	}
	request := vhdxworkspace.NewRequest(vhdxworkspace.OperationRelease, "release-"+lease.runID)
	request.LeaseID = lease.leaseID
	request.RetainChild = retain
	response, err := lease.client.Call(ctx, request)
	lease.apply(response.Metrics)
	if err != nil {
		lease.mu.Lock()
		lease.metrics.CleanupState = vhdxworkspace.CleanupUncertain
		lease.mu.Unlock()
		return err
	}
	lease.mu.Lock()
	if retain {
		lease.metrics.CleanupState = vhdxworkspace.CleanupRetained
	} else {
		lease.metrics.CleanupState = vhdxworkspace.CleanupReleased
	}
	lease.mu.Unlock()
	lease.leaseID = ""
	return nil
}

func (s *Service) selectedWorkspaceBroker() vhdxworkspace.Client {
	if s.WorkspaceBroker != nil {
		return s.WorkspaceBroker
	}
	return vhdxworkspace.DefaultClient()
}

func (s *Service) prepareVHDXWorkspace(ctx context.Context, req Request, runID string, stdout, stderr io.Writer) (*shadow.Workspace, *history.WorkspaceMetrics, *preparedVHDXWorkspace, error) {
	metrics := prepareMetrics(WorkspaceBackendVHDXDiff)
	metrics.Provider = vhdxworkspace.Provider
	client := s.selectedWorkspaceBroker()
	hello := vhdxworkspace.NewRequest(vhdxworkspace.OperationHello, "hello-"+runID)
	response, err := client.Call(ctx, hello)
	if err != nil || !response.OK || response.Provider != vhdxworkspace.Provider || !filepath.IsAbs(response.WorkspaceRoot) {
		return nil, metrics, nil, fmt.Errorf("%w: broker hello: %v", errVHDXPreStartUnavailable, errors.Join(err, response.Error))
	}
	if req.WorkspaceStoreRoot != "" && !sameCleanPath(req.WorkspaceStoreRoot, response.StoreRoot) {
		return nil, metrics, nil, fmt.Errorf("%w: requested store root does not match installed broker root", errVHDXPreStartUnavailable)
	}
	statusRequest := vhdxworkspace.NewRequest(vhdxworkspace.OperationAdmit, "admit-"+runID)
	applyWorkspaceCapacityRequest(req.Config, &statusRequest)
	if _, statusErr := client.Call(ctx, statusRequest); statusErr != nil {
		return nil, metrics, nil, fmt.Errorf("%w: admission: %v", errVHDXPreStartUnavailable, statusErr)
	}

	var configuredPackages map[string]string
	if req.Config.Workspace != nil {
		configuredPackages = req.Config.Workspace.LocalPackageOverrides
	}
	localPackages, localPackagesDigest, err := vhdxworkspace.ResolveLocalPackageOverrides(configuredPackages)
	if err != nil {
		return nil, metrics, nil, err
	}
	metrics.LocalPackageOverrideCount = len(localPackages)
	metrics.LocalPackagesDigest = localPackagesDigest
	key, err := vhdxworkspace.ComputeCompatibilityKeyWithLocalPackages(req.Config.ProjectPath, req.Config.UnityPath, localPackagesDigest)
	if err != nil {
		return nil, metrics, nil, err
	}
	snapshot, err := vhdxworkspace.ComputeSourceSnapshotWithLocalPackages(req.Config.ProjectPath, localPackagesDigest)
	if err != nil {
		return nil, metrics, nil, err
	}
	metrics.ParentKey = key.Digest
	preparationStarted := time.Now()
	ws, err := shadow.Prepare(ctx, req.Config.ProjectPath, runID, shadow.PrepareOptions{WorkspaceRoot: response.WorkspaceRoot, CopyPackages: true, SkipLibrary: true})
	if err != nil {
		return nil, metrics, nil, err
	}
	cleanupWorkspace := true
	defer func() {
		if cleanupWorkspace {
			_ = ws.Cleanup()
		}
	}()
	if err := vhdxworkspace.ApplyLocalPackageOverrides(ctx, ws.ShadowPath, localPackages); err != nil {
		return nil, metrics, nil, err
	}
	metrics.FileCopyMs = (ws.Metrics.AssetsCopy + ws.Metrics.ProjectSettingsCopy + ws.Metrics.PackagesCopy).Milliseconds()

	var parentResponse vhdxworkspace.Response
	for attempt := 1; ; attempt++ {
		begin := vhdxworkspace.NewRequest(vhdxworkspace.OperationBeginParentBuild, fmt.Sprintf("parent-%s-%d", runID, attempt))
		begin.ParentKey, begin.Source, begin.WorkspaceID = &key, &snapshot, runID
		begin.ClientPID = os.Getpid()
		parentResponse, err = client.Call(ctx, begin)
		if err != nil {
			return nil, metrics, nil, err
		}
		applyVHDXMetrics(metrics, parentResponse.Metrics)
		if parentResponse.Parent != nil {
			break
		}
		if parentResponse.ParentBuild == nil {
			return nil, metrics, nil, fmt.Errorf("vhdx-diff broker returned neither a parent nor a parent transaction")
		}
		if parentResponse.ParentBuild.State == "waiting" {
			select {
			case <-ctx.Done():
				return nil, metrics, nil, ctx.Err()
			case <-time.After(250 * time.Millisecond):
				continue
			}
		}
		if parentResponse.ParentBuild.State != "mounted" || parentResponse.ParentBuild.TransactionID == "" {
			return nil, metrics, nil, fmt.Errorf("unexpected parent transaction state %q", parentResponse.ParentBuild.State)
		}
		transactionID := parentResponse.ParentBuild.TransactionID
		abort := func() {
			abortCtx, cancel := context.WithTimeout(context.Background(), workspaceReleaseTimeout)
			defer cancel()
			request := vhdxworkspace.NewRequest(vhdxworkspace.OperationAbortParent, "abort-"+runID)
			request.TransactionID = transactionID
			_, _ = client.Call(abortCtx, request)
		}
		args := append(unity.BuildCompileArgs(ws.ShadowPath), "-disable-assembly-updater")
		builderStarted := time.Now()
		var builderStderr bytes.Buffer
		stderrWriter := io.Writer(&builderStderr)
		if stderr != nil {
			stderrWriter = io.MultiWriter(stderr, &builderStderr)
		}
		exitCode, runErr := s.Runner.Run(ctx, args, stdout, stderrWriter)
		metrics.ParentBuildMs += time.Since(builderStarted).Milliseconds()
		if runErr != nil || exitCode != 0 || ctx.Err() != nil {
			abort()
			return nil, metrics, nil, fmt.Errorf("build VHDX parent: exit=%d: %w: %s", exitCode, errors.Join(runErr, ctx.Err()), tailText(builderStderr.String(), 4000))
		}
		commit := vhdxworkspace.NewRequest(vhdxworkspace.OperationCommitParent, "commit-"+runID)
		commit.TransactionID = transactionID
		parentResponse, err = client.Call(ctx, commit)
		if err != nil {
			abort()
			return nil, metrics, nil, err
		}
		applyVHDXMetrics(metrics, parentResponse.Metrics)
		break
	}
	if parentResponse.Parent == nil {
		return nil, metrics, nil, fmt.Errorf("vhdx-diff broker returned no committed parent")
	}
	metrics.ParentPath = parentResponse.Parent.VHDXPath
	acquire := vhdxworkspace.NewRequest(vhdxworkspace.OperationAcquire, "acquire-"+runID)
	acquire.ParentKey, acquire.RunID, acquire.WorkspaceID, acquire.ClientPID = &key, runID, runID, os.Getpid()
	applyWorkspaceCapacityRequest(req.Config, &acquire)
	leaseResponse, err := client.Call(ctx, acquire)
	if err != nil {
		return nil, metrics, nil, err
	}
	if leaseResponse.Lease == nil {
		return nil, metrics, nil, fmt.Errorf("vhdx-diff broker returned no lease")
	}
	applyVHDXMetrics(metrics, leaseResponse.Metrics)
	metrics.MountPath, metrics.PhysicalDiskPath, metrics.VolumeGUID = leaseResponse.Lease.MountPath, leaseResponse.Lease.PhysicalPath, leaseResponse.Lease.VolumeGUID
	metrics.WorkspacePreparationMs = time.Since(preparationStarted).Milliseconds()
	metrics.LibraryMaterializationMs = metrics.ChildCreateMs + metrics.ChildAttachMs + metrics.ChildMountMs
	cleanupWorkspace = false
	return ws, metrics, &preparedVHDXWorkspace{client: client, leaseID: leaseResponse.Lease.LeaseID, runID: runID, metrics: metrics}, nil
}

func applyWorkspaceCapacityRequest(config *config.Config, request *vhdxworkspace.Request) {
	if config == nil || config.Workspace == nil {
		return
	}
	request.StoreMaxAllocatedBytes = config.Workspace.StoreMaxAllocatedBytes
	request.MinimumHostFreeBytes = config.Workspace.MinimumHostFreeBytes
}

func applyVHDXMetrics(target *history.WorkspaceMetrics, source *vhdxworkspace.Metrics) {
	if target == nil || source == nil {
		return
	}
	if source.ParentStatus != "" {
		target.ParentStatus = source.ParentStatus
	}
	target.ParentCreated = target.ParentCreated || source.ParentCreated
	target.ParentReused = target.ParentReused || source.ParentReused
	target.ParentBuildMs += source.ParentBuildMs
	target.ParentVerifyMs += source.ParentVerifyMs
	if source.ParentVirtualBytes != 0 {
		target.ParentVirtualBytes = source.ParentVirtualBytes
	}
	if source.ParentAllocatedBytes != 0 {
		target.ParentAllocatedBytes = source.ParentAllocatedBytes
	}
	if source.ChildCreateMs != 0 {
		target.ChildCreateMs = source.ChildCreateMs
	}
	if source.ChildAttachMs != 0 {
		target.ChildAttachMs = source.ChildAttachMs
	}
	if source.ChildMountMs != 0 {
		target.ChildMountMs = source.ChildMountMs
	}
	if source.ChildReleaseMs != 0 {
		target.ChildReleaseMs = source.ChildReleaseMs
	}
	if source.ChildReadyMeasured || source.ChildReadyBytes != 0 {
		target.ChildReadyAllocatedBytes = source.ChildReadyBytes
		target.ChildReadyAllocatedMeasured = true
	}
	if source.ChildPeakMeasured || source.ChildPeakBytes != 0 {
		if !target.ChildPeakAllocatedMeasured || source.ChildPeakBytes > target.ChildPeakAllocatedBytes {
			target.ChildPeakAllocatedBytes = source.ChildPeakBytes
		}
		target.ChildPeakAllocatedMeasured = true
	}
	if source.ChildReleasedMeasured || source.ChildReleasedBytes != 0 {
		target.ChildReleasedAllocatedBytes = source.ChildReleasedBytes
		target.ChildReleasedAllocatedMeasured = true
	}
	if source.CleanupState != "" {
		target.CleanupState = source.CleanupState
	}
	if source.Capacity.QuotaBytes != 0 {
		target.StoreQuotaBytes = source.Capacity.QuotaBytes
	}
	if source.Capacity.AllocatedBytes != 0 {
		target.StoreAllocatedBytes = source.Capacity.AllocatedBytes
	}
	if source.Capacity.HostFreeBytes != 0 {
		target.HostFreeBytes = source.Capacity.HostFreeBytes
	}
	if source.Capacity.HostFloorBytes != 0 {
		target.HostFreeFloorBytes = source.Capacity.HostFloorBytes
	}
}

func sameCleanPath(left, right string) bool {
	leftAbs, leftErr := filepath.Abs(left)
	rightAbs, rightErr := filepath.Abs(right)
	return leftErr == nil && rightErr == nil && strings.EqualFold(filepath.Clean(leftAbs), filepath.Clean(rightAbs))
}

func measureVHDXWorkspaceShell(root string) (shadow.DirectoryUsage, error) {
	var total shadow.DirectoryUsage
	for _, name := range []string{"Assets", "Packages", "ProjectSettings"} {
		usage, err := shadow.MeasureDirectoryUsage(filepath.Join(root, name))
		if err != nil {
			return total, err
		}
		total.LogicalBytes += usage.LogicalBytes
		total.AllocatedBytes += usage.AllocatedBytes
	}
	return total, nil
}

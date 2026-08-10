package runsvc

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/Kubonsang/testplay-runner/internal/config"
	"github.com/Kubonsang/testplay-runner/internal/history"
	"github.com/Kubonsang/testplay-runner/internal/shadow"
	"github.com/Kubonsang/testplay-runner/internal/vhdxworkspace"
)

type brokerClientFunc func(context.Context, vhdxworkspace.Request) (vhdxworkspace.Response, error)

func (fn brokerClientFunc) Call(ctx context.Context, request vhdxworkspace.Request) (vhdxworkspace.Response, error) {
	return fn(ctx, request)
}

func TestVHDXAutoFallbackBoundaryIsOnlyHelloOrAdmission(t *testing.T) {
	service := Service{WorkspaceBroker: brokerClientFunc(func(context.Context, vhdxworkspace.Request) (vhdxworkspace.Response, error) {
		return vhdxworkspace.Response{}, vhdxworkspace.ErrBrokerUnavailable
	})}
	_, _, _, err := service.prepareVHDXWorkspace(context.Background(), Request{Config: &config.Config{ProjectPath: t.TempDir(), UnityPath: "unity"}}, "run-prestart", nil, nil)
	if !errors.Is(err, errVHDXPreStartUnavailable) {
		t.Fatalf("error=%v", err)
	}

	project := makeVHDXKeyProject(t)
	workspaceRoot := filepath.Join(t.TempDir(), "workspaces")
	if err := os.Mkdir(workspaceRoot, 0700); err != nil {
		t.Fatal(err)
	}
	started := Service{WorkspaceBroker: brokerClientFunc(func(_ context.Context, request vhdxworkspace.Request) (vhdxworkspace.Response, error) {
		switch request.Operation {
		case vhdxworkspace.OperationHello:
			return vhdxworkspace.Response{OK: true, Provider: vhdxworkspace.Provider, WorkspaceRoot: workspaceRoot}, nil
		case vhdxworkspace.OperationAdmit:
			return vhdxworkspace.Response{OK: true}, nil
		default:
			return vhdxworkspace.Response{}, errors.New("parent creation failed")
		}
	})}
	_, _, _, err = started.prepareVHDXWorkspace(context.Background(), Request{Config: &config.Config{ProjectPath: project, UnityPath: "unity"}}, "run-started", nil, nil)
	if err == nil || errors.Is(err, errVHDXPreStartUnavailable) {
		t.Fatalf("post-start error was fallback eligible: %v", err)
	}
}

func TestVHDXMonitorCancelsAtHostSafetyFloor(t *testing.T) {
	previous := vhdxMonitorInterval
	vhdxMonitorInterval = time.Millisecond
	defer func() { vhdxMonitorInterval = previous }()
	client := brokerClientFunc(func(_ context.Context, request vhdxworkspace.Request) (vhdxworkspace.Response, error) {
		if request.Operation == vhdxworkspace.OperationStatus {
			return vhdxworkspace.Response{OK: true, Status: &vhdxworkspace.Status{Capacity: vhdxworkspace.Capacity{HostFreeBytes: vhdxworkspace.SafetyHostFloor}}}, nil
		}
		return vhdxworkspace.Response{OK: true, Metrics: &vhdxworkspace.Metrics{ChildPeakBytes: 123}}, nil
	})
	metrics := &history.WorkspaceMetrics{}
	lease := preparedVHDXWorkspace{client: client, leaseID: "lease", runID: "run", metrics: metrics}
	ctx, stop := lease.Monitor(context.Background())
	select {
	case <-ctx.Done():
	case <-time.After(time.Second):
		t.Fatal("monitor did not cancel")
	}
	if err := stop(); err == nil {
		t.Fatal("monitor error missing")
	}
	if metrics.ChildPeakAllocatedBytes != 123 {
		t.Fatalf("peak=%d", metrics.ChildPeakAllocatedBytes)
	}
}

func TestWorkspaceLeaseReleasesChildBeforeRemovingShell(t *testing.T) {
	root := filepath.Join(t.TempDir(), "workspace")
	if err := os.Mkdir(root, 0700); err != nil {
		t.Fatal(err)
	}
	childReleased := false
	client := brokerClientFunc(func(_ context.Context, request vhdxworkspace.Request) (vhdxworkspace.Response, error) {
		if request.Operation != vhdxworkspace.OperationRelease {
			t.Fatalf("operation=%s", request.Operation)
		}
		if _, err := os.Stat(root); err != nil {
			t.Fatalf("workspace removed before child release: %v", err)
		}
		childReleased = true
		return vhdxworkspace.Response{OK: true, Metrics: &vhdxworkspace.Metrics{CleanupState: vhdxworkspace.CleanupReleased}}, nil
	})
	metrics := &history.WorkspaceMetrics{WorkspaceBackend: WorkspaceBackendVHDXDiff}
	lease := &serviceWorkspaceLease{workspace: &shadow.Workspace{ShadowPath: root}, metrics: metrics, vhdx: &preparedVHDXWorkspace{client: client, leaseID: "lease", runID: "run", metrics: metrics}}
	if err := lease.Release(context.Background(), ReleasePolicy{}); err != nil {
		t.Fatal(err)
	}
	if !childReleased {
		t.Fatal("child release not called")
	}
	if _, err := os.Stat(root); !os.IsNotExist(err) {
		t.Fatalf("workspace residual err=%v", err)
	}
}

type cleanupContextLease struct {
	errAtCall context.Context
	deadline  time.Time
	hasLimit  bool
}

func (*cleanupContextLease) ProjectPath() string                { return "" }
func (*cleanupContextLease) Metrics() *history.WorkspaceMetrics { return nil }
func (lease *cleanupContextLease) Release(ctx context.Context, _ ReleasePolicy) error {
	lease.errAtCall = context.WithoutCancel(ctx)
	lease.deadline, lease.hasLimit = ctx.Deadline()
	return ctx.Err()
}

func TestWorkspaceCleanupUsesFreshBoundedContext(t *testing.T) {
	lease := &cleanupContextLease{}
	if err := releaseWorkspaceLeaseBounded(lease, ReleasePolicy{}); err != nil {
		t.Fatal(err)
	}
	if lease.errAtCall == nil {
		t.Fatal("cleanup context was not observed")
	}
	if !lease.hasLimit {
		t.Fatal("cleanup context has no deadline")
	}
	remaining := time.Until(lease.deadline)
	if remaining <= 0 || remaining > workspaceReleaseTimeout {
		t.Fatalf("cleanup deadline remaining=%s", remaining)
	}
}

func makeVHDXKeyProject(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	for _, dir := range []string{"Assets", "Packages", "ProjectSettings"} {
		if err := os.Mkdir(filepath.Join(root, dir), 0700); err != nil {
			t.Fatal(err)
		}
	}
	files := map[string]string{"Assets/a": "a", "Packages/manifest.json": "{}", "Packages/packages-lock.json": "{}", "ProjectSettings/ProjectVersion.txt": "m_EditorVersion: 6000.3.8f1", "ProjectSettings/ProjectSettings.asset": "scriptingBackend: 0"}
	for path, value := range files {
		if err := os.WriteFile(filepath.Join(root, filepath.FromSlash(path)), []byte(value), 0600); err != nil {
			t.Fatal(err)
		}
	}
	return root
}

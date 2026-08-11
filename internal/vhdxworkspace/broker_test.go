package vhdxworkspace

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"
)

type fakeNative struct {
	mu          sync.Mutex
	events      []string
	hostFree    int64
	acquiring   int
	maxAcquire  int
	acquireGate chan struct{}
	verifyErr   error
	attachErr   error
	bootSession string
	livePIDs    map[int]bool
}

func (native *fakeNative) event(value string) {
	native.mu.Lock()
	defer native.mu.Unlock()
	native.events = append(native.events, value)
}
func (*fakeNative) Platform() string                { return "fake-windows" }
func (*fakeNative) Available(context.Context) error { return nil }
func (native *fakeNative) BootSessionID() string {
	if native.bootSession == "" {
		return "boot-1"
	}
	return native.bootSession
}
func (native *fakeNative) ProcessAlive(pid int) bool {
	return native.livePIDs != nil && native.livePIDs[pid]
}
func (native *fakeNative) VerifyParent(context.Context, ParentMetadata) error {
	return native.verifyErr
}
func (native *fakeNative) HostFreeBytes(string) (int64, error) {
	if native.hostFree == 0 {
		return 100 << 30, nil
	}
	return native.hostFree, nil
}
func (native *fakeNative) BeginParent(_ context.Context, pending *PendingParent) (ParentSession, error) {
	native.event("parent-create")
	if err := os.WriteFile(pending.StagingPath, []byte("fake-parent"), 0600); err != nil {
		return nil, err
	}
	return &fakeParentSession{native: native, pending: pending}, nil
}
func (native *fakeNative) AcquireChild(_ context.Context, parent ParentMetadata, journal LeaseJournal, transition func(string, string, string) error) (ChildSession, Metrics, error) {
	native.mu.Lock()
	native.acquiring++
	if native.acquiring > native.maxAcquire {
		native.maxAcquire = native.acquiring
	}
	native.mu.Unlock()
	if native.acquireGate != nil {
		<-native.acquireGate
	}
	defer func() { native.mu.Lock(); native.acquiring--; native.mu.Unlock() }()
	if err := os.WriteFile(journal.ChildPath, []byte("child"), 0600); err != nil {
		return nil, Metrics{}, err
	}
	if err := os.Mkdir(journal.MountPath, 0700); err != nil {
		return nil, Metrics{}, err
	}
	if transition != nil {
		_ = transition("ready", `\\.\PhysicalDrive99`, `\\?\Volume{fake}\`)
	}
	lease := Lease{LeaseID: journal.LeaseID, RunID: journal.RunID, ParentKey: parent.CompatibilityKey.Digest, MountPath: journal.MountPath, State: "ready", CreatedAt: time.Now(), PhysicalPath: `\\.\PhysicalDrive99`, VolumeGUID: `\\?\Volume{fake}\`}
	return &fakeChildSession{lease: lease, childPath: journal.ChildPath, identity: FileIdentity{FileID: "fake:" + journal.LeaseID}}, Metrics{ChildCreateMs: 1, ChildAttachMs: 2, ChildMountMs: 3, ChildReadyBytes: 5}, nil
}
func (native *fakeNative) AttachChild(_ context.Context, parent ParentMetadata, journal LeaseJournal) (ChildSession, Metrics, error) {
	if native.attachErr != nil {
		return nil, Metrics{}, native.attachErr
	}
	if _, err := os.Stat(journal.ChildPath); err != nil {
		return nil, Metrics{}, err
	}
	if err := os.Mkdir(journal.MountPath, 0700); err != nil && !os.IsExist(err) {
		return nil, Metrics{}, err
	}
	lease := Lease{LeaseID: journal.LeaseID, RunID: journal.RunID, ParentKey: parent.CompatibilityKey.Digest, MountPath: journal.MountPath, State: "ready", CreatedAt: journal.CreatedAt, Retained: true}
	return &fakeChildSession{lease: lease, childPath: journal.ChildPath, identity: journal.FileIdentity}, Metrics{ChildAttachMs: 1, ChildMountMs: 1}, nil
}

func TestRecoverRecordsAttachFailureEvidence(t *testing.T) {
	native := &fakeNative{}
	broker, key, workspaces := testBroker(t, native)
	commitTestParent(t, broker, key, workspaces)
	workspace := filepath.Join(workspaces, "attach-failure")
	if err := os.Mkdir(workspace, 0700); err != nil {
		t.Fatal(err)
	}
	acquire := request(OperationAcquire, "acquire-attach-failure")
	acquire.ParentKey = &key
	acquire.RunID = "attach-failure"
	acquire.WorkspaceID = "attach-failure"
	ready := broker.Handle(context.Background(), "S-1-5-21-test", acquire)
	if !ready.OK {
		t.Fatalf("acquire=%+v", ready)
	}
	native.attachErr = fmt.Errorf("stale mount target mismatch")
	restarted, err := NewBroker(broker.config, native)
	if err != nil {
		t.Fatal(err)
	}
	restarted.now = func() time.Time { return time.Now().Add(time.Minute) }
	summary, err := restarted.Recover(context.Background(), 30*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if summary.Quarantined != 1 || summary.Released != 0 {
		t.Fatalf("summary=%+v", summary)
	}
	journal, err := restarted.store.ReadLease(ready.Lease.LeaseID)
	if err != nil {
		t.Fatal(err)
	}
	if journal.State != "quarantined" || journal.RecoveryAt == nil || !strings.Contains(journal.RecoveryError, "stale mount target mismatch") {
		t.Fatalf("journal=%+v", journal)
	}
}

type fakeParentSession struct {
	native  *fakeNative
	pending *PendingParent
}

func (*fakeParentSession) Evidence() ParentEvidence { return ParentEvidence{} }
func (session *fakeParentSession) Finalize(context.Context) (ParentEvidence, error) {
	session.native.event("parent-finalize")
	allocated, err := fileAllocatedBytes(session.pending.StagingPath)
	if err != nil {
		return ParentEvidence{}, err
	}
	return ParentEvidence{FileIdentity: FileIdentity{FileID: "parent-id"}, Volume: VolumeIdentity{VirtualDiskID: "disk-id", VolumeGUID: `\\?\Volume{parent}\`, VolumeSerial: "1234", Filesystem: "NTFS", ClusterBytes: 4096}, LogicalBytes: 11, AllocatedBytes: allocated, SHA256: strings.Repeat("b", 64)}, nil
}
func (session *fakeParentSession) Abort(context.Context) error {
	session.native.event("parent-abort")
	return os.Remove(session.pending.StagingPath)
}

type fakeChildSession struct {
	lease     Lease
	childPath string
	identity  FileIdentity
	released  bool
}

func (session *fakeChildSession) Info() Lease                { return session.lease }
func (session *fakeChildSession) FileIdentity() FileIdentity { return session.identity }
func (session *fakeChildSession) Usage() (int64, error) {
	info, err := os.Stat(session.childPath)
	if err != nil {
		return 0, err
	}
	return info.Size(), nil
}
func (session *fakeChildSession) Release(_ context.Context, deleteChild bool) (Metrics, error) {
	if session.released {
		return Metrics{CleanupState: CleanupReleased}, nil
	}
	_ = os.Remove(session.lease.MountPath)
	if deleteChild {
		if err := os.Remove(session.childPath); err != nil && !os.IsNotExist(err) {
			return Metrics{}, err
		}
	}
	session.released = true
	state := CleanupRetained
	if deleteChild {
		state = CleanupReleased
	}
	return Metrics{CleanupState: state, ChildReleaseMs: 1}, nil
}

func testBroker(t *testing.T, native *fakeNative) (*Broker, CompatibilityKey, string) {
	t.Helper()
	root, workspaces := filepath.Join(t.TempDir(), "store"), filepath.Join(t.TempDir(), "workspaces")
	if err := os.MkdirAll(workspaces, 0700); err != nil {
		t.Fatal(err)
	}
	broker, err := NewBroker(BrokerConfig{StoreRoot: root, WorkspaceRoot: workspaces, UserSID: "S-1-5-21-test"}, native)
	if err != nil {
		t.Fatal(err)
	}
	key := CompatibilityKey{SchemaVersion: ParentSchemaVersion, Digest: strings.Repeat("a", 64), Provider: Provider, Filesystem: "NTFS", VirtualBytes: DefaultVirtualBytes, BlockBytes: DefaultBlockBytes, SectorBytes: DefaultSectorBytes}
	return broker, key, workspaces
}

func request(operation, id string) Request { return NewRequest(operation, id) }

func TestBrokerRejectsUnauthorizedCallerAndTraversal(t *testing.T) {
	broker, key, workspaces := testBroker(t, &fakeNative{})
	unauthorized := broker.Handle(context.Background(), "S-1-5-21-other", request(OperationHello, "unauthorized"))
	if unauthorized.OK || unauthorized.Error == nil || unauthorized.Error.Code != "unauthorized-client" {
		t.Fatalf("response=%+v", unauthorized)
	}
	claimed := request(OperationHello, "claimed-sid")
	claimed.UserSID = "S-1-5-18"
	claimedResponse := broker.Handle(context.Background(), "S-1-5-21-test", claimed)
	if claimedResponse.OK || claimedResponse.Error == nil || claimedResponse.Error.Code != "unauthorized-client" {
		t.Fatalf("claimed response=%+v", claimedResponse)
	}
	rootInjection := request(OperationHello, "root-injection")
	rootInjection.WorkspaceRoot = filepath.Join(workspaces, "escape")
	rootResponse := broker.Handle(context.Background(), "S-1-5-21-test", rootInjection)
	if rootResponse.OK || rootResponse.Error == nil || rootResponse.Error.Operation != "validate-client-workspace-root" {
		t.Fatalf("root response=%+v", rootResponse)
	}
	_ = os.Mkdir(filepath.Join(workspaces, "valid"), 0700)
	req := request(OperationBeginParentBuild, "traversal")
	req.ParentKey = &key
	req.Source = &SourceSnapshot{}
	req.WorkspaceID = "..\\escape"
	response := broker.Handle(context.Background(), "S-1-5-21-test", req)
	if response.OK || response.Error == nil || response.Error.Code != "invalid-workspace" || response.Error.Operation != "validate-workspace-id" {
		t.Fatalf("response=%+v", response)
	}
}

func TestBrokerRejectsPreexistingWorkspaceLibraryMount(t *testing.T) {
	broker, key, workspaces := testBroker(t, &fakeNative{})
	workspace := filepath.Join(workspaces, "occupied")
	if err := os.MkdirAll(filepath.Join(workspace, "Library"), 0700); err != nil {
		t.Fatal(err)
	}
	req := request(OperationBeginParentBuild, "occupied-mount")
	req.ParentKey = &key
	req.Source = &SourceSnapshot{}
	req.WorkspaceID = "occupied"
	response := broker.Handle(context.Background(), "S-1-5-21-test", req)
	if response.OK || response.Error == nil || response.Error.Operation != "validate-parent-mount" {
		t.Fatalf("response=%+v", response)
	}
}

func commitTestParent(t *testing.T, broker *Broker, key CompatibilityKey, workspaces string) {
	t.Helper()
	if err := os.Mkdir(filepath.Join(workspaces, "builder"), 0700); err != nil {
		t.Fatal(err)
	}
	begin := request(OperationBeginParentBuild, "begin-parent")
	begin.ParentKey = &key
	begin.Source = &SourceSnapshot{}
	begin.WorkspaceID = "builder"
	started := broker.Handle(context.Background(), "S-1-5-21-test", begin)
	if !started.OK || started.ParentBuild == nil {
		t.Fatalf("begin=%+v", started)
	}
	commit := request(OperationCommitParent, "commit-parent")
	commit.TransactionID = started.ParentBuild.TransactionID
	committed := broker.Handle(context.Background(), "S-1-5-21-test", commit)
	if !committed.OK || committed.Parent == nil || !committed.Parent.Immutable {
		t.Fatalf("commit=%+v", committed)
	}
}

func TestBrokerConcurrentChildrenAndExactRelease(t *testing.T) {
	native := &fakeNative{acquireGate: make(chan struct{})}
	broker, key, workspaces := testBroker(t, native)
	commitTestParent(t, broker, key, workspaces)
	for _, id := range []string{"run-a", "run-b"} {
		if err := os.Mkdir(filepath.Join(workspaces, id), 0700); err != nil {
			t.Fatal(err)
		}
	}
	responses := make(chan Response, 2)
	for _, id := range []string{"run-a", "run-b"} {
		go func(id string) {
			req := request(OperationAcquire, "acquire-"+id)
			req.ParentKey = &key
			req.RunID = id
			req.WorkspaceID = id
			responses <- broker.Handle(context.Background(), "S-1-5-21-test", req)
		}(id)
	}
	deadline := time.Now().Add(2 * time.Second)
	for {
		native.mu.Lock()
		overlap := native.maxAcquire == 2
		native.mu.Unlock()
		if overlap {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("acquires did not overlap")
		}
		time.Sleep(time.Millisecond)
	}
	close(native.acquireGate)
	for i := 0; i < 2; i++ {
		response := <-responses
		if !response.OK || response.Lease == nil {
			t.Fatalf("acquire=%+v", response)
		}
		release := request(OperationRelease, "release-"+response.Lease.RunID)
		release.LeaseID = response.Lease.LeaseID
		released := broker.Handle(context.Background(), "S-1-5-21-test", release)
		if !released.OK {
			t.Fatalf("release=%+v", released)
		}
	}
	status := broker.Handle(context.Background(), "S-1-5-21-test", request(OperationStatus, "status-zero"))
	if !status.OK || status.Status.ActiveChildCount != 0 {
		t.Fatalf("status=%+v", status)
	}
	for _, id := range []string{"run-a", "run-b"} {
		if _, err := os.Lstat(filepath.Join(workspaces, id)); !os.IsNotExist(err) {
			t.Fatalf("released workspace remains: id=%s err=%v", id, err)
		}
	}
}

func TestBrokerRetainedChildAttachAndRemove(t *testing.T) {
	native := &fakeNative{}
	broker, key, workspaces := testBroker(t, native)
	commitTestParent(t, broker, key, workspaces)
	if err := os.Mkdir(filepath.Join(workspaces, "retained-run"), 0700); err != nil {
		t.Fatal(err)
	}
	acquire := request(OperationAcquire, "acquire-retained")
	acquire.ParentKey = &key
	acquire.RunID = "retained-run"
	acquire.WorkspaceID = "retained-run"
	ready := broker.Handle(context.Background(), "S-1-5-21-test", acquire)
	if !ready.OK {
		t.Fatalf("acquire=%+v", ready)
	}
	release := request(OperationRelease, "retain-release")
	release.LeaseID = ready.Lease.LeaseID
	release.RetainChild = true
	if response := broker.Handle(context.Background(), "S-1-5-21-test", release); !response.OK {
		t.Fatalf("retain=%+v", response)
	}
	attach := request(OperationAttachRetained, "attach-retained")
	attach.RunID = "retained-run"
	attach.WorkspaceID = "retained-run"
	if response := broker.Handle(context.Background(), "S-1-5-21-test", attach); !response.OK {
		t.Fatalf("attach=%+v", response)
	}
	remove := request(OperationRemoveRetained, "remove-retained")
	remove.RunID = "retained-run"
	if response := broker.Handle(context.Background(), "S-1-5-21-test", remove); !response.OK {
		t.Fatalf("remove=%+v", response)
	}
	if _, err := os.Stat(filepath.Join(workspaces, "retained-run")); !os.IsNotExist(err) {
		t.Fatalf("retained workspace residual err=%v", err)
	}
}

func TestBrokerRestartRecoversOnlyExpiredEphemeralChild(t *testing.T) {
	native := &fakeNative{}
	broker, key, workspaces := testBroker(t, native)
	commitTestParent(t, broker, key, workspaces)
	if err := os.Mkdir(filepath.Join(workspaces, "orphan-run"), 0700); err != nil {
		t.Fatal(err)
	}
	acquire := request(OperationAcquire, "acquire-orphan")
	acquire.ParentKey = &key
	acquire.RunID = "orphan-run"
	acquire.WorkspaceID = "orphan-run"
	acquire.ClientPID = 12345
	ready := broker.Handle(context.Background(), "S-1-5-21-test", acquire)
	if !ready.OK {
		t.Fatalf("acquire=%+v", ready)
	}
	restarted, err := NewBroker(broker.config, native)
	if err != nil {
		t.Fatal(err)
	}
	restarted.now = func() time.Time { return time.Now().Add(time.Minute) }
	summary, err := restarted.Recover(context.Background(), 30*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if summary.Released != 1 || summary.Quarantined != 0 {
		t.Fatalf("summary=%+v", summary)
	}
	child, _ := restarted.store.paths.Child(ready.Lease.LeaseID)
	if _, err := os.Stat(child); !os.IsNotExist(err) {
		t.Fatalf("child residual err=%v", err)
	}
	if _, err := restarted.store.ReadLease(ready.Lease.LeaseID); !os.IsNotExist(err) {
		t.Fatalf("journal residual err=%v", err)
	}
	if _, err := os.Lstat(filepath.Join(workspaces, "orphan-run")); !os.IsNotExist(err) {
		t.Fatalf("workspace residual err=%v", err)
	}
}

func TestLeaseJournalRecordsBootSessionIdentity(t *testing.T) {
	native := &fakeNative{bootSession: "boot-recorded"}
	broker, key, workspaces := testBroker(t, native)
	commitTestParent(t, broker, key, workspaces)
	if err := os.Mkdir(filepath.Join(workspaces, "boot-recorded-run"), 0700); err != nil {
		t.Fatal(err)
	}
	acquire := request(OperationAcquire, "acquire-boot-recorded")
	acquire.ParentKey = &key
	acquire.RunID = "boot-recorded-run"
	acquire.WorkspaceID = "boot-recorded-run"
	ready := broker.Handle(context.Background(), "S-1-5-21-test", acquire)
	if !ready.OK {
		t.Fatalf("acquire=%+v", ready)
	}
	journal, err := broker.store.ReadLease(ready.Lease.LeaseID)
	if err != nil {
		t.Fatal(err)
	}
	if journal.BootSessionID != "boot-recorded" {
		t.Fatalf("bootSessionId=%q", journal.BootSessionID)
	}
	status := broker.Handle(context.Background(), "S-1-5-21-test", request(OperationStatus, "status-boot-recorded"))
	if !status.OK || status.Status == nil || status.Status.BootSessionID != "boot-recorded" {
		t.Fatalf("status=%+v", status)
	}
}

func TestRecoverPreservesLiveLeaseWithinSameBoot(t *testing.T) {
	native := &fakeNative{bootSession: "boot-same", livePIDs: map[int]bool{12345: true}}
	broker, key, workspaces := testBroker(t, native)
	commitTestParent(t, broker, key, workspaces)
	workspace := filepath.Join(workspaces, "same-boot-run")
	if err := os.Mkdir(workspace, 0700); err != nil {
		t.Fatal(err)
	}
	acquire := request(OperationAcquire, "acquire-same-boot")
	acquire.ParentKey = &key
	acquire.RunID = "same-boot-run"
	acquire.WorkspaceID = "same-boot-run"
	acquire.ClientPID = 12345
	ready := broker.Handle(context.Background(), "S-1-5-21-test", acquire)
	if !ready.OK {
		t.Fatalf("acquire=%+v", ready)
	}
	restarted, err := NewBroker(broker.config, native)
	if err != nil {
		t.Fatal(err)
	}
	restarted.now = func() time.Time { return time.Now().Add(time.Minute) }
	summary, err := restarted.Recover(context.Background(), 30*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if summary.Preserved != 1 || summary.Released != 0 {
		t.Fatalf("summary=%+v", summary)
	}
	if _, err := restarted.store.ReadLease(ready.Lease.LeaseID); err != nil {
		t.Fatalf("live lease was not preserved: %v", err)
	}
}

func TestRecoverIgnoresReusedPIDsAndGraceAfterBootChange(t *testing.T) {
	native := &fakeNative{bootSession: "boot-before", livePIDs: map[int]bool{12345: true}}
	broker, key, workspaces := testBroker(t, native)
	commitTestParent(t, broker, key, workspaces)
	workspace := filepath.Join(workspaces, "reboot-orphan-run")
	if err := os.Mkdir(workspace, 0700); err != nil {
		t.Fatal(err)
	}
	acquire := request(OperationAcquire, "acquire-reboot-orphan")
	acquire.ParentKey = &key
	acquire.RunID = "reboot-orphan-run"
	acquire.WorkspaceID = "reboot-orphan-run"
	acquire.ClientPID = 12345
	ready := broker.Handle(context.Background(), "S-1-5-21-test", acquire)
	if !ready.OK {
		t.Fatalf("acquire=%+v", ready)
	}

	// Simulate a reboot that immediately reuses the original PID. Recovery
	// must use the boot-session identity and must not preserve the orphan due
	// to either PID liveness or the normal 30-second grace period.
	native.bootSession = "boot-after"
	restarted, err := NewBroker(broker.config, native)
	if err != nil {
		t.Fatal(err)
	}
	summary, err := restarted.Recover(context.Background(), 30*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if summary.Released != 1 || summary.Preserved != 0 || summary.Quarantined != 0 {
		t.Fatalf("summary=%+v", summary)
	}
	if _, err := restarted.store.ReadLease(ready.Lease.LeaseID); !os.IsNotExist(err) {
		t.Fatalf("reboot orphan journal residual: %v", err)
	}
	if _, err := os.Lstat(workspace); !os.IsNotExist(err) {
		t.Fatalf("reboot orphan workspace residual: %v", err)
	}
}

func TestRecoverCompletesWhenChildIsGoneAndEmptyMountDirectoryRemains(t *testing.T) {
	native := &fakeNative{}
	broker, key, workspaces := testBroker(t, native)
	commitTestParent(t, broker, key, workspaces)
	workspace := filepath.Join(workspaces, "partial-release")
	if err := os.Mkdir(workspace, 0700); err != nil {
		t.Fatal(err)
	}
	acquire := request(OperationAcquire, "acquire-partial-release")
	acquire.ParentKey = &key
	acquire.RunID = "partial-release"
	acquire.WorkspaceID = "partial-release"
	ready := broker.Handle(context.Background(), "S-1-5-21-test", acquire)
	if !ready.OK {
		t.Fatalf("acquire=%+v", ready)
	}
	journal, err := broker.store.ReadLease(ready.Lease.LeaseID)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(journal.ChildPath); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(journal.MountPath); err != nil {
		t.Fatalf("expected partial empty mount directory: %v", err)
	}
	restarted, err := NewBroker(broker.config, native)
	if err != nil {
		t.Fatal(err)
	}
	restarted.now = func() time.Time { return time.Now().Add(time.Minute) }
	summary, err := restarted.Recover(context.Background(), 30*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if summary.Released != 1 || summary.Quarantined != 0 {
		t.Fatalf("summary=%+v", summary)
	}
	if _, err := os.Lstat(workspace); !os.IsNotExist(err) {
		t.Fatalf("partial workspace residual: %v", err)
	}
	if _, err := restarted.store.ReadLease(journal.LeaseID); !os.IsNotExist(err) {
		t.Fatalf("partial journal residual: %v", err)
	}
}

func TestRecoverQuarantinesTamperedWorkspaceOwner(t *testing.T) {
	native := &fakeNative{}
	broker, key, workspaces := testBroker(t, native)
	commitTestParent(t, broker, key, workspaces)
	workspace := filepath.Join(workspaces, "tampered-run")
	if err := os.Mkdir(workspace, 0700); err != nil {
		t.Fatal(err)
	}
	acquire := request(OperationAcquire, "acquire-tampered")
	acquire.ParentKey = &key
	acquire.RunID = "tampered-run"
	acquire.WorkspaceID = "tampered-run"
	acquire.ClientPID = 12345
	ready := broker.Handle(context.Background(), "S-1-5-21-test", acquire)
	if !ready.OK {
		t.Fatalf("acquire=%+v", ready)
	}
	journal, err := broker.store.ReadLease(ready.Lease.LeaseID)
	if err != nil {
		t.Fatal(err)
	}
	markerPath := filepath.Join(workspace, workspaceOwnerFile)
	var owner WorkspaceOwner
	if err := readJSON(markerPath, &owner); err != nil {
		t.Fatal(err)
	}
	owner.OwnershipToken = "tampered-token"
	if err := writeJSONDurable(markerPath, owner); err != nil {
		t.Fatal(err)
	}

	restarted, err := NewBroker(broker.config, native)
	if err != nil {
		t.Fatal(err)
	}
	restarted.now = func() time.Time { return time.Now().Add(time.Minute) }
	summary, err := restarted.Recover(context.Background(), 30*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if summary.Quarantined != 1 || summary.Released != 0 {
		t.Fatalf("summary=%+v", summary)
	}
	if _, err := os.Lstat(workspace); err != nil {
		t.Fatalf("tampered workspace was removed: %v", err)
	}
	preserved, err := restarted.store.ReadLease(journal.LeaseID)
	if err != nil || preserved.State != "quarantined" {
		t.Fatalf("journal=%+v err=%v", preserved, err)
	}
	status := restarted.Handle(context.Background(), "S-1-5-21-test", request(OperationStatus, "status-tampered"))
	if !status.OK || status.Status.QuarantineCount != 1 || !status.Status.ManualRecoveryRequired {
		t.Fatalf("status=%+v", status)
	}
}

func TestRecoverRejectsWorkspaceReparseEntry(t *testing.T) {
	native := &fakeNative{}
	broker, key, workspaces := testBroker(t, native)
	commitTestParent(t, broker, key, workspaces)
	workspace := filepath.Join(workspaces, "reparse-run")
	if err := os.Mkdir(workspace, 0700); err != nil {
		t.Fatal(err)
	}
	acquire := request(OperationAcquire, "acquire-reparse")
	acquire.ParentKey = &key
	acquire.RunID = "reparse-run"
	acquire.WorkspaceID = "reparse-run"
	acquire.ClientPID = 12345
	ready := broker.Handle(context.Background(), "S-1-5-21-test", acquire)
	if !ready.OK {
		t.Fatalf("acquire=%+v", ready)
	}
	target := t.TempDir()
	link := filepath.Join(workspace, "unsafe-link")
	if err := os.Symlink(target, link); err != nil {
		if runtime.GOOS != "windows" {
			t.Skipf("symlink privilege unavailable: %v", err)
		}
		if output, junctionErr := exec.Command("cmd.exe", "/c", "mklink", "/J", link, target).CombinedOutput(); junctionErr != nil {
			t.Fatalf("create junction fallback: %v: %s", junctionErr, output)
		}
	}
	restarted, err := NewBroker(broker.config, native)
	if err != nil {
		t.Fatal(err)
	}
	restarted.now = func() time.Time { return time.Now().Add(time.Minute) }
	summary, err := restarted.Recover(context.Background(), 30*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if summary.Quarantined != 1 || summary.Released != 0 {
		t.Fatalf("summary=%+v", summary)
	}
	if _, err := os.Lstat(workspace); err != nil {
		t.Fatalf("reparse workspace was removed: %v", err)
	}
}

func TestRecoverQuarantinesOnlyStaleOwnedPendingParent(t *testing.T) {
	native := &fakeNative{}
	broker, key, _ := testBroker(t, native)
	if err := broker.store.EnsureLayout(); err != nil {
		t.Fatal(err)
	}
	pending, err := broker.store.BeginParent(broker.config.UserSID, key, SourceSnapshot{}, filepath.Join(broker.config.WorkspaceRoot, "builder", "Library"), 0)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(pending.StagingPath, []byte("partial-vhdx"), 0600); err != nil {
		t.Fatal(err)
	}
	broker.now = func() time.Time { return pending.UpdatedAt.Add(time.Minute) }
	summary, err := broker.Recover(context.Background(), 30*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if summary.Quarantined != 1 {
		t.Fatalf("summary=%+v", summary)
	}
	if _, err := os.Stat(filepath.Join(broker.store.paths.Quarantine, pending.TransactionID+"-parent")); err != nil {
		t.Fatalf("quarantine missing: %v", err)
	}
}

func TestLRUDeletesOnlyExpiredInactiveParent(t *testing.T) {
	native := &fakeNative{}
	broker, key, workspaces := testBroker(t, native)
	commitTestParent(t, broker, key, workspaces)
	broker.config.QuotaBytes = 1
	broker.config.ChildReserveBytes = 1
	admit := request(OperationAdmit, "admit-recent")
	response := broker.Handle(context.Background(), "S-1-5-21-test", admit)
	if response.OK || response.Error == nil || response.Error.Code != "storage-capacity-unavailable" {
		t.Fatalf("recent parent was admitted/deleted: %+v", response)
	}
	resolved, err := broker.store.ResolveParent(key)
	if err != nil || resolved.Status != ParentStatusValid {
		t.Fatalf("recent parent lost: %+v %v", resolved, err)
	}
	broker.now = func() time.Time { return resolved.Metadata.LastUsedAt.Add(31 * 24 * time.Hour) }
	response = broker.Handle(context.Background(), "S-1-5-21-test", request(OperationAdmit, "admit-expired"))
	if !response.OK {
		t.Fatalf("expired GC failed: %+v", response)
	}
	resolved, err = broker.store.ResolveParent(key)
	if err != nil || resolved.Status != ParentStatusMissing {
		t.Fatalf("expired parent status=%+v err=%v", resolved, err)
	}
}

func TestChangedImmutableParentIsRejectedBeforeChildCreation(t *testing.T) {
	native := &fakeNative{}
	broker, key, workspaces := testBroker(t, native)
	commitTestParent(t, broker, key, workspaces)
	native.verifyErr = ErrOwnershipMismatch
	if err := os.Mkdir(filepath.Join(workspaces, "changed-parent"), 0700); err != nil {
		t.Fatal(err)
	}
	acquire := request(OperationAcquire, "acquire-changed-parent")
	acquire.ParentKey = &key
	acquire.RunID = "changed-parent"
	acquire.WorkspaceID = "changed-parent"
	response := broker.Handle(context.Background(), "S-1-5-21-test", acquire)
	if response.OK || response.Error == nil || response.Error.Code != "parent-corrupt" {
		t.Fatalf("response=%+v", response)
	}
}

func TestAdmissionEnforcesHostFreeFloorAndChildReserve(t *testing.T) {
	native := &fakeNative{hostFree: 21 << 30}
	broker, _, _ := testBroker(t, native)
	response := broker.Handle(context.Background(), "S-1-5-21-test", request(OperationAdmit, "admit-host-floor"))
	if response.OK || response.Error == nil || response.Error.Code != "storage-capacity-unavailable" {
		t.Fatalf("response=%+v", response)
	}
}

func TestConcurrentParentCreationHasSingleBuilder(t *testing.T) {
	native := &fakeNative{}
	broker, key, workspaces := testBroker(t, native)
	for _, workspace := range []string{"builder-race-a", "builder-race-b"} {
		if err := os.Mkdir(filepath.Join(workspaces, workspace), 0700); err != nil {
			t.Fatal(err)
		}
	}
	responses := make(chan Response, 2)
	for i := 0; i < 2; i++ {
		go func(i int) {
			begin := request(OperationBeginParentBuild, fmt.Sprintf("begin-race-%d", i))
			begin.ParentKey = &key
			begin.Source = &SourceSnapshot{}
			begin.WorkspaceID = fmt.Sprintf("builder-race-%c", 'a'+i)
			responses <- broker.Handle(context.Background(), "S-1-5-21-test", begin)
		}(i)
	}
	mounted, waiting := 0, 0
	for i := 0; i < 2; i++ {
		response := <-responses
		if !response.OK || response.ParentBuild == nil {
			t.Fatalf("response=%+v", response)
		}
		switch response.ParentBuild.State {
		case "mounted":
			mounted++
		case "waiting":
			waiting++
		}
	}
	if mounted != 1 || waiting != 1 {
		t.Fatalf("mounted=%d waiting=%d", mounted, waiting)
	}
	native.mu.Lock()
	defer native.mu.Unlock()
	creates := 0
	for _, event := range native.events {
		if event == "parent-create" {
			creates++
		}
	}
	if creates != 1 {
		t.Fatalf("parent creates=%d events=%v", creates, native.events)
	}
}

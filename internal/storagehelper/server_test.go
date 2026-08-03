package storagehelper

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Kubonsang/testplay-runner/internal/vhdxstorage"
)

type fakeBackend struct {
	mu                     sync.Mutex
	platform               string
	elevated               bool
	acquires, releases     int
	acquireErr, releaseErr error
	last                   *fakeLease
	lastRequest            vhdxstorage.AcquireRequest
}

func (f *fakeBackend) Platform() string {
	if f.platform == "" {
		return "windows"
	}
	return f.platform
}
func (f *fakeBackend) Provider() string                         { return vhdxstorage.Provider }
func (f *fakeBackend) Supported() bool                          { return f.Platform() != "unsupported" }
func (f *fakeBackend) RequiresElevation() bool                  { return f.Platform() == "windows" }
func (f *fakeBackend) IsElevated(context.Context) (bool, error) { return f.elevated, nil }
func (f *fakeBackend) Acquire(_ context.Context, request vhdxstorage.AcquireRequest, progress vhdxstorage.ProgressFunc) (vhdxstorage.Lease, vhdxstorage.Metrics, error) {
	f.mu.Lock()
	f.acquires++
	f.lastRequest = request
	f.mu.Unlock()
	if f.acquireErr != nil {
		return nil, vhdxstorage.Metrics{}, f.acquireErr
	}
	for _, value := range []vhdxstorage.Progress{{State: vhdxstorage.StateCreatingChild}, {State: vhdxstorage.StateOpening}, {State: vhdxstorage.StateAttaching}, {State: vhdxstorage.StateWaitingVolume, PhysicalPath: `\\.\PhysicalDrive42`}, {State: vhdxstorage.StateMounting, PhysicalPath: `\\.\PhysicalDrive42`, VolumeGUIDPath: `\\?\Volume{fake}\`}, {State: vhdxstorage.StateReady, PhysicalPath: `\\.\PhysicalDrive42`, VolumeGUIDPath: `\\?\Volume{fake}\`}} {
		if err := progress(value); err != nil {
			return nil, vhdxstorage.Metrics{}, err
		}
	}
	lease := &fakeLease{backend: f, info: vhdxstorage.LeaseInfo{ParentPath: request.ParentPath, ChildPath: request.ChildPath, PhysicalPath: `\\.\PhysicalDrive42`, VolumeGUIDPath: `\\?\Volume{fake}\`, MountPath: request.MountPath}}
	f.last = lease
	value := int64(1)
	return lease, vhdxstorage.Metrics{AcquireWallClockMs: &value, TotalWallClockMs: &value}, nil
}

type fakeLease struct {
	backend  *fakeBackend
	info     vhdxstorage.LeaseInfo
	released bool
}

func (f *fakeLease) Info() vhdxstorage.LeaseInfo { return f.info }
func (f *fakeLease) Release(_ context.Context, _ bool, progress vhdxstorage.ProgressFunc) (vhdxstorage.Metrics, error) {
	f.backend.mu.Lock()
	f.backend.releases++
	f.backend.mu.Unlock()
	if f.backend.releaseErr != nil {
		return vhdxstorage.Metrics{}, f.backend.releaseErr
	}
	for _, value := range []vhdxstorage.Progress{{State: vhdxstorage.StateUnmounting}, {State: vhdxstorage.StateDetaching}, {State: vhdxstorage.StateReleased}} {
		if err := progress(value); err != nil {
			return vhdxstorage.Metrics{}, err
		}
	}
	f.released = true
	value := int64(2)
	return vhdxstorage.Metrics{ReleaseWallClockMs: &value, TotalWallClockMs: &value}, nil
}

type testPaths struct{ store, workspace, parent, child, mount string }

func makeTestPaths(t *testing.T) testPaths {
	t.Helper()
	root := t.TempDir()
	resolvedRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		t.Fatalf("resolve temporary test root: %v", err)
	}
	root = resolvedRoot
	store := filepath.Join(root, "store")
	workspace := filepath.Join(root, "workspace")
	for _, path := range []string{filepath.Join(store, "parents"), filepath.Join(store, "children"), workspace} {
		if err := os.MkdirAll(path, 0700); err != nil {
			t.Fatal(err)
		}
	}
	parent := filepath.Join(store, "parents", "base.vhdx")
	if err := os.WriteFile(parent, []byte("fixture"), 0600); err != nil {
		t.Fatal(err)
	}
	return testPaths{store, workspace, parent, filepath.Join(store, "children", "child.vhdx"), filepath.Join(workspace, "Library")}
}
func acquireRequest(id string, p testPaths) Request {
	return Request{SchemaVersion: SchemaVersion, Operation: OperationAcquire, RequestID: id, StoreRoot: p.store, WorkspaceRoot: p.workspace, ParentPath: p.parent, ChildPath: p.child, MountPath: p.mount, DeleteChildOnRelease: true}
}
func encodeLines(t *testing.T, values ...Request) string {
	t.Helper()
	var b strings.Builder
	encoder := json.NewEncoder(&b)
	for _, value := range values {
		if err := encoder.Encode(value); err != nil {
			t.Fatal(err)
		}
	}
	return b.String()
}
func decodeResponses(t *testing.T, data string) []Response {
	t.Helper()
	var values []Response
	decoder := json.NewDecoder(strings.NewReader(data))
	for {
		var value Response
		err := decoder.Decode(&value)
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		values = append(values, value)
	}
	return values
}
func runServer(t *testing.T, server *Server, input string) ([]Response, string, error) {
	t.Helper()
	var stdout, stderr bytes.Buffer
	err := server.Serve(context.Background(), strings.NewReader(input), &stdout, &stderr)
	return decodeResponses(t, stdout.String()), stderr.String(), err
}

func TestHello(t *testing.T) {
	backend := &fakeBackend{elevated: true}
	responses, stderr, err := runServer(t, NewServer(backend), encodeLines(t, Request{SchemaVersion: 1, Operation: OperationHello, RequestID: "req-hello"}))
	if err != nil || stderr != "" || len(responses) != 1 {
		t.Fatalf("responses=%#v stderr=%q err=%v", responses, stderr, err)
	}
	if !responses[0].OK || responses[0].HelperVersion != HelperVersion || responses[0].Platform != "windows" || responses[0].Provider != vhdxstorage.Provider || responses[0].Elevated == nil || !*responses[0].Elevated || responses[0].RequiresElevation == nil || !*responses[0].RequiresElevation {
		t.Fatalf("response=%#v", responses[0])
	}
}

func TestAcquireReleaseAndDuplicate(t *testing.T) {
	p := makeTestPaths(t)
	backend := &fakeBackend{elevated: true}
	acquire := acquireRequest("req-acquire", p)
	server := NewServer(backend)
	server.leaseID = func() (string, error) { return "lease-test", nil }
	responses, _, err := runServer(t, server, encodeLines(t, acquire, acquire, Request{SchemaVersion: 1, Operation: OperationRelease, RequestID: "req-release", LeaseID: "lease-test"}))
	if err != nil {
		t.Fatal(err)
	}
	if len(responses) != 3 {
		t.Fatalf("responses=%#v", responses)
	}
	if !responses[0].OK || responses[0].Lease == nil || !responses[1].OK || responses[1].Lease == nil || !responses[2].OK {
		t.Fatalf("responses=%#v", responses)
	}
	if responses[0].Lease.LeaseID != "lease-test" || responses[1].Lease.LeaseID != "lease-test" || !responses[2].Released {
		t.Fatalf("responses=%#v", responses)
	}
	if backend.acquires != 1 || backend.releases != 1 {
		t.Fatalf("acquires=%d releases=%d", backend.acquires, backend.releases)
	}
	if backend.lastRequest.StoreRoot != p.store || backend.lastRequest.LeaseID != "lease-test" {
		t.Fatalf("backend ownership inputs=%#v", backend.lastRequest)
	}
	data, err := os.ReadFile(journalPath(p.store, "lease-test"))
	if err != nil {
		t.Fatal(err)
	}
	var journal Journal
	if err := json.Unmarshal(data, &journal); err != nil {
		t.Fatal(err)
	}
	if journal.State != StateReleased {
		t.Fatalf("journal state=%q", journal.State)
	}
}

func TestRequestAndPathConflicts(t *testing.T) {
	p := makeTestPaths(t)
	backend := &fakeBackend{elevated: true}
	server := NewServer(backend)
	server.leaseID = func() (string, error) { return "lease-conflict", nil }
	first := acquireRequest("req-a", p)
	different := acquireRequest("req-b", p)
	responses, _, err := runServer(t, server, encodeLines(t, first, different, Request{SchemaVersion: 1, Operation: OperationHello, RequestID: "req-a"}))
	if err != nil {
		t.Fatal(err)
	}
	if !responses[0].OK {
		t.Fatal(responses[0])
	}
	if responses[1].Error.Code != CodeChildPathConflict {
		t.Fatalf("error=%#v", responses[1].Error)
	}
	if responses[2].Error.Code != CodeRequestConflict {
		t.Fatalf("error=%#v", responses[2].Error)
	}
}

func TestEOFCleanup(t *testing.T) {
	p := makeTestPaths(t)
	backend := &fakeBackend{elevated: true}
	responses, _, err := runServer(t, NewServer(backend), encodeLines(t, acquireRequest("req-eof", p)))
	if err != nil || len(responses) != 1 || !responses[0].OK {
		t.Fatalf("responses=%#v err=%v", responses, err)
	}
	if backend.releases != 1 || backend.last == nil || !backend.last.released {
		t.Fatalf("releases=%d", backend.releases)
	}
}

func TestEOFCleanupFailureIsReported(t *testing.T) {
	p := makeTestPaths(t)
	backend := &fakeBackend{elevated: true, releaseErr: errors.New("detach stuck")}
	responses, stderr, err := runServer(t, NewServer(backend), encodeLines(t, acquireRequest("req-eof-fail", p)))
	if err == nil || len(responses) != 2 || responses[1].Error == nil {
		t.Fatalf("responses=%#v err=%v", responses, err)
	}
	if !strings.Contains(stderr, "EOF cleanup failed") {
		t.Fatalf("stderr=%q", stderr)
	}
}

func TestJournalAtomicWriteAndFailure(t *testing.T) {
	p := makeTestPaths(t)
	store := NewJournalStore()
	now := time.Now().UTC()
	journal := Journal{SchemaVersion: 1, LeaseID: "lease-test", RequestID: "req", State: StateReleased, CreatedAt: now, UpdatedAt: now}
	if err := store.Write(p.store, journal); err != nil {
		t.Fatal(err)
	}
	matches, err := filepath.Glob(filepath.Join(p.store, "leases", ".journal-*.tmp"))
	if err != nil || len(matches) != 0 {
		t.Fatalf("matches=%v err=%v", matches, err)
	}
	failurePaths := makeTestPaths(t)
	backend := &fakeBackend{elevated: true}
	server := NewServer(backend)
	server.journals = &JournalStore{write: func(string, []byte, os.FileMode) error { return errors.New("disk full") }}
	responses, _, _ := runServer(t, server, encodeLines(t, acquireRequest("req-journal-fail", failurePaths)))
	if responses[0].Error.Code != CodeJournalWriteFailed || backend.acquires != 0 {
		t.Fatalf("response=%#v acquires=%d", responses[0], backend.acquires)
	}
}

func TestPathValidationRejectsChildAndMountCollisions(t *testing.T) {
	p := makeTestPaths(t)
	if err := os.WriteFile(p.child, []byte("existing"), 0600); err != nil {
		t.Fatal(err)
	}
	_, err := validateAcquirePaths(acquireRequest("req", p), "windows")
	if errorCode(err) != CodeChildExists {
		t.Fatalf("err=%v", err)
	}
	if err := os.Remove(p.child); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(p.mount, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(p.mount, "owned.txt"), []byte("x"), 0600); err != nil {
		t.Fatal(err)
	}
	_, err = validateAcquirePaths(acquireRequest("req", p), "windows")
	if errorCode(err) != CodeMountPathNotEmpty {
		t.Fatalf("err=%v", err)
	}
}

func TestProtocolErrorsAndStdoutJSONOnly(t *testing.T) {
	backend := &fakeBackend{platform: "unsupported", elevated: false}
	input := strings.Join([]string{`{"schemaVersion":2,"operation":"hello","requestId":"req-schema"}`, `{"schemaVersion":1,"operation":"wat","requestId":"req-op"}`, `{"schemaVersion":1,"operation":"acquire","requestId":"req-platform"}`}, "\n") + "\n"
	responses, stderr, err := runServer(t, NewServer(backend), input)
	if err != nil || stderr != "" {
		t.Fatalf("stderr=%q err=%v", stderr, err)
	}
	codes := []string{CodeUnsupportedSchema, CodeUnknownOperation, CodeUnsupportedPlatform}
	for i, code := range codes {
		if responses[i].Error.Code != code {
			t.Fatalf("response[%d]=%#v", i, responses[i])
		}
	}
}

func TestOrphanFound(t *testing.T) {
	p := makeTestPaths(t)
	store := NewJournalStore()
	now := time.Now().UTC()
	if err := store.Write(p.store, Journal{SchemaVersion: 1, LeaseID: "lease-orphan", RequestID: "old", State: StateReady, CreatedAt: now, UpdatedAt: now}); err != nil {
		t.Fatal(err)
	}
	responses, _, _ := runServer(t, NewServer(&fakeBackend{elevated: true}), encodeLines(t, acquireRequest("req-new", p)))
	if responses[0].Error.Code != CodeOrphanFound {
		t.Fatalf("response=%#v", responses[0])
	}
}

func TestUnknownFieldsRejected(t *testing.T) {
	responses, _, _ := runServer(t, NewServer(&fakeBackend{elevated: true}), `{"schemaVersion":1,"operation":"hello","requestId":"req","extra":true}`+"\n")
	if len(responses) != 1 || responses[0].Error.Code != CodeInvalidRequest {
		t.Fatalf("responses=%#v", responses)
	}
}

func TestReleaseIsIdempotent(t *testing.T) {
	p := makeTestPaths(t)
	backend := &fakeBackend{elevated: true}
	server := NewServer(backend)
	server.leaseID = func() (string, error) { return "lease-idempotent", nil }
	responses, _, err := runServer(t, server, encodeLines(t, acquireRequest("req-acquire", p), Request{SchemaVersion: 1, Operation: OperationRelease, RequestID: "req-release-a", LeaseID: "lease-idempotent"}, Request{SchemaVersion: 1, Operation: OperationRelease, RequestID: "req-release-b", LeaseID: "lease-idempotent"}))
	if err != nil {
		t.Fatal(err)
	}
	if len(responses) != 3 || !responses[1].Released || !responses[2].Released || !responses[2].Completed {
		t.Fatalf("responses=%#v", responses)
	}
	if backend.releases != 1 {
		t.Fatalf("releases=%d", backend.releases)
	}
}

func TestPathValidationRejectsRootEscape(t *testing.T) {
	p := makeTestPaths(t)
	request := acquireRequest("req", p)
	request.ChildPath = filepath.Join(filepath.Dir(p.store), "outside.vhdx")
	_, err := validateAcquirePaths(request, "windows")
	if errorCode(err) != CodeInvalidChildPath {
		t.Fatalf("err=%v", err)
	}
	request = acquireRequest("req", p)
	request.MountPath = filepath.Join(filepath.Dir(p.workspace), "outside-mount")
	_, err = validateAcquirePaths(request, "windows")
	if errorCode(err) != CodeInvalidMountPath {
		t.Fatalf("err=%v", err)
	}
}

func TestJournalDirectorySymlinkRejected(t *testing.T) {
	p := makeTestPaths(t)
	target := filepath.Join(t.TempDir(), "target")
	if err := os.Mkdir(target, 0700); err != nil {
		t.Fatal(err)
	}
	leases := filepath.Join(p.store, "leases")
	if err := os.Symlink(target, leases); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	now := time.Now().UTC()
	err := NewJournalStore().Write(p.store, Journal{SchemaVersion: 1, LeaseID: "lease-link", RequestID: "req", State: StateRequested, CreatedAt: now, UpdatedAt: now})
	if errorCode(err) != CodeJournalWriteFailed {
		t.Fatalf("err=%v", err)
	}
}

func TestJournalStateTransitions(t *testing.T) {
	p := makeTestPaths(t)
	server := NewServer(&fakeBackend{elevated: true})
	server.leaseID = func() (string, error) { return "lease-states", nil }
	var states []string
	server.journals = &JournalStore{write: func(_ string, data []byte, _ os.FileMode) error {
		var journal Journal
		if err := json.Unmarshal(data, &journal); err != nil {
			return err
		}
		states = append(states, journal.State)
		return nil
	}}
	responses, _, err := runServer(t, server, encodeLines(t, acquireRequest("req-acquire", p), Request{SchemaVersion: 1, Operation: OperationRelease, RequestID: "req-release", LeaseID: "lease-states"}))
	if err != nil || !responses[0].OK || !responses[1].OK {
		t.Fatalf("responses=%#v err=%v", responses, err)
	}
	want := []string{StateRequested, vhdxstorage.StateCreatingChild, vhdxstorage.StateOpening, vhdxstorage.StateAttaching, vhdxstorage.StateWaitingVolume, vhdxstorage.StateMounting, vhdxstorage.StateReady, StateReleasing, vhdxstorage.StateUnmounting, vhdxstorage.StateDetaching, vhdxstorage.StateReleased, StateReleased}
	if fmt.Sprint(states) != fmt.Sprint(want) {
		t.Fatalf("states=%v want=%v", states, want)
	}
}

func TestErrorFormatting(t *testing.T) {
	err := helperError(CodeInvalidRequest, "decode", "", fmt.Errorf("bad"))
	if !strings.Contains(err.Error(), "invalid-request") {
		t.Fatal(err)
	}
}

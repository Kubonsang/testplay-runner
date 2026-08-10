package vhdxworkspace

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

type BrokerConfig struct {
	StoreRoot         string
	WorkspaceRoot     string
	UserSID           string
	QuotaBytes        int64
	HostFloorBytes    int64
	ChildReserveBytes int64
	ParentTTL         time.Duration
}

type Broker struct {
	config   BrokerConfig
	store    *Store
	native   Native
	mu       sync.Mutex
	parents  map[string]ParentSession
	pending  map[string]*PendingParent
	children map[string]ChildSession
	requests map[string]Response
	inflight map[string]chan struct{}
	now      func() time.Time
}

type RecoverySummary struct {
	Released    int `json:"released"`
	Preserved   int `json:"preserved"`
	Quarantined int `json:"quarantined"`
}

// Recover reconciles only broker-authored journals. Retained leases and live
// clients are preserved. An identity or attach uncertainty is quarantined;
// it is never converted into a broad filesystem deletion.
func (b *Broker) Recover(ctx context.Context, grace time.Duration) (RecoverySummary, error) {
	var summary RecoverySummary
	if err := b.store.EnsureLayout(); err != nil {
		return summary, err
	}
	leasing, err := b.store.ListLeases()
	if err != nil {
		return summary, err
	}
	if grace <= 0 {
		grace = 30 * time.Second
	}
	preservedPending, quarantinedPending, pendingErr := b.store.RecoverPending(b.now(), grace, b.native.ProcessAlive)
	if pendingErr != nil {
		return summary, pendingErr
	}
	summary.Preserved += preservedPending
	summary.Quarantined += quarantinedPending
	for _, journal := range leasing {
		if journal.Retained {
			summary.Preserved++
			continue
		}
		if b.native.ProcessAlive(journal.ClientPID) || b.native.ProcessAlive(journal.UnityPID) || journal.UpdatedAt.After(b.now().Add(-grace)) {
			summary.Preserved++
			continue
		}
		if _, statErr := os.Lstat(journal.ChildPath); os.IsNotExist(statErr) {
			if removeErr := b.store.RemoveLease(journal); removeErr != nil {
				return summary, removeErr
			}
			summary.Released++
			continue
		}
		resolved, resolveErr := b.store.ResolveParent(CompatibilityKey{SchemaVersion: ParentSchemaVersion, Digest: journal.ParentKey})
		if resolveErr != nil || resolved.Status != ParentStatusValid || resolved.Metadata == nil {
			journal.State = "quarantined"
			_ = b.store.WriteLease(journal)
			summary.Quarantined++
			continue
		}
		if verifyErr := b.native.VerifyParent(ctx, *resolved.Metadata); verifyErr != nil {
			journal.State = "quarantined"
			_ = b.store.WriteLease(journal)
			summary.Quarantined++
			continue
		}
		b.mu.Lock()
		session := b.children[journal.LeaseID]
		b.mu.Unlock()
		if session == nil {
			var attachErr error
			session, _, attachErr = b.native.AttachChild(ctx, *resolved.Metadata, journal)
			if attachErr != nil {
				journal.State = "quarantined"
				_ = b.store.WriteLease(journal)
				summary.Quarantined++
				continue
			}
		}
		_, releaseErr := session.Release(ctx, true)
		if releaseErr != nil {
			journal.State = "quarantined"
			_ = b.store.WriteLease(journal)
			summary.Quarantined++
			continue
		}
		if removeErr := b.store.RemoveLease(journal); removeErr != nil {
			return summary, removeErr
		}
		b.mu.Lock()
		delete(b.children, journal.LeaseID)
		b.mu.Unlock()
		summary.Released++
	}
	return summary, nil
}

func NewBroker(config BrokerConfig, native Native) (*Broker, error) {
	if !filepath.IsAbs(config.StoreRoot) || !filepath.IsAbs(config.WorkspaceRoot) || strings.TrimSpace(config.UserSID) == "" {
		return nil, ErrInvalidInput
	}
	if config.QuotaBytes == 0 {
		config.QuotaBytes = DefaultQuotaBytes
	}
	if config.HostFloorBytes == 0 {
		config.HostFloorBytes = DefaultHostFloor
	}
	if config.ChildReserveBytes == 0 {
		config.ChildReserveBytes = DefaultChildReserve
	}
	if config.ParentTTL == 0 {
		config.ParentTTL = 30 * 24 * time.Hour
	}
	if config.QuotaBytes < 0 || config.HostFloorBytes < 0 || config.ChildReserveBytes < 0 {
		return nil, ErrInvalidInput
	}
	store, err := NewStore(config.StoreRoot, config.UserSID)
	if err != nil {
		return nil, err
	}
	if native == nil {
		native = NewNative()
	}
	return &Broker{config: config, store: store, native: native, parents: map[string]ParentSession{}, pending: map[string]*PendingParent{}, children: map[string]ChildSession{}, requests: map[string]Response{}, inflight: map[string]chan struct{}{}, now: time.Now}, nil
}

func (b *Broker) Handle(ctx context.Context, callerSID string, request Request) Response {
	b.mu.Lock()
	if response, ok := b.requests[request.RequestID]; ok && request.RequestID != "" {
		b.mu.Unlock()
		return response
	}
	if wait, ok := b.inflight[request.RequestID]; ok && request.RequestID != "" {
		b.mu.Unlock()
		select {
		case <-wait:
			b.mu.Lock()
			response := b.requests[request.RequestID]
			b.mu.Unlock()
			return response
		case <-ctx.Done():
			return Response{SchemaVersion: ProtocolSchemaVersion, RequestID: request.RequestID, Error: &Error{Code: "cancelled", Operation: request.Operation, Message: ctx.Err().Error()}}
		}
	}
	var wait chan struct{}
	if request.RequestID != "" {
		wait = make(chan struct{})
		b.inflight[request.RequestID] = wait
	}
	b.mu.Unlock()
	response := b.handleLocked(ctx, callerSID, request)
	if request.RequestID != "" {
		b.mu.Lock()
		b.requests[request.RequestID] = response
		delete(b.inflight, request.RequestID)
		close(wait)
		b.mu.Unlock()
	}
	return response
}

func (b *Broker) handleLocked(ctx context.Context, callerSID string, request Request) Response {
	fail := func(code, operation, path string, err error) Response {
		return Response{SchemaVersion: ProtocolSchemaVersion, RequestID: request.RequestID, OK: false, Error: &Error{Code: code, Operation: operation, Path: path, Message: errorText(err), Cause: err}}
	}
	if request.SchemaVersion != ProtocolSchemaVersion {
		return fail("unsupported-schema", "validate-request", "", fmt.Errorf("schemaVersion=%d", request.SchemaVersion))
	}
	if !identifierPattern.MatchString(request.RequestID) {
		return fail("invalid-request", "validate-request-id", request.RequestID, ErrInvalidInput)
	}
	if !strings.EqualFold(callerSID, b.config.UserSID) || (request.UserSID != "" && !strings.EqualFold(request.UserSID, callerSID)) {
		return fail("unauthorized-client", "authorize-client", callerSID, ErrOwnershipMismatch)
	}
	if request.Operation != OperationHello {
		if err := b.native.Available(ctx); err != nil {
			return fail("broker-unavailable", "native-check", b.native.Platform(), err)
		}
		if err := b.store.EnsureLayout(); err != nil {
			return fail("store-invalid", "ensure-layout", b.config.StoreRoot, err)
		}
	}
	switch request.Operation {
	case OperationHello:
		return Response{SchemaVersion: ProtocolSchemaVersion, RequestID: request.RequestID, OK: true, Provider: Provider, BrokerVersion: "v2", WorkspaceRoot: b.config.WorkspaceRoot, StoreRoot: b.config.StoreRoot}
	case OperationBeginParentBuild:
		return b.beginParent(ctx, request, fail)
	case OperationCommitParent:
		return b.commitParent(ctx, request, fail)
	case OperationAbortParent:
		return b.abortParent(ctx, request, fail)
	case OperationAcquire:
		return b.acquire(ctx, request, fail)
	case OperationHeartbeat:
		return b.heartbeat(request, fail)
	case OperationRelease:
		return b.release(ctx, request, fail)
	case OperationAttachRetained:
		return b.attachRetained(ctx, request, fail)
	case OperationRemoveRetained:
		return b.removeRetained(ctx, request, fail)
	case OperationStatus:
		return b.status(request, fail)
	case OperationAdmit:
		capacity, err := b.ensureCapacityWithLimits(1, false, request.StoreMaxAllocatedBytes, request.MinimumHostFreeBytes)
		if err != nil {
			return fail("storage-capacity-unavailable", "admit", b.config.StoreRoot, err)
		}
		return Response{SchemaVersion: ProtocolSchemaVersion, RequestID: request.RequestID, OK: true, Provider: Provider, Metrics: &Metrics{Capacity: capacity}}
	case OperationGC:
		return b.gc(request, fail)
	default:
		return fail("unknown-operation", "dispatch", request.Operation, ErrInvalidInput)
	}
}

func (b *Broker) attachRetained(ctx context.Context, request Request, fail failureBuilder) Response {
	record, err := b.store.ReadRetained(request.RunID)
	if err != nil {
		return fail("retained-not-found", "attach-retained", request.RunID, err)
	}
	journal, err := b.store.ReadLease(record.LeaseID)
	if err != nil || !journal.Retained || journal.OwnershipToken != record.OwnershipToken || journal.ChildPath != record.ChildPath {
		return fail("retained-identity-mismatch", "validate-retained", record.ChildPath, errors.Join(err, ErrOwnershipMismatch))
	}
	b.mu.Lock()
	_, active := b.children[record.LeaseID]
	b.mu.Unlock()
	if active {
		return fail("lease-conflict", "attach-retained", record.LeaseID, ErrParentConflict)
	}
	resolved, err := b.store.ResolveParent(CompatibilityKey{SchemaVersion: ParentSchemaVersion, Digest: record.ParentKey})
	if err != nil || resolved.Status != ParentStatusValid || resolved.Metadata == nil {
		return fail("parent-unavailable", "attach-retained", record.ParentKey, errors.Join(err, ErrOwnershipMismatch))
	}
	if verifyErr := b.native.VerifyParent(ctx, *resolved.Metadata); verifyErr != nil {
		return fail("parent-corrupt", "attach-retained", record.ParentKey, verifyErr)
	}
	_, mount, err := b.workspaceMount(request.WorkspaceID)
	if err != nil {
		return fail("invalid-workspace", "attach-retained", request.WorkspaceID, err)
	}
	if err := b.validateWorkspaceMount(request.WorkspaceID, mount); err != nil {
		return fail("invalid-workspace", "validate-retained-mount", mount, err)
	}
	journal.MountPath = mount
	session, metrics, err := b.native.AttachChild(ctx, *resolved.Metadata, *journal)
	if err != nil {
		return fail("retained-attach-failed", "attach-retained", record.ChildPath, err)
	}
	journal.State = "ready"
	if err := b.store.WriteLease(*journal); err != nil {
		_, cleanupErr := session.Release(context.Background(), false)
		return fail("journal-write-failed", "attach-retained", record.ChildPath, errors.Join(err, cleanupErr))
	}
	b.mu.Lock()
	b.children[record.LeaseID] = session
	b.mu.Unlock()
	lease := session.Info()
	return Response{SchemaVersion: ProtocolSchemaVersion, RequestID: request.RequestID, OK: true, Provider: Provider, Lease: &lease, Metrics: &metrics}
}

func (b *Broker) removeRetained(ctx context.Context, request Request, fail failureBuilder) Response {
	record, err := b.store.ReadRetained(request.RunID)
	if err != nil {
		return fail("retained-not-found", "remove-retained", request.RunID, err)
	}
	journal, err := b.store.ReadLease(record.LeaseID)
	if err != nil || !journal.Retained || journal.OwnershipToken != record.OwnershipToken || journal.ChildPath != record.ChildPath {
		return fail("retained-identity-mismatch", "remove-retained", record.ChildPath, errors.Join(err, ErrOwnershipMismatch))
	}
	b.mu.Lock()
	session := b.children[record.LeaseID]
	b.mu.Unlock()
	if session == nil {
		resolved, resolveErr := b.store.ResolveParent(CompatibilityKey{SchemaVersion: ParentSchemaVersion, Digest: record.ParentKey})
		if resolveErr != nil || resolved.Status != ParentStatusValid || resolved.Metadata == nil {
			return fail("parent-unavailable", "remove-retained", record.ParentKey, errors.Join(resolveErr, ErrOwnershipMismatch))
		}
		if verifyErr := b.native.VerifyParent(ctx, *resolved.Metadata); verifyErr != nil {
			return fail("parent-corrupt", "remove-retained", record.ParentKey, verifyErr)
		}
		session, _, err = b.native.AttachChild(ctx, *resolved.Metadata, *journal)
		if err != nil {
			return fail("retained-attach-failed", "remove-retained", record.ChildPath, err)
		}
	}
	metrics, err := session.Release(ctx, true)
	if err != nil {
		return fail("retained-release-failed", "remove-retained", record.ChildPath, err)
	}
	if err := b.store.RemoveLease(*journal); err != nil {
		return fail("journal-remove-failed", "remove-retained", record.ChildPath, err)
	}
	if err := b.store.RemoveRetained(*record); err != nil {
		return fail("retained-record-remove-failed", "remove-retained", record.ChildPath, err)
	}
	b.mu.Lock()
	delete(b.children, record.LeaseID)
	b.mu.Unlock()
	lease := session.Info()
	lease.State = "released"
	lease.Retained = false
	return Response{SchemaVersion: ProtocolSchemaVersion, RequestID: request.RequestID, OK: true, Provider: Provider, Lease: &lease, Metrics: &metrics}
}

type failureBuilder func(string, string, string, error) Response

func (b *Broker) beginParent(ctx context.Context, request Request, fail failureBuilder) Response {
	if request.ParentKey == nil || request.Source == nil {
		return fail("invalid-request", "begin-parent", "", ErrInvalidInput)
	}
	resolved, err := b.store.ResolveParent(*request.ParentKey)
	if err != nil {
		return fail("parent-resolve-failed", "resolve-parent", "", err)
	}
	if resolved.Status == ParentStatusValid {
		if verifyErr := b.native.VerifyParent(ctx, *resolved.Metadata); verifyErr != nil {
			return fail("parent-corrupt", "verify-parent", resolved.Path, verifyErr)
		}
		_ = b.store.TouchParent(*resolved.Metadata)
		return Response{SchemaVersion: ProtocolSchemaVersion, RequestID: request.RequestID, OK: true, Provider: Provider, Parent: resolved.Metadata, Metrics: &Metrics{ParentStatus: ParentStatusValid, ParentReused: true, ParentVirtualBytes: resolved.Metadata.VirtualBytes, ParentAllocatedBytes: resolved.Metadata.AllocatedBytes}}
	}
	if resolved.Status != ParentStatusMissing {
		return fail("parent-corrupt", "resolve-parent", resolved.Path, fmt.Errorf("status=%s reason=%s", resolved.Status, resolved.Reason))
	}
	workspace, mount, err := b.workspaceMount(request.WorkspaceID)
	if err != nil {
		return fail("invalid-workspace", "derive-parent-mount", request.WorkspaceID, err)
	}
	if err := b.validateWorkspaceMount(request.WorkspaceID, mount); err != nil {
		return fail("invalid-workspace", "validate-parent-mount", mount, err)
	}
	if _, err := os.Stat(workspace); err != nil {
		return fail("invalid-workspace", "stat-workspace", workspace, err)
	}
	pending, err := b.store.BeginParent(b.config.UserSID, *request.ParentKey, *request.Source, mount, request.ClientPID)
	if err != nil {
		if errors.Is(err, ErrParentConflict) {
			return Response{SchemaVersion: ProtocolSchemaVersion, RequestID: request.RequestID, OK: true, Provider: Provider, ParentBuild: &ParentBuild{ParentKey: request.ParentKey.Digest, State: "waiting"}, Metrics: &Metrics{ParentStatus: ParentStatusPending}}
		}
		return fail("parent-conflict", "begin-parent", resolved.Path, err)
	}
	session, err := b.native.BeginParent(ctx, pending)
	if err != nil {
		_ = b.store.AbortParent(pending)
		return fail("parent-create-failed", "begin-parent-native", pending.StagingPath, err)
	}
	b.mu.Lock()
	b.parents[pending.TransactionID] = session
	b.pending[pending.TransactionID] = pending
	b.mu.Unlock()
	return Response{SchemaVersion: ProtocolSchemaVersion, RequestID: request.RequestID, OK: true, Provider: Provider, ParentBuild: &ParentBuild{TransactionID: pending.TransactionID, ParentKey: pending.Key.Digest, MountPath: pending.MountPath, State: "mounted"}, Metrics: &Metrics{ParentStatus: ParentStatusPending, ParentCreated: true}}
}

func (b *Broker) commitParent(ctx context.Context, request Request, fail failureBuilder) Response {
	b.mu.Lock()
	session, ok := b.parents[request.TransactionID]
	pending := b.pending[request.TransactionID]
	b.mu.Unlock()
	if !ok || pending == nil {
		return fail("parent-transaction-not-found", "commit-parent", request.TransactionID, ErrOwnershipMismatch)
	}
	started := b.now()
	evidence, err := session.Finalize(ctx)
	if err != nil {
		return fail("parent-verification-failed", "finalize-parent", pending.StagingPath, err)
	}
	metadata := ParentMetadata{
		CompatibilityKey: pending.Key, SourceSnapshot: pending.Source, OwnershipToken: pending.OwnershipToken,
		FileIdentity: evidence.FileIdentity, Volume: evidence.Volume,
		FileWriteTime: evidence.FileWriteTime,
		VirtualBytes:  pending.Key.VirtualBytes, BlockBytes: pending.Key.BlockBytes, SectorBytes: pending.Key.SectorBytes,
		LogicalBytes: evidence.LogicalBytes, AllocatedBytes: evidence.AllocatedBytes, CommittedSHA256: evidence.SHA256,
	}
	committed, err := b.store.CommitParent(pending, metadata)
	if err != nil {
		return fail("parent-commit-failed", "commit-parent", pending.StagingPath, err)
	}
	b.mu.Lock()
	delete(b.parents, request.TransactionID)
	delete(b.pending, request.TransactionID)
	b.mu.Unlock()
	return Response{SchemaVersion: ProtocolSchemaVersion, RequestID: request.RequestID, OK: true, Provider: Provider, Parent: committed, Metrics: &Metrics{ParentStatus: ParentStatusValid, ParentCreated: true, ParentVerifyMs: time.Since(started).Milliseconds(), ParentVirtualBytes: committed.VirtualBytes, ParentAllocatedBytes: committed.AllocatedBytes}}
}

func (b *Broker) abortParent(ctx context.Context, request Request, fail failureBuilder) Response {
	b.mu.Lock()
	session, ok := b.parents[request.TransactionID]
	pending := b.pending[request.TransactionID]
	b.mu.Unlock()
	if !ok || pending == nil {
		return fail("parent-transaction-not-found", "abort-parent", request.TransactionID, ErrOwnershipMismatch)
	}
	if err := session.Abort(ctx); err != nil {
		return fail("parent-abort-uncertain", "abort-parent", pending.StagingPath, err)
	}
	if err := b.store.AbortParent(pending); err != nil {
		return fail("parent-abort-failed", "remove-pending", pending.StagingPath, err)
	}
	b.mu.Lock()
	delete(b.parents, request.TransactionID)
	delete(b.pending, request.TransactionID)
	b.mu.Unlock()
	return Response{SchemaVersion: ProtocolSchemaVersion, RequestID: request.RequestID, OK: true, Provider: Provider, Metrics: &Metrics{CleanupState: CleanupReleased}}
}

func (b *Broker) acquire(ctx context.Context, request Request, fail failureBuilder) Response {
	if request.ParentKey == nil || !identifierPattern.MatchString(request.RunID) {
		return fail("invalid-request", "acquire", request.RunID, ErrInvalidInput)
	}
	resolved, err := b.store.ResolveParent(*request.ParentKey)
	if err != nil || resolved.Status != ParentStatusValid || resolved.Metadata == nil {
		return fail("parent-unavailable", "resolve-parent", request.ParentKey.Digest, errors.Join(err, fmt.Errorf("status=%s", resolved.Status)))
	}
	if verifyErr := b.native.VerifyParent(ctx, *resolved.Metadata); verifyErr != nil {
		return fail("parent-corrupt", "verify-parent", resolved.Path, verifyErr)
	}
	_ = b.store.TouchParent(*resolved.Metadata)
	if _, err := b.ensureCapacityWithLimits(1, false, request.StoreMaxAllocatedBytes, request.MinimumHostFreeBytes); err != nil {
		return fail("storage-capacity-unavailable", "admit-child", b.config.StoreRoot, err)
	}
	leaseID, err := randomID("lease")
	if err != nil {
		return fail("lease-create-failed", "lease-id", "", err)
	}
	_, mount, err := b.workspaceMount(request.WorkspaceID)
	if err != nil {
		return fail("invalid-workspace", "derive-child-mount", request.WorkspaceID, err)
	}
	if err := b.validateWorkspaceMount(request.WorkspaceID, mount); err != nil {
		return fail("invalid-workspace", "validate-child-mount", mount, err)
	}
	child, _ := b.store.paths.Child(leaseID)
	token, _ := randomID("owner")
	journal := LeaseJournal{LeaseID: leaseID, RunID: request.RunID, UserSID: b.config.UserSID, OwnershipToken: token, ParentKey: request.ParentKey.Digest, ParentPath: resolved.Metadata.VHDXPath, ChildPath: child, MountPath: mount, State: "requested", ClientPID: request.ClientPID, CreatedAt: b.now().UTC(), UpdatedAt: b.now().UTC()}
	if err := b.store.WriteLease(journal); err != nil {
		return fail("journal-write-failed", "create-lease", child, err)
	}
	transition := func(state, physical, volume string) error {
		journal.State = state
		if physical != "" {
			journal.PhysicalPath = physical
		}
		if volume != "" {
			journal.VolumeGUID = volume
		}
		return b.store.WriteLease(journal)
	}
	session, metrics, err := b.native.AcquireChild(ctx, *resolved.Metadata, journal, transition)
	if err != nil {
		if _, childErr := os.Lstat(child); os.IsNotExist(childErr) {
			_ = b.store.RemoveLease(journal)
		} else {
			journal.State = "quarantined"
			_ = b.store.WriteLease(journal)
		}
		return fail("child-acquire-failed", "acquire-child", child, err)
	}
	info := session.Info()
	journal.State = "ready"
	journal.FileIdentity = session.FileIdentity()
	journal.PhysicalPath = info.PhysicalPath
	journal.VolumeGUID = info.VolumeGUID
	if err := b.store.WriteLease(journal); err != nil {
		_, cleanupErr := session.Release(context.Background(), true)
		return fail("journal-write-failed", "commit-ready", child, errors.Join(err, cleanupErr))
	}
	b.mu.Lock()
	b.children[leaseID] = session
	b.mu.Unlock()
	capacity, _ := b.ensureCapacity(0, false)
	metrics.Capacity = capacity
	return Response{SchemaVersion: ProtocolSchemaVersion, RequestID: request.RequestID, OK: true, Provider: Provider, Parent: resolved.Metadata, Lease: &info, Metrics: &metrics}
}

func (b *Broker) heartbeat(request Request, fail failureBuilder) Response {
	journal, err := b.store.ReadLease(request.LeaseID)
	if err != nil {
		return fail("lease-not-found", "heartbeat", request.LeaseID, err)
	}
	if journal.OwnershipToken == "" || journal.State != "ready" {
		return fail("lease-not-ready", "heartbeat", request.LeaseID, ErrOwnershipMismatch)
	}
	journal.ClientPID = request.ClientPID
	if request.UnityPID != 0 {
		journal.UnityPID = request.UnityPID
	}
	if err := b.store.WriteLease(*journal); err != nil {
		return fail("journal-write-failed", "heartbeat", request.LeaseID, err)
	}
	b.mu.Lock()
	session := b.children[request.LeaseID]
	b.mu.Unlock()
	if session == nil {
		return fail("lease-not-active", "heartbeat", request.LeaseID, ErrOwnershipMismatch)
	}
	lease := session.Info()
	metrics := &Metrics{}
	if usage, usageErr := session.Usage(); usageErr == nil {
		metrics.ChildPeakBytes = usage
	}
	return Response{SchemaVersion: ProtocolSchemaVersion, RequestID: request.RequestID, OK: true, Provider: Provider, Lease: &lease, Metrics: metrics}
}

func (b *Broker) release(ctx context.Context, request Request, fail failureBuilder) Response {
	b.mu.Lock()
	session, ok := b.children[request.LeaseID]
	b.mu.Unlock()
	if !ok {
		return fail("lease-not-found", "release", request.LeaseID, ErrOwnershipMismatch)
	}
	journal, err := b.store.ReadLease(request.LeaseID)
	if err != nil {
		return fail("lease-not-found", "release", request.LeaseID, err)
	}
	journal.State = "releasing"
	journal.Retained = request.RetainChild
	if err := b.store.WriteLease(*journal); err != nil {
		return fail("journal-write-failed", "release", request.LeaseID, err)
	}
	metrics, err := session.Release(ctx, !request.RetainChild)
	if err != nil {
		journal.State = "quarantined"
		_ = b.store.WriteLease(*journal)
		return fail("child-release-failed", "release", journal.ChildPath, err)
	}
	journal.State = "released"
	journal.Retained = request.RetainChild
	if request.RetainChild {
		if err := b.store.WriteLease(*journal); err != nil {
			return fail("journal-write-failed", "release-complete", journal.ChildPath, err)
		}
		if err := b.store.WriteRetained(*journal); err != nil {
			return fail("retained-record-failed", "retain-child", journal.ChildPath, err)
		}
	} else if err := b.store.RemoveLease(*journal); err != nil {
		return fail("journal-remove-failed", "release-complete", journal.ChildPath, err)
	}
	b.mu.Lock()
	delete(b.children, request.LeaseID)
	b.mu.Unlock()
	lease := session.Info()
	lease.State = "released"
	lease.Retained = request.RetainChild
	capacity, _ := b.ensureCapacity(0, false)
	metrics.Capacity = capacity
	return Response{SchemaVersion: ProtocolSchemaVersion, RequestID: request.RequestID, OK: true, Provider: Provider, Lease: &lease, Metrics: &metrics}
}

func (b *Broker) status(request Request, fail failureBuilder) Response {
	hostFree, err := b.native.HostFreeBytes(b.config.StoreRoot)
	if err != nil {
		return fail("status-failed", "host-free", b.config.StoreRoot, err)
	}
	status, err := b.store.Status(hostFree, b.config.QuotaBytes, b.config.HostFloorBytes)
	if err != nil {
		return fail("status-failed", "measure-store", b.config.StoreRoot, err)
	}
	active := map[string]bool{}
	b.mu.Lock()
	for _, child := range b.children {
		active[child.Info().ParentKey] = true
	}
	b.mu.Unlock()
	if parents, listErr := b.store.ListParents(); listErr == nil {
		for _, parent := range parents {
			if !active[parent.CompatibilityKey.Digest] && !parent.LastUsedAt.After(b.now().Add(-b.config.ParentTTL)) {
				status.Capacity.ReclaimableBytes += parent.AllocatedBytes
			}
		}
	}
	return Response{SchemaVersion: ProtocolSchemaVersion, RequestID: request.RequestID, OK: true, Provider: Provider, Status: &status}
}

func (b *Broker) gc(request Request, fail failureBuilder) Response {
	capacity, err := b.ensureCapacity(0, request.DryRun)
	if err != nil {
		return fail("gc-failed", "gc", b.config.StoreRoot, err)
	}
	return Response{SchemaVersion: ProtocolSchemaVersion, RequestID: request.RequestID, OK: true, Provider: Provider, Metrics: &Metrics{Capacity: capacity}}
}

func (b *Broker) ensureCapacity(workers int, dryRun bool) (Capacity, error) {
	return b.ensureCapacityWithLimits(workers, dryRun, 0, 0)
}

func (b *Broker) ensureCapacityWithLimits(workers int, dryRun bool, requestedQuota, requestedFloor int64) (Capacity, error) {
	hostFree, err := b.native.HostFreeBytes(b.config.StoreRoot)
	if err != nil {
		return Capacity{}, err
	}
	quota, floor := b.config.QuotaBytes, b.config.HostFloorBytes
	if requestedQuota > 0 && requestedQuota < quota {
		quota = requestedQuota
	}
	if requestedFloor > floor {
		floor = requestedFloor
	}
	status, err := b.store.Status(hostFree, quota, floor)
	if err != nil {
		return Capacity{}, err
	}
	capacity := status.Capacity
	capacity.ReserveBytes = int64(workers) * b.config.ChildReserveBytes
	needGC := capacity.AllocatedBytes+capacity.ReserveBytes > capacity.QuotaBytes || capacity.HostFreeBytes-capacity.ReserveBytes < capacity.HostFloorBytes
	if !needGC && !dryRun {
		return capacity, nil
	}
	active := map[string]bool{}
	b.mu.Lock()
	for _, child := range b.children {
		active[child.Info().ParentKey] = true
	}
	b.mu.Unlock()
	parents, err := b.store.ListParents()
	if err != nil {
		return capacity, err
	}
	projectedAllocated := capacity.AllocatedBytes
	for _, parent := range parents {
		if active[parent.CompatibilityKey.Digest] {
			continue
		}
		if parent.LastUsedAt.After(b.now().Add(-b.config.ParentTTL)) {
			continue
		}
		capacity.ReclaimableBytes += parent.AllocatedBytes
		if !dryRun {
			reclaimed, removeErr := b.store.RemoveParentIfUnused(parent.CompatibilityKey.Digest, active)
			if removeErr != nil {
				return capacity, removeErr
			}
			capacity.AllocatedBytes -= reclaimed
		}
		projectedAllocated -= parent.AllocatedBytes
		if !dryRun && projectedAllocated+capacity.ReserveBytes <= capacity.QuotaBytes && capacity.HostFreeBytes-capacity.ReserveBytes >= capacity.HostFloorBytes {
			return capacity, nil
		}
	}
	if dryRun {
		return capacity, nil
	}
	return capacity, ErrStorageUnavailable
}

func (b *Broker) workspaceMount(workspaceID string) (string, string, error) {
	if !identifierPattern.MatchString(workspaceID) {
		return "", "", ErrInvalidInput
	}
	workspace := filepath.Join(b.config.WorkspaceRoot, workspaceID)
	rel, err := filepath.Rel(b.config.WorkspaceRoot, workspace)
	if err != nil || rel == "." || strings.HasPrefix(rel, ".."+string(os.PathSeparator)) {
		return "", "", ErrInvalidInput
	}
	return workspace, filepath.Join(workspace, "Library"), nil
}

func (b *Broker) validateWorkspaceMount(workspaceID, mount string) error {
	workspace, expectedMount, err := b.workspaceMount(workspaceID)
	if err != nil || !samePath(expectedMount, mount) {
		return errors.Join(err, ErrInvalidInput)
	}
	for _, path := range []string{b.config.WorkspaceRoot, workspace} {
		info, statErr := os.Lstat(path)
		if statErr != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("%w: workspace component is not a real directory: %s: %v", ErrInvalidInput, path, statErr)
		}
		if platformErr := validatePlatformRealDirectory(path); platformErr != nil {
			return platformErr
		}
	}
	if _, err := os.Lstat(mount); err == nil {
		return fmt.Errorf("%w: Library mount path already exists: %s", ErrInvalidInput, mount)
	} else if !os.IsNotExist(err) {
		return err
	}
	return nil
}

func samePath(left, right string) bool {
	return strings.EqualFold(filepath.Clean(left), filepath.Clean(right))
}

func errorText(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}

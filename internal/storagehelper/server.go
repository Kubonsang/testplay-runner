package storagehelper

import (
	"bufio"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"regexp"
	"strings"
	"time"

	"github.com/Kubonsang/testplay-runner/internal/vhdxstorage"
)

var requestIDPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,127}$`)

type requestRecord struct {
	operation string
	response  Response
}
type releasedRecord struct {
	lease   WorkspaceLease
	metrics vhdxstorage.Metrics
}
type activeLease struct {
	request         Request
	lease           WorkspaceLease
	journal         Journal
	backend         vhdxstorage.Lease
	acquireResponse Response
}

type Server struct {
	backend        vhdxstorage.Backend
	journals       *JournalStore
	eofTimeout     time.Duration
	requestRecords map[string]requestRecord
	released       map[string]releasedRecord
	active         *activeLease
	elevated       bool
	elevationErr   error
	stderr         io.Writer
	leaseID        func() (string, error)
}

func NewServer(backend vhdxstorage.Backend) *Server {
	if backend == nil {
		backend = vhdxstorage.NewBackend()
	}
	return &Server{backend: backend, journals: NewJournalStore(), eofTimeout: 30 * time.Second, requestRecords: map[string]requestRecord{}, released: map[string]releasedRecord{}, leaseID: newLeaseID}
}

// Serve processes versioned NDJSON. Protocol responses are written only to
// stdout; diagnostic text is written only to stderr.
func (s *Server) Serve(ctx context.Context, stdin io.Reader, stdout, stderr io.Writer) error {
	s.stderr = stderr
	s.elevated, s.elevationErr = s.backend.IsElevated(ctx)
	if s.elevationErr != nil {
		fmt.Fprintf(stderr, "storage helper administrator check failed: %v\n", s.elevationErr)
	}
	scanner := bufio.NewScanner(stdin)
	scanner.Buffer(make([]byte, 64<<10), 1<<20)
	encoder := json.NewEncoder(stdout)
	encoder.SetEscapeHTML(false)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		var request Request
		decoder := json.NewDecoder(strings.NewReader(line))
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(&request); err != nil {
			if writeErr := encoder.Encode(responseError("", helperError(CodeInvalidRequest, "decode-request", "", err))); writeErr != nil {
				return writeErr
			}
			continue
		}
		response, shutdown := s.handle(ctx, request)
		if err := encoder.Encode(response); err != nil {
			return err
		}
		if shutdown {
			return nil
		}
	}
	if err := scanner.Err(); err != nil {
		return err
	}
	if s.active == nil {
		return nil
	}
	cleanupCtx, cancel := context.WithTimeout(context.Background(), s.eofTimeout)
	defer cancel()
	requestID := "eof-" + s.active.lease.LeaseID
	response := s.releaseActive(cleanupCtx, requestID)
	if !response.OK {
		_ = encoder.Encode(response)
		fmt.Fprintf(stderr, "storage helper EOF cleanup failed: %s child=%s mount=%s\n", response.Error, s.active.lease.ChildPath, s.active.lease.MountPath)
		return response.Error
	}
	return nil
}

func (s *Server) handle(ctx context.Context, request Request) (Response, bool) {
	if request.SchemaVersion != SchemaVersion {
		return responseError(request.RequestID, helperError(CodeUnsupportedSchema, "validate-schema", "", fmt.Errorf("schemaVersion=%d", request.SchemaVersion))), false
	}
	if !requestIDPattern.MatchString(request.RequestID) {
		return responseError(request.RequestID, helperError(CodeInvalidRequest, "validate-request-id", request.RequestID, fmt.Errorf("invalid requestId"))), false
	}
	if record, ok := s.requestRecords[request.RequestID]; ok {
		if record.operation != request.Operation {
			return responseError(request.RequestID, helperError(CodeRequestConflict, "idempotency", request.RequestID, fmt.Errorf("requestId was already used for %s", record.operation))), false
		}
		return record.response, request.Operation == OperationShutdown && record.response.OK
	}
	var response Response
	shutdown := false
	switch request.Operation {
	case OperationHello:
		response = s.hello(request)
	case OperationAcquire:
		response = s.acquire(ctx, request)
	case OperationRelease:
		response = s.release(ctx, request)
	case OperationShutdown:
		shutdown = true
		if s.active != nil {
			response = s.releaseActive(ctx, request.RequestID)
		} else {
			response = Response{SchemaVersion: SchemaVersion, RequestID: request.RequestID, OK: true, Released: true}
		}
	default:
		response = responseError(request.RequestID, helperError(CodeUnknownOperation, "dispatch", request.Operation, nil))
	}
	s.requestRecords[request.RequestID] = requestRecord{operation: request.Operation, response: response}
	return response, shutdown && response.OK
}

func (s *Server) hello(request Request) Response {
	elevated := s.elevated
	requiresElevation := s.backend.RequiresElevation()
	return Response{SchemaVersion: SchemaVersion, RequestID: request.RequestID, OK: true, HelperVersion: HelperVersion, Platform: s.backend.Platform(), Provider: s.backend.Provider(), Elevated: &elevated, RequiresElevation: &requiresElevation}
}

func (s *Server) acquire(ctx context.Context, request Request) Response {
	if !s.backend.Supported() {
		return responseError(request.RequestID, helperError(CodeUnsupportedPlatform, "acquire", s.backend.Platform(), nil))
	}
	if s.backend.RequiresElevation() && s.elevationErr != nil {
		return responseError(request.RequestID, helperError(CodeNotElevated, "check-administrator", "", s.elevationErr))
	}
	if s.backend.RequiresElevation() && !s.elevated {
		return responseError(request.RequestID, helperError(CodeNotElevated, "acquire", "", fmt.Errorf("launch the helper from an elevated caller")))
	}
	if s.active != nil {
		if s.active.request.RequestID == request.RequestID {
			return s.active.acquireResponse
		}
		if samePath(s.active.lease.ChildPath, request.ChildPath) {
			return responseError(request.RequestID, helperError(CodeChildPathConflict, "acquire", request.ChildPath, nil))
		}
		if samePath(s.active.lease.MountPath, request.MountPath) {
			return responseError(request.RequestID, helperError(CodeMountPathConflict, "acquire", request.MountPath, nil))
		}
		return responseError(request.RequestID, helperError(CodeLeaseConflict, "acquire", s.active.lease.LeaseID, fmt.Errorf("one lease per helper process")))
	}
	paths, err := validateAcquirePaths(request, s.backend.Platform())
	if err != nil {
		return responseError(request.RequestID, err)
	}
	orphans, err := s.journals.FindOrphans(paths.StoreRoot)
	if err != nil {
		return responseError(request.RequestID, err)
	}
	if len(orphans) > 0 {
		return responseError(request.RequestID, helperError(CodeOrphanFound, "scan-journals", journalPath(paths.StoreRoot, orphans[0].LeaseID), fmt.Errorf("state=%s", orphans[0].State)))
	}
	leaseID, err := s.leaseID()
	if err != nil {
		return responseError(request.RequestID, helperError(CodeInvalidRequest, "lease-id", "", err))
	}
	request.StoreRoot = paths.StoreRoot
	request.WorkspaceRoot = paths.WorkspaceRoot
	request.ParentPath = paths.ParentPath
	request.ChildPath = paths.ChildPath
	request.MountPath = paths.MountPath
	now := time.Now().UTC()
	lease := WorkspaceLease{LeaseID: leaseID, Provider: s.backend.Provider(), RequestID: request.RequestID, ParentPath: paths.ParentPath, ChildPath: paths.ChildPath, MountPath: paths.MountPath, State: StateRequested, CreatedAt: now}
	journal := Journal{SchemaVersion: SchemaVersion, LeaseID: leaseID, RequestID: request.RequestID, State: StateRequested, HelperPID: os.Getpid(), ParentPath: paths.ParentPath, ChildPath: paths.ChildPath, MountPath: paths.MountPath, DeleteChildOnRelease: request.DeleteChildOnRelease, CreatedAt: now, UpdatedAt: now}
	if err := s.journals.Write(paths.StoreRoot, journal); err != nil {
		return responseError(request.RequestID, err)
	}
	active := &activeLease{request: request, lease: lease, journal: journal}
	s.active = active
	progress := func(value vhdxstorage.Progress) error { return s.transition(paths.StoreRoot, active, value) }
	backendLease, metrics, err := s.backend.Acquire(ctx, vhdxstorage.AcquireRequest{ParentPath: paths.ParentPath, ChildPath: paths.ChildPath, MountPath: paths.MountPath}, progress)
	if err != nil {
		active.lease.State = StateQuarantined
		active.journal.State = StateQuarantined
		active.journal.UpdatedAt = time.Now().UTC()
		_ = s.journals.Write(paths.StoreRoot, active.journal)
		s.active = nil
		return responseError(request.RequestID, err)
	}
	active.backend = backendLease
	info := backendLease.Info()
	active.lease.PhysicalPath = info.PhysicalPath
	active.lease.VolumeGUIDPath = info.VolumeGUIDPath
	active.lease.State = StateReady
	response := Response{SchemaVersion: SchemaVersion, RequestID: request.RequestID, OK: true, Lease: &active.lease, Metrics: &metrics}
	active.acquireResponse = response
	return response
}

func (s *Server) transition(storeRoot string, active *activeLease, progress vhdxstorage.Progress) error {
	active.lease.State = progress.State
	if progress.PhysicalPath != "" {
		active.lease.PhysicalPath = progress.PhysicalPath
		active.journal.PhysicalPath = progress.PhysicalPath
	}
	if progress.VolumeGUIDPath != "" {
		active.lease.VolumeGUIDPath = progress.VolumeGUIDPath
		active.journal.VolumeGUIDPath = progress.VolumeGUIDPath
	}
	active.journal.State = progress.State
	active.journal.UpdatedAt = time.Now().UTC()
	return s.journals.Write(storeRoot, active.journal)
}

func (s *Server) release(ctx context.Context, request Request) Response {
	if request.LeaseID == "" {
		return responseError(request.RequestID, helperError(CodeInvalidRequest, "release", "", fmt.Errorf("leaseId is required")))
	}
	if released, ok := s.released[request.LeaseID]; ok {
		return Response{SchemaVersion: SchemaVersion, RequestID: request.RequestID, OK: true, Lease: &released.lease, Metrics: &released.metrics, Released: true, Completed: true}
	}
	if s.active == nil {
		return responseError(request.RequestID, helperError(CodeLeaseConflict, "release", request.LeaseID, fmt.Errorf("no active lease")))
	}
	if s.active.lease.LeaseID != request.LeaseID {
		return responseError(request.RequestID, helperError(CodeLeaseConflict, "release", request.LeaseID, fmt.Errorf("active lease is %s", s.active.lease.LeaseID)))
	}
	return s.releaseActive(ctx, request.RequestID)
}

func (s *Server) releaseActive(ctx context.Context, requestID string) Response {
	active := s.active
	if active == nil {
		return Response{SchemaVersion: SchemaVersion, RequestID: requestID, OK: true, Released: true, Completed: true}
	}
	active.lease.State = StateReleasing
	active.journal.State = StateReleasing
	active.journal.UpdatedAt = time.Now().UTC()
	if err := s.journals.Write(active.request.StoreRoot, active.journal); err != nil {
		return responseError(requestID, err)
	}
	progress := func(value vhdxstorage.Progress) error { return s.transition(active.request.StoreRoot, active, value) }
	metrics, err := active.backend.Release(ctx, active.request.DeleteChildOnRelease, progress)
	if err != nil {
		active.lease.State = StateQuarantined
		active.journal.State = StateQuarantined
		active.journal.UpdatedAt = time.Now().UTC()
		journalErr := s.journals.Write(active.request.StoreRoot, active.journal)
		if journalErr != nil {
			err = errors.Join(err, journalErr)
		}
		return responseError(requestID, wrapReleaseError(err))
	}
	active.lease.State = StateReleased
	active.journal.State = StateReleased
	active.journal.UpdatedAt = time.Now().UTC()
	if err := s.journals.Write(active.request.StoreRoot, active.journal); err != nil {
		return responseError(requestID, err)
	}
	record := releasedRecord{lease: active.lease, metrics: metrics}
	s.released[active.lease.LeaseID] = record
	if acquire, ok := s.requestRecords[active.request.RequestID]; ok && acquire.response.Lease != nil {
		copyLease := active.lease
		acquire.response.Lease = &copyLease
		acquire.response.Completed = true
		s.requestRecords[active.request.RequestID] = acquire
	}
	s.active = nil
	return Response{SchemaVersion: SchemaVersion, RequestID: requestID, OK: true, Lease: &record.lease, Metrics: &record.metrics, Released: true}
}

func newLeaseID() (string, error) {
	var value [12]byte
	if _, err := rand.Read(value[:]); err != nil {
		return "", err
	}
	return "lease-" + hex.EncodeToString(value[:]), nil
}

package storagehelper

import (
	"time"

	"github.com/Kubonsang/testplay-runner/internal/vhdxstorage"
)

const (
	SchemaVersion     = 1
	HelperVersion     = "v1"
	OperationHello    = "hello"
	OperationAcquire  = "acquire"
	OperationRelease  = "release"
	OperationShutdown = "shutdown"
	StateRequested    = "requested"
	StateReady        = "ready"
	StateReleasing    = "releasing"
	StateReleased     = "released"
	StateQuarantined  = "quarantined"
)

type Request struct {
	SchemaVersion        int    `json:"schemaVersion"`
	Operation            string `json:"operation"`
	RequestID            string `json:"requestId"`
	StoreRoot            string `json:"storeRoot,omitempty"`
	WorkspaceRoot        string `json:"workspaceRoot,omitempty"`
	ParentPath           string `json:"parentPath,omitempty"`
	ChildPath            string `json:"childPath,omitempty"`
	MountPath            string `json:"mountPath,omitempty"`
	DeleteChildOnRelease bool   `json:"deleteChildOnRelease,omitempty"`
	LeaseID              string `json:"leaseId,omitempty"`
}

type WorkspaceLease struct {
	LeaseID        string    `json:"leaseId"`
	Provider       string    `json:"provider"`
	RequestID      string    `json:"requestId"`
	ParentPath     string    `json:"parentPath"`
	ChildPath      string    `json:"childPath"`
	PhysicalPath   string    `json:"physicalPath,omitempty"`
	VolumeGUIDPath string    `json:"volumeGuidPath"`
	MountPath      string    `json:"mountPath"`
	State          string    `json:"state"`
	CreatedAt      time.Time `json:"createdAt"`
}

type Response struct {
	SchemaVersion int                  `json:"schemaVersion"`
	RequestID     string               `json:"requestId"`
	OK            bool                 `json:"ok"`
	HelperVersion string               `json:"helperVersion,omitempty"`
	Platform      string               `json:"platform,omitempty"`
	Elevated      *bool                `json:"elevated,omitempty"`
	Lease         *WorkspaceLease      `json:"lease,omitempty"`
	Metrics       *vhdxstorage.Metrics `json:"metrics,omitempty"`
	Released      bool                 `json:"released,omitempty"`
	Completed     bool                 `json:"completed,omitempty"`
	Error         *Error               `json:"error,omitempty"`
}

func responseError(requestID string, err error) Response {
	value, ok := err.(*Error)
	if !ok {
		value = helperError(CodeInvalidRequest, "request", "", err)
	}
	return Response{SchemaVersion: SchemaVersion, RequestID: requestID, OK: false, Error: value}
}

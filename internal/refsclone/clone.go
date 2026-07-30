package refsclone

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"time"
)

const (
	ControlDuplicateExtents = "FSCTL_DUPLICATE_EXTENTS_TO_FILE"
	MinimumFixtureBytes     = int64(1 << 20)
	MaxCloneBytes           = int64(4<<30) - 1
	SourceFixtureName       = "source.bin"
	CloneFixtureName        = "clone.bin"
	PhysicalCopyFixtureName = "physical-copy.bin"
)

const (
	CodeUnsupportedPlatform   = "unsupported-platform"
	CodeUnsupportedFilesystem = "unsupported-filesystem"
	CodeDifferentVolume       = "different-volume"
	CodeInvalidAlignment      = "invalid-alignment"
	CodeInvalidLength         = "invalid-length"
	CodeSourceNotFound        = "source-not-found"
	CodeDestinationExists     = "destination-exists"
	CodeAccessDenied          = "access-denied"
	CodeFileLocked            = "file-locked"
	CodeControlUnsupported    = "clone-control-unsupported"
	CodeDeviceIOControl       = "device-io-control-failed"
	CodeVerificationFailed    = "verification-failed"
	CodeMeasurement           = "measurement-unavailable"
	CodeCleanup               = "cleanup-failed"
	CodeCancelled             = "cancelled"
)

var ErrUnsupportedPlatform = errors.New("ReFS block clone is supported only on eligible Windows systems")

type Error struct {
	Code      string `json:"code"`
	Operation string `json:"operation,omitempty"`
	Path      string `json:"path,omitempty"`
	Win32Code uint32 `json:"win32Code,omitempty"`
	Cause     error  `json:"-"`
}

func (e *Error) Error() string {
	message := e.Code
	if e.Operation != "" {
		message += ": " + e.Operation
	}
	if e.Path != "" {
		message += ": " + e.Path
	}
	if e.Cause != nil {
		message += ": " + e.Cause.Error()
	}
	return message
}

func (e *Error) Unwrap() error { return e.Cause }

type Request struct {
	SourcePath        string `json:"sourcePath"`
	DestinationPath   string `json:"destinationPath"`
	SourceOffset      int64  `json:"sourceOffset"`
	DestinationOffset int64  `json:"destinationOffset"`
	Length            int64  `json:"length"`
}

type Result struct {
	ControlCodeUsed string        `json:"controlCodeUsed"`
	BytesCloned     int64         `json:"bytesCloned"`
	Duration        time.Duration `json:"-"`
	DurationMs      int64         `json:"durationMs"`
	ClusterSize     uint64        `json:"clusterSize"`
}

type Capability struct {
	Supported         bool     `json:"supported"`
	Filesystem        string   `json:"filesystem,omitempty"`
	SameVolume        bool     `json:"sameVolume"`
	ClusterSize       uint64   `json:"clusterSize,omitempty"`
	VolumeSerial      uint32   `json:"volumeSerial,omitempty"`
	Evidence          []string `json:"evidence,omitempty"`
	UnsupportedReason string   `json:"unsupportedReason,omitempty"`
}

type VolumeInfo struct {
	Root          string `json:"root"`
	Filesystem    string `json:"filesystem"`
	Serial        uint32 `json:"serial"`
	ClusterSize   uint64 `json:"clusterSize"`
	FreeBytes     uint64 `json:"freeBytes"`
	SupportsClone bool   `json:"supportsBlockRefcounting"`
}

type FileMeasurement struct {
	LogicalBytes    int64  `json:"logicalBytes"`
	AllocationBytes int64  `json:"allocationBytes"`
	CompressedBytes uint64 `json:"compressedBytes"`
}

func Probe(ctx context.Context, root string) (Capability, error) {
	if err := ctx.Err(); err != nil {
		return Capability{}, cancelled("probe", root, err)
	}
	if root == "" {
		return Capability{}, &Error{Code: CodeUnsupportedFilesystem, Operation: "probe", Cause: errors.New("root is empty")}
	}
	return probePlatform(ctx, root)
}

func CloneFile(ctx context.Context, request Request) (*Result, error) {
	if err := ctx.Err(); err != nil {
		return nil, cancelled("clone", request.DestinationPath, err)
	}
	return cloneFilePlatform(ctx, request)
}

func MeasureFile(path string) (FileMeasurement, error) {
	return measureFilePlatform(path)
}

func InspectVolume(path string) (VolumeInfo, error) {
	return inspectVolumePlatform(path)
}

func ValidateRequest(request Request, clusterSize uint64) error {
	if request.SourcePath == "" {
		return &Error{Code: CodeSourceNotFound, Operation: "validate", Path: request.SourcePath}
	}
	if request.DestinationPath == "" {
		return &Error{Code: CodeInvalidLength, Operation: "validate", Cause: errors.New("destination path is empty")}
	}
	source, _ := filepath.Abs(request.SourcePath)
	destination, _ := filepath.Abs(request.DestinationPath)
	if filepath.Clean(source) == filepath.Clean(destination) {
		return &Error{Code: CodeInvalidLength, Operation: "validate", Cause: errors.New("source and destination are the same path")}
	}
	if request.Length <= 0 || request.Length > MaxCloneBytes {
		return &Error{Code: CodeInvalidLength, Operation: "validate", Cause: fmt.Errorf("length must be in [1,%d]", MaxCloneBytes)}
	}
	if request.SourceOffset < 0 || request.DestinationOffset < 0 {
		return &Error{Code: CodeInvalidAlignment, Operation: "validate", Cause: errors.New("offset is negative")}
	}
	if clusterSize == 0 ||
		uint64(request.SourceOffset)%clusterSize != 0 ||
		uint64(request.DestinationOffset)%clusterSize != 0 ||
		uint64(request.Length)%clusterSize != 0 {
		return &Error{Code: CodeInvalidAlignment, Operation: "validate", Cause: fmt.Errorf("offsets and length must align to cluster size %d", clusterSize)}
	}
	return nil
}

func FixtureLength(clusterSize uint64) (int64, error) {
	if clusterSize == 0 {
		return 0, &Error{Code: CodeInvalidAlignment, Operation: "fixture-length"}
	}
	length := uint64(MinimumFixtureBytes)
	if remainder := length % clusterSize; remainder != 0 {
		length += clusterSize - remainder
	}
	if length > uint64(MaxCloneBytes) {
		return 0, &Error{Code: CodeInvalidLength, Operation: "fixture-length"}
	}
	return int64(length), nil
}

func cancelled(operation, path string, cause error) error {
	return &Error{Code: CodeCancelled, Operation: operation, Path: path, Cause: cause}
}

package vhdxworkspace

import (
	"context"
	"time"
)

type ParentEvidence struct {
	FileIdentity   FileIdentity
	Volume         VolumeIdentity
	LogicalBytes   int64
	AllocatedBytes int64
	SHA256         string
	FileWriteTime  time.Time
}

type ParentSession interface {
	Evidence() ParentEvidence
	Finalize(context.Context) (ParentEvidence, error)
	Abort(context.Context) error
}

type ChildSession interface {
	Info() Lease
	FileIdentity() FileIdentity
	Usage() (int64, error)
	Release(context.Context, bool) (Metrics, error)
}

type Native interface {
	Platform() string
	Available(context.Context) error
	BeginParent(context.Context, *PendingParent) (ParentSession, error)
	VerifyParent(context.Context, ParentMetadata) error
	AcquireChild(context.Context, ParentMetadata, LeaseJournal, func(string, string, string) error) (ChildSession, Metrics, error)
	AttachChild(context.Context, ParentMetadata, LeaseJournal) (ChildSession, Metrics, error)
	ProcessAlive(int) bool
	HostFreeBytes(string) (int64, error)
}

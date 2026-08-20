//go:build !windows

package vhdxworkspace

import (
	"context"
	"fmt"
)

type unsupportedNative struct{}

func NewNative() Native                    { return unsupportedNative{} }
func (unsupportedNative) Platform() string { return "unsupported" }
func (unsupportedNative) Available(context.Context) error {
	return fmt.Errorf("%w: Windows is required", ErrBrokerUnavailable)
}
func (unsupportedNative) BeginParent(context.Context, *PendingParent) (ParentSession, error) {
	return nil, fmt.Errorf("%w: Windows is required", ErrBrokerUnavailable)
}
func (unsupportedNative) VerifyParent(context.Context, ParentMetadata) error {
	return fmt.Errorf("%w: Windows is required", ErrBrokerUnavailable)
}
func (unsupportedNative) AcquireChild(context.Context, ParentMetadata, LeaseJournal, func(string, string, string) error) (ChildSession, Metrics, error) {
	return nil, Metrics{}, fmt.Errorf("%w: Windows is required", ErrBrokerUnavailable)
}
func (unsupportedNative) AttachChild(context.Context, ParentMetadata, LeaseJournal) (ChildSession, Metrics, error) {
	return nil, Metrics{}, fmt.Errorf("%w: Windows is required", ErrBrokerUnavailable)
}
func (unsupportedNative) BootSessionID() string { return "" }
func (unsupportedNative) ProcessAlive(int) bool { return false }
func (unsupportedNative) HostFreeBytes(string) (int64, error) {
	return 0, fmt.Errorf("%w: Windows is required", ErrBrokerUnavailable)
}

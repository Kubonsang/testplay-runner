package scenario

import (
	"sync"

	"github.com/Kubonsang/testplay-runner/internal/status"
)

// readyNotifier wraps a status.WriterInterface and closes readyCh the first
// time a Write call contains the target phase. sync.Once prevents double-close.
type readyNotifier struct {
	inner       status.WriterInterface
	targetPhase status.Phase
	readyCh     chan<- struct{}
	once        sync.Once
}

// NewReadyNotifier returns a status.WriterInterface that closes readyCh when
// a Write call with phase == targetPhase is received.
// All writes are forwarded to inner regardless.
// readyCh must be a buffered or otherwise drained channel; closing it is the
// signal mechanism and no value is sent.
func NewReadyNotifier(inner status.WriterInterface, targetPhase string, readyCh chan<- struct{}) status.WriterInterface {
	return &readyNotifier{
		inner:       inner,
		targetPhase: status.Phase(targetPhase),
		readyCh:     readyCh,
	}
}

func (n *readyNotifier) Write(s status.Status) error {
	if s.Phase == n.targetPhase {
		n.once.Do(func() { close(n.readyCh) })
	}
	return n.inner.Write(s)
}

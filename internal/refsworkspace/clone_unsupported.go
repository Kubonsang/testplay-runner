//go:build !windows

package refsworkspace

import "context"

type nativeTreeCloner struct{}

func NewNativeTreeCloner() TreeCloner { return nativeTreeCloner{} }

func (nativeTreeCloner) CloneTree(ctx context.Context, source, destination string, clusterSize int64) (CloneMetrics, error) {
	if err := ctx.Err(); err != nil {
		return CloneMetrics{}, cancelled("clone-tree", destination, err)
	}
	return CloneMetrics{}, newError(CodeUnsupportedPlatform, "clone-tree", source, nil)
}

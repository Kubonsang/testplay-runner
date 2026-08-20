//go:build !windows

package refsworkspace

import "context"

type nativeTreeCloner struct{}

func NewNativeTreeCloner() TreeCloner { return nativeTreeCloner{} }

func (nativeTreeCloner) CloneTree(ctx context.Context, request CloneRequest) (CloneMetrics, error) {
	if err := ctx.Err(); err != nil {
		return CloneMetrics{}, cancelled("clone-tree", request.Destination, err)
	}
	return CloneMetrics{}, newError(CodeUnsupportedPlatform, "clone-tree", request.Source, nil)
}

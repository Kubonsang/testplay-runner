//go:build linux

package vhdxstorage

import "context"

const platformProvider = ProviderReflink

func cloneTree(ctx context.Context, source, destination string) error {
	return cloneTreeWithRunner(ctx, source, destination, execCloneCommandRunner{})
}

//go:build !windows

package refsworkspace

func platformTemporaryMountedPoolReadinessError(error, bool) bool { return false }

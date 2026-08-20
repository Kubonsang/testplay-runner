//go:build !windows

package vhdxworkspace

func validatePlatformRealDirectory(string) error { return nil }
func validatePlatformNonReparse(string) error    { return nil }

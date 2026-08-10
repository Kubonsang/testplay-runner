//go:build !windows

package vhdxworkspace

func DefaultClient() Client { return unavailableClient{} }

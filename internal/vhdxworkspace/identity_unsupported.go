//go:build !windows

package vhdxworkspace

import "runtime"

func CurrentUserSID() (string, error) {
	return "", wrap("unsupported", "current-user-sid", runtime.GOOS, ErrBrokerUnavailable)
}

func IsElevated() bool { return false }

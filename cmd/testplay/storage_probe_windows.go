//go:build windows

package main

import (
	"errors"

	"golang.org/x/sys/windows"
)

func brokerProbeAccessDenied(err error) bool {
	return errors.Is(err, windows.ERROR_ACCESS_DENIED)
}

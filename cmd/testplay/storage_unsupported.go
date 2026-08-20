//go:build !windows

package main

import (
	"fmt"

	"github.com/Kubonsang/testplay-runner/internal/vhdxworkspace"
)

func platformInstallStorage(string) (any, error) {
	return nil, fmt.Errorf("%w: Windows only", vhdxworkspace.ErrBrokerUnavailable)
}
func platformUpgradeStorage() (any, error) {
	return nil, fmt.Errorf("%w: Windows only", vhdxworkspace.ErrBrokerUnavailable)
}
func platformUninstallStorage(bool) (any, error) {
	return nil, fmt.Errorf("%w: Windows only", vhdxworkspace.ErrBrokerUnavailable)
}

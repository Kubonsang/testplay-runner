//go:build !windows

package refsworkspace

import (
	"fmt"
	"path/filepath"
)

type nativeJunctioner struct{}

func NewNativeJunctioner() Junctioner { return nativeJunctioner{} }

func (nativeJunctioner) Create(target, junction string) error {
	return fmt.Errorf("Windows junctions are unavailable")
}

func (nativeJunctioner) Remove(target, junction string) error {
	return fmt.Errorf("Windows junctions are unavailable")
}

func verifyNativeJunctionIdentity(target, junction string) error {
	resolved, err := filepath.EvalSymlinks(junction)
	if err != nil {
		return err
	}
	expected, err := filepath.EvalSymlinks(target)
	if err != nil {
		return err
	}
	if filepath.Clean(resolved) != filepath.Clean(expected) {
		return fmt.Errorf("junction target mismatch")
	}
	return nil
}

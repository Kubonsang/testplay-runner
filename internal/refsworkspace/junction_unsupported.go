//go:build !windows

package refsworkspace

import "fmt"

type nativeJunctioner struct{}

func NewNativeJunctioner() Junctioner { return nativeJunctioner{} }

func (nativeJunctioner) Create(target, junction string) error {
	return fmt.Errorf("Windows junctions are unavailable")
}

func (nativeJunctioner) Remove(target, junction string) error {
	return fmt.Errorf("Windows junctions are unavailable")
}

//go:build !windows

package refsworkspace

import (
	"fmt"
	"os"
	"path/filepath"
)

type testJunctioner struct{}

func (testJunctioner) Create(target, junction string) error { return os.Symlink(target, junction) }

func (testJunctioner) Remove(target, junction string) error {
	resolved, err := filepath.EvalSymlinks(junction)
	if err != nil {
		return err
	}
	expected, err := filepath.EvalSymlinks(target)
	if err != nil {
		return err
	}
	if resolved != expected {
		return fmt.Errorf("target changed")
	}
	return os.Remove(junction)
}

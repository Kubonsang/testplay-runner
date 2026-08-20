//go:build windows

package vhdxworkspace

import (
	"fmt"

	"golang.org/x/sys/windows"
)

func validatePlatformRealDirectory(path string) error {
	pointer, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return err
	}
	attributes, err := windows.GetFileAttributes(pointer)
	if err != nil {
		return err
	}
	if attributes&windows.FILE_ATTRIBUTE_DIRECTORY == 0 || attributes&windows.FILE_ATTRIBUTE_REPARSE_POINT != 0 {
		return fmt.Errorf("%w: workspace component is a reparse point or non-directory: %s", ErrInvalidInput, path)
	}
	return nil
}

func validatePlatformNonReparse(path string) error {
	pointer, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return err
	}
	attributes, err := windows.GetFileAttributes(pointer)
	if err != nil {
		return err
	}
	if attributes&windows.FILE_ATTRIBUTE_REPARSE_POINT != 0 {
		return fmt.Errorf("%w: workspace entry is a reparse point: %s", ErrOwnershipMismatch, path)
	}
	return nil
}

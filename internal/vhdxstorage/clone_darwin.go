//go:build darwin

package vhdxstorage

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"syscall"
	"unsafe"
)

const (
	platformProvider = ProviderAPFS
	sysClonefileat   = 462
	cloneNoFollowAny = 0x0008
	cloneNoOwnerCopy = 0x0002
)

var atFDCWD = ^uintptr(1) // Darwin AT_FDCWD is -2.

func cloneTree(ctx context.Context, source, destination string) error {
	var directories []string
	err := filepath.WalkDir(source, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		relative, err := filepath.Rel(source, path)
		if err != nil {
			return err
		}
		target := destination
		if relative != "." {
			target = filepath.Join(destination, relative)
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		if entry.IsDir() {
			if err := os.Mkdir(target, info.Mode().Perm()|0700); err != nil {
				return err
			}
			directories = append(directories, target)
			return nil
		}
		if err := cloneRegularFile(path, target); err != nil {
			return err
		}
		return nil
	})
	if err != nil {
		return err
	}
	for index := len(directories) - 1; index >= 0; index-- {
		sourcePath := source
		if directories[index] != destination {
			relative, err := filepath.Rel(destination, directories[index])
			if err != nil {
				return err
			}
			sourcePath = filepath.Join(source, relative)
		}
		info, err := os.Stat(sourcePath)
		if err != nil {
			return err
		}
		if err := os.Chmod(directories[index], info.Mode().Perm()); err != nil {
			return err
		}
		if err := os.Chtimes(directories[index], info.ModTime(), info.ModTime()); err != nil {
			return err
		}
	}
	return nil
}

func cloneRegularFile(source, destination string) error {
	sourcePointer, err := syscall.BytePtrFromString(source)
	if err != nil {
		return err
	}
	destinationPointer, err := syscall.BytePtrFromString(destination)
	if err != nil {
		return err
	}
	_, _, errno := syscall.Syscall6(
		sysClonefileat,
		atFDCWD,
		uintptr(unsafe.Pointer(sourcePointer)),
		atFDCWD,
		uintptr(unsafe.Pointer(destinationPointer)),
		cloneNoFollowAny|cloneNoOwnerCopy,
		0,
	)
	runtime.KeepAlive(sourcePointer)
	runtime.KeepAlive(destinationPointer)
	if errno == 0 {
		return nil
	}
	if errors.Is(errno, syscall.EXDEV) || errors.Is(errno, syscall.ENOTSUP) || errors.Is(errno, syscall.EINVAL) {
		return fmt.Errorf("%w: clonefileat: %v", errCoWUnavailable, errno)
	}
	return fmt.Errorf("clonefileat: %w", errno)
}

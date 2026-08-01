package mountedcopy

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

type Result struct {
	LogicalBytes int64
	FileCount    int64
	Duration     time.Duration
}

// Contents copies children of sourceRoot into a new ordinary destination.
// On Windows, sourceRoot itself may be a verified volume mount. Nested
// reparse points are rejected and never followed.
func Contents(ctx context.Context, sourceRoot, destination string) (result *Result, returnErr error) {
	started := time.Now()
	result = &Result{}
	defer func() { result.Duration = time.Since(started) }()

	sourceRoot, err := absoluteCleanPath(sourceRoot)
	if err != nil {
		return result, newError(CodeInvalidSource, "validate-source-root", sourceRoot, err)
	}
	destination, err = absoluteCleanPath(destination)
	if err != nil {
		return result, newError(CodeInvalidDestination, "validate-destination-root", destination, err)
	}
	if pathsOverlap(sourceRoot, destination) {
		return result, newError(CodeRootOverlap, "validate-root-overlap", destination, fmt.Errorf("source and destination overlap"))
	}
	if _, err := os.Lstat(destination); err == nil {
		return result, newError(CodeDestinationExists, "create-destination", destination, fmt.Errorf("destination already exists"))
	} else if !os.IsNotExist(err) {
		return result, newError(CodeInvalidDestination, "inspect-destination", destination, err)
	}

	traversalRoot, err := resolveSourceRoot(sourceRoot)
	if err != nil {
		return result, err
	}
	if err := os.Mkdir(destination, 0700); err != nil {
		return result, newError(CodeInvalidDestination, "create-destination", destination, err)
	}
	succeeded := false
	defer func() {
		if !succeeded {
			_ = os.RemoveAll(destination)
		}
	}()

	if err := copyDirectory(ctx, traversalRoot, destination, destination, result); err != nil {
		return result, err
	}
	if reparse, err := IsReparsePoint(destination); err != nil {
		return result, newError(CodeInvalidDestination, "inspect-destination", destination, err)
	} else if reparse {
		return result, newError(CodeInvalidDestination, "inspect-destination", destination, fmt.Errorf("destination is a reparse point"))
	}
	succeeded = true
	return result, nil
}

func copyDirectory(ctx context.Context, source, destination, destinationRoot string, result *Result) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	entries, err := os.ReadDir(source)
	if err != nil {
		return newError(CodeInvalidSource, "read-source-directory", source, err)
	}
	for _, entry := range entries {
		if err := ctx.Err(); err != nil {
			return err
		}
		sourcePath := filepath.Join(source, entry.Name())
		destinationPath := filepath.Join(destination, entry.Name())
		if !pathWithin(destinationRoot, destinationPath) {
			return newError(CodeInvalidDestination, "map-destination", destinationPath, fmt.Errorf("path escaped destination root"))
		}
		info, err := os.Lstat(sourcePath)
		if err != nil {
			return newError(CodeInvalidSource, "inspect-source-entry", sourcePath, err)
		}
		reparse, err := IsReparsePoint(sourcePath)
		if err != nil {
			return newError(CodeInvalidSource, "inspect-source-entry", sourcePath, err)
		}
		if info.Mode()&os.ModeSymlink != 0 || reparse {
			return newError(CodeNestedReparse, "copy-directory-contents", sourcePath, fmt.Errorf("nested reparse points are not followed"))
		}
		if info.IsDir() {
			if err := os.Mkdir(destinationPath, info.Mode().Perm()); err != nil {
				return newError(CodeCopyFailed, "create-destination-directory", destinationPath, err)
			}
			if err := copyDirectory(ctx, sourcePath, destinationPath, destinationRoot, result); err != nil {
				return err
			}
			continue
		}
		if !info.Mode().IsRegular() {
			return newError(CodeNestedReparse, "copy-directory-contents", sourcePath, fmt.Errorf("unsupported non-regular entry: %s", info.Mode()))
		}
		if err := copyFile(ctx, sourcePath, destinationPath, info.Mode().Perm()); err != nil {
			return err
		}
		result.FileCount++
		result.LogicalBytes += info.Size()
	}
	return nil
}

func copyFile(ctx context.Context, source, destination string, mode os.FileMode) error {
	in, err := os.Open(source)
	if err != nil {
		return newError(CodeInvalidSource, "open-source-file", source, err)
	}
	defer in.Close()
	out, err := os.OpenFile(destination, os.O_WRONLY|os.O_CREATE|os.O_EXCL, mode)
	if err != nil {
		return newError(CodeCopyFailed, "create-destination-file", destination, err)
	}
	_, copyErr := io.Copy(out, &contextReader{ctx: ctx, reader: in})
	closeErr := out.Close()
	if copyErr != nil {
		return newError(CodeCopyFailed, "copy-source-file", source, copyErr)
	}
	if closeErr != nil {
		return newError(CodeCopyFailed, "close-destination-file", destination, closeErr)
	}
	return nil
}

type contextReader struct {
	ctx    context.Context
	reader io.Reader
}

func (r *contextReader) Read(buffer []byte) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	return r.reader.Read(buffer)
}

func absoluteCleanPath(path string) (string, error) {
	if !filepath.IsAbs(path) {
		return "", fmt.Errorf("absolute path required")
	}
	return filepath.Clean(path), nil
}

func pathsOverlap(left, right string) bool {
	left, right = filepath.Clean(left), filepath.Clean(right)
	if runtime.GOOS == "windows" {
		left, right = strings.ToLower(left), strings.ToLower(right)
	}
	return pathWithin(left, right) || pathWithin(right, left)
}

func pathWithin(root, candidate string) bool {
	relative, err := filepath.Rel(root, candidate)
	return err == nil && (relative == "." || (relative != ".." && !strings.HasPrefix(relative, ".."+string(os.PathSeparator))))
}

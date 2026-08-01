package unityvhdxfixture

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

// PhysicalLibraryMaterialization describes a fixture-only physical copy. The
// source root may be a verified Windows volume mount, but the destination is
// always a new ordinary directory. The production PhysicalCopyMaterializer's
// link-preserving semantics are intentionally unchanged.
type PhysicalLibraryMaterialization struct {
	LogicalBytes int64
	FileCount    int64
	Duration     time.Duration
}

// MaterializeDirectoryContents copies the children of sourceRoot rather than
// copying sourceRoot's reparse metadata. Only a verified Windows volume mount
// may be dereferenced. Nested reparse points are never followed or reproduced.
func MaterializeDirectoryContents(ctx context.Context, sourceRoot, destination string) (result *PhysicalLibraryMaterialization, returnErr error) {
	started := time.Now()
	result = &PhysicalLibraryMaterialization{}
	defer func() { result.Duration = time.Since(started) }()

	sourceRoot, err := absoluteCleanPath(sourceRoot)
	if err != nil {
		return result, fixtureError(CodePhysicalLibraryNotDirectory, "validate-source-root", sourceRoot, err)
	}
	destination, err = absoluteCleanPath(destination)
	if err != nil {
		return result, fixtureError(CodePhysicalLibraryCopyEscaped, "validate-destination-root", destination, err)
	}
	if physicalPathsOverlap(sourceRoot, destination) {
		return result, fixtureError(CodePhysicalLibraryCopyEscaped, "validate-root-overlap", destination, fmt.Errorf("source and destination overlap"))
	}
	if _, err := os.Lstat(destination); err == nil {
		return result, fixtureError(CodePhysicalLibraryCopyEscaped, "create-destination", destination, fmt.Errorf("destination already exists"))
	} else if !os.IsNotExist(err) {
		return result, fixtureError(CodePhysicalLibraryCopyEscaped, "inspect-destination", destination, err)
	}

	traversalRoot, err := resolvePhysicalLibrarySourceRoot(sourceRoot)
	if err != nil {
		return result, err
	}
	if err := os.Mkdir(destination, 0700); err != nil {
		return result, fixtureError(CodePhysicalLibraryCopyEscaped, "create-destination", destination, err)
	}
	succeeded := false
	defer func() {
		if !succeeded {
			_ = os.RemoveAll(destination)
		}
	}()

	if err := copyPhysicalDirectory(ctx, traversalRoot, destination, destination, result); err != nil {
		return result, err
	}
	if reparse, err := physicalPathIsReparse(destination); err != nil {
		return result, fixtureError(CodePhysicalLibraryCopyEscaped, "inspect-destination", destination, err)
	} else if reparse {
		return result, fixtureError(CodePhysicalLibraryIsReparse, "inspect-destination", destination, fmt.Errorf("destination is a reparse point"))
	}
	succeeded = true
	return result, nil
}

// ValidatePhysicalLibraryDirectory prevents Unity from receiving a dangling,
// linked, or incomplete Library. Unity 6000.3 stores SourceAssetDB as a regular
// LMDB data file, not a directory. The fixture Runtime assembly is the warm
// import/compile sentinel.
func ValidatePhysicalLibraryDirectory(path string) error {
	path, err := absoluteCleanPath(path)
	if err != nil {
		return fixtureError(CodePhysicalLibraryNotDirectory, "validate-physical-library", path, err)
	}
	info, err := os.Lstat(path)
	if err != nil {
		return fixtureError(CodePhysicalLibraryDangling, "validate-physical-library", path, err)
	}
	reparse, attrErr := physicalPathIsReparse(path)
	if attrErr != nil {
		return fixtureError(CodePhysicalLibraryDangling, "inspect-physical-library", path, attrErr)
	}
	if info.Mode()&os.ModeSymlink != 0 || reparse {
		return fixtureError(CodePhysicalLibraryIsReparse, "validate-physical-library", path, fmt.Errorf("Library must be an ordinary directory"))
	}
	if !info.IsDir() {
		return fixtureError(CodePhysicalLibraryNotDirectory, "validate-physical-library", path, fmt.Errorf("Library is not a directory"))
	}
	if err := validateReadableRegularFile(
		filepath.Join(path, "SourceAssetDB"),
		CodePhysicalLibraryDangling,
		CodePhysicalLibraryInvalidDB,
		"validate-source-asset-db",
	); err != nil {
		return err
	}
	// Unity 6000.3.8f1 produced SourceAssetDB-lock in both observed hardware
	// attempts. It is not a seed-completion sentinel because lock-file lifetime
	// is implementation state; when present it must still be an ordinary file.
	if err := validateOptionalSourceAssetDBLock(filepath.Join(path, "SourceAssetDB-lock")); err != nil {
		return err
	}
	scriptAssemblies := filepath.Join(path, "ScriptAssemblies")
	assembliesInfo, err := os.Lstat(scriptAssemblies)
	if err != nil {
		return fixtureError(CodePhysicalLibraryIncomplete, "validate-script-assemblies", scriptAssemblies, err)
	}
	assembliesReparse, err := physicalPathIsReparse(scriptAssemblies)
	if err != nil {
		return fixtureError(CodePhysicalLibraryIncomplete, "inspect-script-assemblies", scriptAssemblies, err)
	}
	if !assembliesInfo.IsDir() || assembliesInfo.Mode()&os.ModeSymlink != 0 || assembliesReparse {
		return fixtureError(CodePhysicalLibraryIncomplete, "validate-script-assemblies", scriptAssemblies, fmt.Errorf("ScriptAssemblies must be an ordinary directory"))
	}
	if err := validateReadableRegularFile(
		filepath.Join(scriptAssemblies, "TestPlayFixture.Runtime.dll"),
		CodePhysicalLibraryIncomplete,
		CodePhysicalLibraryIncomplete,
		"validate-fixture-runtime-assembly",
	); err != nil {
		return err
	}
	return nil
}

func validateReadableRegularFile(path, missingCode, invalidCode, operation string) error {
	info, err := os.Lstat(path)
	if err != nil {
		return fixtureError(missingCode, operation, path, err)
	}
	reparse, err := physicalPathIsReparse(path)
	if err != nil {
		return fixtureError(missingCode, operation, path, err)
	}
	if !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 || reparse {
		return fixtureError(invalidCode, operation, path, fmt.Errorf("expected a regular non-reparse file"))
	}
	if info.Size() < 1 {
		return fixtureError(invalidCode, operation, path, fmt.Errorf("file is empty"))
	}
	file, err := os.Open(path)
	if err != nil {
		return fixtureError(invalidCode, operation, path, err)
	}
	var firstByte [1]byte
	_, readErr := file.Read(firstByte[:])
	closeErr := file.Close()
	if readErr != nil {
		return fixtureError(invalidCode, operation, path, readErr)
	}
	if closeErr != nil {
		return fixtureError(invalidCode, operation, path, closeErr)
	}
	return nil
}

func validateOptionalSourceAssetDBLock(path string) error {
	info, err := os.Lstat(path)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return fixtureError(CodePhysicalLibraryDangling, "inspect-source-asset-db-lock", path, err)
	}
	reparse, err := physicalPathIsReparse(path)
	if err != nil {
		return fixtureError(CodePhysicalLibraryDangling, "inspect-source-asset-db-lock", path, err)
	}
	if !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 || reparse {
		return fixtureError(CodePhysicalLibraryInvalidDB, "validate-source-asset-db-lock", path, fmt.Errorf("lock must be a regular non-reparse file when present"))
	}
	return nil
}

func copyPhysicalDirectory(ctx context.Context, source, destination, destinationRoot string, result *PhysicalLibraryMaterialization) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	entries, err := os.ReadDir(source)
	if err != nil {
		return fixtureError(CodePhysicalLibraryDangling, "read-source-directory", source, err)
	}
	for _, entry := range entries {
		if err := ctx.Err(); err != nil {
			return err
		}
		sourcePath := filepath.Join(source, entry.Name())
		destinationPath := filepath.Join(destination, entry.Name())
		if !physicalPathWithin(destinationRoot, destinationPath) {
			return fixtureError(CodePhysicalLibraryCopyEscaped, "map-destination", destinationPath, fmt.Errorf("path escaped destination root"))
		}
		info, err := os.Lstat(sourcePath)
		if err != nil {
			return fixtureError(CodePhysicalLibraryDangling, "inspect-source-entry", sourcePath, err)
		}
		reparse, attrErr := physicalPathIsReparse(sourcePath)
		if attrErr != nil {
			return fixtureError(CodePhysicalLibraryDangling, "inspect-source-entry", sourcePath, attrErr)
		}
		if info.Mode()&os.ModeSymlink != 0 || reparse {
			return fixtureError(CodeNestedReparsePointFound, "copy-directory-contents", sourcePath, fmt.Errorf("nested reparse points are not followed"))
		}
		if info.IsDir() {
			if err := os.Mkdir(destinationPath, info.Mode().Perm()); err != nil {
				return fixtureError(CodePhysicalLibraryCopyEscaped, "create-destination-directory", destinationPath, err)
			}
			if err := copyPhysicalDirectory(ctx, sourcePath, destinationPath, destinationRoot, result); err != nil {
				return err
			}
			continue
		}
		if !info.Mode().IsRegular() {
			return fixtureError(CodeNestedReparsePointFound, "copy-directory-contents", sourcePath, fmt.Errorf("unsupported non-regular entry: %s", info.Mode()))
		}
		if err := copyPhysicalFile(ctx, sourcePath, destinationPath, info.Mode().Perm()); err != nil {
			return err
		}
		result.FileCount++
		result.LogicalBytes += info.Size()
	}
	return nil
}

func copyPhysicalFile(ctx context.Context, source, destination string, mode os.FileMode) error {
	in, err := os.Open(source)
	if err != nil {
		return fixtureError(CodePhysicalLibraryDangling, "open-source-file", source, err)
	}
	defer in.Close()
	out, err := os.OpenFile(destination, os.O_WRONLY|os.O_CREATE|os.O_EXCL, mode)
	if err != nil {
		return fixtureError(CodePhysicalLibraryCopyEscaped, "create-destination-file", destination, err)
	}
	_, copyErr := io.Copy(out, &physicalContextReader{ctx: ctx, reader: in})
	closeErr := out.Close()
	if copyErr != nil {
		return fixtureError(CodePhysicalLibraryDangling, "copy-source-file", source, copyErr)
	}
	if closeErr != nil {
		return fixtureError(CodePhysicalLibraryCopyEscaped, "close-destination-file", destination, closeErr)
	}
	return nil
}

type physicalContextReader struct {
	ctx    context.Context
	reader io.Reader
}

func (r *physicalContextReader) Read(buffer []byte) (int, error) {
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

func physicalPathsOverlap(left, right string) bool {
	left, right = filepath.Clean(left), filepath.Clean(right)
	if runtime.GOOS == "windows" {
		left, right = strings.ToLower(left), strings.ToLower(right)
	}
	return physicalPathWithin(left, right) || physicalPathWithin(right, left)
}

func physicalPathWithin(root, candidate string) bool {
	relative, err := filepath.Rel(root, candidate)
	return err == nil && (relative == "." || (relative != ".." && !strings.HasPrefix(relative, ".."+string(os.PathSeparator))))
}

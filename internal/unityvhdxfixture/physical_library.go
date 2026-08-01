package unityvhdxfixture

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/Kubonsang/testplay-runner/internal/mountedcopy"
)

// PhysicalLibraryMaterialization describes a fixture-only physical copy. The
// source root may be a verified Windows volume mount, but the destination is
// always a new ordinary directory. The production PhysicalCopyMaterializer's
// link-preserving semantics are intentionally unchanged.
type PhysicalLibraryMaterialization = mountedcopy.Result

// MaterializeDirectoryContents copies the children of sourceRoot rather than
// copying sourceRoot's reparse metadata. Only a verified Windows volume mount
// may be dereferenced. Nested reparse points are never followed or reproduced.
func MaterializeDirectoryContents(ctx context.Context, sourceRoot, destination string) (result *PhysicalLibraryMaterialization, returnErr error) {
	result, err := mountedcopy.Contents(ctx, sourceRoot, destination)
	if err == nil {
		return result, nil
	}
	code := CodePhysicalLibraryCopyEscaped
	switch mountedcopy.ErrorCode(err) {
	case mountedcopy.CodeInvalidSource:
		code = CodePhysicalLibraryDangling
	case mountedcopy.CodeSourceNotDirectory:
		code = CodePhysicalLibraryNotDirectory
	case mountedcopy.CodeRootNotVolumeMount:
		code = CodePhysicalLibraryIsReparse
	case mountedcopy.CodeNestedReparse:
		code = CodeNestedReparsePointFound
	}
	return result, fixtureError(code, "materialize-directory-contents", destination, err)
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
	reparse, attrErr := mountedcopy.IsReparsePoint(path)
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
	assembliesReparse, err := mountedcopy.IsReparsePoint(scriptAssemblies)
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
	reparse, err := mountedcopy.IsReparsePoint(path)
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
	reparse, err := mountedcopy.IsReparsePoint(path)
	if err != nil {
		return fixtureError(CodePhysicalLibraryDangling, "inspect-source-asset-db-lock", path, err)
	}
	if !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 || reparse {
		return fixtureError(CodePhysicalLibraryInvalidDB, "validate-source-asset-db-lock", path, fmt.Errorf("lock must be a regular non-reparse file when present"))
	}
	return nil
}

func absoluteCleanPath(path string) (string, error) {
	if !filepath.IsAbs(path) {
		return "", fmt.Errorf("absolute path required")
	}
	return filepath.Clean(path), nil
}

func physicalPathIsReparse(path string) (bool, error) {
	return mountedcopy.IsReparsePoint(path)
}

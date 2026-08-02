package storagehelper

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

type validatedPaths struct{ StoreRoot, WorkspaceRoot, ParentPath, ChildPath, MountPath string }

func validateAcquirePaths(request Request) (validatedPaths, error) {
	store, err := validateRoot(request.StoreRoot, CodeInvalidStoreRoot)
	if err != nil {
		return validatedPaths{}, err
	}
	workspace, err := validateRoot(request.WorkspaceRoot, CodeInvalidWorkspaceRoot)
	if err != nil {
		return validatedPaths{}, err
	}
	parent, err := validateAbsolutePath(request.ParentPath, CodeInvalidParentPath)
	if err != nil {
		return validatedPaths{}, err
	}
	child, err := validateAbsolutePath(request.ChildPath, CodeInvalidChildPath)
	if err != nil {
		return validatedPaths{}, err
	}
	mount, err := validateAbsolutePath(request.MountPath, CodeInvalidMountPath)
	if err != nil {
		return validatedPaths{}, err
	}
	if samePath(parent, child) {
		return validatedPaths{}, helperError(CodeInvalidChildPath, "validate-child", child, fmt.Errorf("parent and child paths must differ"))
	}
	if !strings.EqualFold(filepath.Ext(parent), ".vhdx") {
		return validatedPaths{}, helperError(CodeInvalidParentPath, "validate-parent", parent, fmt.Errorf("parent must use the .vhdx extension"))
	}
	if !strings.EqualFold(filepath.Ext(child), ".vhdx") {
		return validatedPaths{}, helperError(CodeInvalidChildPath, "validate-child", child, fmt.Errorf("child must use the .vhdx extension"))
	}
	if !pathWithinOrEqual(store, child, false) {
		return validatedPaths{}, helperError(CodeInvalidChildPath, "validate-child-root", child, fmt.Errorf("child must be below storeRoot"))
	}
	if !pathWithinOrEqual(workspace, mount, false) {
		return validatedPaths{}, helperError(CodeInvalidMountPath, "validate-mount-root", mount, fmt.Errorf("mount must be below workspaceRoot"))
	}
	if err := ensureResolvedWithin(store, filepath.Dir(child), CodeInvalidChildPath); err != nil {
		return validatedPaths{}, err
	}
	if err := ensureResolvedWithin(workspace, filepath.Dir(mount), CodeInvalidMountPath); err != nil {
		return validatedPaths{}, err
	}
	parentInfo, err := os.Lstat(parent)
	if os.IsNotExist(err) {
		return validatedPaths{}, helperError(CodeParentNotFound, "stat-parent", parent, err)
	}
	if err != nil {
		return validatedPaths{}, helperError(CodeParentInvalid, "stat-parent", parent, err)
	}
	if !parentInfo.Mode().IsRegular() || parentInfo.Mode()&os.ModeSymlink != 0 {
		return validatedPaths{}, helperError(CodeParentInvalid, "validate-parent", parent, fmt.Errorf("parent must be a regular non-link file"))
	}
	resolvedParentDir, err := filepath.EvalSymlinks(filepath.Dir(parent))
	if err != nil {
		return validatedPaths{}, helperError(CodeInvalidParentPath, "resolve-parent-directory", parent, err)
	}
	if !samePath(filepath.Dir(parent), resolvedParentDir) {
		return validatedPaths{}, helperError(CodeInvalidParentPath, "validate-parent-directory", parent, fmt.Errorf("parent path traverses a symlink or reparse point"))
	}
	if _, err := os.Lstat(child); err == nil {
		return validatedPaths{}, helperError(CodeChildExists, "stat-child", child, nil)
	} else if !os.IsNotExist(err) {
		return validatedPaths{}, helperError(CodeInvalidChildPath, "stat-child", child, err)
	}
	if info, err := os.Lstat(mount); err == nil {
		if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			return validatedPaths{}, helperError(CodeInvalidMountPath, "validate-mount", mount, fmt.Errorf("mount path must be a real directory"))
		}
		entries, readErr := os.ReadDir(mount)
		if readErr != nil {
			return validatedPaths{}, helperError(CodeInvalidMountPath, "read-mount", mount, readErr)
		}
		if len(entries) != 0 {
			return validatedPaths{}, helperError(CodeMountPathNotEmpty, "validate-mount", mount, fmt.Errorf("mount path is not empty"))
		}
	} else if !os.IsNotExist(err) {
		return validatedPaths{}, helperError(CodeInvalidMountPath, "stat-mount", mount, err)
	}
	return validatedPaths{store, workspace, parent, child, mount}, nil
}

func validateRoot(path, code string) (string, error) {
	clean, err := validateAbsolutePath(path, code)
	if err != nil {
		return "", err
	}
	info, err := os.Lstat(clean)
	if err != nil {
		return "", helperError(code, "stat-root", clean, err)
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return "", helperError(code, "validate-root", clean, fmt.Errorf("root must be a real directory"))
	}
	resolved, err := filepath.EvalSymlinks(clean)
	if err != nil {
		return "", helperError(code, "resolve-root", clean, err)
	}
	if !samePath(clean, resolved) {
		return "", helperError(code, "validate-root", clean, fmt.Errorf("root resolves through a symlink or reparse point"))
	}
	return clean, nil
}

func validateAbsolutePath(path, code string) (string, error) {
	if path == "" || !filepath.IsAbs(path) {
		return "", helperError(code, "validate-path", path, fmt.Errorf("path must be absolute"))
	}
	clean := filepath.Clean(path)
	if strings.HasPrefix(clean, `\\`) || strings.HasPrefix(clean, "//") {
		return "", helperError(code, "validate-path", clean, fmt.Errorf("network paths are forbidden"))
	}
	volume := filepath.VolumeName(clean)
	if clean == string(os.PathSeparator) || (volume != "" && samePath(clean, volume+string(os.PathSeparator))) {
		return "", helperError(code, "validate-path", clean, fmt.Errorf("drive root is forbidden"))
	}
	return clean, nil
}

func ensureResolvedWithin(root, target, code string) error {
	info, err := os.Lstat(target)
	if err != nil {
		return helperError(code, "stat-parent", target, err)
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return helperError(code, "validate-parent", target, fmt.Errorf("parent must be a real directory"))
	}
	resolved, err := filepath.EvalSymlinks(target)
	if err != nil {
		return helperError(code, "resolve-parent", target, err)
	}
	if !pathWithinOrEqual(root, resolved, true) {
		return helperError(code, "validate-resolved-path", target, fmt.Errorf("resolved path escapes its root"))
	}
	return nil
}

func pathWithinOrEqual(root, target string, allowEqual bool) bool {
	rel, err := filepath.Rel(root, target)
	if err != nil || filepath.IsAbs(rel) {
		return false
	}
	if rel == "." {
		return allowEqual
	}
	return rel != ".." && !strings.HasPrefix(rel, ".."+string(os.PathSeparator))
}
func samePath(left, right string) bool {
	if runtime.GOOS == "windows" {
		return strings.EqualFold(filepath.Clean(left), filepath.Clean(right))
	}
	return filepath.Clean(left) == filepath.Clean(right)
}

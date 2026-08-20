package refsworkspace

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

func NewPaths(config Config) (Config, Paths, error) {
	if config.Root == "" || !filepath.IsAbs(config.Root) {
		return Config{}, Paths{}, newError(CodeInvalidConfiguration, "validate-root", config.Root, fmt.Errorf("pool root must be absolute"))
	}
	requestedRoot := filepath.Clean(config.Root)
	root, err := resolveConfiguredRoot(requestedRoot)
	if err != nil {
		return Config{}, Paths{}, err
	}
	if config.MaximumBytes == 0 {
		config.MaximumBytes = DefaultMaximumBytes
	}
	if config.WorkerReserveBytes == 0 {
		config.WorkerReserveBytes = DefaultReserveBytes
	}
	if config.SoftBudgetBytes == 0 {
		config.SoftBudgetBytes = DefaultSoftBudget
	}
	if config.MinimumHostFreeBytes == 0 {
		config.MinimumHostFreeBytes = DefaultMinimumHostFreeBytes
	}
	if config.VHDXOverheadReserveBytes == 0 {
		config.VHDXOverheadReserveBytes = DefaultVHDXOverheadReserveBytes
	}
	budgetAndReserve, budgetOK := checkedAddInt64(config.SoftBudgetBytes, config.WorkerReserveBytes)
	if config.MaximumBytes < MinimumDevDriveVHDXBytes || config.MaximumBytes%512 != 0 || config.SoftBudgetBytes <= 0 || config.WorkerReserveBytes <= 0 || !budgetOK || budgetAndReserve > config.MaximumBytes {
		return Config{}, Paths{}, newError(CodeInvalidConfiguration, "validate-storage-ceiling", root, fmt.Errorf("require Dev Drive maximum >= 50 GiB and soft budget + reserve <= maximum"))
	}
	if config.MinimumHostFreeBytes < 0 || config.VHDXOverheadReserveBytes < 0 {
		return Config{}, Paths{}, newError(CodeInvalidConfiguration, "validate-host-free-floor", root, fmt.Errorf("host free floor and VHDX overhead reserve must not be negative"))
	}
	config.Root = root
	vhdxPath := filepath.Join(root, "managed-library-pool.vhdx")
	if config.VHDXPath != "" {
		if !filepath.IsAbs(config.VHDXPath) {
			return Config{}, Paths{}, newError(CodeInvalidConfiguration, "validate-vhdx-path", config.VHDXPath, fmt.Errorf("VHDX path must be absolute"))
		}
		requested := filepath.Clean(config.VHDXPath)
		if filepath.Dir(requested) != requestedRoot {
			return Config{}, Paths{}, newError(CodeInvalidConfiguration, "validate-vhdx-path", requested, fmt.Errorf("VHDX must be a direct child of the requested root"))
		}
		vhdxPath = filepath.Join(root, filepath.Base(requested))
	}
	mount := filepath.Join(root, "mount")
	if config.MountRoot != "" {
		if !filepath.IsAbs(config.MountRoot) {
			return Config{}, Paths{}, newError(CodeInvalidConfiguration, "validate-mount-root", config.MountRoot, fmt.Errorf("mount root must be absolute"))
		}
		requested := filepath.Clean(config.MountRoot)
		if filepath.Dir(requested) != requestedRoot {
			return Config{}, Paths{}, newError(CodeInvalidConfiguration, "validate-mount-root", requested, fmt.Errorf("mount must be a direct child of the requested root"))
		}
		mount = filepath.Join(root, filepath.Base(requested))
	}
	if filepath.Dir(vhdxPath) != root || filepath.Dir(mount) != root {
		return Config{}, Paths{}, newError(CodeInvalidConfiguration, "validate-owned-overrides", root, fmt.Errorf("VHDX and mount must be direct children of the storage root"))
	}
	config.VHDXPath = vhdxPath
	config.MountRoot = mount
	poolRoot := filepath.Join(mount, "testplay")
	return config, Paths{
		Root:         root,
		VHDX:         vhdxPath,
		Owner:        filepath.Join(root, "pool-owner.json"),
		PendingOwner: filepath.Join(root, "pool-owner.pending.json"),
		Mount:        mount,
		PoolRoot:     poolRoot,
		PoolFile:     filepath.Join(poolRoot, "pool.json"),
		Baselines:    filepath.Join(poolRoot, "baselines"),
		Workers:      filepath.Join(poolRoot, "workers"),
		Leases:       filepath.Join(poolRoot, "leases"),
		Quarantine:   filepath.Join(poolRoot, "quarantine"),
	}, nil
}

func resolveConfiguredRoot(requested string) (string, error) {
	if requested == "" || !filepath.IsAbs(requested) {
		return "", newError(CodeInvalidConfiguration, "validate-root", requested, fmt.Errorf("pool root must be absolute"))
	}
	requested = filepath.Clean(requested)
	if isFilesystemRoot(requested) {
		return "", newError(CodeInvalidConfiguration, "validate-root", requested, fmt.Errorf("filesystem root is forbidden"))
	}
	ancestor, missing, err := nearestExistingAncestor(requested)
	if err != nil {
		return "", newError(CodeInvalidConfiguration, "find-root-ancestor", requested, err)
	}
	reparse, err := inspectPathReparse(ancestor)
	if err != nil {
		return "", newError(CodeInvalidConfiguration, "inspect-root-ancestor", ancestor, err)
	}
	if reparse {
		return "", newError(CodeOwnershipMismatch, "inspect-root-ancestor", ancestor, fmt.Errorf("existing ancestor is a symlink or reparse point"))
	}
	canonical, err := filepath.EvalSymlinks(ancestor)
	if err != nil {
		return "", newError(CodeInvalidConfiguration, "canonical-root-ancestor", ancestor, err)
	}
	for index := len(missing) - 1; index >= 0; index-- {
		canonical = filepath.Join(canonical, missing[index])
	}
	return filepath.Clean(canonical), nil
}

// PrepareOwnedRoot creates a fresh storage path one segment at a time while
// rejecting filesystem roots, files, symlinks, and Windows reparse points.
func PrepareOwnedRoot(root string) (string, error) {
	root = filepath.Clean(root)
	if !filepath.IsAbs(root) || isFilesystemRoot(root) {
		return "", newError(CodeInvalidConfiguration, "prepare-owned-root", root, fmt.Errorf("safe absolute non-root path required"))
	}
	ancestor, missing, err := nearestExistingAncestor(root)
	if err != nil {
		return "", newError(CodeInvalidConfiguration, "find-root-ancestor", root, err)
	}
	info, err := os.Lstat(ancestor)
	if err != nil || !info.IsDir() {
		return "", newError(CodeOwnershipMismatch, "inspect-root-ancestor", ancestor, errors.Join(err, fmt.Errorf("existing ancestor is not a directory")))
	}
	reparse, err := inspectPathReparse(ancestor)
	if err != nil || reparse {
		return "", newError(CodeOwnershipMismatch, "inspect-root-ancestor", ancestor, errors.Join(err, fmt.Errorf("existing ancestor is a symlink or reparse point")))
	}
	canonical, err := filepath.EvalSymlinks(ancestor)
	if err != nil {
		return "", newError(CodeInvalidConfiguration, "canonical-root-ancestor", ancestor, err)
	}
	expected := canonical
	for index := len(missing) - 1; index >= 0; index-- {
		expected = filepath.Join(expected, missing[index])
	}
	if filepath.Clean(expected) != root {
		return "", newError(CodeOwnershipMismatch, "canonical-root", root, fmt.Errorf("canonical target is %q", expected))
	}
	current := canonical
	for index := len(missing) - 1; index >= 0; index-- {
		current = filepath.Join(current, missing[index])
		if err := os.Mkdir(current, 0700); err != nil && !os.IsExist(err) {
			return "", newError(CodePoolCorrupt, "create-owned-root-segment", current, err)
		}
		info, err := os.Lstat(current)
		if err != nil || !info.IsDir() {
			return "", newError(CodeOwnershipMismatch, "inspect-owned-root-segment", current, errors.Join(err, fmt.Errorf("created segment is not a directory")))
		}
		reparse, err := inspectPathReparse(current)
		if err != nil || reparse {
			return "", newError(CodeOwnershipMismatch, "inspect-owned-root-segment", current, errors.Join(err, fmt.Errorf("created segment is a symlink or reparse point")))
		}
	}
	final, err := filepath.EvalSymlinks(root)
	if err != nil || filepath.Clean(final) != root {
		return "", newError(CodeOwnershipMismatch, "canonical-owned-root", root, errors.Join(err, fmt.Errorf("canonical result is %q", final)))
	}
	return root, nil
}

func nearestExistingAncestor(path string) (string, []string, error) {
	current := filepath.Clean(path)
	var missing []string
	for {
		_, err := os.Lstat(current)
		if err == nil {
			return current, missing, nil
		}
		if !os.IsNotExist(err) {
			return "", nil, err
		}
		parent := filepath.Dir(current)
		if parent == current {
			return "", nil, err
		}
		missing = append(missing, filepath.Base(current))
		current = parent
	}
}

func isFilesystemRoot(path string) bool {
	volume := filepath.VolumeName(path)
	return path == string(os.PathSeparator) || (volume != "" && strings.EqualFold(path, volume+string(os.PathSeparator)))
}

func PathWithin(root, target string) bool {
	root = filepath.Clean(root)
	target = filepath.Clean(target)
	rel, err := filepath.Rel(root, target)
	if err != nil || rel == "." || filepath.IsAbs(rel) {
		return false
	}
	return rel != ".." && !strings.HasPrefix(rel, ".."+string(os.PathSeparator))
}

func validateOwnedPaths(paths Paths) error {
	for _, target := range []string{paths.VHDX, paths.Owner, paths.PendingOwner, paths.Mount} {
		if !PathWithin(paths.Root, target) || filepath.Dir(target) != paths.Root {
			return newError(CodeOwnershipMismatch, "validate-owned-path", target, fmt.Errorf("target escaped pool root"))
		}
	}
	if !PathWithin(paths.Mount, paths.PoolRoot) || filepath.Dir(paths.PoolRoot) != paths.Mount {
		return newError(CodeOwnershipMismatch, "validate-pool-path", paths.PoolRoot, fmt.Errorf("pool path escaped mount"))
	}
	return nil
}

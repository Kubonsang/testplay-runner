package refsworkspace

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

func NewPaths(config Config) (Config, Paths, error) {
	if config.Root == "" || !filepath.IsAbs(config.Root) {
		return Config{}, Paths{}, newError(CodeInvalidConfiguration, "validate-root", config.Root, fmt.Errorf("pool root must be absolute"))
	}
	root := filepath.Clean(config.Root)
	resolvedParent, err := filepath.EvalSymlinks(filepath.Dir(root))
	if err != nil {
		return Config{}, Paths{}, newError(CodeInvalidConfiguration, "canonical-root-parent", filepath.Dir(root), err)
	}
	root = filepath.Join(filepath.Clean(resolvedParent), filepath.Base(root))
	volume := filepath.VolumeName(root)
	if root == string(os.PathSeparator) || (volume != "" && strings.EqualFold(root, volume+string(os.PathSeparator))) {
		return Config{}, Paths{}, newError(CodeInvalidConfiguration, "validate-root", root, fmt.Errorf("filesystem root is forbidden"))
	}
	if config.MaximumBytes == 0 {
		config.MaximumBytes = DefaultMaximumBytes
	}
	if config.WorkerReserveBytes == 0 {
		config.WorkerReserveBytes = DefaultReserveBytes
	}
	if config.SoftBudgetBytes == 0 {
		config.SoftBudgetBytes = config.MaximumBytes - config.WorkerReserveBytes
	}
	if config.MaximumBytes < 8<<30 || config.MaximumBytes%512 != 0 || config.SoftBudgetBytes <= 0 || config.WorkerReserveBytes <= 0 || config.SoftBudgetBytes+config.WorkerReserveBytes > config.MaximumBytes {
		return Config{}, Paths{}, newError(CodeInvalidConfiguration, "validate-storage-ceiling", root, fmt.Errorf("require maximum >= 8 GiB and soft budget + reserve <= maximum"))
	}
	config.Root = root
	vhdxPath := filepath.Join(root, "managed-library-pool.vhdx")
	if config.VHDXPath != "" {
		if !filepath.IsAbs(config.VHDXPath) {
			return Config{}, Paths{}, newError(CodeInvalidConfiguration, "validate-vhdx-path", config.VHDXPath, fmt.Errorf("VHDX path must be absolute"))
		}
		vhdxPath = filepath.Clean(config.VHDXPath)
	}
	mount := filepath.Join(root, "mount")
	if config.MountRoot != "" {
		if !filepath.IsAbs(config.MountRoot) {
			return Config{}, Paths{}, newError(CodeInvalidConfiguration, "validate-mount-root", config.MountRoot, fmt.Errorf("mount root must be absolute"))
		}
		mount = filepath.Clean(config.MountRoot)
	}
	if filepath.Dir(vhdxPath) != root || filepath.Dir(mount) != root {
		return Config{}, Paths{}, newError(CodeInvalidConfiguration, "validate-owned-overrides", root, fmt.Errorf("VHDX and mount must be direct children of the storage root"))
	}
	config.VHDXPath = vhdxPath
	config.MountRoot = mount
	poolRoot := filepath.Join(mount, "testplay")
	return config, Paths{
		Root:       root,
		VHDX:       vhdxPath,
		Owner:      filepath.Join(root, "pool-owner.json"),
		Mount:      mount,
		PoolRoot:   poolRoot,
		PoolFile:   filepath.Join(poolRoot, "pool.json"),
		Baselines:  filepath.Join(poolRoot, "baselines"),
		Workers:    filepath.Join(poolRoot, "workers"),
		Leases:     filepath.Join(poolRoot, "leases"),
		Quarantine: filepath.Join(poolRoot, "quarantine"),
	}, nil
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
	for _, target := range []string{paths.VHDX, paths.Owner, paths.Mount} {
		if !PathWithin(paths.Root, target) || filepath.Dir(target) != paths.Root {
			return newError(CodeOwnershipMismatch, "validate-owned-path", target, fmt.Errorf("target escaped pool root"))
		}
	}
	if !PathWithin(paths.Mount, paths.PoolRoot) || filepath.Dir(paths.PoolRoot) != paths.Mount {
		return newError(CodeOwnershipMismatch, "validate-pool-path", paths.PoolRoot, fmt.Errorf("pool path escaped mount"))
	}
	return nil
}

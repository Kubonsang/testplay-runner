package refsworkspace

import (
	"os"
	"path/filepath"
	"testing"
)

func TestNewPathsSeparatesHardAndSoftCeilings(t *testing.T) {
	root := filepath.Join(t.TempDir(), "storage")
	config, paths, err := NewPaths(Config{Root: root})
	if err != nil {
		t.Fatal(err)
	}
	if config.MaximumBytes != DefaultMaximumBytes || config.SoftBudgetBytes != DefaultSoftBudget || config.WorkerReserveBytes != DefaultReserveBytes || config.MinimumHostFreeBytes != DefaultMinimumHostFreeBytes || config.VHDXOverheadReserveBytes != DefaultVHDXOverheadReserveBytes {
		t.Fatalf("config=%+v", config)
	}
	if !PathWithin(paths.Root, paths.VHDX) || !PathWithin(paths.Mount, paths.PoolRoot) {
		t.Fatalf("paths=%+v", paths)
	}
}

func TestPrepareOwnedRootSupportsMissingParents(t *testing.T) {
	requested := filepath.Join(t.TempDir(), "TestPlay", "Storage")
	_, paths, err := NewPaths(Config{Root: requested})
	if err != nil {
		t.Fatal(err)
	}
	prepared, err := PrepareOwnedRoot(paths.Root)
	if err != nil {
		t.Fatal(err)
	}
	if prepared != paths.Root {
		t.Fatalf("prepared=%q root=%q", prepared, paths.Root)
	}
	for _, path := range []string{filepath.Dir(paths.Root), paths.Root} {
		info, err := os.Lstat(path)
		if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			t.Fatalf("unsafe created path %q: %v", path, err)
		}
	}
}

func TestNewPathsRejectsIntermediateSymlink(t *testing.T) {
	base := t.TempDir()
	target := filepath.Join(base, "real")
	if err := os.Mkdir(target, 0700); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(base, "link")
	if err := os.Symlink(target, link); err != nil {
		if reason := symlinkFixtureUnavailableReason(err); reason != "" {
			t.Skip(reason)
		}
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Remove(link) })
	if _, _, err := NewPaths(Config{Root: filepath.Join(link, "Storage")}); ErrorCode(err) != CodeOwnershipMismatch {
		t.Fatalf("err=%v", err)
	}
}

func TestNewPathsRejectsIntermediateReparsePoint(t *testing.T) {
	base := t.TempDir()
	target := filepath.Join(base, "real")
	if err := os.Mkdir(target, 0700); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(base, "reparse")
	createPathReparseFixture(t, target, link)
	if _, _, err := NewPaths(Config{Root: filepath.Join(link, "Storage")}); ErrorCode(err) != CodeOwnershipMismatch {
		t.Fatalf("err=%v", err)
	}
}

func TestPrepareOwnedRootRejectsFileAndFilesystemRoot(t *testing.T) {
	file := filepath.Join(t.TempDir(), "storage")
	if err := os.WriteFile(file, []byte("not a directory"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := PrepareOwnedRoot(file); ErrorCode(err) != CodeOwnershipMismatch {
		t.Fatalf("file err=%v", err)
	}
	if _, err := PrepareOwnedRoot(string(os.PathSeparator)); ErrorCode(err) != CodeInvalidConfiguration {
		t.Fatalf("root err=%v", err)
	}
}

func TestPrepareSetupRootAcceptsEmptyAndRejectsNonEmpty(t *testing.T) {
	empty := filepath.Join(t.TempDir(), "empty")
	if err := os.Mkdir(empty, 0700); err != nil {
		t.Fatal(err)
	}
	_, emptyPaths, err := NewPaths(Config{Root: empty})
	if err != nil {
		t.Fatal(err)
	}
	if err := prepareSetupRoot(emptyPaths); err != nil {
		t.Fatal(err)
	}

	nonempty := filepath.Join(t.TempDir(), "nonempty")
	if err := os.Mkdir(nonempty, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(nonempty, "foreign"), []byte("x"), 0600); err != nil {
		t.Fatal(err)
	}
	_, nonemptyPaths, err := NewPaths(Config{Root: nonempty})
	if err != nil {
		t.Fatal(err)
	}
	if err := prepareSetupRoot(nonemptyPaths); ErrorCode(err) != CodePoolCorrupt {
		t.Fatalf("err=%v", err)
	}
}

func TestPrepareOwnedRootUsesReparseInspectionSeam(t *testing.T) {
	original := inspectPathReparse
	t.Cleanup(func() { inspectPathReparse = original })
	ancestor := t.TempDir()
	inspectPathReparse = func(path string) (bool, error) { return path == ancestor, nil }
	if _, err := PrepareOwnedRoot(filepath.Join(ancestor, "Storage")); ErrorCode(err) != CodeOwnershipMismatch {
		t.Fatalf("err=%v", err)
	}
}

func TestNewPathsRejectsBudgetWithoutEmergencyReserve(t *testing.T) {
	_, _, err := NewPaths(Config{
		Root:               filepath.Join(t.TempDir(), "storage"),
		MaximumBytes:       64 << 30,
		SoftBudgetBytes:    63 << 30,
		WorkerReserveBytes: 2 << 30,
	})
	if ErrorCode(err) != CodeInvalidConfiguration {
		t.Fatalf("err=%v", err)
	}
}

func TestNewPathsKeepsDefaultSoftBudgetIndependentFromMaximum(t *testing.T) {
	config, _, err := NewPaths(Config{Root: filepath.Join(t.TempDir(), "storage"), MaximumBytes: 80 << 30})
	if err != nil {
		t.Fatal(err)
	}
	if config.MaximumBytes != 80<<30 || config.SoftBudgetBytes != DefaultSoftBudget {
		t.Fatalf("config=%+v", config)
	}
}

func TestNewPathsRejectsDevDriveBelowFiftyGiB(t *testing.T) {
	_, _, err := NewPaths(Config{Root: filepath.Join(t.TempDir(), "storage"), MaximumBytes: MinimumDevDriveVHDXBytes - 512})
	if ErrorCode(err) != CodeInvalidConfiguration {
		t.Fatalf("err=%v", err)
	}
}

func TestPathWithinRejectsSiblingPrefix(t *testing.T) {
	root := filepath.Join(t.TempDir(), "pool")
	if PathWithin(root, root) || PathWithin(root, root+"-other") || PathWithin(root, filepath.Dir(root)) {
		t.Fatal("escaped path accepted")
	}
	if !PathWithin(root, filepath.Join(root, "child")) {
		t.Fatal("direct child rejected")
	}
}

package refsworkspace

import (
	"path/filepath"
	"testing"
)

func TestNewPathsSeparatesHardAndSoftCeilings(t *testing.T) {
	root := filepath.Join(t.TempDir(), "storage")
	config, paths, err := NewPaths(Config{Root: root})
	if err != nil {
		t.Fatal(err)
	}
	if config.MaximumBytes != DefaultMaximumBytes || config.SoftBudgetBytes != DefaultSoftBudget || config.WorkerReserveBytes != DefaultReserveBytes {
		t.Fatalf("config=%+v", config)
	}
	if !PathWithin(paths.Root, paths.VHDX) || !PathWithin(paths.Mount, paths.PoolRoot) {
		t.Fatalf("paths=%+v", paths)
	}
}

func TestNewPathsRejectsBudgetWithoutEmergencyReserve(t *testing.T) {
	_, _, err := NewPaths(Config{
		Root:               filepath.Join(t.TempDir(), "storage"),
		MaximumBytes:       8 << 30,
		SoftBudgetBytes:    7 << 30,
		WorkerReserveBytes: 2 << 30,
	})
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

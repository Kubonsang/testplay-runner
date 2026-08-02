package mountedcopy

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestContentsCopiesOrdinaryTreeAndSurvivesSourceRemoval(t *testing.T) {
	root := t.TempDir()
	source := filepath.Join(root, "source")
	destination := filepath.Join(root, "destination")
	if err := os.MkdirAll(filepath.Join(source, "nested"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(source, "nested", "value.bin"), []byte("value"), 0600); err != nil {
		t.Fatal(err)
	}
	result, err := Contents(context.Background(), source, destination)
	if err != nil {
		t.Fatal(err)
	}
	if result.FileCount != 1 || result.LogicalBytes != 5 {
		t.Fatalf("result = %#v", result)
	}
	if err := os.RemoveAll(source); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(destination, "nested", "value.bin"))
	if err != nil || string(data) != "value" {
		t.Fatalf("data=%q err=%v", data, err)
	}
	reparse, err := IsReparsePoint(destination)
	if err != nil || reparse {
		t.Fatalf("destination reparse=%t err=%v", reparse, err)
	}
}

func TestContentsRejectsOverlap(t *testing.T) {
	source := filepath.Join(t.TempDir(), "source")
	if err := os.Mkdir(source, 0700); err != nil {
		t.Fatal(err)
	}
	_, err := Contents(context.Background(), source, filepath.Join(source, "child"))
	if ErrorCode(err) != CodeRootOverlap {
		t.Fatalf("error=%v", err)
	}
}

func TestContentsRejectsNestedReparse(t *testing.T) {
	root := t.TempDir()
	source := filepath.Join(root, "source")
	target := filepath.Join(root, "target")
	if err := os.Mkdir(source, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(target, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, filepath.Join(source, "nested-link")); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	_, err := Contents(context.Background(), source, filepath.Join(root, "destination"))
	if ErrorCode(err) != CodeNestedReparse {
		t.Fatalf("error=%v", err)
	}
}

//go:build windows

package shadow

import (
	"os"
	"path/filepath"
	"testing"
)

func TestCreateDirectoryJunction_NoDeveloperModeFallback(t *testing.T) {
	root := filepath.Join(t.TempDir(), "paths with spaces")
	src := filepath.Join(root, "source Packages")
	dst := filepath.Join(root, "shadow Packages")
	if err := os.MkdirAll(src, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(src, "sentinel.txt"), []byte("junction-ok"), 0644); err != nil {
		t.Fatal(err)
	}

	// Call the fallback directly so this test does not become a symlink test on
	// Developer Mode/CI machines where os.Symlink happens to be privileged.
	if err := createDirectoryJunction(src, dst); err != nil {
		t.Fatalf("junction fallback failed: %v", err)
	}
	data, err := os.ReadFile(filepath.Join(dst, "sentinel.txt"))
	if err != nil {
		t.Fatalf("junction target cannot be read: %v", err)
	}
	if string(data) != "junction-ok" {
		t.Fatalf("junction target content = %q, want %q", data, "junction-ok")
	}
}

package shadow_test

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/Kubonsang/testplay-runner/internal/shadow"
)

func TestParallelCopy_CopiesAllFiles(t *testing.T) {
	t.Parallel()
	src := t.TempDir()
	for _, rel := range []string{
		"a/1.txt", "a/2.txt", "b/3.txt", "b/c/4.txt", "5.txt",
	} {
		p := filepath.Join(src, rel)
		must(t, os.MkdirAll(filepath.Dir(p), 0755))
		must(t, os.WriteFile(p, []byte("content-"+rel), 0644))
	}

	dst := filepath.Join(t.TempDir(), "out")
	err := shadow.CopyDirParallel(context.Background(), src, dst, 4)
	if err != nil {
		t.Fatalf("CopyDirParallel: %v", err)
	}

	for _, rel := range []string{
		"a/1.txt", "a/2.txt", "b/3.txt", "b/c/4.txt", "5.txt",
	} {
		got, err := os.ReadFile(filepath.Join(dst, rel))
		if err != nil {
			t.Errorf("missing %s: %v", rel, err)
			continue
		}
		want := "content-" + rel
		if string(got) != want {
			t.Errorf("%s: got %q, want %q", rel, got, want)
		}
	}
}

func TestParallelCopy_PreservesSymlinks(t *testing.T) {
	t.Parallel()
	src := t.TempDir()
	must(t, os.MkdirAll(filepath.Join(src, "dir"), 0755))
	must(t, os.WriteFile(filepath.Join(src, "dir", "real.txt"), []byte("real"), 0644))
	if err := os.Symlink("real.txt", filepath.Join(src, "dir", "link.txt")); err != nil {
		t.Skipf("symlinks not supported: %v", err)
	}

	dst := filepath.Join(t.TempDir(), "out")
	must(t, shadow.CopyDirParallel(context.Background(), src, dst, 4))

	info, err := os.Lstat(filepath.Join(dst, "dir", "link.txt"))
	if err != nil {
		t.Fatalf("missing symlink: %v", err)
	}
	if info.Mode()&os.ModeSymlink == 0 {
		t.Error("expected symlink, got regular file")
	}
	target, _ := os.Readlink(filepath.Join(dst, "dir", "link.txt"))
	if target != "real.txt" {
		t.Errorf("symlink target: got %q, want %q", target, "real.txt")
	}
}

func TestParallelCopy_PreservesFileMode(t *testing.T) {
	t.Parallel()
	src := t.TempDir()
	p := filepath.Join(src, "exec.sh")
	must(t, os.WriteFile(p, []byte("#!/bin/sh"), 0755))

	dst := filepath.Join(t.TempDir(), "out")
	must(t, shadow.CopyDirParallel(context.Background(), src, dst, 4))

	info, err := os.Stat(filepath.Join(dst, "exec.sh"))
	if err != nil {
		t.Fatalf("missing file: %v", err)
	}
	if info.Mode()&0111 == 0 {
		t.Errorf("executable bit lost: mode %v", info.Mode())
	}
}

func TestParallelCopy_RespectsContextCancellation(t *testing.T) {
	t.Parallel()
	src := t.TempDir()
	for i := 0; i < 50; i++ {
		must(t, os.WriteFile(filepath.Join(src, fmt.Sprintf("file%d.txt", i)), []byte("data"), 0644))
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	dst := filepath.Join(t.TempDir(), "out")
	err := shadow.CopyDirParallel(ctx, src, dst, 4)
	if err == nil {
		t.Fatal("expected error from cancelled context")
	}
}

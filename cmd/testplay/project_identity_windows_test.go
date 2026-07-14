//go:build windows

package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestSameProjectPath_ResolvesWindowsJunctionAlias(t *testing.T) {
	root := filepath.Join(t.TempDir(), "identity paths with spaces")
	project := filepath.Join(root, "real project")
	alias := filepath.Join(root, "project junction")
	if err := os.MkdirAll(project, 0755); err != nil {
		t.Fatal(err)
	}
	if out, err := exec.Command("cmd.exe", "/d", "/c", "mklink", "/J", alias, project).CombinedOutput(); err != nil {
		t.Fatalf("create junction fixture: %v: %s", err, out)
	}

	same, known := sameProjectPath(project, alias)
	if !known || !same {
		t.Fatalf("junction comparison = same:%v known:%v; want true, true", same, known)
	}
}

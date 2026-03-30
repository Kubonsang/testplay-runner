package shadow_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/Kubonsang/testplay-runner/internal/shadow"
)

func TestIsLocked_FalseWhenNoTempDir(t *testing.T) {
	dir := t.TempDir()
	if shadow.IsLocked(dir) {
		t.Error("expected false: Temp/ does not exist")
	}
}

func TestIsLocked_FalseWhenLockfileAbsent(t *testing.T) {
	dir := t.TempDir()
	_ = os.MkdirAll(filepath.Join(dir, "Temp"), 0755)
	if shadow.IsLocked(dir) {
		t.Error("expected false: UnityLockfile not present")
	}
}

func TestIsLocked_TrueWhenLockfilePresent(t *testing.T) {
	dir := t.TempDir()
	_ = os.MkdirAll(filepath.Join(dir, "Temp"), 0755)
	f, _ := os.Create(filepath.Join(dir, "Temp", "UnityLockfile"))
	_ = f.Close()
	if !shadow.IsLocked(dir) {
		t.Error("expected true: UnityLockfile exists")
	}
}

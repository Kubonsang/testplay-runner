package shadow_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Kubonsang/testplay-runner/internal/shadow"
)

func TestEnsureIgnored_CreatesFileAndAppendsEntry(t *testing.T) {
	dir := t.TempDir()
	if err := shadow.EnsureIgnored(dir, ".testplay-shadow/"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	data, _ := os.ReadFile(filepath.Join(dir, ".gitignore"))
	if !strings.Contains(string(data), ".testplay-shadow/") {
		t.Error(".testplay-shadow/ not found in created .gitignore")
	}
	if strings.HasPrefix(string(data), "\n") {
		t.Error("file starts with unexpected blank line")
	}
}

func TestEnsureIgnored_AppendsToExistingFile(t *testing.T) {
	dir := t.TempDir()
	_ = os.WriteFile(filepath.Join(dir, ".gitignore"), []byte("*.log\n"), 0644)
	_ = shadow.EnsureIgnored(dir, ".testplay-shadow/")
	data, _ := os.ReadFile(filepath.Join(dir, ".gitignore"))
	if !strings.Contains(string(data), ".testplay-shadow/") {
		t.Error("entry not appended to existing .gitignore")
	}
	if !strings.Contains(string(data), "*.log") {
		t.Error("existing content was lost")
	}
}

func TestEnsureIgnored_NoopWhenAlreadyPresent(t *testing.T) {
	dir := t.TempDir()
	_ = os.WriteFile(filepath.Join(dir, ".gitignore"), []byte(".testplay-shadow/\n"), 0644)
	_ = shadow.EnsureIgnored(dir, ".testplay-shadow/")
	data, _ := os.ReadFile(filepath.Join(dir, ".gitignore"))
	if strings.Count(string(data), ".testplay-shadow/") != 1 {
		t.Error("entry was duplicated")
	}
}

func TestEnsureIgnored_NoDoubleBlankLineWhenFileEndsWithNewline(t *testing.T) {
	dir := t.TempDir()
	// File already ends with \n — the appended entry must not be preceded by a blank line.
	_ = os.WriteFile(filepath.Join(dir, ".gitignore"), []byte("*.log\n"), 0644)
	_ = shadow.EnsureIgnored(dir, ".testplay-shadow/")
	data, _ := os.ReadFile(filepath.Join(dir, ".gitignore"))
	if strings.Contains(string(data), "\n\n") {
		t.Errorf("double blank line found in .gitignore: %q", string(data))
	}
}

func TestEnsureIgnored_AddsNewlineWhenFileHasNoTrailingNewline(t *testing.T) {
	dir := t.TempDir()
	// File has no trailing \n — must separate existing content from new entry.
	_ = os.WriteFile(filepath.Join(dir, ".gitignore"), []byte("*.log"), 0644)
	_ = shadow.EnsureIgnored(dir, ".testplay-shadow/")
	data, _ := os.ReadFile(filepath.Join(dir, ".gitignore"))
	if !strings.Contains(string(data), "*.log\n.testplay-shadow/\n") {
		t.Errorf("expected separator newline, got: %q", string(data))
	}
}

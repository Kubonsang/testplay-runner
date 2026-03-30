package shadow

import (
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/Kubonsang/testplay-runner/internal/history"
)

// Workspace represents an active shadow workspace for a Unity project.
type Workspace struct {
	SourcePath string // original project root (absolute)
	ShadowPath string // .fastplay-shadow/ root (absolute)
}

// ShadowDir returns the canonical shadow workspace path for a project.
func ShadowDir(sourcePath string) string {
	return filepath.Join(sourcePath, ".fastplay-shadow")
}

// Prepare creates or refreshes a shadow workspace.
//   - Assets/ and ProjectSettings/ are always re-copied from source.
//   - Packages/ is linked (symlink on unix, junction on windows) on first call; skipped if link already exists.
//   - Library/ is created empty on first call; preserved on subsequent calls so Unity reuses its import cache.
//   - Temp/ is deleted before each run and recreated empty by Unity.
//   - .gitignore is patched to exclude .fastplay-shadow/ (non-fatal on failure).
func Prepare(sourcePath string) (*Workspace, error) {
	abs, err := filepath.Abs(sourcePath)
	if err != nil {
		return nil, err
	}
	shadowPath := ShadowDir(abs)
	ws := &Workspace{SourcePath: abs, ShadowPath: shadowPath}

	if err := os.MkdirAll(shadowPath, 0755); err != nil {
		return nil, err
	}

	// Re-copy mutable directories on every call.
	for _, dir := range []string{"Assets", "ProjectSettings"} {
		src := filepath.Join(abs, dir)
		dst := filepath.Join(shadowPath, dir)
		if err := copyDir(src, dst); err != nil {
			return nil, err
		}
	}

	// Link Packages/ once; links do not need refreshing.
	pkgDst := filepath.Join(shadowPath, "Packages")
	if _, err := os.Lstat(pkgDst); os.IsNotExist(err) {
		if err := linkPackages(filepath.Join(abs, "Packages"), pkgDst); err != nil {
			return nil, err
		}
	}

	// Ensure Library/ exists (Unity will populate on first run, reuse on subsequent runs).
	if err := os.MkdirAll(filepath.Join(shadowPath, "Library"), 0755); err != nil {
		return nil, err
	}

	// Clean Temp/ so Unity starts fresh each run.
	_ = os.RemoveAll(filepath.Join(shadowPath, "Temp"))

	// Patch .gitignore — non-fatal.
	_ = EnsureIgnored(abs, ".fastplay-shadow/")

	return ws, nil
}

// Reset destroys the shadow workspace and rebuilds it from scratch.
// Use when the Library cache is stale (e.g. after a Unity version upgrade).
func Reset(sourcePath string) (*Workspace, error) {
	abs, err := filepath.Abs(sourcePath)
	if err != nil {
		return nil, err
	}
	if err := os.RemoveAll(ShadowDir(abs)); err != nil {
		return nil, err
	}
	return Prepare(abs)
}

// RemapPaths replaces shadow workspace path prefixes with the original source
// path in all AbsolutePath fields of the result. This makes the JSON output
// transparent — consumers see source paths, not shadow paths.
func (w *Workspace) RemapPaths(result *history.RunResult) {
	for i := range result.Tests {
		result.Tests[i].AbsolutePath = strings.ReplaceAll(
			result.Tests[i].AbsolutePath, w.ShadowPath, w.SourcePath)
	}
	for i := range result.Errors {
		result.Errors[i].AbsolutePath = strings.ReplaceAll(
			result.Errors[i].AbsolutePath, w.ShadowPath, w.SourcePath)
	}
}

// copyDir removes dst and recursively copies all files from src to dst.
func copyDir(src, dst string) error {
	if err := os.RemoveAll(dst); err != nil {
		return err
	}
	return filepath.WalkDir(src, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(src, path)
		target := filepath.Join(dst, rel)
		if d.IsDir() {
			return os.MkdirAll(target, 0755)
		}
		return copyFile(path, target)
	})
}

func copyFile(src, dst string) error {
	if err := os.MkdirAll(filepath.Dir(dst), 0755); err != nil {
		return err
	}
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer out.Close()
	_, err = io.Copy(out, in)
	return err
}

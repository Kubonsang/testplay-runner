package shadow

import (
	"context"
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
	ShadowPath string // .testplay-shadow/ root (absolute)
}

// ShadowWorkspaceDir returns the per-run shadow directory for a given runID.
func ShadowWorkspaceDir(sourcePath, runID string) string {
	return filepath.Join(sourcePath, ".testplay-shadow-"+runID)
}

// Prepare creates an isolated shadow workspace for a single run.
//   - Assets/ and ProjectSettings/ are always copied fresh from source.
//   - Packages/ is linked (symlink on unix, junction on windows); linked once per workspace.
//   - Library/ is created empty; Unity populates it during the run.
//   - Temp/ is deleted before the run; Unity recreates it.
//   - .gitignore is patched to exclude .testplay-shadow-*/ (non-fatal on failure).
//
// The caller must call ws.Cleanup() after the run to remove the per-run directory.
func Prepare(ctx context.Context, sourcePath, runID string) (*Workspace, error) {
	abs, err := filepath.Abs(sourcePath)
	if err != nil {
		return nil, err
	}
	shadowPath := ShadowWorkspaceDir(abs, runID)
	ws := &Workspace{SourcePath: abs, ShadowPath: shadowPath}

	succeeded := false
	defer func() {
		if !succeeded {
			os.RemoveAll(shadowPath)
		}
	}()

	if err := os.MkdirAll(shadowPath, 0755); err != nil {
		return nil, err
	}

	// Re-copy mutable directories on every call.
	for _, dir := range []string{"Assets", "ProjectSettings"} {
		src := filepath.Join(abs, dir)
		dst := filepath.Join(shadowPath, dir)
		if err := copyDir(ctx, src, dst); err != nil {
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

	// Ensure Library/ exists (Unity will populate during the run; starts empty each run).
	if err := os.MkdirAll(filepath.Join(shadowPath, "Library"), 0755); err != nil {
		return nil, err
	}

	// Clean Temp/ so Unity starts fresh each run.
	_ = os.RemoveAll(filepath.Join(shadowPath, "Temp"))

	// Patch .gitignore — non-fatal.
	_ = EnsureIgnored(abs, ".testplay-shadow-*/")

	succeeded = true
	return ws, nil
}

// Reset is equivalent to Prepare with per-run isolation — each run already
// starts with a fresh workspace. Kept for API stability.
func Reset(ctx context.Context, sourcePath, runID string) (*Workspace, error) {
	return Prepare(ctx, sourcePath, runID)
}

// Cleanup removes the per-run shadow workspace directory.
// Always call this after the run completes (defer recommended).
func (w *Workspace) Cleanup() error {
	return os.RemoveAll(w.ShadowPath)
}

// RemapPaths replaces shadow workspace path prefixes with the original source
// path in all AbsolutePath fields of the result. This makes the JSON output
// transparent — consumers see source paths, not shadow paths.
//
// On Windows, Unity logs may use forward slashes and lowercase drive letters
// while filepath.Abs returns backslashes with an uppercase drive letter.
// remapAbsPath normalises both sides to forward slashes and compares
// case-insensitively to handle all combinations.
func (w *Workspace) RemapPaths(result *history.RunResult) {
	for i := range result.Tests {
		result.Tests[i].AbsolutePath = remapAbsPath(
			result.Tests[i].AbsolutePath, w.ShadowPath, w.SourcePath)
		result.Tests[i].Message = remapString(
			result.Tests[i].Message, w.ShadowPath, w.SourcePath)
	}
	for i := range result.Errors {
		result.Errors[i].AbsolutePath = remapAbsPath(
			result.Errors[i].AbsolutePath, w.ShadowPath, w.SourcePath)
		result.Errors[i].Message = remapString(
			result.Errors[i].Message, w.ShadowPath, w.SourcePath)
	}
	for i := range result.NewFailures {
		result.NewFailures[i].AbsolutePath = remapAbsPath(
			result.NewFailures[i].AbsolutePath, w.ShadowPath, w.SourcePath)
		result.NewFailures[i].Message = remapString(
			result.NewFailures[i].Message, w.ShadowPath, w.SourcePath)
	}
}

// remapAbsPath swaps a shadowPath prefix with sourcePath in absPath.
// Normalises all three paths to forward slashes before comparison so that
// Windows backslash/forward-slash mixing and case differences in drive letters
// (e.g. "C:/Proj" vs "c:/proj") do not cause silent mismatches.
// The returned path uses forward slashes throughout.
//
// strings.ReplaceAll is used instead of filepath.ToSlash because ToSlash only
// converts os.PathSeparator; on macOS/Linux it leaves '\' unchanged, so test
// fixtures that use Windows-style paths would not be normalised correctly.
//
// shadowPath is anchored with a trailing "/" before comparison to prevent
// accidental prefix matches against sibling directories whose names start with
// ".testplay-shadow" (e.g. ".testplay-shadowX").
func remapAbsPath(absPath, shadowPath, sourcePath string) string {
	norm := strings.ReplaceAll(absPath, `\`, "/")
	// Anchor with trailing "/" so prefix matching is directory-exact.
	shadowSlash := strings.TrimRight(strings.ReplaceAll(shadowPath, `\`, "/"), "/") + "/"
	sourceSlash := strings.TrimRight(strings.ReplaceAll(sourcePath, `\`, "/"), "/")

	if strings.HasPrefix(strings.ToLower(norm), strings.ToLower(shadowSlash)) {
		// norm[len(shadowSlash):] starts after the trailing "/", so join with "/".
		return sourceSlash + "/" + norm[len(shadowSlash):]
	}
	// Return the forward-slash normalised form for consistency even on no-match.
	return norm
}

// remapString replaces all occurrences of shadowPath inside s with sourcePath.
// Both paths are normalised to forward slashes before replacement so that
// Windows backslash/forward-slash mixing is handled consistently with remapAbsPath.
// Comparison is case-insensitive to handle mixed drive-letter casing on Windows
// (e.g. Unity logs "c:/project/..." while filepath.Abs returns "C:/Project/...").
func remapString(s, shadowPath, sourcePath string) string {
	shadowSlash := strings.TrimRight(strings.ReplaceAll(shadowPath, `\`, "/"), "/")
	sourceSlash := strings.TrimRight(strings.ReplaceAll(sourcePath, `\`, "/"), "/")
	if shadowSlash == "" {
		return s
	}
	norm := strings.ReplaceAll(s, `\`, "/")
	shadowLower := strings.ToLower(shadowSlash)
	normLower := strings.ToLower(norm)
	var result strings.Builder
	pos := 0
	for {
		idx := strings.Index(normLower[pos:], shadowLower)
		if idx == -1 {
			result.WriteString(norm[pos:])
			break
		}
		result.WriteString(norm[pos : pos+idx])
		result.WriteString(sourceSlash)
		pos += idx + len(shadowSlash)
	}
	return result.String()
}

// ctxReader wraps an io.Reader and checks ctx.Err() before each Read call.
// This allows io.Copy to respect context cancellation between 32KB chunks,
// preventing large-file copies from blocking cancellation indefinitely.
type ctxReader struct {
	ctx context.Context
	r   io.Reader
}

func (r *ctxReader) Read(p []byte) (n int, err error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	return r.r.Read(p)
}

// copyDir removes dst and recursively copies all files from src to dst.
// It checks ctx.Err() at every WalkDir iteration so cancellation returns immediately.
func copyDir(ctx context.Context, src, dst string) error {
	if err := os.RemoveAll(dst); err != nil {
		return err
	}
	return filepath.WalkDir(src, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
		rel, _ := filepath.Rel(src, path)
		target := filepath.Join(dst, rel)
		if d.IsDir() {
			return os.MkdirAll(target, 0755)
		}
		if d.Type()&fs.ModeSymlink != 0 {
			linkTarget, err := os.Readlink(path)
			if err != nil {
				return err
			}
			return os.Symlink(linkTarget, target)
		}
		return copyFile(ctx, path, target)
	})
}

func copyFile(ctx context.Context, src, dst string) error {
	if err := os.MkdirAll(filepath.Dir(dst), 0755); err != nil {
		return err
	}
	info, err := os.Stat(src)
	if err != nil {
		return err
	}
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, info.Mode())
	if err != nil {
		return err
	}
	if _, err = io.Copy(out, &ctxReader{ctx: ctx, r: in}); err != nil {
		out.Close()
		return err
	}
	return out.Close()
}

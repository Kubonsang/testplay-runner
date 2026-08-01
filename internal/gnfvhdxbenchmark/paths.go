package gnfvhdxbenchmark

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

func ValidateRoots(project, work, artifact string) error {
	values := map[string]string{"project": project, "work": work, "artifact": artifact}
	for name, value := range values {
		if !filepath.IsAbs(value) {
			return benchmarkError(CodeInvalidInput, "validate-"+name+"-root", value, fmt.Errorf("absolute path required"))
		}
		clean := filepath.Clean(value)
		if clean == filepath.VolumeName(clean)+string(os.PathSeparator) {
			return benchmarkError(CodeInvalidInput, "validate-"+name+"-root", value, fmt.Errorf("drive root is forbidden"))
		}
	}
	if pathsOverlap(project, work) || pathsOverlap(project, artifact) || pathsOverlap(work, artifact) {
		return benchmarkError(CodeInvalidInput, "validate-root-overlap", work, fmt.Errorf("project, work, and artifact roots must not overlap"))
	}
	return nil
}

func RequireEmptyOrAbsent(path string) error {
	info, err := os.Lstat(path)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return benchmarkError(CodeInvalidInput, "inspect-root", path, err)
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return benchmarkError(CodeInvalidInput, "inspect-root", path, fmt.Errorf("ordinary directory required"))
	}
	entries, err := os.ReadDir(path)
	if err != nil || len(entries) != 0 {
		return benchmarkError(CodeInvalidInput, "inspect-root", path, fmt.Errorf("root must be empty: entries=%d error=%v", len(entries), err))
	}
	return nil
}

func pathsOverlap(left, right string) bool {
	left, right = filepath.Clean(left), filepath.Clean(right)
	if runtime.GOOS == "windows" {
		left, right = strings.ToLower(left), strings.ToLower(right)
	}
	return pathWithin(left, right) || pathWithin(right, left)
}

func pathWithin(root, candidate string) bool {
	relative, err := filepath.Rel(root, candidate)
	return err == nil && (relative == "." || (relative != ".." && !strings.HasPrefix(relative, ".."+string(os.PathSeparator))))
}

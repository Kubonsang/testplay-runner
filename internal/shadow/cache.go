package shadow

import (
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"
)

// CacheKey computes a deterministic cache key for a Unity project by hashing
// ProjectSettings/ProjectVersion.txt and Packages/manifest.json.
func CacheKey(projectPath string) (string, error) {
	h := sha256.New()
	for _, rel := range []string{
		filepath.Join("ProjectSettings", "ProjectVersion.txt"),
		filepath.Join("Packages", "manifest.json"),
	} {
		data, err := os.ReadFile(filepath.Join(projectPath, rel))
		if err != nil {
			return "", fmt.Errorf("cache key: %w", err)
		}
		h.Write([]byte(rel))
		h.Write(data)
	}
	return fmt.Sprintf("%x", h.Sum(nil)), nil
}

// CacheLibraryDir returns the path to the cached Library directory for a project.
func CacheLibraryDir(projectPath string) string {
	return CacheLibraryDirAt(filepath.Join(projectPath, ".testplay", "cache"))
}

// CacheLibraryDirAt returns the Library path under an explicit cache root.
func CacheLibraryDirAt(cacheRoot string) string {
	return filepath.Join(cacheRoot, "Library")
}

func cacheKeyFileAt(cacheRoot string) string {
	return filepath.Join(cacheRoot, "cache.key")
}

// ValidateCache returns true if the cached Library directory exists and
// the stored cache key matches the current project state.
func ValidateCache(projectPath string) bool {
	return ValidateCacheAt(projectPath, filepath.Join(projectPath, ".testplay", "cache"))
}

// ValidateCacheAt validates an explicit persistent cache root against the
// current source project.
func ValidateCacheAt(projectPath, cacheRoot string) bool {
	// Check that the Library directory actually exists on disk.
	if _, err := os.Stat(CacheLibraryDirAt(cacheRoot)); err != nil {
		return false
	}
	stored, err := os.ReadFile(cacheKeyFileAt(cacheRoot))
	if err != nil {
		return false
	}
	current, err := CacheKey(projectPath)
	if err != nil {
		return false
	}
	return string(stored) == current
}

// SaveCacheKey writes the current cache key to disk.
func SaveCacheKey(projectPath string) error {
	return SaveCacheKeyAt(projectPath, filepath.Join(projectPath, ".testplay", "cache"))
}

// SaveCacheKeyAt stores the current project key under an explicit cache root.
func SaveCacheKeyAt(projectPath, cacheRoot string) error {
	key, err := CacheKey(projectPath)
	if err != nil {
		return err
	}
	keyPath := cacheKeyFileAt(cacheRoot)
	if err := os.MkdirAll(filepath.Dir(keyPath), 0755); err != nil {
		return err
	}
	return os.WriteFile(keyPath, []byte(key), 0644)
}

// ClearCache removes the entire cache directory for a project.
func ClearCache(projectPath string) error {
	return ClearCacheAt(filepath.Join(projectPath, ".testplay", "cache"))
}

// ClearCacheAt removes only the selected persistent cache root.
func ClearCacheAt(cacheRoot string) error {
	return os.RemoveAll(cacheRoot)
}

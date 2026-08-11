package vhdxworkspace

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/Kubonsang/testplay-runner/internal/atomicfile"
	"github.com/Kubonsang/testplay-runner/internal/shadow"
)

var unityPackageNamePattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]*$`)

type LocalPackageOverride struct {
	Name          string `json:"name"`
	SourcePath    string `json:"sourcePath"`
	Version       string `json:"version"`
	ContentDigest string `json:"contentDigest"`
	FileCount     int64  `json:"fileCount"`
	LogicalBytes  int64  `json:"logicalBytes"`
}

// ResolveLocalPackageOverrides validates local Unity packages before a VHDX
// parent key is calculated. The aggregate digest deliberately excludes the
// machine-local source path so identical package contents reuse one parent.
func ResolveLocalPackageOverrides(configured map[string]string) ([]LocalPackageOverride, string, error) {
	if len(configured) == 0 {
		return nil, "", nil
	}
	names := make([]string, 0, len(configured))
	for name := range configured {
		names = append(names, name)
	}
	sort.Strings(names)
	resolved := make([]LocalPackageOverride, 0, len(names))
	var aggregate strings.Builder
	for _, name := range names {
		if !unityPackageNamePattern.MatchString(name) || name == "." || name == ".." {
			return nil, "", fmt.Errorf("%w: invalid Unity package name %q", ErrInvalidInput, name)
		}
		configuredPath := strings.TrimSpace(configured[name])
		if !filepath.IsAbs(configuredPath) || strings.HasPrefix(filepath.Clean(configuredPath), `\\`) {
			return nil, "", fmt.Errorf("%w: local package %q must use an absolute local path", ErrInvalidInput, name)
		}
		path := filepath.Clean(configuredPath)
		info, err := os.Lstat(path)
		if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			return nil, "", fmt.Errorf("%w: local package %q must be an existing real directory: %v", ErrInvalidInput, name, err)
		}
		if err := validatePlatformRealDirectory(path); err != nil {
			return nil, "", fmt.Errorf("validate local package %q: %w", name, err)
		}
		var packageJSON struct {
			Name    string `json:"name"`
			Version string `json:"version"`
		}
		data, err := os.ReadFile(filepath.Join(path, "package.json"))
		if err != nil {
			return nil, "", fmt.Errorf("read local package %q package.json: %w", name, err)
		}
		if err := json.Unmarshal(data, &packageJSON); err != nil {
			return nil, "", fmt.Errorf("decode local package %q package.json: %w", name, err)
		}
		if packageJSON.Name != name || strings.TrimSpace(packageJSON.Version) == "" {
			return nil, "", fmt.Errorf("%w: local package identity mismatch: configured=%q package=%q version=%q", ErrInvalidInput, name, packageJSON.Name, packageJSON.Version)
		}
		digest, count, logical, err := digestTree(path)
		if err != nil {
			return nil, "", fmt.Errorf("hash local package %q: %w", name, err)
		}
		resolved = append(resolved, LocalPackageOverride{Name: name, SourcePath: path, Version: packageJSON.Version, ContentDigest: digest, FileCount: count, LogicalBytes: logical})
		aggregate.WriteString(name)
		aggregate.WriteByte(0)
		aggregate.WriteString(digest)
		aggregate.WriteByte(0)
	}
	return resolved, digestBytes([]byte(aggregate.String())), nil
}

// ApplyLocalPackageOverrides embeds validated packages in the private
// workspace. It never edits the source project and refuses destination reuse.
func ApplyLocalPackageOverrides(ctx context.Context, workspace string, packages []LocalPackageOverride) error {
	if len(packages) == 0 {
		return nil
	}
	packagesRoot := filepath.Join(workspace, "Packages")
	if err := validatePlatformRealDirectory(packagesRoot); err != nil {
		return fmt.Errorf("validate workspace Packages: %w", err)
	}
	manifestPath := filepath.Join(packagesRoot, "manifest.json")
	lockPath := filepath.Join(packagesRoot, "packages-lock.json")
	manifest, err := readJSONObject(manifestPath)
	if err != nil {
		return err
	}
	manifestDependencies, ok := manifest["dependencies"].(map[string]any)
	if !ok {
		return fmt.Errorf("%w: manifest dependencies object is missing", ErrInvalidInput)
	}
	lock, err := readJSONObject(lockPath)
	if err != nil {
		return err
	}
	lockDependencies, ok := lock["dependencies"].(map[string]any)
	if !ok {
		return fmt.Errorf("%w: packages-lock dependencies object is missing", ErrInvalidInput)
	}
	for _, pkg := range packages {
		dependency, ok := manifestDependencies[pkg.Name].(string)
		if !ok || !strings.HasPrefix(strings.TrimSpace(dependency), "file:") {
			return fmt.Errorf("%w: manifest dependency %q is missing or not a file dependency", ErrInvalidInput, pkg.Name)
		}
		lockEntry, ok := lockDependencies[pkg.Name].(map[string]any)
		if !ok {
			return fmt.Errorf("%w: packages-lock entry is missing: %s", ErrInvalidInput, pkg.Name)
		}
		destination := filepath.Join(packagesRoot, pkg.Name)
		if _, err := os.Lstat(destination); err == nil {
			return fmt.Errorf("%w: embedded package destination already exists: %s", ErrOwnershipMismatch, destination)
		} else if !os.IsNotExist(err) {
			return err
		}
		if err := shadow.CopyDirParallel(ctx, pkg.SourcePath, destination, 0); err != nil {
			return fmt.Errorf("copy local package %q: %w", pkg.Name, err)
		}
		delete(manifestDependencies, pkg.Name)
		lockEntry["version"] = "file:" + pkg.Name
		lockEntry["depth"] = float64(0)
		lockEntry["source"] = "embedded"
		delete(lockEntry, "hash")
	}
	if err := writeJSONObject(manifestPath, manifest); err != nil {
		return err
	}
	return writeJSONObject(lockPath, lock)
}

func readJSONObject(path string) (map[string]any, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var value map[string]any
	if err := json.Unmarshal(data, &value); err != nil {
		return nil, err
	}
	return value, nil
}

func writeJSONObject(path string, value map[string]any) error {
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	return atomicfile.Write(path, data, 0600)
}

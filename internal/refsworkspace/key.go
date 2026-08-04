package refsworkspace

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

type CompatibilityOptions struct {
	ProjectPath      string
	UnityExecutable  string
	BuildTarget      string
	ScriptingBackend string
}

type CompatibilityKey struct {
	SchemaVersion         int    `json:"schemaVersion"`
	Digest                string `json:"digest"`
	UnityVersion          string `json:"unityVersion"`
	UnityExecutableSHA256 string `json:"unityExecutableSha256"`
	ManifestSHA256        string `json:"manifestSha256"`
	PackagesLockSHA256    string `json:"packagesLockSha256"`
	ProjectSettingsSHA256 string `json:"projectSettingsSha256"`
	BuildTarget           string `json:"buildTarget"`
	ScriptingBackend      string `json:"scriptingBackend"`
	ProjectIdentitySHA256 string `json:"projectIdentitySha256"`
	AssetsSHA256          string `json:"assetsSha256"`
}

type KeyMetrics struct {
	KeyComputationMs      int64 `json:"keyComputationMs"`
	AssetsHashMs          int64 `json:"assetsHashMs"`
	PackagesHashMs        int64 `json:"packagesHashMs"`
	ProjectSettingsHashMs int64 `json:"projectSettingsHashMs"`
}

type TreeInfo struct {
	Digest       string `json:"digest"`
	FileCount    int64  `json:"fileCount"`
	LogicalBytes int64  `json:"logicalBytes"`
}

type keyPayload struct {
	SchemaVersion         int    `json:"schemaVersion"`
	UnityVersion          string `json:"unityVersion"`
	UnityExecutableSHA256 string `json:"unityExecutableSha256"`
	ManifestSHA256        string `json:"manifestSha256"`
	PackagesLockSHA256    string `json:"packagesLockSha256"`
	ProjectSettingsSHA256 string `json:"projectSettingsSha256"`
	BuildTarget           string `json:"buildTarget"`
	ScriptingBackend      string `json:"scriptingBackend"`
	ProjectIdentitySHA256 string `json:"projectIdentitySha256"`
	AssetsSHA256          string `json:"assetsSha256"`
}

func ComputeCompatibilityKey(ctx context.Context, options CompatibilityOptions) (CompatibilityKey, KeyMetrics, error) {
	started := time.Now()
	metrics := KeyMetrics{}
	if err := ctx.Err(); err != nil {
		return CompatibilityKey{}, metrics, cancelled("compatibility-key", options.ProjectPath, err)
	}
	projectPath, err := canonicalExistingPath(options.ProjectPath)
	if err != nil {
		return CompatibilityKey{}, metrics, newError(CodeInvalidConfiguration, "canonical-project", options.ProjectPath, err)
	}
	if options.UnityExecutable == "" || options.BuildTarget == "" || options.ScriptingBackend == "" {
		return CompatibilityKey{}, metrics, newError(CodeInvalidConfiguration, "compatibility-key", projectPath, fmt.Errorf("Unity executable, build target, and scripting backend are required"))
	}

	versionData, err := os.ReadFile(filepath.Join(projectPath, "ProjectSettings", "ProjectVersion.txt"))
	if err != nil {
		return CompatibilityKey{}, metrics, newError(CodeInvalidConfiguration, "read-unity-version", projectPath, err)
	}
	unityDigest, err := hashFileContext(ctx, options.UnityExecutable)
	if err != nil {
		return CompatibilityKey{}, metrics, newError(CodeInvalidConfiguration, "hash-unity-executable", options.UnityExecutable, err)
	}

	phase := time.Now()
	manifest, err := hashFileContext(ctx, filepath.Join(projectPath, "Packages", "manifest.json"))
	if err != nil {
		return CompatibilityKey{}, metrics, newError(CodeInvalidConfiguration, "hash-manifest", projectPath, err)
	}
	packagesLock := "missing"
	lockPath := filepath.Join(projectPath, "Packages", "packages-lock.json")
	if _, statErr := os.Stat(lockPath); statErr == nil {
		packagesLock, err = hashFileContext(ctx, lockPath)
		if err != nil {
			return CompatibilityKey{}, metrics, newError(CodeInvalidConfiguration, "hash-packages-lock", lockPath, err)
		}
	} else if !os.IsNotExist(statErr) {
		return CompatibilityKey{}, metrics, newError(CodeInvalidConfiguration, "stat-packages-lock", lockPath, statErr)
	}
	metrics.PackagesHashMs = time.Since(phase).Milliseconds()

	phase = time.Now()
	projectSettings, err := HashTree(ctx, filepath.Join(projectPath, "ProjectSettings"))
	if err != nil {
		return CompatibilityKey{}, metrics, newError(CodeInvalidConfiguration, "hash-project-settings", projectPath, err)
	}
	metrics.ProjectSettingsHashMs = time.Since(phase).Milliseconds()

	phase = time.Now()
	assets, err := HashTree(ctx, filepath.Join(projectPath, "Assets"))
	if err != nil {
		return CompatibilityKey{}, metrics, newError(CodeInvalidConfiguration, "hash-assets", projectPath, err)
	}
	metrics.AssetsHashMs = time.Since(phase).Milliseconds()

	payload := keyPayload{
		SchemaVersion:         BaselineSchemaVersion,
		UnityVersion:          parseUnityVersion(versionData),
		UnityExecutableSHA256: unityDigest,
		ManifestSHA256:        manifest,
		PackagesLockSHA256:    packagesLock,
		ProjectSettingsSHA256: projectSettings.Digest,
		BuildTarget:           options.BuildTarget,
		ScriptingBackend:      options.ScriptingBackend,
		ProjectIdentitySHA256: hashBytes([]byte(projectPath)),
		AssetsSHA256:          assets.Digest,
	}
	canonical, err := json.Marshal(payload)
	if err != nil {
		return CompatibilityKey{}, metrics, newError(CodeInvalidConfiguration, "encode-key", projectPath, err)
	}
	key := CompatibilityKey{
		SchemaVersion:         payload.SchemaVersion,
		Digest:                hashBytes(canonical),
		UnityVersion:          payload.UnityVersion,
		UnityExecutableSHA256: payload.UnityExecutableSHA256,
		ManifestSHA256:        payload.ManifestSHA256,
		PackagesLockSHA256:    payload.PackagesLockSHA256,
		ProjectSettingsSHA256: payload.ProjectSettingsSHA256,
		BuildTarget:           payload.BuildTarget,
		ScriptingBackend:      payload.ScriptingBackend,
		ProjectIdentitySHA256: payload.ProjectIdentitySHA256,
		AssetsSHA256:          payload.AssetsSHA256,
	}
	metrics.KeyComputationMs = time.Since(started).Milliseconds()
	return key, metrics, nil
}

func canonicalExistingPath(path string) (string, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	resolved, err := filepath.EvalSymlinks(abs)
	if err != nil {
		return "", err
	}
	return filepath.Clean(resolved), nil
}

func parseUnityVersion(data []byte) string {
	for _, line := range strings.Split(string(data), "\n") {
		if value, ok := strings.CutPrefix(strings.TrimSpace(line), "m_EditorVersion:"); ok {
			return strings.TrimSpace(value)
		}
	}
	return strings.TrimSpace(string(data))
}

func HashTree(ctx context.Context, root string) (TreeInfo, error) {
	var paths []string
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		if path == root {
			return nil
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		paths = append(paths, filepath.ToSlash(rel))
		return nil
	})
	if err != nil {
		return TreeInfo{}, err
	}
	sort.Strings(paths)
	h := sha256.New()
	result := TreeInfo{}
	for _, rel := range paths {
		if err := ctx.Err(); err != nil {
			return TreeInfo{}, err
		}
		path := filepath.Join(root, filepath.FromSlash(rel))
		info, err := os.Lstat(path)
		if err != nil {
			return TreeInfo{}, err
		}
		_, _ = io.WriteString(h, rel)
		// Permission bits are protection policy, not Library content. Hash only
		// the entry type so sealing read-only attributes does not invalidate the
		// canonical payload digest.
		_, _ = io.WriteString(h, "\x00"+info.Mode().Type().String()+"\x00")
		switch {
		case info.Mode()&os.ModeSymlink != 0:
			target, err := os.Readlink(path)
			if err != nil {
				return TreeInfo{}, err
			}
			_, _ = io.WriteString(h, target)
		case info.Mode().IsRegular():
			fileDigest, err := hashFileInto(ctx, path, h)
			if err != nil {
				return TreeInfo{}, err
			}
			_ = fileDigest
			result.FileCount++
			result.LogicalBytes += info.Size()
		}
	}
	result.Digest = hex.EncodeToString(h.Sum(nil))
	return result, nil
}

func hashFileContext(ctx context.Context, path string) (string, error) {
	h := sha256.New()
	if _, err := hashFileInto(ctx, path, h); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

func hashFileInto(ctx context.Context, path string, writer io.Writer) (int64, error) {
	f, err := os.Open(path)
	if err != nil {
		return 0, err
	}
	defer f.Close()
	buffer := make([]byte, 1<<20)
	var total int64
	for {
		if err := ctx.Err(); err != nil {
			return total, err
		}
		n, readErr := f.Read(buffer)
		if n > 0 {
			written, writeErr := writer.Write(buffer[:n])
			total += int64(written)
			if writeErr != nil {
				return total, writeErr
			}
			if written != n {
				return total, io.ErrShortWrite
			}
		}
		if readErr == io.EOF {
			return total, nil
		}
		if readErr != nil {
			return total, readErr
		}
	}
}

func hashBytes(value []byte) string {
	sum := sha256.Sum256(value)
	return hex.EncodeToString(sum[:])
}

package libraryimage

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
)

const SchemaVersion = "1"

// Key describes every input that can make a Unity Library image incompatible.
// Digest is the SHA-256 of the remaining canonical fields.
type Key struct {
	SchemaVersion         string `json:"schemaVersion"`
	Digest                string `json:"digest"`
	UnityVersion          string `json:"unityVersion"`
	UnityExecutableSHA256 string `json:"unityExecutableSha256"`
	ManifestSHA256        string `json:"manifestSha256"`
	PackagesLockSHA256    string `json:"packagesLockSha256"`
	ProjectSettingsSHA256 string `json:"projectSettingsSha256"`
	BuildTarget           string `json:"buildTarget"`
	ScriptingBackend      string `json:"scriptingBackend"`
	ProjectIdentitySHA256 string `json:"projectIdentitySha256"`
}

type keyPayload struct {
	SchemaVersion         string `json:"schemaVersion"`
	UnityVersion          string `json:"unityVersion"`
	UnityExecutableSHA256 string `json:"unityExecutableSha256"`
	ManifestSHA256        string `json:"manifestSha256"`
	PackagesLockSHA256    string `json:"packagesLockSha256"`
	ProjectSettingsSHA256 string `json:"projectSettingsSha256"`
	BuildTarget           string `json:"buildTarget"`
	ScriptingBackend      string `json:"scriptingBackend"`
	ProjectIdentitySHA256 string `json:"projectIdentitySha256"`
}

// ComputeKey builds a deterministic, project-local Library image key.
// Project identity is intentionally included: Unity Library files may embed
// absolute project paths, so moving a portable project invalidates its image.
func ComputeKey(projectPath, unityPath string) (Key, error) {
	projectIdentity, err := canonicalProjectIdentity(projectPath)
	if err != nil {
		return Key{}, fmt.Errorf("library image key: project identity: %w", err)
	}

	versionPath := filepath.Join(projectPath, "ProjectSettings", "ProjectVersion.txt")
	versionData, err := os.ReadFile(versionPath)
	if err != nil {
		return Key{}, fmt.Errorf("library image key: read ProjectVersion.txt: %w", err)
	}

	manifestDigest, err := hashFile(filepath.Join(projectPath, "Packages", "manifest.json"))
	if err != nil {
		return Key{}, fmt.Errorf("library image key: read manifest.json: %w", err)
	}

	lockDigest := "missing"
	lockPath := filepath.Join(projectPath, "Packages", "packages-lock.json")
	if _, err := os.Stat(lockPath); err == nil {
		lockDigest, err = hashFile(lockPath)
		if err != nil {
			return Key{}, fmt.Errorf("library image key: read packages-lock.json: %w", err)
		}
	} else if !os.IsNotExist(err) {
		return Key{}, fmt.Errorf("library image key: stat packages-lock.json: %w", err)
	}

	settingsDigest, _, _, err := hashTree(filepath.Join(projectPath, "ProjectSettings"))
	if err != nil {
		return Key{}, fmt.Errorf("library image key: hash ProjectSettings: %w", err)
	}

	payload := keyPayload{
		SchemaVersion:         SchemaVersion,
		UnityVersion:          parseUnityVersion(versionData),
		UnityExecutableSHA256: hashString(filepath.Clean(unityPath)),
		ManifestSHA256:        manifestDigest,
		PackagesLockSHA256:    lockDigest,
		ProjectSettingsSHA256: settingsDigest,
		BuildTarget:           runtime.GOOS + "/" + runtime.GOARCH,
		ScriptingBackend:      scriptingBackend(projectPath),
		ProjectIdentitySHA256: hashString(projectIdentity),
	}
	canonical, err := json.Marshal(payload)
	if err != nil {
		return Key{}, fmt.Errorf("library image key: encode payload: %w", err)
	}

	return Key{
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
	}, nil
}

func canonicalProjectIdentity(projectPath string) (string, error) {
	abs, err := filepath.Abs(projectPath)
	if err != nil {
		return "", err
	}
	if resolved, resolveErr := filepath.EvalSymlinks(abs); resolveErr == nil {
		abs = resolved
	}
	return filepath.Clean(abs), nil
}

func parseUnityVersion(data []byte) string {
	for _, line := range strings.Split(string(data), "\n") {
		if value, ok := strings.CutPrefix(strings.TrimSpace(line), "m_EditorVersion:"); ok {
			return strings.TrimSpace(value)
		}
	}
	return strings.TrimSpace(string(data))
}

func scriptingBackend(projectPath string) string {
	data, err := os.ReadFile(filepath.Join(projectPath, "ProjectSettings", "ProjectSettings.asset"))
	if err != nil {
		return "covered-by-project-settings-hash"
	}
	for _, line := range strings.Split(string(data), "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "scriptingBackend:") {
			return trimmed
		}
	}
	return "covered-by-project-settings-hash"
}

func hashFile(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()

	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

func hashTree(root string) (digest string, fileCount int64, logicalBytes int64, err error) {
	var paths []string
	err = filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if path == root {
			return nil
		}
		rel, relErr := filepath.Rel(root, path)
		if relErr != nil {
			return relErr
		}
		paths = append(paths, filepath.ToSlash(rel))
		return nil
	})
	if err != nil {
		return "", 0, 0, err
	}
	sort.Strings(paths)

	h := sha256.New()
	for _, rel := range paths {
		path := filepath.Join(root, filepath.FromSlash(rel))
		info, statErr := os.Lstat(path)
		if statErr != nil {
			return "", 0, 0, statErr
		}
		_, _ = io.WriteString(h, rel)
		_, _ = io.WriteString(h, "\x00"+info.Mode().String()+"\x00")
		switch {
		case info.Mode()&os.ModeSymlink != 0:
			target, readErr := os.Readlink(path)
			if readErr != nil {
				return "", 0, 0, readErr
			}
			_, _ = io.WriteString(h, target)
		case info.Mode().IsRegular():
			f, openErr := os.Open(path)
			if openErr != nil {
				return "", 0, 0, openErr
			}
			_, copyErr := io.Copy(h, f)
			closeErr := f.Close()
			if copyErr != nil {
				return "", 0, 0, copyErr
			}
			if closeErr != nil {
				return "", 0, 0, closeErr
			}
			fileCount++
			logicalBytes += info.Size()
		}
	}
	return hex.EncodeToString(h.Sum(nil)), fileCount, logicalBytes, nil
}

func hashString(value string) string {
	return hashBytes([]byte(value))
}

func hashBytes(value []byte) string {
	sum := sha256.Sum256(value)
	return hex.EncodeToString(sum[:])
}

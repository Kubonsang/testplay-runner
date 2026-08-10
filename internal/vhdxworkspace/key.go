package vhdxworkspace

import (
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

	"github.com/Kubonsang/testplay-runner/internal/libraryimage"
)

type CompatibilityKey struct {
	SchemaVersion int              `json:"schemaVersion"`
	Digest        string           `json:"digest"`
	LibraryKey    libraryimage.Key `json:"libraryKey"`
	Provider      string           `json:"provider"`
	Filesystem    string           `json:"filesystem"`
	VirtualBytes  int64            `json:"virtualBytes"`
	BlockBytes    int64            `json:"blockBytes"`
	SectorBytes   int64            `json:"sectorBytes"`
}

type SourceSnapshot struct {
	Digest         string `json:"digest"`
	AssetsDigest   string `json:"assetsDigest"`
	PackagesDigest string `json:"packagesDigest"`
	SettingsDigest string `json:"projectSettingsDigest"`
	FileCount      int64  `json:"fileCount"`
	LogicalBytes   int64  `json:"logicalBytes"`
}

func ComputeCompatibilityKey(projectPath, unityPath string) (CompatibilityKey, error) {
	base, err := libraryimage.ComputeKey(projectPath, unityPath)
	if err != nil {
		return CompatibilityKey{}, err
	}
	key := CompatibilityKey{
		SchemaVersion: ParentSchemaVersion,
		LibraryKey:    base, Provider: Provider, Filesystem: "NTFS",
		VirtualBytes: DefaultVirtualBytes, BlockBytes: DefaultBlockBytes,
		SectorBytes: DefaultSectorBytes,
	}
	payload := key
	payload.Digest = ""
	encoded, err := json.Marshal(payload)
	if err != nil {
		return CompatibilityKey{}, err
	}
	key.Digest = digestBytes(encoded)
	return key, nil
}

func ComputeSourceSnapshot(projectPath string) (SourceSnapshot, error) {
	assets, ac, ab, err := digestTree(filepath.Join(projectPath, "Assets"))
	if err != nil {
		return SourceSnapshot{}, fmt.Errorf("snapshot Assets: %w", err)
	}
	packages, pc, pb, err := digestTree(filepath.Join(projectPath, "Packages"))
	if err != nil {
		return SourceSnapshot{}, fmt.Errorf("snapshot Packages: %w", err)
	}
	settings, sc, sb, err := digestTree(filepath.Join(projectPath, "ProjectSettings"))
	if err != nil {
		return SourceSnapshot{}, fmt.Errorf("snapshot ProjectSettings: %w", err)
	}
	payload := strings.Join([]string{assets, packages, settings}, "\x00")
	return SourceSnapshot{
		Digest: digestBytes([]byte(payload)), AssetsDigest: assets,
		PackagesDigest: packages, SettingsDigest: settings,
		FileCount: ac + pc + sc, LogicalBytes: ab + pb + sb,
	}, nil
}

func digestTree(root string) (string, int64, int64, error) {
	var paths []string
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if path == root {
			return nil
		}
		if entry.Type()&os.ModeSymlink != 0 {
			return fmt.Errorf("reparse/symlink entry is not allowed: %s", path)
		}
		if entry.IsDir() {
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
		return "", 0, 0, err
	}
	sort.Strings(paths)
	h := sha256.New()
	var count, bytes int64
	for _, rel := range paths {
		path := filepath.Join(root, filepath.FromSlash(rel))
		file, err := os.Open(path)
		if err != nil {
			return "", 0, 0, err
		}
		info, err := file.Stat()
		if err == nil {
			bytes += info.Size()
		}
		_, _ = io.WriteString(h, rel+"\x00")
		_, copyErr := io.Copy(h, file)
		closeErr := file.Close()
		if copyErr != nil {
			return "", 0, 0, copyErr
		}
		if closeErr != nil {
			return "", 0, 0, closeErr
		}
		count++
	}
	return hex.EncodeToString(h.Sum(nil)), count, bytes, nil
}

func digestBytes(value []byte) string {
	sum := sha256.Sum256(value)
	return hex.EncodeToString(sum[:])
}

package contentcache

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
)

type Retirement struct {
	Path         string `json:"path"`
	LogicalBytes int64  `json:"logicalBytes"`
	FileCount    int    `json:"fileCount"`
	Executed     bool   `json:"executed"`
}

// RetireLocal only removes the old generated cache after verifying a matching
// shared manifest, every referenced blob, and the complete removal boundary.
func (s Store) RetireLocal(ctx context.Context, project, key string, execute bool) (Retirement, error) {
	p, err := filepath.Abs(project)
	if err != nil {
		return Retirement{}, err
	}
	target := filepath.Join(p, ".testplay", "cache")
	result := Retirement{Path: target}
	if err = s.ValidateLocation(p); err != nil {
		return result, err
	}
	if err = safePath(target); err != nil {
		return result, err
	}
	if _, err = os.Lstat(filepath.Join(p, "Temp", "UnityLockfile")); err == nil {
		return result, errors.New("close Unity before retiring the local cache")
	} else if !os.IsNotExist(err) {
		return result, err
	}
	shadows, err := filepath.Glob(filepath.Join(p, ".testplay-shadow-*"))
	if err != nil {
		return result, err
	}
	if len(shadows) > 0 {
		return result, errors.New("shadow workspaces are present; finish or resolve those runs first")
	}
	tracked, err := exec.CommandContext(ctx, "git", "-C", p, "ls-files", "-z", "--", ".testplay/cache").Output()
	if err != nil {
		return result, fmt.Errorf("cannot verify Git ownership: %w", err)
	}
	if len(tracked) > 0 {
		return result, errors.New("local cache contains tracked files")
	}
	// v0.11 local-cache ownership marker: exact original two-file compatibility key.
	h := sha256.New()
	for _, rel := range []string{filepath.Join("ProjectSettings", "ProjectVersion.txt"), filepath.Join("Packages", "manifest.json")} {
		data, err := os.ReadFile(filepath.Join(p, rel))
		if err != nil {
			return result, err
		}
		h.Write([]byte(rel))
		h.Write(data)
	}
	marker, err := os.ReadFile(filepath.Join(target, "cache.key"))
	if err != nil {
		return result, err
	}
	if string(marker) != hex.EncodeToString(h.Sum(nil)) {
		return result, errors.New("legacy cache ownership/key cannot be verified")
	}
	children, err := os.ReadDir(target)
	if err != nil {
		return result, err
	}
	for _, child := range children {
		if child.Name() != "Library" && child.Name() != "cache.key" {
			return result, errors.New("unexpected files in local cache; preserved")
		}
	}
	id, err := Identity(p)
	if err != nil {
		return result, err
	}
	unlock, err := s.lock(ctx, true, false)
	if err != nil {
		return result, err
	}
	defer unlock()
	manifest, err := s.readManifest(id)
	if err != nil {
		return result, err
	}
	if manifest.Key != key {
		return result, errors.New("no compatible shared cache; run the updated TestPlay successfully first")
	}
	for _, e := range manifest.Entries {
		if e.Directory {
			continue
		}
		blob := filepath.Join(s.Root, "blobs", e.Hash)
		if err = safePath(blob); err != nil {
			return result, err
		}
		file, err := os.Open(blob)
		if err != nil {
			return result, err
		}
		h := sha256.New()
		n, copyErr := io.Copy(h, &contextReader{ctx, file})
		file.Close()
		if copyErr != nil {
			return result, copyErr
		}
		if n != e.Size || hex.EncodeToString(h.Sum(nil)) != e.Hash {
			return result, errors.New("shared cache verification failed; local cache preserved")
		}
	}
	err = filepath.WalkDir(target, func(path string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		if isLink(info) || (!info.IsDir() && !info.Mode().IsRegular()) {
			return errors.New("linked or special cache entries are preserved")
		}
		if !info.IsDir() {
			result.FileCount++
			result.LogicalBytes += info.Size()
		}
		return nil
	})
	if err != nil {
		return result, err
	}
	if execute {
		// The fixed child boundary and every descendant were checked above. Never
		// compose a shell deletion or use a manifest-supplied path for removal.
		if err = safePath(target); err != nil {
			return result, err
		}
		if filepath.Dir(filepath.Dir(target)) != p {
			return result, errors.New("retirement path escaped project")
		}
		if err = os.RemoveAll(target); err != nil {
			return result, err
		}
		result.Executed = true
	}
	return result, nil
}

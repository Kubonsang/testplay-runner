// Package contentcache stores immutable file contents with project-local manifests.
// Unity only sees independent copies. OS locks pin all referenced blobs while
// restoring; a crashed process releases its lock without stale reader journals.
package contentcache

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/Kubonsang/testplay-runner/internal/atomicfile"
	"github.com/Kubonsang/testplay-runner/internal/libraryimage"
)

type Entry struct {
	Path      string    `json:"path"`
	Hash      string    `json:"hash,omitempty"`
	Size      int64     `json:"size"`
	Mode      uint32    `json:"mode"`
	Modified  time.Time `json:"modified"`
	Directory bool      `json:"directory,omitempty"`
}
type Manifest struct {
	SchemaVersion int     `json:"schemaVersion"`
	ProjectID     string  `json:"projectId"`
	Key           string  `json:"key"`
	Entries       []Entry `json:"entries"`
}
type Usage struct {
	SchemaVersion    int       `json:"schemaVersion"`
	Root             string    `json:"root"`
	ManifestCount    int       `json:"manifestCount"`
	ReferencedBytes  int64     `json:"referencedBytes"`
	UniqueBytes      int64     `json:"uniqueBytes"`
	ReclaimableBytes int64     `json:"reclaimableBytes"`
	PrunedBytes      int64     `json:"prunedBytes"`
	MeasuredAt       time.Time `json:"measuredAt"`
}
type Store struct{ Root string }

func DefaultRoot() (string, error) {
	root, err := os.UserCacheDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(root, "TestPlay", "Cache", "v1"), nil
}
func New(root string) Store { return Store{Root: root} }
func (s Store) ValidateLocation(project string) error {
	if !filepath.IsAbs(s.Root) {
		return errors.New("cache root must be absolute")
	}
	if err := safePath(s.Root); err != nil {
		return err
	}
	p, err := filepath.Abs(project)
	if err != nil {
		return err
	}
	p, err = filepath.EvalSymlinks(p)
	if err != nil {
		return err
	}
	p = canonicalPath(filepath.Clean(p))
	root := canonicalPath(filepath.Clean(s.Root))
	sep := string(os.PathSeparator)
	if p == root || strings.HasPrefix(p, root+sep) || strings.HasPrefix(root, p+sep) {
		return errors.New("shared cache root must be outside the project")
	}
	return nil
}
func Identity(project string) (string, error) {
	absolute, err := filepath.Abs(project)
	if err != nil {
		return "", err
	}
	absolute, err = filepath.EvalSymlinks(absolute)
	if err != nil {
		return "", err
	}
	return digest([]byte(canonicalPath(absolute))), nil
}
func Key(project, unity string) (string, error) {
	key, err := libraryimage.ComputeKey(project, unity)
	if err != nil {
		return "", err
	}
	// The older image key hashes the executable path, not its contents.
	executable, err := os.Open(unity)
	if err != nil {
		return "", err
	}
	defer executable.Close()
	h := sha256.New()
	if _, err = io.Copy(h, executable); err != nil {
		return "", err
	}
	return digest([]byte(key.Digest + hex.EncodeToString(h.Sum(nil)))), nil
}
func digest(data []byte) string { sum := sha256.Sum256(data); return hex.EncodeToString(sum[:]) }
func validHash(s string) bool {
	data, err := hex.DecodeString(s)
	return err == nil && len(data) == 32 && strings.ToLower(s) == s
}

// Check every ancestor, including the root itself, before traversing or deleting.
func safePath(target string) error {
	absolute, err := filepath.Abs(target)
	if err != nil {
		return err
	}
	for current := absolute; ; current = filepath.Dir(current) {
		info, err := os.Lstat(current)
		if err != nil && !os.IsNotExist(err) {
			return err
		}
		if err == nil && isLink(info) {
			return fmt.Errorf("cache path is a link: %s", current)
		}
		parent := filepath.Dir(current)
		if parent == current {
			break
		}
	}
	return nil
}
func (s Store) initialize() error {
	if !filepath.IsAbs(s.Root) {
		return errors.New("cache root must be absolute")
	}
	if err := safePath(s.Root); err != nil {
		return err
	}
	for _, child := range []string{"blobs", "manifests"} {
		p := filepath.Join(s.Root, child)
		if err := safePath(p); err != nil {
			return err
		}
		if err := os.MkdirAll(p, 0700); err != nil {
			return err
		}
	}
	return nil
}
func (s Store) lock(ctx context.Context, write bool, create bool) (func(), error) {
	if !filepath.IsAbs(s.Root) {
		return nil, errors.New("cache root must be absolute")
	}
	if create {
		if err := s.initialize(); err != nil {
			return nil, err
		}
	}
	if err := safePath(s.Root); err != nil {
		return nil, err
	}
	p := filepath.Join(s.Root, "store.lock")
	if err := safePath(p); err != nil {
		return nil, err
	}
	flags := os.O_RDWR
	if create {
		flags |= os.O_CREATE
	}
	f, err := os.OpenFile(p, flags, 0600)
	if err != nil {
		return nil, err
	}
	for {
		err = lockFile(f, write)
		if err == nil {
			return func() { _ = unlockFile(f); _ = f.Close() }, nil
		}
		if !lockBusy(err) {
			_ = f.Close()
			return nil, err
		}
		select {
		case <-ctx.Done():
			_ = f.Close()
			return nil, ctx.Err()
		case <-time.After(20 * time.Millisecond):
		}
	}
}
func (s Store) manifestPath(id string) (string, error) {
	if !validHash(id) {
		return "", errors.New("invalid cache project ID")
	}
	p := filepath.Join(s.Root, "manifests", id+".json")
	return p, safePath(p)
}
func (s Store) readManifest(id string) (Manifest, error) {
	var m Manifest
	p, err := s.manifestPath(id)
	if err != nil {
		return m, err
	}
	data, err := os.ReadFile(p)
	if err != nil {
		return m, err
	}
	if err = json.Unmarshal(data, &m); err != nil {
		return m, err
	}
	if m.SchemaVersion != 1 || m.ProjectID != id || !validHash(m.Key) {
		return m, errors.New("invalid cache manifest")
	}
	seen := map[string]bool{}
	for _, e := range m.Entries {
		rel := filepath.FromSlash(e.Path)
		clean := canonicalPath(rel)
		if !filepath.IsLocal(rel) || rel == "." || strings.ContainsAny(e.Path, "\\:") || filepath.ToSlash(filepath.Clean(rel)) != e.Path || seen[clean] || e.Size < 0 || (!e.Directory && !validHash(e.Hash)) {
			return m, errors.New("invalid cache entry")
		}
		seen[clean] = true
	}
	return m, nil
}

// Publish keeps the previous manifest reachable until every blob is durable.
func (s Store) Publish(ctx context.Context, project, key, library string) error {
	if err := s.ValidateLocation(project); err != nil {
		return err
	}
	if err := s.ValidateLocation(library); err != nil {
		return err
	}
	if !validHash(key) {
		return errors.New("invalid cache key")
	}
	if err := safePath(library); err != nil {
		return err
	}
	id, err := Identity(project)
	if err != nil {
		return err
	}
	unlock, err := s.lock(ctx, true, true)
	if err != nil {
		return err
	}
	defer unlock()
	m := Manifest{SchemaVersion: 1, ProjectID: id, Key: key, Entries: []Entry{}}
	type sourceFile struct {
		path  string
		info  fs.FileInfo
		index int
	}
	var files []sourceFile
	err = filepath.WalkDir(library, func(p string, d fs.DirEntry, walkErr error) error {
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
			return fmt.Errorf("unsupported cache entry: %s", p)
		}
		if p == library {
			return nil
		}
		rel, err := filepath.Rel(library, p)
		if err != nil {
			return err
		}
		e := Entry{Path: filepath.ToSlash(rel), Mode: uint32(info.Mode().Perm()), Modified: info.ModTime(), Directory: info.IsDir()}
		if !e.Directory {
			e.Size = info.Size()
			files = append(files, sourceFile{p, info, len(m.Entries)})
		}
		m.Entries = append(m.Entries, e)
		return nil
	})
	if err != nil {
		return err
	}
	var stripes [64]sync.Mutex
	if err = parallel(ctx, len(files), func(ctx context.Context, i int) error {
		file := files[i]
		hash, err := s.putBlob(ctx, file.path, file.info, &stripes)
		if err == nil {
			m.Entries[file.index].Hash = hash
		}
		return err
	}); err != nil {
		return err
	}
	target, err := s.manifestPath(id)
	if err != nil {
		return err
	}
	data, err := json.Marshal(m)
	if err != nil {
		return err
	}
	return durableWrite(target, data)
}
func (s Store) putBlob(ctx context.Context, source string, before fs.FileInfo, stripes *[64]sync.Mutex) (string, error) {
	f, err := os.Open(source)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err = io.Copy(h, &contextReader{ctx, f}); err != nil {
		return "", err
	}
	hash := hex.EncodeToString(h.Sum(nil))
	stripe := &stripes[h.Sum(nil)[0]%64]
	stripe.Lock()
	defer stripe.Unlock()
	target := filepath.Join(s.Root, "blobs", hash)
	// initialize checked the store ancestors; the exclusive store lock keeps
	// other cache operations from changing the blob tree. Check the leaf here.
	existing, statErr := os.Lstat(target)
	if statErr == nil {
		if isLink(existing) || !existing.Mode().IsRegular() || existing.Size() != before.Size() {
			return "", errors.New("corrupt existing cache blob")
		}
		// Verify existing bytes; equal length alone must never poison new manifests.
		check, err := os.Open(target)
		if err != nil {
			return "", err
		}
		verifier := sha256.New()
		_, err = io.Copy(verifier, &contextReader{ctx, check})
		_ = check.Close()
		if err != nil {
			return "", err
		}
		if hex.EncodeToString(verifier.Sum(nil)) != hash {
			return "", errors.New("corrupt existing cache blob")
		}
	} else if !os.IsNotExist(statErr) {
		return "", statErr
	} else {
		if _, err = f.Seek(0, 0); err != nil {
			return "", err
		}
		tmp, err := os.CreateTemp(filepath.Join(s.Root, "blobs"), ".pending-")
		if err != nil {
			return "", err
		}
		tempPath := tmp.Name()
		defer os.Remove(tempPath)
		verifier := sha256.New()
		n, copyErr := io.Copy(io.MultiWriter(tmp, verifier), &contextReader{ctx, f})
		if copyErr == nil {
			copyErr = tmp.Sync()
		}
		closeErr := tmp.Close()
		if err = errors.Join(copyErr, closeErr); err != nil {
			return "", err
		}
		if n != before.Size() || hex.EncodeToString(verifier.Sum(nil)) != hash {
			return "", errors.New("cache source changed while copying")
		}
		if err = atomicfile.Rename(tempPath, target); err != nil {
			return "", err
		}
	}
	after, err := os.Stat(source)
	if err != nil {
		return "", err
	}
	if !os.SameFile(before, after) || before.Size() != after.Size() || !before.ModTime().Equal(after.ModTime()) {
		return "", errors.New("cache source changed while hashing")
	}
	return hash, nil
}
func durableWrite(target string, data []byte) error {
	tmp, err := os.CreateTemp(filepath.Dir(target), ".pending-")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	_, err = tmp.Write(data)
	if err == nil {
		err = tmp.Sync()
	}
	closeErr := tmp.Close()
	if err = errors.Join(err, closeErr); err != nil {
		return err
	}
	return atomicfile.Rename(tmp.Name(), target)
}

type contextReader struct {
	ctx context.Context
	r   io.Reader
}

func (r *contextReader) Read(p []byte) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	return r.r.Read(p)
}

// Restore requires a new destination and pins the manifest for the entire copy.
// A corrupt cache returns an error; callers must discard only their own shadow.
func (s Store) Restore(ctx context.Context, project, key, destination string) (bool, error) {
	id, err := Identity(project)
	if err != nil {
		return false, err
	}
	unlock, err := s.lock(ctx, false, false)
	if os.IsNotExist(err) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	defer unlock()
	m, err := s.readManifest(id)
	if os.IsNotExist(err) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if m.Key != key {
		return false, nil
	}
	if err = safePath(destination); err != nil {
		return false, err
	}
	if err = safePath(filepath.Join(s.Root, "blobs")); err != nil {
		return false, err
	}
	if err = os.Mkdir(destination, 0700); err != nil {
		return false, err
	}
	var files []Entry
	for _, e := range m.Entries {
		if err = ctx.Err(); err != nil {
			return false, err
		}
		target := filepath.Join(destination, filepath.FromSlash(e.Path))
		if e.Directory {
			// Every directory is created exclusively under the new destination.
			// Never accept an existing directory or follow a pre-existing junction.
			if err = os.Mkdir(target, 0700); err != nil {
				return false, err
			}
			continue
		}
		files = append(files, e)
	}
	if err = parallel(ctx, len(files), func(ctx context.Context, i int) error {
		e := files[i]
		blob := filepath.Join(s.Root, "blobs", e.Hash)
		info, err := os.Lstat(blob)
		if err != nil {
			return err
		}
		if isLink(info) || !info.Mode().IsRegular() {
			return errors.New("unsafe cache blob")
		}
		target := filepath.Join(destination, filepath.FromSlash(e.Path))
		// Manifest paths are normalized/local, all parents were just created,
		// and restoreFile uses exclusive creation for each regular file.
		return restoreFile(ctx, blob, target, e)
	}); err != nil {
		return false, err
	}
	for i := len(m.Entries) - 1; i >= 0; i-- {
		e := m.Entries[i]
		if !e.Directory {
			continue
		}
		target := filepath.Join(destination, filepath.FromSlash(e.Path))
		if err = restoreMode(target, fs.FileMode(e.Mode)&0777); err != nil {
			return false, err
		}
		if err = os.Chtimes(target, e.Modified, e.Modified); err != nil {
			return false, err
		}
	}
	return true, nil
}

// Bounded parallel IO matches the legacy copier without unbounded file handles.
func parallel(parent context.Context, count int, operation func(context.Context, int) error) error {
	ctx, cancel := context.WithCancelCause(parent)
	defer cancel(nil)
	jobs := make(chan int)
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for job := range jobs {
				if ctx.Err() != nil {
					return
				}
				if err := operation(ctx, job); err != nil {
					cancel(err)
					return
				}
			}
		}()
	}
send:
	for i := 0; i < count; i++ {
		select {
		case jobs <- i:
		case <-ctx.Done():
			break send
		}
	}
	close(jobs)
	wg.Wait()
	return context.Cause(ctx)
}
func restoreFile(ctx context.Context, blob, target string, e Entry) error {
	src, err := os.Open(blob)
	if err != nil {
		return err
	}
	defer src.Close()
	dst, err := os.OpenFile(target, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	h := sha256.New()
	n, copyErr := io.Copy(io.MultiWriter(dst, h), &contextReader{ctx, src})
	closeErr := dst.Close()
	if err = errors.Join(copyErr, closeErr); err != nil {
		return err
	}
	if n != e.Size || hex.EncodeToString(h.Sum(nil)) != e.Hash {
		return errors.New("cache blob checksum mismatch")
	}
	if err = restoreMode(target, fs.FileMode(e.Mode)&0777); err != nil {
		return err
	}
	return os.Chtimes(target, e.Modified, e.Modified)
}
func (s Store) Clear(ctx context.Context, project string) error {
	id, err := Identity(project)
	if err != nil {
		return err
	}
	unlock, err := s.lock(ctx, true, false)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	defer unlock()
	p, err := s.manifestPath(id)
	if err != nil {
		return err
	}
	err = os.Remove(p)
	if os.IsNotExist(err) {
		return nil
	}
	return err
}

// Inspect never creates the store. Prune only deletes validated, unreferenced
// regular blobs under this exact store; manifests and project files are retained.
func (s Store) Inspect(ctx context.Context, prune bool) (Usage, error) {
	result := Usage{SchemaVersion: 1, Root: s.Root, MeasuredAt: time.Now().UTC()}
	unlock, err := s.lock(ctx, prune, false)
	if os.IsNotExist(err) {
		return result, nil
	}
	if err != nil {
		return result, err
	}
	defer unlock()
	refs := map[string]bool{}
	if err = safePath(filepath.Join(s.Root, "manifests")); err != nil {
		return result, err
	}
	manifests, err := os.ReadDir(filepath.Join(s.Root, "manifests"))
	if err != nil {
		return result, err
	}
	for _, d := range manifests {
		if strings.HasPrefix(d.Name(), ".pending-") {
			continue
		}
		if !strings.HasSuffix(d.Name(), ".json") {
			return result, errors.New("unexpected manifest file")
		}
		m, err := s.readManifest(strings.TrimSuffix(d.Name(), ".json"))
		if err != nil {
			return result, err
		}
		result.ManifestCount++
		for _, e := range m.Entries {
			if !e.Directory {
				refs[e.Hash] = true
				result.ReferencedBytes += e.Size
			}
		}
	}
	if err = safePath(filepath.Join(s.Root, "blobs")); err != nil {
		return result, err
	}
	blobs, err := os.ReadDir(filepath.Join(s.Root, "blobs"))
	if err != nil {
		return result, err
	}
	var candidates []string
	for _, d := range blobs {
		if err = ctx.Err(); err != nil {
			return result, err
		}
		info, err := d.Info()
		if err != nil {
			return result, err
		}
		if isLink(info) || !info.Mode().IsRegular() {
			return result, errors.New("unsafe blob entry")
		}
		if !validHash(d.Name()) && !strings.HasPrefix(d.Name(), ".pending-") {
			return result, errors.New("unexpected blob file")
		}
		result.UniqueBytes += info.Size()
		if !refs[d.Name()] {
			result.ReclaimableBytes += info.Size()
			candidates = append(candidates, d.Name())
		}
	}
	if prune {
		for _, name := range candidates {
			if err = ctx.Err(); err != nil {
				return result, err
			}
			p := filepath.Join(s.Root, "blobs", name)
			if err = safePath(p); err != nil {
				return result, err
			}
			info, err := os.Lstat(p)
			if err != nil {
				return result, err
			}
			if err = os.Remove(p); err != nil {
				return result, err
			}
			result.PrunedBytes += info.Size()
		}
	}
	return result, nil
}

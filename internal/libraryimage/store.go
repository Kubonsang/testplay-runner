package libraryimage

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/Kubonsang/testplay-runner/internal/atomicfile"
	"github.com/Kubonsang/testplay-runner/internal/shadow"
)

type Status string

const (
	StatusValid       Status = "valid"
	StatusMissing     Status = "missing"
	StatusStale       Status = "stale"
	StatusCorrupt     Status = "corrupt"
	StatusUnsupported Status = "unsupported"
)

const (
	metadataFile = "metadata.json"
	completeFile = "COMPLETE"
)

type Metadata struct {
	SchemaVersion string    `json:"schemaVersion"`
	Key           Key       `json:"key"`
	CreatedAt     time.Time `json:"createdAt"`
	LibraryDigest string    `json:"libraryDigest"`
	FileCount     int64     `json:"fileCount"`
	LogicalBytes  int64     `json:"logicalBytes"`
}

type Image struct {
	Path        string
	LibraryPath string
	Metadata    Metadata
}

type Resolution struct {
	Status Status
	Image  *Image
	Reason string
}

type ImageSource struct {
	LibraryPath string
	Release     func()
}

// VerificationMetrics reports cumulative Store verification work. Callers can
// take snapshots before and after a lifecycle operation to attribute phases
// without weakening or repeating integrity checks.
type VerificationMetrics struct {
	MetadataVerify time.Duration
	FullHash       time.Duration
	FullHashCount  int64
}

type Store struct {
	root         string
	now          func() time.Time
	pid          int
	processAlive func(int) bool
	metricsMu    sync.Mutex
	metrics      VerificationMetrics
}

func NewStore(projectPath string) *Store {
	return NewStoreAt(filepath.Join(projectPath, ".testplay", "library-images"))
}

// NewStoreAt creates an image store at an explicit persistent root. The
// caller is responsible for project namespacing the root.
func NewStoreAt(root string) *Store {
	return &Store{
		root:         root,
		now:          time.Now,
		pid:          os.Getpid(),
		processAlive: processIsAlive,
	}
}

func (s *Store) Root() string {
	return s.root
}

func (s *Store) VerificationMetrics() VerificationMetrics {
	s.metricsMu.Lock()
	defer s.metricsMu.Unlock()
	return s.metrics
}

func (s *Store) recordMetadataVerify(duration time.Duration) {
	s.metricsMu.Lock()
	defer s.metricsMu.Unlock()
	s.metrics.MetadataVerify += duration
}

func (s *Store) recordFullHash(duration time.Duration) {
	s.metricsMu.Lock()
	defer s.metricsMu.Unlock()
	s.metrics.FullHash += duration
	s.metrics.FullHashCount++
}

// Clear removes all images only when no image creation lock is present.
func (s *Store) Clear() error {
	entries, err := os.ReadDir(filepath.Join(s.root, "locks"))
	if err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("clear library images: list locks: %w", err)
	}
	if len(entries) > 0 {
		return fmt.Errorf("clear library images: %w", errLockConflict)
	}
	return os.RemoveAll(s.root)
}

func (s *Store) imageDir(key Key) string {
	return filepath.Join(s.root, "images", key.Digest)
}

func (s *Store) Resolve(ctx context.Context, key Key) (Resolution, error) {
	if err := ctx.Err(); err != nil {
		return Resolution{}, err
	}
	if key.SchemaVersion != SchemaVersion {
		return Resolution{
			Status: StatusUnsupported,
			Reason: fmt.Sprintf("image schema %q is unsupported", key.SchemaVersion),
		}, nil
	}

	path := s.imageDir(key)
	if _, err := os.Lstat(path); err != nil {
		if !os.IsNotExist(err) {
			return Resolution{}, fmt.Errorf("resolve library image: %w", err)
		}
		stale, staleErr := s.hasOtherImages(key.Digest)
		if staleErr != nil {
			return Resolution{}, staleErr
		}
		if stale {
			return Resolution{Status: StatusStale, Reason: "a Library image exists for a different key"}, nil
		}
		return Resolution{Status: StatusMissing, Reason: "no Library image exists"}, nil
	}

	image, reason, err := s.verifyPath(ctx, path, key)
	if err != nil {
		return Resolution{}, err
	}
	if image == nil {
		return Resolution{Status: StatusCorrupt, Reason: reason}, nil
	}
	return Resolution{Status: StatusValid, Image: image}, nil
}

func (s *Store) Verify(ctx context.Context, image *Image) (Resolution, error) {
	if image == nil {
		return Resolution{Status: StatusCorrupt, Reason: "image is nil"}, nil
	}
	verified, reason, err := s.verifyPath(ctx, image.Path, image.Metadata.Key)
	if err != nil {
		return Resolution{}, err
	}
	if verified == nil {
		return Resolution{Status: StatusCorrupt, Reason: reason}, nil
	}
	return Resolution{Status: StatusValid, Image: verified}, nil
}

// Create copies a fully prepared Library into an immutable image directory.
// The image becomes visible only after metadata, integrity digest, and COMPLETE
// have been written and the staging directory is atomically renamed.
func (s *Store) Create(ctx context.Context, key Key, sourceLibrary string) (*Image, error) {
	image, _, err := s.Ensure(ctx, key, func() (ImageSource, error) {
		return ImageSource{LibraryPath: sourceLibrary}, nil
	})
	return image, err
}

// Ensure serializes the entire image generation lifecycle, including the
// caller's Unity builder. A competing process receives an explicit lock
// conflict instead of doing duplicate import/compile work.
func (s *Store) Ensure(
	ctx context.Context,
	key Key,
	build func() (ImageSource, error),
) (*Image, Status, error) {
	lock, err := s.acquireLock(key)
	if err != nil {
		return nil, "", err
	}
	defer lock.release()

	resolution, err := s.Resolve(ctx, key)
	if err != nil {
		return nil, "", err
	}
	if resolution.Status == StatusValid {
		return resolution.Image, StatusValid, nil
	}
	if resolution.Status == StatusUnsupported {
		return nil, resolution.Status, fmt.Errorf("create library image: %s", resolution.Reason)
	}
	if resolution.Status == StatusCorrupt {
		if err := s.quarantine(s.imageDir(key), "corrupt"); err != nil {
			return nil, resolution.Status, fmt.Errorf("create library image: quarantine corrupt image: %w", err)
		}
	}

	source, err := build()
	if err != nil {
		return nil, resolution.Status, err
	}
	if source.Release != nil {
		defer source.Release()
	}
	image, err := s.createLocked(ctx, key, source.LibraryPath)
	return image, resolution.Status, err
}

func (s *Store) createLocked(ctx context.Context, key Key, sourceLibrary string) (*Image, error) {
	if info, err := os.Stat(sourceLibrary); err != nil {
		return nil, fmt.Errorf("create library image: source Library: %w", err)
	} else if !info.IsDir() {
		return nil, fmt.Errorf("create library image: source Library is not a directory")
	}

	imagesDir := filepath.Join(s.root, "images")
	if err := os.MkdirAll(imagesDir, 0755); err != nil {
		return nil, fmt.Errorf("create library image: images directory: %w", err)
	}
	staging, err := os.MkdirTemp(imagesDir, "."+key.Digest+".tmp-")
	if err != nil {
		return nil, fmt.Errorf("create library image: staging directory: %w", err)
	}
	committed := false
	defer func() {
		if !committed {
			_ = os.RemoveAll(staging)
		}
	}()

	libraryPath := filepath.Join(staging, "Library")
	if err := shadow.CopyDirParallel(ctx, sourceLibrary, libraryPath, 0); err != nil {
		return nil, fmt.Errorf("create library image: copy Library: %w", err)
	}
	hashStarted := time.Now()
	digest, fileCount, logicalBytes, err := hashTree(libraryPath)
	s.recordFullHash(time.Since(hashStarted))
	if err != nil {
		return nil, fmt.Errorf("create library image: verify staged Library: %w", err)
	}
	metadata := Metadata{
		SchemaVersion: SchemaVersion,
		Key:           key,
		CreatedAt:     s.now().UTC(),
		LibraryDigest: digest,
		FileCount:     fileCount,
		LogicalBytes:  logicalBytes,
	}
	data, err := json.MarshalIndent(metadata, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("create library image: encode metadata: %w", err)
	}
	if err := os.WriteFile(filepath.Join(staging, metadataFile), data, 0644); err != nil {
		return nil, fmt.Errorf("create library image: write metadata: %w", err)
	}
	if err := os.WriteFile(filepath.Join(staging, completeFile), []byte("complete\n"), 0644); err != nil {
		return nil, fmt.Errorf("create library image: write completion marker: %w", err)
	}
	stagedImage, reason, err := s.verifyPath(ctx, staging, key)
	if err != nil {
		return nil, fmt.Errorf("create library image: verify staging: %w", err)
	}
	if stagedImage == nil {
		return nil, fmt.Errorf("create library image: staged image is corrupt: %s", reason)
	}

	finalPath := s.imageDir(key)
	if err := atomicfile.Rename(staging, finalPath); err != nil {
		return nil, fmt.Errorf("create library image: commit: %w", err)
	}
	committed = true
	stagedImage.Path = finalPath
	stagedImage.LibraryPath = filepath.Join(finalPath, "Library")
	return stagedImage, nil
}

func (s *Store) verifyPath(ctx context.Context, path string, key Key) (*Image, string, error) {
	metadataStarted := time.Now()
	metadataRecorded := false
	defer func() {
		if !metadataRecorded {
			s.recordMetadataVerify(time.Since(metadataStarted))
		}
	}()
	if err := ctx.Err(); err != nil {
		return nil, "", err
	}
	if _, err := os.Stat(filepath.Join(path, completeFile)); err != nil {
		if os.IsNotExist(err) {
			return nil, "completion marker is missing", nil
		}
		return nil, "", err
	}
	data, err := os.ReadFile(filepath.Join(path, metadataFile))
	if err != nil {
		if os.IsNotExist(err) {
			return nil, "metadata is missing", nil
		}
		return nil, "", err
	}
	var metadata Metadata
	if err := json.Unmarshal(data, &metadata); err != nil {
		return nil, "metadata is invalid JSON", nil
	}
	if metadata.SchemaVersion != SchemaVersion {
		return nil, "metadata schema does not match", nil
	}
	if metadata.Key.Digest != key.Digest || metadata.Key.SchemaVersion != key.SchemaVersion {
		return nil, "metadata key does not match requested key", nil
	}
	libraryPath := filepath.Join(path, "Library")
	s.recordMetadataVerify(time.Since(metadataStarted))
	metadataRecorded = true
	hashStarted := time.Now()
	digest, fileCount, logicalBytes, err := hashTree(libraryPath)
	s.recordFullHash(time.Since(hashStarted))
	if err != nil {
		if os.IsNotExist(err) {
			return nil, "Library directory is missing", nil
		}
		return nil, "", err
	}
	if digest != metadata.LibraryDigest || fileCount != metadata.FileCount || logicalBytes != metadata.LogicalBytes {
		return nil, "Library integrity verification failed", nil
	}
	return &Image{Path: path, LibraryPath: libraryPath, Metadata: metadata}, "", nil
}

func (s *Store) hasOtherImages(requestedDigest string) (bool, error) {
	entries, err := os.ReadDir(filepath.Join(s.root, "images"))
	if os.IsNotExist(err) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("resolve library image: list images: %w", err)
	}
	for _, entry := range entries {
		if entry.IsDir() && entry.Name() != requestedDigest && !strings.Contains(entry.Name(), ".tmp-") {
			return true, nil
		}
	}
	return false, nil
}

func (s *Store) quarantine(path, reason string) error {
	if _, err := os.Lstat(path); err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	dir := filepath.Join(s.root, "quarantine")
	if err := os.MkdirAll(dir, 0755); err != nil {
		return err
	}
	name := filepath.Base(path) + "-" + reason + "-" + s.now().UTC().Format("20060102T150405.000000000")
	return atomicfile.Rename(path, filepath.Join(dir, name))
}

var errLockConflict = errors.New("library image creation lock is held")

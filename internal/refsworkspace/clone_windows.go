//go:build windows

package refsworkspace

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

const (
	fsctlDuplicateExtentsToFile  = uint32(0x00098344)
	fsctlGetIntegrityInformation = uint32(0x0009027c)
	fsctlSetSparse               = uint32(0x000900c4)
	fsctlQueryAllocatedRanges    = uint32(0x000940cf)
	fileAttributeSparseFile      = uint32(0x00000200)
)

type nativeTreeCloner struct{}

func NewNativeTreeCloner() TreeCloner { return nativeTreeCloner{} }

type duplicateExtentsData struct {
	FileHandle       windows.Handle
	SourceFileOffset int64
	TargetFileOffset int64
	ByteCount        int64
}

type integrityInformation struct {
	ChecksumAlgorithm        uint16
	Reserved                 uint16
	Flags                    uint32
	ChecksumChunkSizeInBytes uint32
	ClusterSizeInBytes       uint32
}

type fileAllocatedRangeBuffer struct {
	FileOffset int64
	Length     int64
}

type volumeIdentity struct {
	Serial     uint32
	Filesystem string
	Flags      uint32
}

type directoryMetadata struct {
	path       string
	attributes uint32
	created    windows.Filetime
	accessed   windows.Filetime
	written    windows.Filetime
}

func (nativeTreeCloner) CloneTree(ctx context.Context, source, destination string, clusterSize int64) (metrics CloneMetrics, returnErr error) {
	started := time.Now()
	defer func() { metrics.CloneTreeMs = time.Since(started).Milliseconds() }()
	if err := ctx.Err(); err != nil {
		return metrics, cancelled("clone-tree", destination, err)
	}
	if _, err := PlanClone(0, clusterSize); err != nil {
		return metrics, newError(CodeInvalidConfiguration, "clone-tree-cluster", source, err)
	}
	source, err := canonicalExistingPath(source)
	if err != nil {
		return metrics, newError(CodeCloneFailed, "canonical-clone-source", source, err)
	}
	info, err := os.Lstat(source)
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return metrics, newError(CodeCloneFailed, "validate-clone-source", source, errors.Join(err, fmt.Errorf("source must be a real directory")))
	}
	if _, err := os.Lstat(destination); !os.IsNotExist(err) {
		return metrics, newError(CodeCloneFailed, "validate-clone-destination", destination, fmt.Errorf("destination must not exist"))
	}
	if err := os.MkdirAll(destination, 0700); err != nil {
		return metrics, newError(CodeCloneFailed, "create-clone-destination", destination, err)
	}
	var directories []directoryMetadata
	err = filepath.WalkDir(source, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		rel, err := filepath.Rel(source, path)
		if err != nil {
			return err
		}
		target := filepath.Join(destination, rel)
		info, err := entry.Info()
		if err != nil {
			return err
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("reparse points are forbidden in Library trees: %s", path)
		}
		switch {
		case entry.IsDir():
			if path != source {
				if err := os.Mkdir(target, 0700); err != nil {
					return err
				}
			}
			metadata, err := readDirectoryMetadata(path, target)
			if err != nil {
				return err
			}
			directories = append(directories, metadata)
			metrics.MetadataOnlyFileCount++
		case info.Mode().IsRegular():
			fileMetrics, err := cloneFile(ctx, path, target, clusterSize)
			metrics.ClonedFileCount += fileMetrics.ClonedFileCount
			metrics.ClonedBytes += fileMetrics.ClonedBytes
			metrics.PhysicalCopiedFileCount += fileMetrics.PhysicalCopiedFileCount
			metrics.PhysicalCopiedBytes += fileMetrics.PhysicalCopiedBytes
			metrics.TailCopiedBytes += fileMetrics.TailCopiedBytes
			metrics.MetadataOnlyFileCount += fileMetrics.MetadataOnlyFileCount
			metrics.SparseFileCount += fileMetrics.SparseFileCount
			metrics.SparseLogicalBytes += fileMetrics.SparseLogicalBytes
			metrics.SparseAllocatedSourceBytes += fileMetrics.SparseAllocatedSourceBytes
			metrics.SparseClonedBytes += fileMetrics.SparseClonedBytes
			metrics.SparseHoleBytes += fileMetrics.SparseHoleBytes
			if err != nil {
				metrics.FailedFileCount++
				return err
			}
		default:
			return fmt.Errorf("unsupported Library entry type %s: %s", info.Mode(), path)
		}
		return nil
	})
	if err != nil {
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			return metrics, cancelled("clone-tree", destination, err)
		}
		if ErrorCode(err) != "unknown" {
			return metrics, err
		}
		return metrics, newError(CodeCloneFailed, "clone-tree", destination, err)
	}
	// Restore directories from leaves to root so child creation does not alter
	// the baseline timestamps copied onto parents.
	sort.Slice(directories, func(i, j int) bool { return len(directories[i].path) > len(directories[j].path) })
	for _, metadata := range directories {
		if err := applyDirectoryMetadata(metadata); err != nil {
			return metrics, newError(CodeCloneFailed, "restore-directory-metadata", metadata.path, err)
		}
	}
	return metrics, nil
}

func cloneFile(ctx context.Context, sourcePath, destinationPath string, clusterSize int64) (CloneMetrics, error) {
	metrics := CloneMetrics{}
	source, err := os.Open(sourcePath)
	if err != nil {
		return metrics, err
	}
	defer source.Close()
	info, err := source.Stat()
	if err != nil {
		return metrics, err
	}
	destination, err := os.OpenFile(destinationPath, os.O_CREATE|os.O_EXCL|os.O_RDWR, 0600)
	if err != nil {
		return metrics, err
	}
	closeDestination := true
	defer func() {
		if closeDestination {
			_ = destination.Close()
		}
	}()
	sourceHandle := windows.Handle(source.Fd())
	destinationHandle := windows.Handle(destination.Fd())
	sourceAttributes, err := fileAttributes(sourcePath)
	if err != nil {
		return metrics, err
	}
	isSparse := sourceAttributes&fileAttributeSparseFile != 0
	if isSparse {
		var returned uint32
		if err := windows.DeviceIoControl(destinationHandle, fsctlSetSparse, nil, 0, nil, 0, &returned, nil); err != nil {
			return metrics, newError(CodeBlockCloneUnavailable, "set-destination-sparse", destinationPath, err)
		}
	}
	if err := destination.Truncate(info.Size()); err != nil {
		return metrics, err
	}
	sourceVolume, err := volumeForHandle(sourceHandle)
	if err != nil {
		return metrics, err
	}
	destinationVolume, err := volumeForHandle(destinationHandle)
	if err != nil {
		return metrics, err
	}
	if sourceVolume.Serial != destinationVolume.Serial || !strings.EqualFold(sourceVolume.Filesystem, "ReFS") || !strings.EqualFold(destinationVolume.Filesystem, "ReFS") {
		return metrics, newError(CodeBlockCloneUnavailable, "same-refs-volume", destinationPath, fmt.Errorf("source=%08x/%s destination=%08x/%s", sourceVolume.Serial, sourceVolume.Filesystem, destinationVolume.Serial, destinationVolume.Filesystem))
	}
	if sourceVolume.Flags&fileSupportsBlockRefcounting == 0 || destinationVolume.Flags&fileSupportsBlockRefcounting == 0 {
		return metrics, newError(CodeBlockCloneUnavailable, "block-refcount-capability", destinationPath, nil)
	}
	sourceIntegrity, err := getIntegrity(sourceHandle)
	if err != nil {
		return metrics, newError(CodeBlockCloneUnavailable, "source-integrity", sourcePath, err)
	}
	destinationIntegrity, err := getIntegrity(destinationHandle)
	if err != nil {
		return metrics, newError(CodeBlockCloneUnavailable, "destination-integrity", destinationPath, err)
	}
	if sourceIntegrity.ChecksumAlgorithm != destinationIntegrity.ChecksumAlgorithm || sourceIntegrity.Flags != destinationIntegrity.Flags || sourceIntegrity.ChecksumChunkSizeInBytes != destinationIntegrity.ChecksumChunkSizeInBytes {
		return metrics, newError(CodeBlockCloneUnavailable, "integrity-stream-mismatch", destinationPath, fmt.Errorf("source=%+v destination=%+v", sourceIntegrity, destinationIntegrity))
	}
	if int64(sourceIntegrity.ClusterSizeInBytes) != clusterSize || int64(destinationIntegrity.ClusterSizeInBytes) != clusterSize {
		return metrics, newError(CodePoolCorrupt, "cluster-size-mismatch", destinationPath, fmt.Errorf("expected=%d source=%d destination=%d", clusterSize, sourceIntegrity.ClusterSizeInBytes, destinationIntegrity.ClusterSizeInBytes))
	}
	var cloneRanges, physicalRanges []CloneRange
	if isSparse {
		plan, err := PlanSparseCloneFromQuery(info.Size(), clusterSize, func() ([]AllocatedRange, error) { return queryAllocatedRangesForClone(sourceHandle, info.Size()) })
		if err != nil {
			return metrics, newError(CodeBlockCloneUnavailable, "FSCTL_QUERY_ALLOCATED_RANGES", sourcePath, err)
		}
		cloneRanges, physicalRanges = plan.CloneRanges, plan.PhysicalRanges
		metrics.SparseFileCount = 1
		metrics.SparseLogicalBytes = info.Size()
		metrics.SparseAllocatedSourceBytes = plan.AllocatedBytes
		metrics.SparseHoleBytes = plan.HoleBytes
	} else {
		plan, err := PlanClone(info.Size(), clusterSize)
		if err != nil {
			return metrics, err
		}
		cloneRanges = plan.Ranges
		if plan.TailBytes > 0 {
			physicalRanges = []CloneRange{{Offset: plan.TailOffset, Length: plan.TailBytes}}
		}
	}
	for _, cloneRange := range cloneRanges {
		if err := ctx.Err(); err != nil {
			return metrics, err
		}
		request := duplicateExtentsData{
			FileHandle:       sourceHandle,
			SourceFileOffset: cloneRange.Offset,
			TargetFileOffset: cloneRange.Offset,
			ByteCount:        cloneRange.Length,
		}
		var returned uint32
		err := windows.DeviceIoControl(destinationHandle, fsctlDuplicateExtentsToFile, (*byte)(unsafe.Pointer(&request)), uint32(unsafe.Sizeof(request)), nil, 0, &returned, nil)
		if err != nil {
			code := CodeCloneFailed
			if errors.Is(err, windows.ERROR_INVALID_FUNCTION) || errors.Is(err, windows.ERROR_NOT_SUPPORTED) {
				code = CodeBlockCloneUnavailable
			}
			return metrics, newError(code, "FSCTL_DUPLICATE_EXTENTS_TO_FILE", destinationPath, err)
		}
		metrics.ClonedBytes += cloneRange.Length
		if isSparse {
			metrics.SparseClonedBytes += cloneRange.Length
		}
	}
	if len(cloneRanges) > 0 {
		metrics.ClonedFileCount = 1
	}
	for _, physicalRange := range physicalRanges {
		if err := copyFileRange(ctx, source, destination, physicalRange); err != nil {
			return metrics, err
		}
		metrics.PhysicalCopiedBytes += physicalRange.Length
		metrics.TailCopiedBytes += physicalRange.Length
	}
	if len(physicalRanges) > 0 {
		metrics.PhysicalCopiedFileCount = 1
	}
	if info.Size() == 0 {
		metrics.MetadataOnlyFileCount = 1
	}
	var sourceInfo windows.ByHandleFileInformation
	if err := windows.GetFileInformationByHandle(sourceHandle, &sourceInfo); err != nil {
		return metrics, err
	}
	if err := windows.SetFileTime(destinationHandle, &sourceInfo.CreationTime, &sourceInfo.LastAccessTime, &sourceInfo.LastWriteTime); err != nil {
		return metrics, err
	}
	if err := destination.Sync(); err != nil {
		return metrics, err
	}
	if err := destination.Close(); err != nil {
		return metrics, err
	}
	closeDestination = false
	if destinationInfo, err := os.Stat(destinationPath); err != nil || destinationInfo.Size() != info.Size() {
		return metrics, errors.Join(err, fmt.Errorf("destination size mismatch"))
	}
	if err := setCloneFileAttributes(destinationPath, sourceAttributes); err != nil {
		return metrics, err
	}
	return metrics, nil
}

var queryAllocatedRangesForClone = queryAllocatedRanges

func queryAllocatedRanges(handle windows.Handle, fileSize int64) ([]AllocatedRange, error) {
	const rangeBatch = 128
	output := make([]fileAllocatedRangeBuffer, rangeBatch)
	return collectAllocatedRangesPaged(fileSize, func(offset, length int64) ([]AllocatedRange, bool, error) {
		input := fileAllocatedRangeBuffer{FileOffset: offset, Length: length}
		var returned uint32
		err := windows.DeviceIoControl(handle, fsctlQueryAllocatedRanges,
			(*byte)(unsafe.Pointer(&input)), uint32(unsafe.Sizeof(input)),
			(*byte)(unsafe.Pointer(&output[0])), uint32(len(output))*uint32(unsafe.Sizeof(output[0])), &returned, nil)
		if err != nil && !errors.Is(err, windows.ERROR_MORE_DATA) {
			return nil, false, err
		}
		count := int(returned / uint32(unsafe.Sizeof(output[0])))
		if returned%uint32(unsafe.Sizeof(output[0])) != 0 || count > len(output) {
			return nil, false, fmt.Errorf("malformed allocated range response: %d bytes", returned)
		}
		ranges := make([]AllocatedRange, 0, count)
		for _, item := range output[:count] {
			ranges = append(ranges, AllocatedRange{Offset: item.FileOffset, Length: item.Length})
		}
		return ranges, errors.Is(err, windows.ERROR_MORE_DATA), nil
	})
}

func copyFileRange(ctx context.Context, source, destination *os.File, cloneRange CloneRange) error {
	buffer := make([]byte, 1<<20)
	for offset, remaining := cloneRange.Offset, cloneRange.Length; remaining > 0; {
		if err := ctx.Err(); err != nil {
			return err
		}
		chunk := int64(len(buffer))
		if chunk > remaining {
			chunk = remaining
		}
		read, err := source.ReadAt(buffer[:chunk], offset)
		if err != nil && err != io.EOF {
			return err
		}
		if int64(read) != chunk {
			return io.ErrUnexpectedEOF
		}
		written, err := destination.WriteAt(buffer[:chunk], offset)
		if err != nil {
			return err
		}
		if written != read {
			return io.ErrShortWrite
		}
		offset += chunk
		remaining -= chunk
	}
	return nil
}

func volumeForHandle(handle windows.Handle) (volumeIdentity, error) {
	filesystem := make([]uint16, 64)
	var serial, maxComponent, flags uint32
	if err := windows.GetVolumeInformationByHandle(handle, nil, 0, &serial, &maxComponent, &flags, &filesystem[0], uint32(len(filesystem))); err != nil {
		return volumeIdentity{}, err
	}
	return volumeIdentity{Serial: serial, Filesystem: windows.UTF16ToString(filesystem), Flags: flags}, nil
}

func getIntegrity(handle windows.Handle) (integrityInformation, error) {
	var result integrityInformation
	var returned uint32
	err := windows.DeviceIoControl(handle, fsctlGetIntegrityInformation, nil, 0, (*byte)(unsafe.Pointer(&result)), uint32(unsafe.Sizeof(result)), &returned, nil)
	if err != nil {
		return result, err
	}
	if returned < uint32(unsafe.Sizeof(result)) {
		return result, fmt.Errorf("short integrity response: %d", returned)
	}
	return result, nil
}

func fileAttributes(path string) (uint32, error) {
	ptr, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return 0, err
	}
	return windows.GetFileAttributes(ptr)
}

func setCloneFileAttributes(path string, attributes uint32) error {
	ptr, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return err
	}
	// Sparse and directory state are controlled by their dedicated operations.
	attributes &^= fileAttributeSparseFile | windows.FILE_ATTRIBUTE_DIRECTORY | windows.FILE_ATTRIBUTE_REPARSE_POINT
	if attributes == 0 {
		attributes = windows.FILE_ATTRIBUTE_NORMAL
	}
	return windows.SetFileAttributes(ptr, attributes)
}

func readDirectoryMetadata(source, destination string) (directoryMetadata, error) {
	ptr, err := windows.UTF16PtrFromString(source)
	if err != nil {
		return directoryMetadata{}, err
	}
	handle, err := windows.CreateFile(ptr, windows.FILE_READ_ATTRIBUTES, windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE, nil, windows.OPEN_EXISTING, windows.FILE_FLAG_BACKUP_SEMANTICS, 0)
	if err != nil {
		return directoryMetadata{}, err
	}
	defer windows.CloseHandle(handle)
	var info windows.ByHandleFileInformation
	if err := windows.GetFileInformationByHandle(handle, &info); err != nil {
		return directoryMetadata{}, err
	}
	return directoryMetadata{path: destination, attributes: info.FileAttributes, created: info.CreationTime, accessed: info.LastAccessTime, written: info.LastWriteTime}, nil
}

func applyDirectoryMetadata(metadata directoryMetadata) error {
	ptr, err := windows.UTF16PtrFromString(metadata.path)
	if err != nil {
		return err
	}
	handle, err := windows.CreateFile(ptr, windows.FILE_WRITE_ATTRIBUTES, windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE, nil, windows.OPEN_EXISTING, windows.FILE_FLAG_BACKUP_SEMANTICS, 0)
	if err != nil {
		return err
	}
	if err := windows.SetFileTime(handle, &metadata.created, &metadata.accessed, &metadata.written); err != nil {
		_ = windows.CloseHandle(handle)
		return err
	}
	if err := windows.CloseHandle(handle); err != nil {
		return err
	}
	attributes := metadata.attributes &^ windows.FILE_ATTRIBUTE_REPARSE_POINT
	return windows.SetFileAttributes(ptr, attributes)
}

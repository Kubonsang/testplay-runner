//go:build windows

package refsworkspace

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"golang.org/x/sys/windows"
)

type cloneDirectoryIdentity struct {
	Volume    volumeIdentity
	FinalPath string
}

type clonePathInspector interface {
	Attributes(string) (uint32, error)
	DirectoryIdentity(string) (cloneDirectoryIdentity, error)
}

type windowsClonePathInspector struct{}

func (windowsClonePathInspector) Attributes(path string) (uint32, error) {
	return fileAttributes(path)
}

func (windowsClonePathInspector) DirectoryIdentity(path string) (cloneDirectoryIdentity, error) {
	ptr, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return cloneDirectoryIdentity{}, err
	}
	handle, err := windows.CreateFile(ptr, windows.FILE_READ_ATTRIBUTES,
		windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE,
		nil, windows.OPEN_EXISTING, windows.FILE_FLAG_BACKUP_SEMANTICS, 0)
	if err != nil {
		return cloneDirectoryIdentity{}, err
	}
	defer windows.CloseHandle(handle)
	volume, err := volumeForHandle(handle)
	if err != nil {
		return cloneDirectoryIdentity{}, err
	}
	finalPath, err := finalPathForHandle(handle)
	if err != nil {
		return cloneDirectoryIdentity{}, err
	}
	return cloneDirectoryIdentity{Volume: volume, FinalPath: finalPath}, nil
}

type validatedCloneScope struct {
	TrustedRoot       string
	Source            string
	Destination       string
	DestinationParent string
	Volume            volumeIdentity
}

func prepareCloneScope(request CloneRequest, inspector clonePathInspector) (validatedCloneScope, error) {
	originalRoot, originalSource, originalDestination := request.TrustedRoot, request.Source, request.Destination
	if !filepath.IsAbs(originalRoot) {
		return validatedCloneScope{}, newError(CodeCloneFailed, "validate-clone-trusted-root", originalRoot, fmt.Errorf("trusted root must be absolute"))
	}
	trustedRoot, err := absoluteCleanPath(originalRoot)
	if err != nil {
		return validatedCloneScope{}, newError(CodeCloneFailed, "canonical-clone-trusted-root", originalRoot, err)
	}
	source, err := absoluteCleanPath(originalSource)
	if err != nil {
		return validatedCloneScope{}, newError(CodeCloneFailed, "canonical-clone-source", originalSource, err)
	}
	destination, err := absoluteCleanPath(originalDestination)
	if err != nil {
		return validatedCloneScope{}, newError(CodeCloneFailed, "canonical-clone-destination", originalDestination, err)
	}
	if !windowsPathWithin(trustedRoot, source) {
		return validatedCloneScope{}, newError(CodeCloneFailed, "validate-clone-containment", originalSource, fmt.Errorf("source is outside trusted root %q", originalRoot))
	}
	if !windowsPathWithin(trustedRoot, destination) {
		return validatedCloneScope{}, newError(CodeCloneFailed, "validate-clone-containment", originalDestination, fmt.Errorf("destination is outside trusted root %q", originalRoot))
	}
	if strings.EqualFold(source, destination) {
		return validatedCloneScope{}, newError(CodeCloneFailed, "validate-clone-destination", originalDestination, fmt.Errorf("source and destination must differ"))
	}
	if err := requireRealDirectory(trustedRoot, originalRoot, "validate-clone-trusted-root"); err != nil {
		return validatedCloneScope{}, err
	}
	if err := requireRealDirectory(source, originalSource, "validate-clone-source"); err != nil {
		return validatedCloneScope{}, err
	}
	if _, err := os.Lstat(destination); err == nil {
		return validatedCloneScope{}, newError(CodeCloneFailed, "validate-clone-destination", originalDestination, fmt.Errorf("destination already exists"))
	} else if !os.IsNotExist(err) {
		return validatedCloneScope{}, newError(CodeCloneFailed, "validate-clone-destination", originalDestination, err)
	}
	destinationParent := filepath.Dir(destination)
	if err := requireRealDirectory(destinationParent, filepath.Dir(originalDestination), "validate-clone-destination-parent"); err != nil {
		return validatedCloneScope{}, err
	}
	for _, boundary := range []struct {
		path, original string
	}{
		{trustedRoot, originalRoot},
		{source, originalSource},
		{destinationParent, filepath.Dir(originalDestination)},
	} {
		if err := validateNoCloneReparseComponents(trustedRoot, boundary.path, boundary.original, inspector); err != nil {
			return validatedCloneScope{}, err
		}
	}
	rootIdentity, err := inspector.DirectoryIdentity(trustedRoot)
	if err != nil {
		return validatedCloneScope{}, newError(CodeCloneFailed, "open-clone-trusted-root", originalRoot, err)
	}
	if err := validateCloneVolume(rootIdentity.Volume, rootIdentity.Volume, originalRoot); err != nil {
		return validatedCloneScope{}, err
	}
	sourceIdentity, err := inspector.DirectoryIdentity(source)
	if err != nil {
		return validatedCloneScope{}, newError(CodeCloneFailed, "open-clone-source", originalSource, err)
	}
	if err := validateCloneVolume(rootIdentity.Volume, sourceIdentity.Volume, originalSource); err != nil {
		return validatedCloneScope{}, err
	}
	parentIdentity, err := inspector.DirectoryIdentity(destinationParent)
	if err != nil {
		return validatedCloneScope{}, newError(CodeCloneFailed, "open-clone-destination-parent", filepath.Dir(originalDestination), err)
	}
	if err := validateCloneVolume(rootIdentity.Volume, parentIdentity.Volume, filepath.Dir(originalDestination)); err != nil {
		return validatedCloneScope{}, err
	}
	if err := os.Mkdir(destination, 0700); err != nil {
		return validatedCloneScope{}, newError(CodeCloneFailed, "create-clone-destination", originalDestination, err)
	}
	cleanupCreated := func(primary error) (validatedCloneScope, error) {
		if cleanupErr := os.Remove(destination); cleanupErr != nil {
			primary = errors.Join(primary, newError(CodeCleanupFailed, "remove-invalid-clone-destination", originalDestination, cleanupErr))
		}
		return validatedCloneScope{}, primary
	}
	attributes, err := inspector.Attributes(destination)
	if err != nil {
		return cleanupCreated(newError(CodeCloneFailed, "inspect-clone-destination", originalDestination, err))
	}
	if attributes&windows.FILE_ATTRIBUTE_REPARSE_POINT != 0 {
		return cleanupCreated(newError(CodeCloneFailed, "validate-clone-reparse-boundary", originalDestination, fmt.Errorf("destination is a reparse point")))
	}
	destinationIdentity, err := inspector.DirectoryIdentity(destination)
	if err != nil {
		return cleanupCreated(newError(CodeCloneFailed, "open-clone-destination", originalDestination, err))
	}
	if err := validateCloneVolume(rootIdentity.Volume, destinationIdentity.Volume, originalDestination); err != nil {
		return cleanupCreated(err)
	}
	if parentIdentity.FinalPath != "" && destinationIdentity.FinalPath != "" {
		expected := filepath.Join(normalizeFinalPath(parentIdentity.FinalPath), filepath.Base(destination))
		if !strings.EqualFold(filepath.Clean(expected), filepath.Clean(normalizeFinalPath(destinationIdentity.FinalPath))) {
			return cleanupCreated(newError(CodeCloneFailed, "validate-clone-destination-identity", originalDestination, fmt.Errorf("final destination escaped expected parent")))
		}
	}
	return validatedCloneScope{
		TrustedRoot: trustedRoot, Source: source, Destination: destination,
		DestinationParent: destinationParent, Volume: rootIdentity.Volume,
	}, nil
}

func absoluteCleanPath(path string) (string, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	return filepath.Clean(abs), nil
}

func windowsPathWithin(root, path string) bool {
	relative, err := filepath.Rel(root, path)
	if err != nil || filepath.IsAbs(relative) {
		return false
	}
	return relative == "." || (relative != ".." && !strings.HasPrefix(relative, ".."+string(os.PathSeparator)))
}

func requireRealDirectory(path, original, operation string) error {
	info, err := os.Lstat(path)
	if err != nil {
		return newError(CodeCloneFailed, operation, original, err)
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return newError(CodeCloneFailed, operation, original, fmt.Errorf("path must be a real directory"))
	}
	return nil
}

func validateNoCloneReparseComponents(trustedRoot, target, original string, inspector clonePathInspector) error {
	relative, err := filepath.Rel(trustedRoot, target)
	if err != nil || !windowsPathWithin(trustedRoot, target) {
		return newError(CodeCloneFailed, "validate-clone-containment", original, errors.Join(err, fmt.Errorf("path is outside trusted root")))
	}
	current := trustedRoot
	components := []string{}
	if relative != "." {
		components = strings.Split(relative, string(os.PathSeparator))
	}
	for index := -1; index < len(components); index++ {
		if index >= 0 {
			current = filepath.Join(current, components[index])
		}
		attributes, attrErr := inspector.Attributes(current)
		if attrErr != nil {
			return newError(CodeCloneFailed, "validate-clone-reparse-boundary", original, attrErr)
		}
		if attributes&windows.FILE_ATTRIBUTE_REPARSE_POINT != 0 {
			return newError(CodeCloneFailed, "validate-clone-reparse-boundary", original, fmt.Errorf("managed path component is a reparse point: %s", current))
		}
	}
	return nil
}

func validateCloneTreeEntry(path string, inspector clonePathInspector) error {
	attributes, err := inspector.Attributes(path)
	if err != nil {
		return err
	}
	if attributes&windows.FILE_ATTRIBUTE_REPARSE_POINT != 0 {
		return newError(CodeCloneFailed, "validate-clone-reparse-boundary", path, fmt.Errorf("reparse points are forbidden in Library trees"))
	}
	return nil
}

func validateCloneVolume(expected, actual volumeIdentity, originalPath string) error {
	if !strings.EqualFold(actual.Filesystem, "ReFS") {
		return newError(CodeBlockCloneUnavailable, "same-refs-volume", originalPath, fmt.Errorf("filesystem=%s", actual.Filesystem))
	}
	if actual.Flags&fileSupportsBlockRefcounting == 0 {
		return newError(CodeBlockCloneUnavailable, "block-refcount-capability", originalPath, nil)
	}
	if expected.Serial != actual.Serial {
		return newError(CodeBlockCloneUnavailable, "same-refs-volume", originalPath, fmt.Errorf("expected serial=%08x actual=%08x", expected.Serial, actual.Serial))
	}
	return nil
}

func finalPathForHandle(handle windows.Handle) (string, error) {
	buffer := make([]uint16, 512)
	for {
		length, err := windows.GetFinalPathNameByHandle(handle, &buffer[0], uint32(len(buffer)), 0)
		if err != nil {
			return "", err
		}
		if length < uint32(len(buffer)) {
			return windows.UTF16ToString(buffer[:length]), nil
		}
		buffer = make([]uint16, length+1)
	}
}

func normalizeFinalPath(path string) string {
	path = strings.TrimPrefix(path, `\\?\`)
	return filepath.Clean(path)
}

//go:build windows

package refsworkspace

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"golang.org/x/sys/windows"
)

type fakeClonePathInspector struct {
	attributes map[string]uint32
	identities map[string]cloneDirectoryIdentity
	attrCalls  []string
}

func cloneTestPathKey(path string) string {
	return strings.ToLower(filepath.Clean(path))
}

func (fake *fakeClonePathInspector) Attributes(path string) (uint32, error) {
	fake.attrCalls = append(fake.attrCalls, filepath.Clean(path))
	if attributes, ok := fake.attributes[cloneTestPathKey(path)]; ok {
		return attributes, nil
	}
	return windows.FILE_ATTRIBUTE_DIRECTORY, nil
}

func (fake *fakeClonePathInspector) DirectoryIdentity(path string) (cloneDirectoryIdentity, error) {
	if identity, ok := fake.identities[cloneTestPathKey(path)]; ok {
		return identity, nil
	}
	return cloneDirectoryIdentity{
		Volume:    volumeIdentity{Serial: 7, Filesystem: "ReFS", Flags: fileSupportsBlockRefcounting},
		FinalPath: filepath.Clean(path),
	}, nil
}

func newFakeClonePathInspector() *fakeClonePathInspector {
	return &fakeClonePathInspector{attributes: map[string]uint32{}, identities: map[string]cloneDirectoryIdentity{}}
}

type cloneScopeFixture struct {
	root, source, destinationParent, destination string
}

func newCloneScopeFixture(t *testing.T) cloneScopeFixture {
	t.Helper()
	base := t.TempDir()
	root := filepath.Join(base, "mount", "testplay")
	source := filepath.Join(root, "baselines", "key", "Library")
	destinationParent := filepath.Join(root, "workers", "lease")
	for _, path := range []string{source, destinationParent} {
		if err := os.MkdirAll(path, 0700); err != nil {
			t.Fatal(err)
		}
	}
	return cloneScopeFixture{root: root, source: source, destinationParent: destinationParent, destination: filepath.Join(destinationParent, "Library")}
}

func (fixture cloneScopeFixture) request() CloneRequest {
	return CloneRequest{TrustedRoot: fixture.root, Source: fixture.source, Destination: fixture.destination, ClusterSize: 4096}
}

func requireCloneCode(t *testing.T, err error, code string) *Error {
	t.Helper()
	var cloneErr *Error
	if !errors.As(err, &cloneErr) || cloneErr.Code != code {
		t.Fatalf("code=%q err=%v", code, err)
	}
	return cloneErr
}

func TestCloneScopeAcceptsNormalPathsUnderTrustedRoot(t *testing.T) {
	fixture := newCloneScopeFixture(t)
	scope, err := prepareCloneScope(fixture.request(), newFakeClonePathInspector())
	if err != nil {
		t.Fatal(err)
	}
	defer os.Remove(scope.Destination)
	if !strings.EqualFold(scope.TrustedRoot, fixture.root) || !strings.EqualFold(scope.Source, fixture.source) || !strings.EqualFold(scope.Destination, fixture.destination) {
		t.Fatalf("scope=%+v", scope)
	}
}

func TestCloneScopeRejectsUnsafeLexicalPaths(t *testing.T) {
	tests := map[string]func(t *testing.T, fixture cloneScopeFixture, request *CloneRequest){
		"source outside trusted root": func(t *testing.T, fixture cloneScopeFixture, request *CloneRequest) {
			request.Source = filepath.Join(filepath.Dir(fixture.root), "outside-source")
			if err := os.MkdirAll(request.Source, 0700); err != nil {
				t.Fatal(err)
			}
		},
		"destination outside trusted root": func(t *testing.T, fixture cloneScopeFixture, request *CloneRequest) {
			parent := filepath.Join(filepath.Dir(fixture.root), "outside-destination")
			if err := os.MkdirAll(parent, 0700); err != nil {
				t.Fatal(err)
			}
			request.Destination = filepath.Join(parent, "Library")
		},
		"source and destination equal": func(_ *testing.T, fixture cloneScopeFixture, request *CloneRequest) {
			request.Destination = fixture.source
		},
		"destination already exists": func(t *testing.T, fixture cloneScopeFixture, _ *CloneRequest) {
			if err := os.Mkdir(fixture.destination, 0700); err != nil {
				t.Fatal(err)
			}
		},
		"missing destination parent": func(_ *testing.T, fixture cloneScopeFixture, request *CloneRequest) {
			request.Destination = filepath.Join(fixture.root, "missing", "Library")
		},
		"prefix collision": func(t *testing.T, fixture cloneScopeFixture, request *CloneRequest) {
			request.Source = fixture.root + "-escape"
			if err := os.MkdirAll(request.Source, 0700); err != nil {
				t.Fatal(err)
			}
		},
	}
	for name, mutate := range tests {
		t.Run(name, func(t *testing.T) {
			fixture := newCloneScopeFixture(t)
			request := fixture.request()
			mutate(t, fixture, &request)
			requireCloneCode(t, func() error { _, err := prepareCloneScope(request, newFakeClonePathInspector()); return err }(), CodeCloneFailed)
		})
	}
}

func TestCloneScopeComparisonIsComponentAwareAndCaseInsensitive(t *testing.T) {
	fixture := newCloneScopeFixture(t)
	request := fixture.request()
	request.TrustedRoot = strings.ToUpper(request.TrustedRoot)
	request.Source = strings.ToLower(request.Source)
	request.Destination = strings.ToUpper(request.Destination)
	scope, err := prepareCloneScope(request, newFakeClonePathInspector())
	if err != nil {
		t.Fatal(err)
	}
	defer os.Remove(scope.Destination)
	if windowsPathWithin(fixture.root, fixture.root+"-escape") {
		t.Fatal("prefix collision was treated as contained")
	}
}

func TestCloneScopeRejectsEveryManagedReparseBoundary(t *testing.T) {
	tests := map[string]func(cloneScopeFixture) string{
		"trusted root":        func(f cloneScopeFixture) string { return f.root },
		"source root":         func(f cloneScopeFixture) string { return f.source },
		"destination parent":  func(f cloneScopeFixture) string { return f.destinationParent },
		"source intermediate": func(f cloneScopeFixture) string { return filepath.Join(f.root, "baselines") },
	}
	for name, selectPath := range tests {
		t.Run(name, func(t *testing.T) {
			fixture := newCloneScopeFixture(t)
			fake := newFakeClonePathInspector()
			fake.attributes[cloneTestPathKey(selectPath(fixture))] = windows.FILE_ATTRIBUTE_DIRECTORY | windows.FILE_ATTRIBUTE_REPARSE_POINT
			err := func() error { _, err := prepareCloneScope(fixture.request(), fake); return err }()
			cloneErr := requireCloneCode(t, err, CodeCloneFailed)
			if cloneErr.Operation != "validate-clone-reparse-boundary" {
				t.Fatalf("operation=%s err=%v", cloneErr.Operation, err)
			}
		})
	}
}

func TestCloneScopeAllowsVolumeMountAncestorOutsideTrustedRoot(t *testing.T) {
	fixture := newCloneScopeFixture(t)
	fake := newFakeClonePathInspector()
	ancestor := filepath.Dir(fixture.root)
	fake.attributes[cloneTestPathKey(ancestor)] = windows.FILE_ATTRIBUTE_DIRECTORY | windows.FILE_ATTRIBUTE_REPARSE_POINT
	scope, err := prepareCloneScope(fixture.request(), fake)
	if err != nil {
		t.Fatal(err)
	}
	defer os.Remove(scope.Destination)
	for _, inspected := range fake.attrCalls {
		if strings.EqualFold(inspected, ancestor) {
			t.Fatalf("trusted-root ancestor was inspected: %s", inspected)
		}
	}
}

func TestCloneTreeEntryRejectsEveryWindowsReparseKind(t *testing.T) {
	for _, kind := range []string{"junction", "symlink", "nested volume mount", "other reparse point"} {
		t.Run(kind, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), kind)
			fake := newFakeClonePathInspector()
			fake.attributes[cloneTestPathKey(path)] = windows.FILE_ATTRIBUTE_REPARSE_POINT
			cloneErr := requireCloneCode(t, validateCloneTreeEntry(path, fake), CodeCloneFailed)
			if cloneErr.Operation != "validate-clone-reparse-boundary" || cloneErr.Path != path {
				t.Fatalf("error=%+v", cloneErr)
			}
		})
	}
}

func TestCloneScopeValidatesSameRefsBlockCloneVolume(t *testing.T) {
	tests := map[string]func(cloneScopeFixture, *fakeClonePathInspector){
		"source volume mismatch": func(f cloneScopeFixture, fake *fakeClonePathInspector) {
			fake.identities[cloneTestPathKey(f.source)] = cloneDirectoryIdentity{Volume: volumeIdentity{Serial: 8, Filesystem: "ReFS", Flags: fileSupportsBlockRefcounting}, FinalPath: f.source}
		},
		"destination parent mismatch": func(f cloneScopeFixture, fake *fakeClonePathInspector) {
			fake.identities[cloneTestPathKey(f.destinationParent)] = cloneDirectoryIdentity{Volume: volumeIdentity{Serial: 8, Filesystem: "ReFS", Flags: fileSupportsBlockRefcounting}, FinalPath: f.destinationParent}
		},
		"non ReFS source": func(f cloneScopeFixture, fake *fakeClonePathInspector) {
			fake.identities[cloneTestPathKey(f.source)] = cloneDirectoryIdentity{Volume: volumeIdentity{Serial: 7, Filesystem: "NTFS", Flags: fileSupportsBlockRefcounting}, FinalPath: f.source}
		},
		"missing block clone capability": func(f cloneScopeFixture, fake *fakeClonePathInspector) {
			fake.identities[cloneTestPathKey(f.source)] = cloneDirectoryIdentity{Volume: volumeIdentity{Serial: 7, Filesystem: "ReFS"}, FinalPath: f.source}
		},
		"destination identity changed after creation": func(f cloneScopeFixture, fake *fakeClonePathInspector) {
			fake.identities[cloneTestPathKey(f.destination)] = cloneDirectoryIdentity{Volume: volumeIdentity{Serial: 8, Filesystem: "ReFS", Flags: fileSupportsBlockRefcounting}, FinalPath: f.destination}
		},
	}
	for name, mutate := range tests {
		t.Run(name, func(t *testing.T) {
			fixture := newCloneScopeFixture(t)
			fake := newFakeClonePathInspector()
			mutate(fixture, fake)
			err := func() error { _, err := prepareCloneScope(fixture.request(), fake); return err }()
			requireCloneCode(t, err, CodeBlockCloneUnavailable)
			if _, statErr := os.Lstat(fixture.destination); !os.IsNotExist(statErr) {
				t.Fatalf("invalid created destination was not removed: %v", statErr)
			}
		})
	}
}

func TestCloneScopeNormalizationFailureRetainsOriginalPath(t *testing.T) {
	fixture := newCloneScopeFixture(t)
	request := fixture.request()
	request.Source = "original-source-that-does-not-exist"
	err := func() error { _, err := prepareCloneScope(request, newFakeClonePathInspector()); return err }()
	cloneErr := requireCloneCode(t, err, CodeCloneFailed)
	if cloneErr.Path != request.Source {
		t.Fatalf("path=%q want original=%q", cloneErr.Path, request.Source)
	}
}

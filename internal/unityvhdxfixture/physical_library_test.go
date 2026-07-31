package unityvhdxfixture

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestMaterializeDirectoryContentsCreatesIndependentDirectory(t *testing.T) {
	root := t.TempDir()
	source := filepath.Join(root, "source")
	destination := filepath.Join(root, "destination")
	mustCreatePhysicalLibrary(t, source)

	result, err := MaterializeDirectoryContents(context.Background(), source, destination)
	if err != nil {
		t.Fatal(err)
	}
	if result.FileCount != 2 || result.LogicalBytes == 0 {
		t.Fatalf("result=%#v", result)
	}
	if err := ValidatePhysicalLibraryDirectory(destination); err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(source); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(destination, "SourceAssetDB", "data.mdb"))
	if err != nil || string(data) != "database" {
		t.Fatalf("materialized data=%q err=%v", data, err)
	}
}

func TestMaterializeDirectoryContentsRejectsNestedReparsePoint(t *testing.T) {
	root := t.TempDir()
	source := filepath.Join(root, "source")
	mustCreatePhysicalLibrary(t, source)
	link := filepath.Join(source, "nested-link")
	if err := os.Symlink(filepath.Join(source, "SourceAssetDB"), link); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	_, err := MaterializeDirectoryContents(context.Background(), source, filepath.Join(root, "destination"))
	if ErrorCode(err) != CodeNestedReparsePointFound {
		t.Fatalf("error=%v code=%q", err, ErrorCode(err))
	}
}

func TestMaterializeDirectoryContentsRejectsOverlap(t *testing.T) {
	root := t.TempDir()
	source := filepath.Join(root, "source")
	mustCreatePhysicalLibrary(t, source)
	_, err := MaterializeDirectoryContents(context.Background(), source, filepath.Join(source, "destination"))
	if ErrorCode(err) != CodePhysicalLibraryCopyEscaped {
		t.Fatalf("error=%v code=%q", err, ErrorCode(err))
	}
}

func TestValidatePhysicalLibraryRejectsReparseRoot(t *testing.T) {
	root := t.TempDir()
	source := filepath.Join(root, "source")
	mustCreatePhysicalLibrary(t, source)
	link := filepath.Join(root, "library-link")
	if err := os.Symlink(source, link); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	if code := ErrorCode(ValidatePhysicalLibraryDirectory(link)); code != CodePhysicalLibraryIsReparse {
		t.Fatalf("code=%q", code)
	}
}

func mustCreatePhysicalLibrary(t *testing.T, root string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Join(root, "SourceAssetDB"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "SourceAssetDB", "data.mdb"), []byte("database"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "artifact.txt"), []byte("payload"), 0600); err != nil {
		t.Fatal(err)
	}
}

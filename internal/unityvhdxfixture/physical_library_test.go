package unityvhdxfixture

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestMaterializedPhysicalLibrarySurvivesSourceRemoval(t *testing.T) {
	root := t.TempDir()
	source := filepath.Join(root, "source")
	destination := filepath.Join(root, "destination")
	mustCreatePhysicalLibrary(t, source)

	result, err := MaterializeDirectoryContents(context.Background(), source, destination)
	if err != nil {
		t.Fatal(err)
	}
	if result.FileCount != 4 || result.LogicalBytes == 0 {
		t.Fatalf("result=%#v", result)
	}
	if err := ValidatePhysicalLibraryDirectory(destination); err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(source); err != nil {
		t.Fatal(err)
	}
	if err := ValidatePhysicalLibraryDirectory(destination); err != nil {
		t.Fatalf("validation after source removal: %v", err)
	}
	data, err := os.ReadFile(filepath.Join(destination, "SourceAssetDB"))
	if err != nil || string(data) != "database" {
		t.Fatalf("materialized data=%q err=%v", data, err)
	}
}

func TestValidatePhysicalLibraryAcceptsUnity6000Layout(t *testing.T) {
	library := filepath.Join(t.TempDir(), "Library")
	mustCreatePhysicalLibrary(t, library)
	if err := ValidatePhysicalLibraryDirectory(library); err != nil {
		t.Fatal(err)
	}
}

func TestValidatePhysicalLibraryAcceptsMissingOptionalLock(t *testing.T) {
	library := filepath.Join(t.TempDir(), "Library")
	mustCreatePhysicalLibrary(t, library)
	if err := os.Remove(filepath.Join(library, "SourceAssetDB-lock")); err != nil {
		t.Fatal(err)
	}
	if err := ValidatePhysicalLibraryDirectory(library); err != nil {
		t.Fatal(err)
	}
}

func TestValidatePhysicalLibraryRejectsSourceAssetDBDirectory(t *testing.T) {
	library := filepath.Join(t.TempDir(), "Library")
	mustCreatePhysicalLibrary(t, library)
	database := filepath.Join(library, "SourceAssetDB")
	if err := os.Remove(database); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(database, 0700); err != nil {
		t.Fatal(err)
	}
	if code := ErrorCode(ValidatePhysicalLibraryDirectory(library)); code != CodePhysicalLibraryInvalidDB {
		t.Fatalf("code=%q", code)
	}
}

func TestValidatePhysicalLibraryRejectsMissingSourceAssetDB(t *testing.T) {
	library := filepath.Join(t.TempDir(), "Library")
	mustCreatePhysicalLibrary(t, library)
	if err := os.Remove(filepath.Join(library, "SourceAssetDB")); err != nil {
		t.Fatal(err)
	}
	if code := ErrorCode(ValidatePhysicalLibraryDirectory(library)); code != CodePhysicalLibraryDangling {
		t.Fatalf("code=%q", code)
	}
}

func TestValidatePhysicalLibraryRejectsEmptySourceAssetDB(t *testing.T) {
	library := filepath.Join(t.TempDir(), "Library")
	mustCreatePhysicalLibrary(t, library)
	if err := os.WriteFile(filepath.Join(library, "SourceAssetDB"), nil, 0600); err != nil {
		t.Fatal(err)
	}
	if code := ErrorCode(ValidatePhysicalLibraryDirectory(library)); code != CodePhysicalLibraryInvalidDB {
		t.Fatalf("code=%q", code)
	}
}

func TestValidatePhysicalLibraryRejectsSourceAssetDBSymlink(t *testing.T) {
	library := filepath.Join(t.TempDir(), "Library")
	mustCreatePhysicalLibrary(t, library)
	database := filepath.Join(library, "SourceAssetDB")
	target := filepath.Join(library, "database-target")
	if err := os.WriteFile(target, []byte("database"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(database); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, database); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	if code := ErrorCode(ValidatePhysicalLibraryDirectory(library)); code != CodePhysicalLibraryInvalidDB {
		t.Fatalf("code=%q", code)
	}
}

func TestValidatePhysicalLibraryRejectsInvalidOptionalLock(t *testing.T) {
	library := filepath.Join(t.TempDir(), "Library")
	mustCreatePhysicalLibrary(t, library)
	lock := filepath.Join(library, "SourceAssetDB-lock")
	if err := os.Remove(lock); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(lock, 0700); err != nil {
		t.Fatal(err)
	}
	if code := ErrorCode(ValidatePhysicalLibraryDirectory(library)); code != CodePhysicalLibraryInvalidDB {
		t.Fatalf("code=%q", code)
	}
}

func TestValidatePhysicalLibraryRejectsMissingScriptAssemblies(t *testing.T) {
	library := filepath.Join(t.TempDir(), "Library")
	mustCreatePhysicalLibrary(t, library)
	if err := os.RemoveAll(filepath.Join(library, "ScriptAssemblies")); err != nil {
		t.Fatal(err)
	}
	if code := ErrorCode(ValidatePhysicalLibraryDirectory(library)); code != CodePhysicalLibraryIncomplete {
		t.Fatalf("code=%q", code)
	}
}

func TestValidatePhysicalLibraryRejectsMissingRuntimeAssembly(t *testing.T) {
	library := filepath.Join(t.TempDir(), "Library")
	mustCreatePhysicalLibrary(t, library)
	if err := os.Remove(filepath.Join(library, "ScriptAssemblies", "TestPlayFixture.Runtime.dll")); err != nil {
		t.Fatal(err)
	}
	if code := ErrorCode(ValidatePhysicalLibraryDirectory(library)); code != CodePhysicalLibraryIncomplete {
		t.Fatalf("code=%q", code)
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
	if err := os.MkdirAll(filepath.Join(root, "ScriptAssemblies"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "SourceAssetDB"), []byte("database"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "SourceAssetDB-lock"), []byte("lock"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "ScriptAssemblies", "TestPlayFixture.Runtime.dll"), []byte("assembly"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "artifact.txt"), []byte("payload"), 0600); err != nil {
		t.Fatal(err)
	}
}

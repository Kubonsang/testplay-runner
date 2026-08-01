//go:build windows

package unityvhdxfixture

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestMaterializeDirectoryContentsRejectsJunctionRoot(t *testing.T) {
	root := t.TempDir()
	source := filepath.Join(root, "source")
	mustCreatePhysicalLibrary(t, source)
	junction := filepath.Join(root, "junction")
	if output, err := exec.Command("cmd.exe", "/c", "mklink", "/J", junction, source).CombinedOutput(); err != nil {
		t.Skipf("junction unavailable: %v: %s", err, output)
	}
	defer os.Remove(junction)
	_, err := MaterializeDirectoryContents(context.Background(), junction, filepath.Join(root, "destination"))
	if ErrorCode(err) != CodePhysicalLibraryIsReparse {
		t.Fatalf("error=%v code=%q", err, ErrorCode(err))
	}
}

func TestMaterializeDirectoryContentsRejectsNestedJunction(t *testing.T) {
	root := t.TempDir()
	source := filepath.Join(root, "source")
	external := filepath.Join(root, "external")
	mustCreatePhysicalLibrary(t, source)
	if err := os.Mkdir(external, 0700); err != nil {
		t.Fatal(err)
	}
	junction := filepath.Join(source, "nested-junction")
	if output, err := exec.Command("cmd.exe", "/c", "mklink", "/J", junction, external).CombinedOutput(); err != nil {
		t.Skipf("junction unavailable: %v: %s", err, output)
	}
	defer os.Remove(junction)
	_, err := MaterializeDirectoryContents(context.Background(), source, filepath.Join(root, "destination"))
	if ErrorCode(err) != CodeNestedReparsePointFound {
		t.Fatalf("error=%v code=%q", err, ErrorCode(err))
	}
}

func TestValidatePhysicalLibraryRejectsJunctionRoot(t *testing.T) {
	root := t.TempDir()
	source := filepath.Join(root, "source")
	mustCreatePhysicalLibrary(t, source)
	junction := filepath.Join(root, "junction")
	if output, err := exec.Command("cmd.exe", "/c", "mklink", "/J", junction, source).CombinedOutput(); err != nil {
		t.Skipf("junction unavailable: %v: %s", err, output)
	}
	defer os.Remove(junction)
	if code := ErrorCode(ValidatePhysicalLibraryDirectory(junction)); code != CodePhysicalLibraryIsReparse {
		t.Fatalf("code=%q", code)
	}
}

func TestMaterializedDestinationHasNoReparseAttribute(t *testing.T) {
	root := t.TempDir()
	source := filepath.Join(root, "source")
	destination := filepath.Join(root, "destination")
	mustCreatePhysicalLibrary(t, source)
	if _, err := MaterializeDirectoryContents(context.Background(), source, destination); err != nil {
		t.Fatal(err)
	}
	reparse, err := physicalPathIsReparse(destination)
	if err != nil || reparse {
		t.Fatalf("destination reparse=%t err=%v", reparse, err)
	}
}

func TestValidatePhysicalLibraryRejectsSourceAssetDBJunction(t *testing.T) {
	root := t.TempDir()
	library := filepath.Join(root, "Library")
	mustCreatePhysicalLibrary(t, library)
	database := filepath.Join(library, "SourceAssetDB")
	if err := os.Remove(database); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(root, "database-directory")
	if err := os.Mkdir(target, 0700); err != nil {
		t.Fatal(err)
	}
	if output, err := exec.Command("cmd.exe", "/c", "mklink", "/J", database, target).CombinedOutput(); err != nil {
		t.Skipf("junction unavailable: %v: %s", err, output)
	}
	defer os.Remove(database)
	if code := ErrorCode(ValidatePhysicalLibraryDirectory(library)); code != CodePhysicalLibraryInvalidDB {
		t.Fatalf("code=%q", code)
	}
}

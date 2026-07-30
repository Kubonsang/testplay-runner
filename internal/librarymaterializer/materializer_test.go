package librarymaterializer_test

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/Kubonsang/testplay-runner/internal/librarymaterializer"
)

func TestPhysicalCopyMaterializerID(t *testing.T) {
	t.Parallel()
	materializer := librarymaterializer.PhysicalCopyMaterializer{}
	if got := materializer.ID(); got != librarymaterializer.PhysicalCopyID {
		t.Fatalf("ID = %q, want %q", got, librarymaterializer.PhysicalCopyID)
	}
}

func TestPhysicalCopyMaterializerCopiesAndIsolatesLibrary(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	source := filepath.Join(root, "기준 Library")
	destination := filepath.Join(root, "실행 Library")
	mustMkdir(t, filepath.Join(source, "빈 디렉터리"))
	mustWrite(t, filepath.Join(source, "empty.bin"), nil, 0444)
	mustWrite(t, filepath.Join(source, "한글 파일.txt"), []byte("base"), 0644)
	for index := 0; index < 32; index++ {
		mustWrite(
			t,
			filepath.Join(source, "small files", fileName(index)),
			[]byte{byte(index)},
			0644,
		)
	}

	materializer := librarymaterializer.PhysicalCopyMaterializer{}
	result, err := materializer.Materialize(context.Background(), librarymaterializer.Request{
		SourcePath:      source,
		DestinationPath: destination,
	})
	if err != nil {
		t.Fatalf("Materialize: %v", err)
	}
	if result.MaterializerID != librarymaterializer.PhysicalCopyID {
		t.Fatalf("MaterializerID = %q", result.MaterializerID)
	}
	if result.FileCount != 34 {
		t.Fatalf("FileCount = %d, want 34", result.FileCount)
	}
	if result.LogicalBytes != 36 {
		t.Fatalf("LogicalBytes = %d, want 36", result.LogicalBytes)
	}
	if result.Duration < 0 {
		t.Fatalf("Duration = %s", result.Duration)
	}
	if info, err := os.Stat(filepath.Join(destination, "빈 디렉터리")); err != nil || !info.IsDir() {
		t.Fatalf("empty directory missing: info=%v err=%v", info, err)
	}
	if data, err := os.ReadFile(filepath.Join(destination, "empty.bin")); err != nil || len(data) != 0 {
		t.Fatalf("empty file mismatch: len=%d err=%v", len(data), err)
	}

	sourceFile := filepath.Join(source, "한글 파일.txt")
	destinationFile := filepath.Join(destination, "한글 파일.txt")
	sourceInfo, err := os.Stat(sourceFile)
	if err != nil {
		t.Fatal(err)
	}
	destinationInfo, err := os.Stat(destinationFile)
	if err != nil {
		t.Fatal(err)
	}
	if os.SameFile(sourceInfo, destinationInfo) {
		t.Fatal("destination is the same file or a hardlink to source")
	}
	if err := os.WriteFile(destinationFile, []byte("workspace mutation"), 0644); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(sourceFile)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "base" {
		t.Fatalf("source changed through destination: %q", data)
	}
}

func TestPhysicalCopyMaterializerCancellationRemovesDestination(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	source := filepath.Join(root, "source")
	destination := filepath.Join(root, "destination")
	mustWrite(t, filepath.Join(source, "file.bin"), []byte("data"), 0644)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	result, err := (librarymaterializer.PhysicalCopyMaterializer{}).Materialize(
		ctx,
		librarymaterializer.Request{
			SourcePath:      source,
			DestinationPath: destination,
		},
	)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("error = %v, want context.Canceled", err)
	}
	if result == nil ||
		result.MaterializerID != librarymaterializer.PhysicalCopyID ||
		result.Duration < 0 {
		t.Fatalf("failed materialization result = %+v", result)
	}
	if _, err := os.Stat(destination); !os.IsNotExist(err) {
		t.Fatalf("failed destination still exists: %v", err)
	}
}

func fileName(index int) string {
	return fmt.Sprintf("file-%02d.bin", index)
}

func mustMkdir(t *testing.T, path string) {
	t.Helper()
	if err := os.MkdirAll(path, 0755); err != nil {
		t.Fatal(err)
	}
}

func mustWrite(t *testing.T, path string, data []byte, mode os.FileMode) {
	t.Helper()
	mustMkdir(t, filepath.Dir(path))
	if err := os.WriteFile(path, data, mode); err != nil {
		t.Fatal(err)
	}
}

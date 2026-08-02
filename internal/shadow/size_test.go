package shadow

import (
	"os"
	"path/filepath"
	"testing"
)

func TestMeasureDirectoryUsageSeparatesLogicalAndAllocatedBytes(t *testing.T) {
	root := t.TempDir()
	data := []byte("library-image-usage")
	if err := os.WriteFile(filepath.Join(root, "artifact.bin"), data, 0644); err != nil {
		t.Fatal(err)
	}

	usage, err := MeasureDirectoryUsage(root)
	if err != nil {
		t.Fatal(err)
	}
	if usage.LogicalBytes != int64(len(data)) {
		t.Fatalf("logical bytes = %d, want %d", usage.LogicalBytes, len(data))
	}
	if usage.AllocatedBytes <= 0 {
		t.Fatalf("allocated bytes = %d, want positive", usage.AllocatedBytes)
	}

	allocated, err := DirectoryAllocatedBytes(root)
	if err != nil {
		t.Fatal(err)
	}
	if allocated != usage.AllocatedBytes {
		t.Fatalf("allocated wrapper = %d, want %d", allocated, usage.AllocatedBytes)
	}
}

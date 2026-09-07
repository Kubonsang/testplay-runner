//go:build windows

package shadow

import (
	"fmt"
	"golang.org/x/sys/windows"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestBatchedAllocationMatchesFileHandles(t *testing.T) {
	root := t.TempDir()
	nested := filepath.Join(root, "한글 자료")
	if err := os.Mkdir(nested, 0700); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 700; i++ {
		name := fmt.Sprintf("%04d-%s.bin", i, strings.Repeat("a", 80))
		if err := os.WriteFile(filepath.Join(nested, name), make([]byte, i%17*100), 0600); err != nil {
			t.Fatal(err)
		}
	}
	longDir := filepath.Join(root, strings.Repeat("long-", 35), strings.Repeat("nested-", 20))
	if err := os.MkdirAll(longDir, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(longDir, "allocated.bin"), make([]byte, 8193), 0600); err != nil {
		t.Fatal(err)
	}
	sparse, err := os.Create(filepath.Join(nested, "sparse.bin"))
	if err != nil {
		t.Fatal(err)
	}
	var returned uint32
	if err := windows.DeviceIoControl(windows.Handle(sparse.Fd()), windows.FSCTL_SET_SPARSE, nil, 0, nil, 0, &returned, nil); err != nil {
		sparse.Close()
		t.Fatal(err)
	}
	if err := sparse.Truncate(1 << 20); err != nil {
		sparse.Close()
		t.Fatal(err)
	}
	if err := sparse.Close(); err != nil {
		t.Fatal(err)
	}
	want, err := measureDirectoryUsageByWalk(root)
	if err != nil {
		t.Fatal(err)
	}
	got, err := MeasureDirectoryUsage(root)
	if err != nil {
		t.Fatal(err)
	}
	if got != want || got.AllocatedBytes <= 0 {
		t.Fatalf("batch=%+v individual=%+v", got, want)
	}
}

// Optional read-only check against an existing large cache; never modifies it.
func TestNativeAllocationMeasurementProbe(t *testing.T) {
	root := os.Getenv("TESTPLAY_USAGE_PROBE_ROOT")
	if root == "" {
		t.Skip("set TESTPLAY_USAGE_PROBE_ROOT for a read-only native comparison")
	}
	started := time.Now()
	got, err := MeasureDirectoryUsage(root)
	if err != nil {
		t.Fatal(err)
	}
	batchMs := time.Since(started).Milliseconds()
	started = time.Now()
	want, err := measureDirectoryUsageByWalk(root)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("batchMs=%d individualMs=%d logicalBytes=%d allocatedBytes=%d", batchMs, time.Since(started).Milliseconds(), got.LogicalBytes, got.AllocatedBytes)
	if got != want {
		t.Fatalf("batch=%+v individual=%+v", got, want)
	}
}

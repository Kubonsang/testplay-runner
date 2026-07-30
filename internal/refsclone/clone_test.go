package refsclone_test

import (
	"context"
	"encoding/json"
	"errors"
	"regexp"
	"runtime"
	"testing"

	"github.com/Kubonsang/testplay-runner/internal/refsclone"
)

func TestFixtureLengthIsClusterAlignedAndAtLeastOneMiB(t *testing.T) {
	for _, cluster := range []uint64{4096, 65536, 98304} {
		length, err := refsclone.FixtureLength(cluster)
		if err != nil {
			t.Fatal(err)
		}
		if length < refsclone.MinimumFixtureBytes || uint64(length)%cluster != 0 {
			t.Fatalf("cluster=%d length=%d", cluster, length)
		}
	}
}

func TestValidateRequest(t *testing.T) {
	valid := refsclone.Request{
		SourcePath: "source.bin", DestinationPath: "clone.bin",
		SourceOffset: 4096, DestinationOffset: 8192, Length: 1 << 20,
	}
	if err := refsclone.ValidateRequest(valid, 4096); err != nil {
		t.Fatalf("valid request: %v", err)
	}
	cases := []struct {
		name string
		edit func(*refsclone.Request)
		code string
	}{
		{"zero-length", func(r *refsclone.Request) { r.Length = 0 }, refsclone.CodeInvalidLength},
		{"negative-length", func(r *refsclone.Request) { r.Length = -1 }, refsclone.CodeInvalidLength},
		{"four-gib", func(r *refsclone.Request) { r.Length = 4 << 30 }, refsclone.CodeInvalidLength},
		{"source-offset", func(r *refsclone.Request) { r.SourceOffset++ }, refsclone.CodeInvalidAlignment},
		{"destination-offset", func(r *refsclone.Request) { r.DestinationOffset++ }, refsclone.CodeInvalidAlignment},
		{"length-alignment", func(r *refsclone.Request) { r.Length++ }, refsclone.CodeInvalidAlignment},
		{"same-path", func(r *refsclone.Request) { r.DestinationPath = r.SourcePath }, refsclone.CodeInvalidLength},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			request := valid
			tc.edit(&request)
			var classified *refsclone.Error
			if err := refsclone.ValidateRequest(request, 4096); !errors.As(err, &classified) || classified.Code != tc.code {
				t.Fatalf("error=%v classified=%+v want code=%s", err, classified, tc.code)
			}
		})
	}
}

func TestResultJSONUsesCamelCase(t *testing.T) {
	data, err := json.Marshal(refsclone.Result{
		ControlCodeUsed: refsclone.ControlDuplicateExtents,
		BytesCloned:     1 << 20, DurationMs: 4, ClusterSize: 4096,
	})
	if err != nil {
		t.Fatal(err)
	}
	const want = `{"controlCodeUsed":"FSCTL_DUPLICATE_EXTENTS_TO_FILE","bytesCloned":1048576,"durationMs":4,"clusterSize":4096}`
	if string(data) != want {
		t.Fatalf("JSON=%s want=%s", data, want)
	}
}

func TestFixtureNamesAreWindowsSafe(t *testing.T) {
	safe := regexp.MustCompile(`^[A-Za-z0-9._-]+$`)
	for _, name := range []string{
		refsclone.SourceFixtureName,
		refsclone.CloneFixtureName,
		refsclone.PhysicalCopyFixtureName,
	} {
		if !safe.MatchString(name) {
			t.Fatalf("unsafe fixture name %q", name)
		}
	}
}

func TestCancellationIsStructuredBeforePlatformDispatch(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := refsclone.CloneFile(ctx, refsclone.Request{
		SourcePath: "source.bin", DestinationPath: "clone.bin", Length: 4096,
	})
	var classified *refsclone.Error
	if !errors.As(err, &classified) || classified.Code != refsclone.CodeCancelled {
		t.Fatalf("error=%v classified=%+v", err, classified)
	}
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("error does not preserve context cancellation: %v", err)
	}
}

func TestUnsupportedPlatformIsStructured(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("non-Windows contract")
	}
	capability, err := refsclone.Probe(context.Background(), t.TempDir())
	var classified *refsclone.Error
	if !errors.As(err, &classified) || classified.Code != refsclone.CodeUnsupportedPlatform {
		t.Fatalf("error=%v classified=%+v", err, classified)
	}
	if capability.Supported || !errors.Is(err, refsclone.ErrUnsupportedPlatform) {
		t.Fatalf("capability=%+v error=%v", capability, err)
	}
}

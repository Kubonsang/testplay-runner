package vhdxprobe

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestNewPlanBuildsUniquePaths(t *testing.T) {
	root := t.TempDir()
	plan, err := NewPlan(Config{Root: root}, "op-0123456789abcdef")
	if err != nil {
		t.Fatal(err)
	}
	if plan.Config.ParentVirtualBytes != DefaultParentVirtualBytes {
		t.Fatalf("parent size = %d", plan.Config.ParentVirtualBytes)
	}
	if plan.Config.PayloadBytes != DefaultPayloadBytes {
		t.Fatalf("payload size = %d", plan.Config.PayloadBytes)
	}
	paths := []string{
		plan.Paths.Parent,
		plan.Paths.ChildA,
		plan.Paths.ChildB,
		plan.Paths.Mounts,
	}
	seen := make(map[string]bool)
	for _, path := range paths {
		if seen[path] {
			t.Fatalf("duplicate path %q", path)
		}
		seen[path] = true
		if !pathWithin(root, path) {
			t.Fatalf("path escaped root: %q", path)
		}
	}
}

func TestNewPlanRejectsUnsafeRoots(t *testing.T) {
	parent := t.TempDir()
	nonEmpty := filepath.Join(parent, "not-empty")
	if err := os.Mkdir(nonEmpty, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(nonEmpty, "important.txt"), []byte("keep"), 0600); err != nil {
		t.Fatal(err)
	}
	cases := []string{
		"relative",
		filepath.VolumeName(parent) + string(os.PathSeparator),
		nonEmpty,
	}
	for _, root := range cases {
		t.Run(strings.ReplaceAll(root, string(os.PathSeparator), "_"), func(t *testing.T) {
			if _, err := NewPlan(Config{Root: root}, "op-0123456789abcdef"); err == nil {
				t.Fatalf("NewPlan(%q) succeeded", root)
			}
		})
	}
}

func TestNewPlanRejectsBadOperationIDsAndSizes(t *testing.T) {
	root := t.TempDir()
	if _, err := NewPlan(Config{Root: root}, `..\escape`); err == nil {
		t.Fatal("unsafe operation ID succeeded")
	}
	if _, err := NewPlan(
		Config{
			Root:               root,
			ParentVirtualBytes: 128 << 20,
			PayloadBytes:       80 << 20,
		},
		"op-0123456789abcdef",
	); err == nil {
		t.Fatal("oversized payload succeeded")
	}
}

func TestResultJSONAndDurations(t *testing.T) {
	result := Result{
		OperationID:               "op-0123456789abcdef",
		ParentIsolationPassed:     true,
		SiblingIsolationPassed:    true,
		ReattachPersistencePassed: true,
		Durations:                 Durations{ParentCreateMs: time.Millisecond.Milliseconds()},
	}
	data, err := json.Marshal(result)
	if err != nil {
		t.Fatal(err)
	}
	for _, expected := range []string{
		`"operationId":"op-0123456789abcdef"`,
		`"parentIsolationPassed":true`,
		`"siblingIsolationPassed":true`,
		`"reattachPersistencePassed":true`,
		`"parentCreateMs":1`,
	} {
		if !strings.Contains(string(data), expected) {
			t.Fatalf("JSON missing %s: %s", expected, data)
		}
	}
	if result.Durations.ParentCreateMs < 0 ||
		result.Durations.ParentAttachMs < 0 ||
		result.Durations.CleanupMs < 0 {
		t.Fatal("negative duration")
	}
}

func TestStructuredErrorPreservesCauseAndWin32Code(t *testing.T) {
	cause := errors.New("boom")
	err := &Error{
		Code:      CodeParentAttachFailed,
		Operation: "AttachVirtualDisk",
		Path:      `C:\probe\parent.vhdx`,
		Win32Code: 5,
		Cause:     cause,
	}
	if !errors.Is(err, cause) {
		t.Fatal("cause was not preserved")
	}
	if !strings.Contains(err.Error(), "win32=5") {
		t.Fatalf("Win32 code missing: %s", err)
	}
}

func TestCancellationWinsBeforePlatformWork(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := Run(ctx, Config{Root: filepath.Join(t.TempDir(), "probe")})
	var probeErr *Error
	if !errors.As(err, &probeErr) || probeErr.Code != CodeCancelled {
		t.Fatalf("error = %#v", err)
	}
}

func TestCleanupTargetCannotEscapeRoot(t *testing.T) {
	root := t.TempDir()
	plan, err := NewPlan(Config{Root: root}, "op-0123456789abcdef")
	if err != nil {
		t.Fatal(err)
	}
	plan.Paths.Operation = filepath.Dir(root)
	if err := validateCleanupTarget(plan); err == nil {
		t.Fatal("cleanup escape succeeded")
	}
}

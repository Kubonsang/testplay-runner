//go:build windows

package main

import (
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
)

func TestSummaryRetainsSetupEvidenceWhenProbeFails(t *testing.T) {
	_, current, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	repository := filepath.Clean(filepath.Join(filepath.Dir(current), "..", ".."))
	script := filepath.Join(repository, "scripts", "test-managed-refs-pool-probe-summary.ps1")
	command := exec.Command("powershell.exe", "-NoProfile", "-NonInteractive", "-ExecutionPolicy", "Bypass", "-File", script)
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("summary behavior test failed: %v\n%s", err, output)
	}
}

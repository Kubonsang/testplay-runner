//go:build windows && vhdx_integration

package vhdxprobe

import (
	"context"
	"encoding/json"
	"os"
	"testing"
)

func runIntegrationProbe(t *testing.T) *Result {
	t.Helper()
	root := os.Getenv("TESTPLAY_VHDX_PROBE_ROOT")
	if root == "" {
		t.Skip("TESTPLAY_VHDX_PROBE_ROOT is not set; elevated VHDX integration is opt-in")
	}
	result, err := Run(context.Background(), Config{Root: root})
	if err != nil {
		t.Fatalf("VHDX integration failed: %v", err)
	}
	data, err := json.MarshalIndent(result, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("VHDX probe result:\n%s", data)
	return result
}

func TestDifferencingVHDXProbe(t *testing.T) {
	result := runIntegrationProbe(t)
	if !result.ParentIsolationPassed ||
		!result.SiblingIsolationPassed ||
		!result.ReattachPersistencePassed ||
		!result.CleanupPassed {
		t.Fatalf("incomplete probe result: %+v", result)
	}
}

func TestDifferencingVHDXParentIsolation(t *testing.T) {
	if result := runIntegrationProbe(t); !result.ParentIsolationPassed {
		t.Fatal("Parent isolation failed")
	}
}

func TestDifferencingVHDXSiblingIsolation(t *testing.T) {
	if result := runIntegrationProbe(t); !result.SiblingIsolationPassed {
		t.Fatal("sibling isolation failed")
	}
}

func TestDifferencingVHDXReattachPersistence(t *testing.T) {
	if result := runIntegrationProbe(t); !result.ReattachPersistencePassed {
		t.Fatal("reattach persistence failed")
	}
}

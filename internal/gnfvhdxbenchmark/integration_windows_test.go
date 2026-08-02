//go:build windows && gnf_vhdx_integration

package gnfvhdxbenchmark

import (
	"context"
	"os"
	"strconv"
	"testing"
)

func TestGNFVHDXSingleWorkerBenchmark(t *testing.T) {
	mode := Mode(os.Getenv("TESTPLAY_GNF_BENCHMARK_MODE"))
	if mode == "" {
		t.Skip("TESTPLAY_GNF_BENCHMARK_MODE is not set")
	}
	parentBytes := int64(0)
	if value := os.Getenv("TESTPLAY_GNF_PARENT_VHDX_SIZE_GIB"); value != "" {
		gib, err := strconv.ParseInt(value, 10, 64)
		if err != nil || gib <= 0 {
			t.Fatal("invalid TESTPLAY_GNF_PARENT_VHDX_SIZE_GIB")
		}
		parentBytes = gib << 30
	}
	config := HardwareConfig{EditorPath: os.Getenv("TESTPLAY_UNITY_EDITOR_PATH"), ProjectPath: os.Getenv("TESTPLAY_GNF_PROJECT_PATH"), WorkRoot: os.Getenv("TESTPLAY_GNF_WORK_ROOT"), ArtifactRoot: os.Getenv("TESTPLAY_GNF_ARTIFACT_ROOT"), HelperPath: os.Getenv("TESTPLAY_STORAGE_HELPER_PATH"), Mode: mode, ParentBytes: parentBytes, SourceRevision: os.Getenv("TESTPLAY_GNF_SOURCE_REVISION")}
	summary, err := RunHardware(context.Background(), config)
	if err != nil {
		t.Fatalf("benchmark failed: %v; session=%s", err, summary.SessionID)
	}
	if summary.Verdict != "COMPATIBLE" {
		t.Fatalf("verdict=%s", summary.Verdict)
	}
}

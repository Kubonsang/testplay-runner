package gnfvhdxbenchmark

import (
	"encoding/json"
	"math"
	"os"
	"path/filepath"
	"testing"
)

func TestBuildSmokePlan(t *testing.T) {
	plan, err := BuildPlan(ModeSmoke)
	if err != nil {
		t.Fatal(err)
	}
	if plan.Concurrency != 1 || len(plan.Runs) != 3 {
		t.Fatalf("plan=%#v", plan)
	}
	for index, backend := range baseOrder {
		if plan.Runs[index].Backend != backend || plan.Runs[index].Phase != PhaseGate {
			t.Fatalf("run=%#v", plan.Runs[index])
		}
	}
}

func TestBuildFullPlanRotatesWarmOrder(t *testing.T) {
	plan, err := BuildPlan(ModeFull)
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Runs) != 36 {
		t.Fatalf("runs=%d, want 36", len(plan.Runs))
	}
	warm := plan.Runs[6:]
	wants := [][]Backend{{BackendLegacy, BackendPhysical, BackendVHDX}, {BackendPhysical, BackendVHDX, BackendLegacy}, {BackendVHDX, BackendLegacy, BackendPhysical}}
	for round, want := range wants {
		for index := range want {
			if warm[round*3+index].Backend != want[index] {
				t.Fatalf("round %d = %#v", round+1, warm[round*3:round*3+3])
			}
		}
	}
}

func TestCalculateStatistics(t *testing.T) {
	stats, err := CalculateStatistics([]float64{1, 2, 3, 4, 100})
	if err != nil {
		t.Fatal(err)
	}
	if stats.Median != 3 || stats.Min != 1 || stats.Max != 100 || stats.P95 != 100 {
		t.Fatalf("stats=%#v", stats)
	}
	if math.Abs(stats.Mean-22) > 0.001 || stats.StandardDeviation == 0 {
		t.Fatalf("stats=%#v", stats)
	}
}

func TestParseSemanticRequiresExactGNFSelection(t *testing.T) {
	path := filepath.Join(t.TempDir(), "results.xml")
	writeResultXML(t, path, SelectionFilter, "Passed")
	result, err := ParseSemantic(0, path, "editor.log", 10)
	if err != nil || result.Total != 1 {
		t.Fatalf("result=%#v err=%v", result, err)
	}
	other := filepath.Join(t.TempDir(), "other.xml")
	writeResultXML(t, other, "Other.Test", "Passed")
	if _, err := ParseSemantic(0, other, "editor.log", 10); ErrorCode(err) != CodeSelectionMismatch {
		t.Fatalf("error=%v", err)
	}
}

func TestSemanticDigestIgnoresDurationButNotOutcome(t *testing.T) {
	root := t.TempDir()
	first := filepath.Join(root, "first.xml")
	second := filepath.Join(root, "second.xml")
	writeResultXML(t, first, SelectionFilter, "Passed")
	writeResultXML(t, second, SelectionFilter, "Passed")
	a, _ := ParseSemantic(0, first, "a.log", 1)
	b, _ := ParseSemantic(0, second, "b.log", 999)
	if err := CompareSemantic(a, b); err != nil {
		t.Fatal(err)
	}
	b.SemanticDigest = "different"
	if ErrorCode(CompareSemantic(a, b)) != CodeSemanticMismatch {
		t.Fatal("expected mismatch")
	}
}

func TestValidateRootsRejectsOverlap(t *testing.T) {
	root := t.TempDir()
	project := filepath.Join(root, "project")
	work := filepath.Join(root, "work")
	artifact := filepath.Join(work, "artifacts")
	if ErrorCode(ValidateRoots(project, work, artifact)) != CodeInvalidInput {
		t.Fatal("expected overlap rejection")
	}
}

func TestRequireEmptyOrAbsent(t *testing.T) {
	root := filepath.Join(t.TempDir(), "root")
	if err := RequireEmptyOrAbsent(root); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(root, 0700); err != nil {
		t.Fatal(err)
	}
	if err := RequireEmptyOrAbsent(root); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "owned.txt"), []byte("x"), 0600); err != nil {
		t.Fatal(err)
	}
	if ErrorCode(RequireEmptyOrAbsent(root)) != CodeInvalidInput {
		t.Fatal("expected nonempty rejection")
	}
}

func TestValidateWarmLibraryUsesGNFSentinel(t *testing.T) {
	library := filepath.Join(t.TempDir(), "Library")
	if err := os.MkdirAll(filepath.Join(library, "ScriptAssemblies"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(library, "SourceAssetDB"), []byte("db"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(library, "ScriptAssemblies", SelectionAssembly), []byte("assembly"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := ValidateWarmLibrary(library); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(library, "ScriptAssemblies", SelectionAssembly)); err != nil {
		t.Fatal(err)
	}
	if ErrorCode(ValidateWarmLibrary(library)) != CodeWarmLibraryInvalid {
		t.Fatal("expected missing sentinel failure")
	}
}

func TestEvidenceOmitsUnavailableMetrics(t *testing.T) {
	data, err := json.Marshal(RunMetrics{})
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "{}" {
		t.Fatalf("metrics=%s", data)
	}
}

func TestParentHashAndContaminationVerdicts(t *testing.T) {
	if err := VerifyParentHash("same", "same"); err != nil {
		t.Fatal(err)
	}
	if ErrorCode(VerifyParentHash("before", "after")) != CodeParentChanged {
		t.Fatal("expected changed parent")
	}
	if err := VerifyMarkers("current", []string{"current"}, nil); err != nil {
		t.Fatal(err)
	}
	if ErrorCode(VerifyMarkers("current", []string{"previous"}, []string{"current"})) != CodeContamination {
		t.Fatal("expected contamination")
	}
}

func writeResultXML(t *testing.T, path, name, outcome string) {
	t.Helper()
	data := `<?xml version="1.0"?><test-run total="1" passed="1" failed="0" skipped="0"><test-suite type="Assembly" name="GNF.Tests.PlayMode"><test-case fullname="` + name + `" result="` + outcome + `" duration="0.01"/></test-suite></test-run>`
	if err := os.WriteFile(path, []byte(data), 0600); err != nil {
		t.Fatal(err)
	}
}

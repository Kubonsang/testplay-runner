package gnfvhdxbenchmark

import (
	"encoding/csv"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"time"

	"github.com/Kubonsang/testplay-runner/internal/atomicfile"
)

const EvidenceSchemaVersion = 1

type RunMetrics struct {
	TotalWallClockMs         *int64 `json:"totalWallClockMs,omitempty"`
	WorkspacePrepareMs       *int64 `json:"workspacePrepareMs,omitempty"`
	UnityWallClockMs         *int64 `json:"unityWallClockMs,omitempty"`
	TestExecutionMs          *int64 `json:"testExecutionMs,omitempty"`
	CleanupMs                *int64 `json:"cleanupMs,omitempty"`
	LogicalBytes             *int64 `json:"logicalBytes,omitempty"`
	AllocatedBytes           *int64 `json:"allocatedBytes,omitempty"`
	ProjectCopyMs            *int64 `json:"projectCopyMs,omitempty"`
	LibraryRestoreMs         *int64 `json:"libraryRestoreMs,omitempty"`
	CacheWriteBackMs         *int64 `json:"cacheWriteBackMs,omitempty"`
	CacheWritePeakBytes      *int64 `json:"cacheWritePeakPhysicalBytes,omitempty"`
	ImageResolveMs           *int64 `json:"imageResolveMs,omitempty"`
	ImageValidationMs        *int64 `json:"imageValidationMs,omitempty"`
	PhysicalMaterializeMs    *int64 `json:"physicalMaterializeMs,omitempty"`
	HelperStartupMs          *int64 `json:"helperStartupMs,omitempty"`
	HelperAcquireMs          *int64 `json:"helperAcquireMs,omitempty"`
	ChildCreateMs            *int64 `json:"childCreateMs,omitempty"`
	ChildOpenMs              *int64 `json:"childOpenMs,omitempty"`
	ChildAttachMs            *int64 `json:"childAttachMs,omitempty"`
	PhysicalPathResolveMs    *int64 `json:"physicalPathResolveMs,omitempty"`
	PnPDiscoveryWaitMs       *int64 `json:"pnpDiscoveryWaitMs,omitempty"`
	VolumeReadyWaitMs        *int64 `json:"volumeReadyWaitMs,omitempty"`
	MountResolveMs           *int64 `json:"mountResolveMs,omitempty"`
	MountVisibilityWaitMs    *int64 `json:"mountVisibilityWaitMs,omitempty"`
	HelperReleaseMs          *int64 `json:"helperReleaseMs,omitempty"`
	ChildDetachMs            *int64 `json:"childDetachMs,omitempty"`
	DetachVisibilityWaitMs   *int64 `json:"detachVisibilityWaitMs,omitempty"`
	ChildInitialLogical      *int64 `json:"childInitialLogicalBytes,omitempty"`
	ChildInitialAllocated    *int64 `json:"childInitialAllocatedBytes,omitempty"`
	ChildAfterUnityLogical   *int64 `json:"childAfterUnityLogicalBytes,omitempty"`
	ChildAfterUnityAllocated *int64 `json:"childAfterUnityAllocatedBytes,omitempty"`
}

type MountIdentity struct {
	Phase          string `json:"phase"`
	DiskNumber     int    `json:"diskNumber,omitempty"`
	VolumeGUIDPath string `json:"volumeGuidPath,omitempty"`
	VolumeSerial   string `json:"volumeSerial,omitempty"`
	MountPath      string `json:"mountPath"`
	ChildPath      string `json:"childPath,omitempty"`
	LeaseID        string `json:"leaseId,omitempty"`
	JournalPath    string `json:"journalPath,omitempty"`
	Exists         bool   `json:"exists"`
}

type RunEvidence struct {
	SchemaVersion        int             `json:"schemaVersion"`
	Spec                 RunSpec         `json:"spec"`
	SourceRevision       string          `json:"sourceRevision"`
	UnityVersion         string          `json:"unityVersion"`
	Selection            Selection       `json:"selection"`
	Result               *SemanticResult `json:"result,omitempty"`
	SemanticParity       *bool           `json:"semanticParity,omitempty"`
	ParentIsolation      *bool           `json:"parentIsolation,omitempty"`
	MountIntegrity       *bool           `json:"mountIntegrity,omitempty"`
	CleanupPassed        bool            `json:"cleanupPassed"`
	ResidualDiskCount    int             `json:"residualDiskCount"`
	ResidualMountCount   int             `json:"residualMountCount"`
	ResidualChildCount   int             `json:"residualChildCount"`
	ResidualJournalCount int             `json:"residualJournalCount"`
	Metrics              RunMetrics      `json:"metrics"`
	Mounts               []MountIdentity `json:"mountSnapshots,omitempty"`
	Outliers             []string        `json:"outliers,omitempty"`
	Error                *Error          `json:"error,omitempty"`
}

type SeedEvidence struct {
	FixtureCopyMs              *int64 `json:"fixtureCopyMs,omitempty"`
	ParentCreateMs             *int64 `json:"parentCreateMs,omitempty"`
	ParentAttachMs             *int64 `json:"parentAttachMs,omitempty"`
	ParentInitializeMs         *int64 `json:"parentInitializeMs,omitempty"`
	UnitySeedImportMs          *int64 `json:"unitySeedImportMs,omitempty"`
	PhysicalImageMaterializeMs *int64 `json:"physicalImageMaterializeMs,omitempty"`
	ParentDetachMs             *int64 `json:"parentDetachMs,omitempty"`
	ParentVirtualBytes         *int64 `json:"parentVirtualBytes,omitempty"`
	ParentLogicalBytes         *int64 `json:"parentLogicalBytes,omitempty"`
	ParentAllocatedBytes       *int64 `json:"parentAllocatedBytes,omitempty"`
	ParentHash                 string `json:"parentHash"`
}

type Summary struct {
	SchemaVersion      int                    `json:"schemaVersion"`
	SessionID          string                 `json:"sessionId"`
	Mode               Mode                   `json:"mode"`
	SourceRevision     string                 `json:"sourceRevision"`
	UnityVersion       string                 `json:"unityVersion"`
	Selection          Selection              `json:"selection"`
	Seed               SeedEvidence           `json:"seed"`
	Runs               []RunEvidence          `json:"runs"`
	WarmTotalMs        map[Backend]Statistics `json:"warmTotalWallClockMs,omitempty"`
	WarmUnityMs        map[Backend]Statistics `json:"warmUnityWallClockMs,omitempty"`
	Verdict            string                 `json:"verdict"`
	PerformanceVerdict string                 `json:"performanceVerdict"`
	CreatedAt          time.Time              `json:"createdAt"`
}

func WriteJSON(path string, value any) error {
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return benchmarkError(CodeEvidenceWrite, "create-artifact-directory", path, err)
	}
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return benchmarkError(CodeEvidenceWrite, "encode-json", path, err)
	}
	if err := atomicfile.Write(path, append(data, '\n'), 0600); err != nil {
		return benchmarkError(CodeEvidenceWrite, "write-json", path, err)
	}
	return nil
}

func WriteRunsCSV(path string, runs []RunEvidence) error {
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err
	}
	file, err := os.Create(path)
	if err != nil {
		return err
	}
	writer := csv.NewWriter(file)
	_ = writer.Write([]string{"id", "phase", "round", "order", "backend", "exitCode", "total", "passed", "failed", "semanticDigest", "totalWallClockMs", "workspacePrepareMs", "unityWallClockMs", "cleanupMs"})
	for _, run := range runs {
		row := []string{run.Spec.ID, string(run.Spec.Phase), strconv.Itoa(run.Spec.Round), strconv.Itoa(run.Spec.Order), string(run.Spec.Backend), unavailableInt(nil), unavailableInt(nil), unavailableInt(nil), unavailableInt(nil), "unavailable", unavailable(run.Metrics.TotalWallClockMs), unavailable(run.Metrics.WorkspacePrepareMs), unavailable(run.Metrics.UnityWallClockMs), unavailable(run.Metrics.CleanupMs)}
		if run.Result != nil {
			row[5] = strconv.Itoa(run.Result.ExitCode)
			row[6] = strconv.Itoa(run.Result.Total)
			row[7] = strconv.Itoa(run.Result.Passed)
			row[8] = strconv.Itoa(run.Result.Failed)
			row[9] = run.Result.SemanticDigest
		}
		if err := writer.Write(row); err != nil {
			_ = file.Close()
			return err
		}
	}
	writer.Flush()
	if err := writer.Error(); err != nil {
		_ = file.Close()
		return err
	}
	return file.Close()
}

func unavailable(value *int64) string {
	if value == nil {
		return "unavailable"
	}
	return strconv.FormatInt(*value, 10)
}
func unavailableInt(value *int) string {
	if value == nil {
		return "unavailable"
	}
	return strconv.Itoa(*value)
}

func SummaryMarkdown(summary Summary) string {
	return fmt.Sprintf("# GNF_ single-worker VHDX benchmark\n\n- Session: `%s`\n- Mode: `%s`\n- Revision: `%s`\n- Selection: `%s`\n- Correctness verdict: **%s**\n- Performance verdict: **%s**\n", summary.SessionID, summary.Mode, summary.SourceRevision, summary.Selection.Filter, summary.Verdict, summary.PerformanceVerdict)
}

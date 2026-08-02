package unityvhdxfixture

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/Kubonsang/testplay-runner/internal/atomicfile"
)

const EvidenceSchemaVersion = 1

type SemanticTest struct {
	FullName string `json:"fullName"`
	Outcome  string `json:"outcome"`
}

type PlatformResult struct {
	Platform       string         `json:"platform"`
	ExitCode       int            `json:"exitCode"`
	Total          int            `json:"total"`
	Passed         int            `json:"passed"`
	Failed         int            `json:"failed"`
	Skipped        int            `json:"skipped"`
	Inconclusive   int            `json:"inconclusive"`
	Tests          []SemanticTest `json:"tests"`
	SemanticDigest string         `json:"semanticDigest"`
	ResultsXML     string         `json:"resultsXml"`
	EditorLog      string         `json:"editorLog"`
	WallClockMs    int64          `json:"wallClockMs"`
}

type Metrics struct {
	TotalWallClockMs                 *int64 `json:"totalWallClockMs,omitempty"`
	FixtureCopyMs                    *int64 `json:"fixtureCopyMs,omitempty"`
	ParentCreateMs                   *int64 `json:"parentCreateMs,omitempty"`
	ParentAttachMs                   *int64 `json:"parentAttachMs,omitempty"`
	ParentInitializeMs               *int64 `json:"parentInitializeMs,omitempty"`
	UnitySeedImportMs                *int64 `json:"unitySeedImportMs,omitempty"`
	PhysicalLibraryMaterializeMs     *int64 `json:"physicalLibraryMaterializeMs,omitempty"`
	ParentDetachMs                   *int64 `json:"parentDetachMs,omitempty"`
	PhysicalEditModeMs               *int64 `json:"physicalEditModeMs,omitempty"`
	PhysicalPlayModeMs               *int64 `json:"physicalPlayModeMs,omitempty"`
	HelperStartupMs                  *int64 `json:"helperStartupMs,omitempty"`
	HelperAcquireMs                  *int64 `json:"helperAcquireMs,omitempty"`
	ChildCreateMs                    *int64 `json:"childCreateMs,omitempty"`
	ChildAttachMs                    *int64 `json:"childAttachMs,omitempty"`
	VolumeReadyWaitMs                *int64 `json:"volumeReadyWaitMs,omitempty"`
	MountVisibilityWaitMs            *int64 `json:"mountVisibilityWaitMs,omitempty"`
	VHDXEditModeMs                   *int64 `json:"vhdxEditModeMs,omitempty"`
	VHDXPlayModeMs                   *int64 `json:"vhdxPlayModeMs,omitempty"`
	HelperReleaseMs                  *int64 `json:"helperReleaseMs,omitempty"`
	DetachVisibilityWaitMs           *int64 `json:"detachVisibilityWaitMs,omitempty"`
	CleanupMs                        *int64 `json:"cleanupMs,omitempty"`
	ParentLibraryLogicalBytes        *int64 `json:"parentLibraryLogicalBytes,omitempty"`
	PhysicalLibraryLogicalBytes      *int64 `json:"physicalLibraryLogicalBytes,omitempty"`
	PhysicalLibraryAllocatedBytes    *int64 `json:"physicalLibraryAllocatedBytes,omitempty"`
	ChildReadyLogicalBytes           *int64 `json:"childReadyLogicalBytes,omitempty"`
	ChildReadyAllocatedBytes         *int64 `json:"childReadyAllocatedBytes,omitempty"`
	ChildAfterEditModeLogicalBytes   *int64 `json:"childAfterEditModeLogicalBytes,omitempty"`
	ChildAfterEditModeAllocatedBytes *int64 `json:"childAfterEditModeAllocatedBytes,omitempty"`
	ChildAfterPlayModeLogicalBytes   *int64 `json:"childAfterPlayModeLogicalBytes,omitempty"`
	ChildAfterPlayModeAllocatedBytes *int64 `json:"childAfterPlayModeAllocatedBytes,omitempty"`
}

type ReimportObservations struct {
	ScriptCompilation *bool `json:"scriptCompilation,omitempty"`
	PackageResolution *bool `json:"packageResolution,omitempty"`
	AssetImport       *bool `json:"assetImport,omitempty"`
	DomainReload      *bool `json:"domainReload,omitempty"`
	LibraryRebuild    *bool `json:"libraryRebuildSuspected,omitempty"`
}

type Evidence struct {
	SchemaVersion          int                  `json:"schemaVersion"`
	RunID                  string               `json:"runId"`
	UnityVersion           string               `json:"unityVersion"`
	UnityEditorPath        string               `json:"unityEditorPath"`
	FixtureProjectVersion  string               `json:"fixtureProjectVersion"`
	SeedProjectPath        string               `json:"seedProjectPath"`
	PhysicalProjectPath    string               `json:"physicalProjectPath"`
	VHDXProjectPath        string               `json:"vhdxProjectPath"`
	ParentVirtualBytes     int64                `json:"parentVirtualBytes"`
	ParentLogicalBytes     int64                `json:"parentLogicalBytes"`
	ParentAllocatedBytes   *int64               `json:"parentAllocatedBytes,omitempty"`
	ParentHashBefore       string               `json:"parentHashBefore"`
	ParentHashAfter        string               `json:"parentHashAfter"`
	PhysicalEditMode       *PlatformResult      `json:"physicalEditModeResult,omitempty"`
	PhysicalPlayMode       *PlatformResult      `json:"physicalPlayModeResult,omitempty"`
	VHDXEditMode           *PlatformResult      `json:"vhdxEditModeResult,omitempty"`
	VHDXPlayMode           *PlatformResult      `json:"vhdxPlayModeResult,omitempty"`
	SemanticParity         bool                 `json:"semanticParity"`
	ParentIsolation        bool                 `json:"parentIsolation"`
	MountIntegrity         bool                 `json:"mountIntegrity"`
	CleanupPassed          bool                 `json:"cleanupPassed"`
	ResidualDiskCount      int                  `json:"residualDiskCount"`
	ResidualMountCount     int                  `json:"residualMountCount"`
	ResidualChildCount     int                  `json:"residualChildCount"`
	ResidualJournalCount   int                  `json:"residualJournalCount"`
	ResidualUnityProcesses int                  `json:"residualUnityProcessCount"`
	Metrics                Metrics              `json:"metrics"`
	Reimport               ReimportObservations `json:"reimportObservations"`
	Outliers               []string             `json:"outliers,omitempty"`
	Error                  *Error               `json:"error,omitempty"`
	CreatedAt              time.Time            `json:"createdAt"`
}

func NewEvidence(runID string) Evidence {
	return Evidence{SchemaVersion: EvidenceSchemaVersion, RunID: runID, CreatedAt: time.Now().UTC()}
}

func WriteEvidence(path string, evidence Evidence) error {
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return fixtureError(CodeEvidenceWriteFailed, "create-artifact-directory", filepath.Dir(path), err)
	}
	data, err := json.MarshalIndent(evidence, "", "  ")
	if err != nil {
		return fixtureError(CodeEvidenceWriteFailed, "encode-evidence", path, err)
	}
	data = append(data, '\n')
	if err := atomicfile.Write(path, data, 0600); err != nil {
		return fixtureError(CodeEvidenceWriteFailed, "write-evidence", path, err)
	}
	return nil
}

func milliseconds(value int64) *int64 { return &value }

func phaseOutliers(metrics Metrics) []string {
	values := []struct {
		name  string
		value *int64
	}{
		{"totalWallClockMs", metrics.TotalWallClockMs},
		{"fixtureCopyMs", metrics.FixtureCopyMs},
		{"parentCreateMs", metrics.ParentCreateMs},
		{"parentAttachMs", metrics.ParentAttachMs},
		{"parentInitializeMs", metrics.ParentInitializeMs},
		{"unitySeedImportMs", metrics.UnitySeedImportMs},
		{"physicalLibraryMaterializeMs", metrics.PhysicalLibraryMaterializeMs},
		{"parentDetachMs", metrics.ParentDetachMs},
		{"physicalEditModeMs", metrics.PhysicalEditModeMs},
		{"physicalPlayModeMs", metrics.PhysicalPlayModeMs},
		{"helperStartupMs", metrics.HelperStartupMs},
		{"helperAcquireMs", metrics.HelperAcquireMs},
		{"volumeReadyWaitMs", metrics.VolumeReadyWaitMs},
		{"mountVisibilityWaitMs", metrics.MountVisibilityWaitMs},
		{"vhdxEditModeMs", metrics.VHDXEditModeMs},
		{"vhdxPlayModeMs", metrics.VHDXPlayModeMs},
		{"helperReleaseMs", metrics.HelperReleaseMs},
		{"detachVisibilityWaitMs", metrics.DetachVisibilityWaitMs},
		{"cleanupMs", metrics.CleanupMs},
	}
	var outliers []string
	for _, value := range values {
		if value.value != nil && *value.value >= 30000 {
			outliers = append(outliers, fmt.Sprintf("%s=%d", value.name, *value.value))
		}
	}
	return outliers
}

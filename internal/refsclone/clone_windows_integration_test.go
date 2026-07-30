//go:build windows && refs_integration

package refsclone_test

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/Kubonsang/testplay-runner/internal/refsclone"
)

type probeEvidence struct {
	SchemaVersion              int                       `json:"schemaVersion"`
	Operation                  string                    `json:"operation"`
	Volume                     refsclone.VolumeInfo      `json:"volume"`
	FixtureBytes               int64                     `json:"fixtureBytes"`
	CloneResult                *refsclone.Result         `json:"cloneResult"`
	SourceMeasurement          refsclone.FileMeasurement `json:"sourceMeasurement"`
	CloneMeasurement           refsclone.FileMeasurement `json:"cloneMeasurement"`
	PhysicalCopyMeasurement    refsclone.FileMeasurement `json:"physicalCopyMeasurement"`
	FreeBefore                 uint64                    `json:"freeBefore"`
	FreeAfterClone             uint64                    `json:"freeAfterClone"`
	FreeAfterDestinationWrite  uint64                    `json:"freeAfterDestinationWrite"`
	FreeAfterSourceWrite       uint64                    `json:"freeAfterSourceWrite"`
	FreeAfterPhysicalCopy      uint64                    `json:"freeAfterPhysicalCopy"`
	BlockCloneVolumeDelta      uint64                    `json:"blockCloneVolumeDelta"`
	PhysicalCopyVolumeDelta    uint64                    `json:"physicalCopyVolumeDelta"`
	ByteParityPassed           bool                      `json:"byteParityPassed"`
	DestinationIsolationPassed bool                      `json:"destinationIsolationPassed"`
	SourceIsolationPassed      bool                      `json:"sourceIsolationPassed"`
	StorageSavingsPassed       bool                      `json:"storageSavingsPassed"`
	CleanupPassed              bool                      `json:"cleanupPassed"`
	Verdict                    string                    `json:"verdict"`
}

func TestReFSBlockCloneProbe(t *testing.T) {
	root := os.Getenv("TESTPLAY_REFS_PROBE_ROOT")
	if root == "" {
		t.Skip("TESTPLAY_REFS_PROBE_ROOT is not set; real ReFS validation is opt-in")
	}
	ctx := context.Background()
	capability, err := refsclone.Probe(ctx, root)
	if err != nil {
		t.Fatalf("actual DeviceIoControl capability probe: %v (capability=%+v)", err, capability)
	}
	volume, err := refsclone.InspectVolume(root)
	if err != nil {
		t.Fatal(err)
	}
	length, err := refsclone.FixtureLength(volume.ClusterSize)
	if err != nil {
		t.Fatal(err)
	}
	dir, err := os.MkdirTemp(root, "testplay-refs-probe-")
	if err != nil {
		t.Fatal(err)
	}
	source := filepath.Join(dir, refsclone.SourceFixtureName)
	clone := filepath.Join(dir, refsclone.CloneFixtureName)
	physical := filepath.Join(dir, refsclone.PhysicalCopyFixtureName)
	evidence := probeEvidence{
		SchemaVersion: 1, Operation: "refs-block-clone-probe",
		Volume: volume, FixtureBytes: length,
	}
	defer func() {
		if err := os.RemoveAll(dir); err != nil {
			t.Errorf("cleanup %s: %v", dir, err)
		}
	}()

	writePatternFile(t, source, length, volume.ClusterSize)
	sourceBefore := hashFile(t, source)
	evidence.SourceMeasurement, err = refsclone.MeasureFile(source)
	if err != nil {
		t.Fatal(err)
	}
	evidence.FreeBefore = inspectFree(t, root)
	evidence.CloneResult, err = refsclone.CloneFile(ctx, refsclone.Request{
		SourcePath: source, DestinationPath: clone, Length: length,
	})
	if err != nil {
		t.Fatal(err)
	}
	evidence.ByteParityPassed = sourceBefore == hashFile(t, clone)
	if !evidence.ByteParityPassed {
		t.Fatal("source and clone hash differ immediately after DeviceIoControl")
	}
	evidence.CloneMeasurement, err = refsclone.MeasureFile(clone)
	if err != nil {
		t.Fatal(err)
	}
	evidence.FreeAfterClone = inspectFree(t, root)
	evidence.BlockCloneVolumeDelta = consumed(evidence.FreeBefore, evidence.FreeAfterClone)

	writeCluster(t, clone, length/2, volume.ClusterSize, 0xd3)
	evidence.FreeAfterDestinationWrite = inspectFree(t, root)
	evidence.DestinationIsolationPassed = hashFile(t, source) == sourceBefore
	if !evidence.DestinationIsolationPassed {
		t.Fatal("destination write changed source")
	}
	cloneAfterOwnWrite := hashFile(t, clone)

	writeCluster(t, source, int64(volume.ClusterSize), volume.ClusterSize, 0x5a)
	evidence.FreeAfterSourceWrite = inspectFree(t, root)
	evidence.SourceIsolationPassed = hashFile(t, clone) == cloneAfterOwnWrite
	if !evidence.SourceIsolationPassed {
		t.Fatal("source write changed clone")
	}

	copyFile(t, source, physical)
	evidence.PhysicalCopyMeasurement, err = refsclone.MeasureFile(physical)
	if err != nil {
		t.Fatal(err)
	}
	evidence.FreeAfterPhysicalCopy = inspectFree(t, root)
	evidence.PhysicalCopyVolumeDelta = consumed(
		evidence.FreeAfterSourceWrite,
		evidence.FreeAfterPhysicalCopy,
	)
	evidence.StorageSavingsPassed =
		evidence.BlockCloneVolumeDelta < evidence.PhysicalCopyVolumeDelta
	if err := os.RemoveAll(dir); err != nil {
		t.Fatalf("cleanup %s: %v", dir, err)
	}
	evidence.CleanupPassed = true
	evidence.Verdict = "promising"
	if evidence.StorageSavingsPassed {
		evidence.Verdict = "proven"
	}
	data, err := json.MarshalIndent(evidence, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("ReFS probe evidence:\n%s", data)
}

func consumed(before, after uint64) uint64 {
	if after >= before {
		return 0
	}
	return before - after
}

func writePatternFile(t *testing.T, path string, length int64, cluster uint64) {
	t.Helper()
	file, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	block := make([]byte, cluster)
	for offset := int64(0); offset < length; offset += int64(cluster) {
		for index := range block {
			block[index] = byte((offset/int64(cluster) + int64(index)) % 251)
		}
		if _, err := file.Write(block); err != nil {
			t.Fatal(err)
		}
	}
	if err := file.Sync(); err != nil {
		t.Fatal(err)
	}
}

func hashFile(t *testing.T, path string) [sha256.Size]byte {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return sha256.Sum256(data)
}

func writeCluster(t *testing.T, path string, offset int64, cluster uint64, value byte) {
	t.Helper()
	file, err := os.OpenFile(path, os.O_WRONLY, 0600)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	if _, err := file.WriteAt(make([]byte, cluster), offset); err != nil {
		t.Fatal(err)
	}
	block := make([]byte, cluster)
	for index := range block {
		block[index] = value
	}
	if _, err := file.WriteAt(block, offset); err != nil {
		t.Fatal(err)
	}
	if err := file.Sync(); err != nil {
		t.Fatal(err)
	}
}

func inspectFree(t *testing.T, root string) uint64 {
	t.Helper()
	info, err := refsclone.InspectVolume(root)
	if err != nil {
		t.Fatal(err)
	}
	return info.FreeBytes
}

func copyFile(t *testing.T, source, destination string) {
	t.Helper()
	data, err := os.ReadFile(source)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(destination, data, 0600); err != nil {
		t.Fatal(err)
	}
}

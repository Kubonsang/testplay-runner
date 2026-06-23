package bridge

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// TestAtomicWriteRead_NoTornReads hammers the atomic response file with a
// concurrent writer and a tight reader to prove tmp+rename never exposes a
// partial/torn document and that the replace-existing rename never fails while a
// reader is contending for the same path.
//
// This is the cross-platform stand-in for the Windows atomic-file-handoff spike
// (RELEASE-PLAN.md): on the CI windows-latest job it exercises os.Rename's
// MoveFileExW(REPLACE_EXISTING) semantics under concurrency, which is exactly the
// "sharing violation / partial read" risk the spike targets. Run with -race.
func TestAtomicWriteRead_NoTornReads(t *testing.T) {
	dir := t.TempDir()
	const runID = "20260101-000000-deadbeef"
	respPath := filepath.Join(dir, "responses", runID+".resp.json")

	const iterations = 1000
	done := make(chan struct{})
	var writeErr error

	go func() {
		defer close(done)
		for i := 0; i < iterations; i++ {
			if err := writeAtomicJSON(respPath, responseFile{
				SchemaVersion:         "1",
				BridgeProtocolVersion: ProtocolVersion,
				RunID:                 runID,
				Outcome:               OutcomeCompleted,
				ResultsXMLWritten:     true,
				CompileErrorCount:     i, // varies every write so a torn read is detectable
			}); err != nil {
				writeErr = err
				return
			}
		}
	}()

	valid, torn := 0, 0
	check := func() {
		// readResponse guards against torn reads by failing json.Unmarshal, so an
		// ok=true read MUST be a fully-formed response for this runID.
		if r, ok := readResponse(respPath, runID); ok {
			if r.SchemaVersion == "1" && r.RunID == runID && r.Outcome == OutcomeCompleted && r.ResultsXMLWritten {
				valid++
			} else {
				torn++
			}
		}
	}

	for {
		check()
		select {
		case <-done:
			check() // final drain after the writer finished
			if writeErr != nil {
				t.Fatalf("atomic write failed under concurrent reader: %v", writeErr)
			}
			if torn > 0 {
				t.Fatalf("observed %d torn reads — tmp+rename is not atomic on this platform", torn)
			}
			if valid == 0 {
				t.Fatal("reader never observed a valid response despite 1000 writes")
			}
			t.Logf("atomic handoff OK: %d writes, %d valid concurrent reads, 0 torn", iterations, valid)
			return
		default:
			// Brief yield models the production poller (which opens the file only
			// briefly) so the writer's replace-rename gets windows to succeed.
			time.Sleep(200 * time.Microsecond)
		}
	}
}

// TestAtomicRequestRoundTrip_NoTornReads mirrors the above for the request file
// (the Go-written side of the protocol), so both directions of the atomic
// handoff are covered on every platform including CI Windows.
func TestAtomicRequestRoundTrip_NoTornReads(t *testing.T) {
	dir := t.TempDir()
	const runID = "20260101-000001-feedface"
	reqPath := filepath.Join(dir, "requests", runID+".req.json")

	const iterations = 1000
	done := make(chan struct{})
	var writeErr error

	go func() {
		defer close(done)
		for i := 0; i < iterations; i++ {
			if err := writeAtomicJSON(reqPath, requestFile{
				SchemaVersion:         "1",
				BridgeProtocolVersion: ProtocolVersion,
				RunID:                 runID,
				TestPlatform:          "edit_mode",
				ResultsXML:            "/x/results.xml",
				IdleDeadlineMs:        int64(i),
			}); err != nil {
				writeErr = err
				return
			}
		}
	}()

	reads := 0
	read := func() {
		raw, err := os.ReadFile(reqPath)
		if err != nil {
			return // not written yet
		}
		var rf requestFile
		if json.Unmarshal(raw, &rf) != nil {
			t.Fatalf("torn request read: unmarshal failed on %d bytes", len(raw))
		}
		if rf.RunID != runID || rf.BridgeProtocolVersion != ProtocolVersion {
			t.Fatalf("torn request read: %+v", rf)
		}
		reads++
	}

	for {
		read()
		select {
		case <-done:
			read()
			if writeErr != nil {
				t.Fatalf("atomic request write failed under concurrent reader: %v", writeErr)
			}
			if reads == 0 {
				t.Fatal("reader never observed a valid request")
			}
			t.Logf("request handoff OK: %d writes, %d valid concurrent reads", iterations, reads)
			return
		default:
			time.Sleep(200 * time.Microsecond)
		}
	}
}

package status_test

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/Kubonsang/testplay-runner/internal/status"
)

func TestWrite_CreatesFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "testplay-status.json")
	w := status.NewWriter(path)
	if err := w.Write(status.Status{Phase: status.PhaseWaiting}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if _, err := os.Stat(path); os.IsNotExist(err) {
		t.Error("status file was not created")
	}
}

func TestWrite_ValidJSON(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "testplay-status.json")
	w := status.NewWriter(path)
	_ = w.Write(status.Status{Phase: status.PhaseCompiling, RunID: "20250301-102200"})

	data, _ := os.ReadFile(path)
	var s status.Status
	if err := json.Unmarshal(data, &s); err != nil {
		t.Fatalf("not valid JSON: %v", err)
	}
	if s.Phase != status.PhaseCompiling {
		t.Errorf("got phase %q", s.Phase)
	}
}

func TestWrite_IsAtomic(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "testplay-status.json")
	w := status.NewWriter(path)

	// Write twice; no temp file should linger
	_ = w.Write(status.Status{Phase: status.PhaseWaiting})
	_ = w.Write(status.Status{Phase: status.PhaseRunning})

	entries, _ := os.ReadDir(dir)
	if len(entries) != 1 {
		names := make([]string, len(entries))
		for i, e := range entries {
			names[i] = e.Name()
		}
		t.Errorf("expected only status file, found %d files: %v", len(entries), names)
	}
}

func TestWrite_ContainsSchemaVersion(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "testplay-status.json")
	w := status.NewWriter(path)
	_ = w.Write(status.Status{Phase: status.PhaseRunning})

	data, _ := os.ReadFile(path)
	if !bytes.Contains(data, []byte(`"schema_version"`)) {
		t.Error("status JSON must contain schema_version")
	}
}

func TestWrite_SeqStartsAtOne(t *testing.T) {
	dir := t.TempDir()
	w := status.NewWriter(filepath.Join(dir, "status.json"))
	_ = w.Write(status.Status{Phase: status.PhaseCompiling})

	data, _ := os.ReadFile(filepath.Join(dir, "status.json"))
	var s status.Status
	if err := json.Unmarshal(data, &s); err != nil {
		t.Fatalf("not valid JSON: %v", err)
	}
	if s.Seq != 1 {
		t.Errorf("first Write: seq = %d, want 1", s.Seq)
	}
}

func TestWrite_SeqIncrementsOnEachWrite(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "status.json")
	w := status.NewWriter(path)

	for want := 1; want <= 3; want++ {
		_ = w.Write(status.Status{Phase: status.PhaseCompiling})
		data, _ := os.ReadFile(path)
		var s status.Status
		if err := json.Unmarshal(data, &s); err != nil {
			t.Fatalf("write %d: not valid JSON: %v", want, err)
		}
		if s.Seq != want {
			t.Errorf("write %d: seq = %d, want %d", want, s.Seq, want)
		}
	}
}

func TestWrite_NewWriterHasIndependentSeq(t *testing.T) {
	dir := t.TempDir()
	w1 := status.NewWriter(filepath.Join(dir, "a.json"))
	w2 := status.NewWriter(filepath.Join(dir, "b.json"))

	_ = w1.Write(status.Status{Phase: status.PhaseCompiling})
	_ = w1.Write(status.Status{Phase: status.PhaseCompiling})
	_ = w2.Write(status.Status{Phase: status.PhaseCompiling})

	read := func(path string) status.Status {
		data, _ := os.ReadFile(path)
		var s status.Status
		_ = json.Unmarshal(data, &s)
		return s
	}
	if got := read(filepath.Join(dir, "a.json")).Seq; got != 2 {
		t.Errorf("w1 seq = %d, want 2", got)
	}
	if got := read(filepath.Join(dir, "b.json")).Seq; got != 1 {
		t.Errorf("w2 seq = %d, want 1", got)
	}
}

package ipc_test

import (
	"bufio"
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/Kubonsang/testplay-runner/internal/ipc"
)

func TestBusWriter_RoundTrip(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "bus.ndjson")

	w, err := ipc.NewBusWriter(path, "host")
	if err != nil {
		t.Fatalf("NewBusWriter: %v", err)
	}

	if _, err := w.Append(ipc.Message{To: "*", Kind: "ready", Payload: map[string]any{"port": 7777}}); err != nil {
		t.Fatalf("Append: %v", err)
	}

	msgs := readAll(t, path)
	if len(msgs) != 1 {
		t.Fatalf("got %d messages, want 1", len(msgs))
	}
	m := msgs[0]
	if m.From != "host" {
		t.Errorf("From = %q, want %q", m.From, "host")
	}
	if m.To != "*" {
		t.Errorf("To = %q, want %q", m.To, "*")
	}
	if m.Kind != "ready" {
		t.Errorf("Kind = %q, want %q", m.Kind, "ready")
	}
	if m.Seq != 1 {
		t.Errorf("Seq = %d, want 1", m.Seq)
	}
	if m.Ts == "" {
		t.Errorf("Ts is empty; should be auto-set")
	}
	if m.Payload == nil {
		t.Errorf("Payload is nil")
	}
}

func TestBusWriter_SeqStartsAt1AndIncrements(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "bus.ndjson")
	w, _ := ipc.NewBusWriter(path, "host")

	for i := 1; i <= 3; i++ {
		m, err := w.Append(ipc.Message{To: "client", Kind: "tick"})
		if err != nil {
			t.Fatalf("Append #%d: %v", i, err)
		}
		if m.Seq != i {
			t.Errorf("Append #%d: Seq = %d, want %d", i, m.Seq, i)
		}
	}
}

func TestBusWriter_ConcurrentAppend(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "bus.ndjson")
	w, _ := ipc.NewBusWriter(path, "host")

	const N = 50
	var wg sync.WaitGroup
	wg.Add(N)
	for i := 0; i < N; i++ {
		go func() {
			defer wg.Done()
			_, _ = w.Append(ipc.Message{To: "*", Kind: "tick"})
		}()
	}
	wg.Wait()

	msgs := readAll(t, path)
	if len(msgs) != N {
		t.Fatalf("got %d messages, want %d (lost or extra writes)", len(msgs), N)
	}

	// Every seq from 1..N must appear exactly once.
	seen := make(map[int]bool, N)
	for _, m := range msgs {
		if m.Seq < 1 || m.Seq > N {
			t.Errorf("seq %d out of range", m.Seq)
		}
		if seen[m.Seq] {
			t.Errorf("seq %d appeared twice", m.Seq)
		}
		seen[m.Seq] = true
	}
	for i := 1; i <= N; i++ {
		if !seen[i] {
			t.Errorf("seq %d missing", i)
		}
	}
}

func TestBusWriter_LongPayloadStillReadable(t *testing.T) {
	// PIPE_BUF (typically 4096B) is the OS atomic-append boundary.
	// Beyond that, atomic guarantee is gone, but a single-writer scenario
	// must still produce readable lines.
	dir := t.TempDir()
	path := filepath.Join(dir, "bus.ndjson")
	w, _ := ipc.NewBusWriter(path, "host")

	big := make([]byte, 5000)
	for i := range big {
		big[i] = 'x'
	}
	if _, err := w.Append(ipc.Message{To: "*", Kind: "blob", Payload: string(big)}); err != nil {
		t.Fatalf("Append: %v", err)
	}

	msgs := readAll(t, path)
	if len(msgs) != 1 {
		t.Fatalf("got %d messages, want 1", len(msgs))
	}
	if s, ok := msgs[0].Payload.(string); !ok || len(s) != 5000 {
		t.Errorf("payload not preserved")
	}
}

func TestBusWriter_FromIsBoundToWriterRole(t *testing.T) {
	// User-supplied From in the input Message is overridden by the writer's
	// role — this prevents impersonation in test fixtures.
	dir := t.TempDir()
	path := filepath.Join(dir, "bus.ndjson")
	w, _ := ipc.NewBusWriter(path, "host")

	m, err := w.Append(ipc.Message{From: "client", To: "*", Kind: "spoof"})
	if err != nil {
		t.Fatal(err)
	}
	if m.From != "host" {
		t.Errorf("From = %q, want host (writer-bound)", m.From)
	}
}

func TestBusWriter_NewBusWriter_InvalidPath(t *testing.T) {
	// Writer creation should not fail for a path whose parent dir doesn't
	// exist yet — file is created lazily on first Append.
	// But Append should fail with a clear error.
	dir := t.TempDir()
	path := filepath.Join(dir, "nope", "bus.ndjson")
	w, err := ipc.NewBusWriter(path, "host")
	if err != nil {
		t.Fatalf("NewBusWriter should not fail for nonexistent parent: %v", err)
	}
	if _, err := w.Append(ipc.Message{Kind: "x"}); err == nil {
		t.Error("Append should fail when parent dir is missing")
	}
}

// readAll opens the bus file and decodes every NDJSON line into Message.
func readAll(t *testing.T, path string) []ipc.Message {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		t.Fatalf("open %s: %v", path, err)
	}
	defer f.Close()

	var out []ipc.Message
	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 0, 8192), 1<<20)
	for scanner.Scan() {
		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}
		var m ipc.Message
		if err := json.Unmarshal(line, &m); err != nil {
			t.Fatalf("unmarshal %q: %v", string(line), err)
		}
		out = append(out, m)
	}
	if err := scanner.Err(); err != nil {
		t.Fatalf("scan: %v", err)
	}
	return out
}

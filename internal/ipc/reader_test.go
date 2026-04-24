package ipc_test

import (
	"context"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/Kubonsang/testplay-runner/internal/ipc"
)

func TestPollingReader_PicksUpNewLines(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "bus.ndjson")
	hostW, _ := ipc.NewBusWriter(path, "host")
	if _, err := hostW.Append(ipc.Message{To: "*", Kind: "ready"}); err != nil {
		t.Fatal(err)
	}

	r := ipc.NewPollingReader(path, "client", 20*time.Millisecond)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	out := make(chan ipc.ReadEvent, 8)
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		_ = r.Run(ctx, out)
	}()

	select {
	case ev := <-out:
		if ev.Direction != "recv" {
			t.Errorf("Direction = %q, want recv", ev.Direction)
		}
		if ev.Msg.Kind != "ready" {
			t.Errorf("Kind = %q, want ready", ev.Msg.Kind)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("reader did not pick up message within 2s")
	}

	cancel()
	wg.Wait()
}

func TestPollingReader_FilterByRole(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "bus.ndjson")

	hostW, _ := ipc.NewBusWriter(path, "host")
	clientW, _ := ipc.NewBusWriter(path, "client")
	otherW, _ := ipc.NewBusWriter(path, "spectator")

	// host → client: client should receive (recv)
	_, _ = hostW.Append(ipc.Message{To: "client", Kind: "for-client"})
	// host → spectator: client should NOT see
	_, _ = hostW.Append(ipc.Message{To: "spectator", Kind: "for-spec"})
	// client → host: client should see as send (own message)
	_, _ = clientW.Append(ipc.Message{To: "host", Kind: "from-client"})
	// spectator → host: client should NOT see (not addressed to client)
	_, _ = otherW.Append(ipc.Message{To: "host", Kind: "spec-to-host"})

	r := ipc.NewPollingReader(path, "client", 20*time.Millisecond)
	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()

	out := make(chan ipc.ReadEvent, 16)
	go func() { _ = r.Run(ctx, out); close(out) }()

	var got []ipc.ReadEvent
	for ev := range out {
		got = append(got, ev)
	}

	if len(got) != 2 {
		t.Fatalf("got %d events, want 2 (one recv + one send)", len(got))
	}

	var recv, send int
	for _, ev := range got {
		switch ev.Direction {
		case "recv":
			recv++
			if ev.Msg.Kind != "for-client" {
				t.Errorf("recv kind = %q, want for-client", ev.Msg.Kind)
			}
		case "send":
			send++
			if ev.Msg.Kind != "from-client" {
				t.Errorf("send kind = %q, want from-client", ev.Msg.Kind)
			}
		}
	}
	if recv != 1 || send != 1 {
		t.Errorf("recv=%d send=%d, want 1/1", recv, send)
	}
}

func TestPollingReader_BroadcastTo(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "bus.ndjson")
	hostW, _ := ipc.NewBusWriter(path, "host")
	_, _ = hostW.Append(ipc.Message{To: "*", Kind: "broadcast"})

	r := ipc.NewPollingReader(path, "client", 20*time.Millisecond)
	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()

	out := make(chan ipc.ReadEvent, 4)
	go func() { _ = r.Run(ctx, out); close(out) }()

	var got []ipc.ReadEvent
	for ev := range out {
		got = append(got, ev)
	}
	if len(got) != 1 || got[0].Direction != "recv" || got[0].Msg.Kind != "broadcast" {
		t.Errorf("got %+v, want one recv broadcast", got)
	}
}

func TestPollingReader_StopOnContextCancel(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "bus.ndjson")
	r := ipc.NewPollingReader(path, "client", 20*time.Millisecond)

	ctx, cancel := context.WithCancel(context.Background())
	out := make(chan ipc.ReadEvent, 4)

	done := make(chan struct{})
	go func() {
		_ = r.Run(ctx, out)
		close(done)
	}()

	cancel()
	select {
	case <-done:
	case <-time.After(500 * time.Millisecond):
		t.Fatal("reader did not exit after context cancel")
	}
}

func TestPollingReader_MissingFileWaits(t *testing.T) {
	// Bus file may not exist yet when reader starts (race against
	// scenario.runner creating the file). Reader should poll without
	// erroring until the file appears.
	dir := t.TempDir()
	path := filepath.Join(dir, "bus.ndjson") // not created yet

	r := ipc.NewPollingReader(path, "client", 20*time.Millisecond)
	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()

	out := make(chan ipc.ReadEvent, 4)
	done := make(chan struct{})
	go func() { _ = r.Run(ctx, out); close(done) }()

	// Create file midway and append.
	time.Sleep(100 * time.Millisecond)
	w, _ := ipc.NewBusWriter(path, "host")
	_, _ = w.Append(ipc.Message{To: "*", Kind: "late"})

	select {
	case ev := <-out:
		if ev.Msg.Kind != "late" {
			t.Errorf("kind = %q, want late", ev.Msg.Kind)
		}
	case <-time.After(500 * time.Millisecond):
		t.Fatal("reader did not pick up late-created file")
	}
	cancel()
	<-done
}

func TestAccumulator_AddSnapshot(t *testing.T) {
	var a ipc.Accumulator
	a.Add(ipc.ReadEvent{Direction: "send", Msg: ipc.Message{Seq: 1, Kind: "x"}})
	a.Add(ipc.ReadEvent{Direction: "recv", Msg: ipc.Message{Seq: 2, Kind: "y"}})

	snap := a.Snapshot()
	if len(snap) != 2 {
		t.Fatalf("got %d, want 2", len(snap))
	}
	if snap[0].Msg.Kind != "x" || snap[1].Msg.Kind != "y" {
		t.Errorf("order broken: %+v", snap)
	}

	// Snapshot must be a copy: mutating it should not affect the accumulator.
	snap[0].Msg.Kind = "mutated"
	again := a.Snapshot()
	if again[0].Msg.Kind != "x" {
		t.Errorf("Snapshot leaked internal slice: %q", again[0].Msg.Kind)
	}
}

func TestRunReaderInto_AccumulatesUntilCancel(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "bus.ndjson")
	hostW, _ := ipc.NewBusWriter(path, "host")
	_, _ = hostW.Append(ipc.Message{To: "*", Kind: "a"})
	_, _ = hostW.Append(ipc.Message{To: "*", Kind: "b"})

	r := ipc.NewPollingReader(path, "client", 20*time.Millisecond)
	var acc ipc.Accumulator
	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()

	if err := ipc.RunReaderInto(ctx, r, &acc); err != nil {
		t.Fatalf("RunReaderInto: %v", err)
	}

	snap := acc.Snapshot()
	if len(snap) != 2 {
		t.Fatalf("got %d events, want 2", len(snap))
	}
	if snap[0].Msg.Kind != "a" || snap[1].Msg.Kind != "b" {
		t.Errorf("order: %+v", snap)
	}
}

func TestPollingReader_HandlesMalformedLine(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "bus.ndjson")
	if err := os.WriteFile(path, []byte("not json\n"+`{"seq":1,"ts":"t","from":"host","to":"*","kind":"ok"}`+"\n"), 0644); err != nil {
		t.Fatal(err)
	}

	r := ipc.NewPollingReader(path, "client", 20*time.Millisecond)
	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()

	out := make(chan ipc.ReadEvent, 4)
	go func() { _ = r.Run(ctx, out); close(out) }()

	var got []ipc.ReadEvent
	for ev := range out {
		got = append(got, ev)
	}
	if len(got) != 1 || got[0].Msg.Kind != "ok" {
		t.Errorf("got %+v, want one ok event (malformed line skipped)", got)
	}
}

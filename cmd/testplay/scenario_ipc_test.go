package main

import (
	"bufio"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/Kubonsang/testplay-runner/internal/ipc"
)

func TestAppendIpcEventsToLog_WritesEventsWithOriginalTimestamps(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "events.ndjson")
	if err := os.WriteFile(path, []byte(`{"event":"run_started","timestamp":"2026-04-24T10:00:00Z"}`+"\n"), 0644); err != nil {
		t.Fatal(err)
	}

	events := []ipc.ReadEvent{
		{Direction: "send", Msg: ipc.Message{Seq: 1, Ts: "2026-04-24T10:00:05Z", From: "host", To: "*", Kind: "ready"}},
		{Direction: "recv", Msg: ipc.Message{Seq: 2, Ts: "2026-04-24T10:00:07Z", From: "client", To: "host", Kind: "joined"}},
	}
	appendIpcEventsToLog(path, "host", events)

	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()

	var lines []map[string]any
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		var m map[string]any
		if err := json.Unmarshal(scanner.Bytes(), &m); err != nil {
			t.Fatalf("bad line: %v", err)
		}
		lines = append(lines, m)
	}
	if len(lines) != 3 {
		t.Fatalf("got %d lines, want 3 (1 prior + 2 ipc)", len(lines))
	}

	send, recv := lines[1], lines[2]
	if send["event"] != "ipc_send" || send["ipc_kind"] != "ready" || send["ipc_peer"] != "*" {
		t.Errorf("send line shape wrong: %+v", send)
	}
	if send["timestamp"] != "2026-04-24T10:00:05Z" {
		t.Errorf("send timestamp not preserved: %v", send["timestamp"])
	}
	if recv["event"] != "ipc_recv" || recv["ipc_kind"] != "joined" || recv["ipc_peer"] != "client" {
		t.Errorf("recv line shape wrong: %+v", recv)
	}
	if recv["timestamp"] != "2026-04-24T10:00:07Z" {
		t.Errorf("recv timestamp not preserved: %v", recv["timestamp"])
	}
}

func TestAppendIpcEventsToLog_NoOpWhenEventsFileMissing(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "events.ndjson") // never created
	events := []ipc.ReadEvent{
		{Direction: "send", Msg: ipc.Message{Seq: 1, Ts: "t", From: "host", To: "*", Kind: "x"}},
	}
	// Must not panic, must not create the file.
	appendIpcEventsToLog(path, "host", events)
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Errorf("expected file to remain absent, got err=%v", err)
	}
}

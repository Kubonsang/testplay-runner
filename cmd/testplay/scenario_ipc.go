package main

import (
	"os"

	"github.com/Kubonsang/testplay-runner/internal/ipc"
	"github.com/Kubonsang/testplay-runner/internal/status"
)

// appendIpcEventsToLog writes ipc_send/ipc_recv entries into the per-instance
// events.ndjson at path. Best-effort: a missing file (e.g., test mode that
// never opened a runs/<runID>/ dir) is silently skipped.
func appendIpcEventsToLog(path, role string, events []ipc.ReadEvent) {
	if _, err := os.Stat(path); err != nil {
		return
	}
	log := status.NewEventLog(path)
	for _, ev := range events {
		seq := ev.Msg.Seq
		peer := ev.Msg.From
		if ev.Direction == "send" {
			peer = ev.Msg.To
		}
		_ = log.Append(status.Event{
			Event:     "ipc_" + ev.Direction,
			Timestamp: ev.Msg.Ts,
			IpcSeq:    &seq,
			IpcKind:   ev.Msg.Kind,
			IpcPeer:   peer,
		})
	}
	_ = role // role retained for future correlation; currently embedded via run_id at the file path
}

// ipcMessagesFromEvents flattens captured ReadEvents into the raw Message
// list emitted in scenario output's instances[].ipc_messages.
func ipcMessagesFromEvents(events []ipc.ReadEvent) []ipc.Message {
	out := make([]ipc.Message, len(events))
	for i, ev := range events {
		out[i] = ev.Msg
	}
	return out
}

// ipcSummary is the per-instance roll-up emitted under
// instances[].ipc_summary. last_sent / last_received are nil when this
// instance never sent or received anything.
type ipcSummary struct {
	SentCount     int          `json:"sent_count"`
	ReceivedCount int          `json:"received_count"`
	LastSent      *ipc.Message `json:"last_sent"`
	LastReceived  *ipc.Message `json:"last_received"`
}

// ipcSummaryFromEvents computes counts and last-of-each-direction from
// the captured event stream.
func ipcSummaryFromEvents(events []ipc.ReadEvent) ipcSummary {
	var s ipcSummary
	for i := range events {
		ev := events[i]
		switch ev.Direction {
		case "send":
			s.SentCount++
			m := ev.Msg
			s.LastSent = &m
		case "recv":
			s.ReceivedCount++
			m := ev.Msg
			s.LastReceived = &m
		}
	}
	return s
}

package runsvc

import (
	"strings"
	"testing"
	"time"
)

func TestGenerateRunID_Format(t *testing.T) {
	t.Parallel()
	ts := time.Date(2026, 3, 26, 14, 30, 55, 0, time.UTC)
	id := generateRunID(ts)
	// Expected: "20260326-143055-xxxxxxxx" (8 lower-hex chars)
	if !strings.HasPrefix(id, "20260326-143055-") {
		t.Fatalf("expected prefix '20260326-143055-', got %q", id)
	}
	suffix := strings.TrimPrefix(id, "20260326-143055-")
	if len(suffix) != 8 {
		t.Fatalf("expected 8-char hex suffix, got %q (len %d)", suffix, len(suffix))
	}
	for _, c := range suffix {
		if !strings.ContainsRune("0123456789abcdef", c) {
			t.Fatalf("non-hex char %q in suffix %q", c, suffix)
		}
	}
}

func TestGenerateRunID_Unique(t *testing.T) {
	t.Parallel()
	ts := time.Date(2026, 3, 26, 14, 30, 55, 0, time.UTC)
	seen := make(map[string]bool)
	for i := 0; i < 1000; i++ {
		id := generateRunID(ts)
		if seen[id] {
			t.Fatalf("collision on iteration %d: %q", i, id)
		}
		seen[id] = true
	}
}

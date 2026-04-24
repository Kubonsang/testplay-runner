package runid_test

import (
	"strings"
	"testing"
	"time"

	"github.com/Kubonsang/testplay-runner/internal/runid"
)

func TestGenerate_Format(t *testing.T) {
	t.Parallel()
	ts := time.Date(2026, 3, 26, 14, 30, 55, 0, time.UTC)
	id := runid.Generate(ts)
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

func TestGenerate_Unique(t *testing.T) {
	t.Parallel()
	ts := time.Date(2026, 3, 26, 14, 30, 55, 0, time.UTC)
	seen := make(map[string]bool)
	for i := 0; i < 1000; i++ {
		id := runid.Generate(ts)
		if seen[id] {
			t.Fatalf("collision on iteration %d: %q", i, id)
		}
		seen[id] = true
	}
}

func TestGenerate_RoundTripsThroughIsValid(t *testing.T) {
	t.Parallel()
	id := runid.Generate(time.Now().UTC())
	if !runid.IsValid(id) {
		t.Errorf("IsValid rejected freshly generated id %q", id)
	}
}

func TestIsValid(t *testing.T) {
	t.Parallel()
	cases := []struct {
		id   string
		want bool
	}{
		{"20260326-143055-a3f8b2c1", true},
		{"20260326-143055", true}, // legacy
		{"not-a-runid", false},
		{"20260326-143055-XYZ12345", false},
		{"", false},
	}
	for _, c := range cases {
		if got := runid.IsValid(c.id); got != c.want {
			t.Errorf("IsValid(%q) = %v, want %v", c.id, got, c.want)
		}
	}
}

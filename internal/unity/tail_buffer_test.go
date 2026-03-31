package unity

import (
	"bytes"
	"testing"
)

func TestTailBuffer_BasicWrite(t *testing.T) {
	var tb tailBuffer
	tb.Write([]byte("hello"))
	if got := string(tb.Bytes()); got != "hello" {
		t.Errorf("got %q, want %q", got, "hello")
	}
}

func TestTailBuffer_TruncatesToMaxBytes(t *testing.T) {
	var tb tailBuffer
	// Write maxTailBytes+100 bytes; only the last maxTailBytes must be retained.
	data := make([]byte, maxTailBytes+100)
	for i := range data {
		data[i] = byte(i % 256)
	}
	tb.Write(data)
	got := tb.Bytes()
	if len(got) != maxTailBytes {
		t.Fatalf("len: got %d, want %d", len(got), maxTailBytes)
	}
	// Verify it's the LAST maxTailBytes bytes, not the first.
	want := data[100:]
	if !bytes.Equal(got, want) {
		t.Errorf("content mismatch: first byte got %d want %d, last byte got %d want %d",
			got[0], want[0], got[len(got)-1], want[len(want)-1])
	}
}

func TestTailBuffer_MultipleWritesRetainsTail(t *testing.T) {
	var tb tailBuffer
	// Fill to capacity with 'A's, then overwrite with 'B's.
	tb.Write(bytes.Repeat([]byte("A"), maxTailBytes))
	tb.Write(bytes.Repeat([]byte("B"), maxTailBytes))
	got := tb.Bytes()
	if len(got) != maxTailBytes {
		t.Fatalf("len: got %d, want %d", len(got), maxTailBytes)
	}
	if !bytes.Equal(got, bytes.Repeat([]byte("B"), maxTailBytes)) {
		t.Errorf("expected all B's, got first byte %q", got[0])
	}
}

func TestTailBuffer_PartialOverwrite(t *testing.T) {
	var tb tailBuffer
	// Write maxTailBytes bytes of 'A', then write 10 bytes of 'B'.
	// Result: last 10 bytes are 'B', preceding bytes are 'A'.
	tb.Write(bytes.Repeat([]byte("A"), maxTailBytes))
	tb.Write([]byte("BBBBBBBBBB")) // 10 B's
	got := tb.Bytes()
	if len(got) != maxTailBytes {
		t.Fatalf("len: got %d, want %d", len(got), maxTailBytes)
	}
	tail := got[maxTailBytes-10:]
	if !bytes.Equal(tail, []byte("BBBBBBBBBB")) {
		t.Errorf("last 10 bytes: got %q, want %q", tail, "BBBBBBBBBB")
	}
	head := got[:maxTailBytes-10]
	for _, b := range head {
		if b != 'A' {
			t.Errorf("expected A in head, got %q", b)
			break
		}
	}
}

func TestTailBuffer_ZeroAllocationAfterFill(t *testing.T) {
	var tb tailBuffer
	// Fill the buffer.
	tb.Write(bytes.Repeat([]byte("X"), maxTailBytes))
	// Subsequent writes must not allocate heap memory.
	allocs := testing.AllocsPerRun(100, func() {
		tb.Write([]byte("hello world"))
	})
	if allocs > 0 {
		t.Errorf("expected 0 allocations per write after fill, got %v", allocs)
	}
}

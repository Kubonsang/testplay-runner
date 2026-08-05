package atomicfile

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

func TestWriteDurableReplacesAndReadsBack(t *testing.T) {
	path := filepath.Join(t.TempDir(), "journal.json")
	if err := os.WriteFile(path, []byte("old"), 0600); err != nil {
		t.Fatal(err)
	}
	want := bytes.Repeat([]byte("durable"), 64)
	if err := WriteDurable(path, want, 0600); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(path)
	if err != nil || !bytes.Equal(got, want) {
		t.Fatalf("got=%q err=%v", got, err)
	}
	matches, err := filepath.Glob(filepath.Join(filepath.Dir(path), ".journal.json.tmp-*"))
	if err != nil || len(matches) != 0 {
		t.Fatalf("temporary files=%v err=%v", matches, err)
	}
}

func TestWriteExclusiveDurableRejectsExisting(t *testing.T) {
	path := filepath.Join(t.TempDir(), "marker.json")
	if err := WriteExclusiveDurable(path, []byte("first"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := WriteExclusiveDurable(path, []byte("second"), 0600); err == nil {
		t.Fatal("existing durable file was replaced")
	}
	got, err := os.ReadFile(path)
	if err != nil || string(got) != "first" {
		t.Fatalf("got=%q err=%v", got, err)
	}
}

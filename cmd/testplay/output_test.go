package main

import (
	"bytes"
	"encoding/json"
	"testing"
)

func TestWriteJSON_IncludesSchemaVersion(t *testing.T) {
	var buf bytes.Buffer
	writeJSON(&buf, map[string]any{"ready": true})

	var m map[string]any
	if err := json.Unmarshal(buf.Bytes(), &m); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	if m["schema_version"] == nil {
		t.Error("schema_version missing from output")
	}
	if m["schema_version"] != "1" {
		t.Errorf("schema_version should be '1', got %v", m["schema_version"])
	}
}

func TestWriteJSON_PreservesFields(t *testing.T) {
	var buf bytes.Buffer
	writeJSON(&buf, map[string]any{"ready": true, "hint": "fix your config"})

	var m map[string]any
	json.Unmarshal(buf.Bytes(), &m)
	if m["ready"] != true {
		t.Error("ready field should be preserved")
	}
	if m["hint"] != "fix your config" {
		t.Error("hint field should be preserved")
	}
}

func TestWriteJSON_ValidJSON(t *testing.T) {
	var buf bytes.Buffer
	writeJSON(&buf, map[string]any{"x": 1, "y": "hello"})

	if !json.Valid(buf.Bytes()) {
		t.Errorf("output is not valid JSON: %q", buf.String())
	}
}

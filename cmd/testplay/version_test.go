package main

import (
	"bytes"
	"encoding/json"
	"testing"
)

func TestVersionCmd_OutputsJSONWithSchemaVersion(t *testing.T) {
	var buf bytes.Buffer
	runVersion(&buf)

	var out map[string]any
	if err := json.Unmarshal(buf.Bytes(), &out); err != nil {
		t.Fatalf("output is not valid JSON: %v\nraw: %s", err, buf.String())
	}
	if out["schema_version"] != "1" {
		t.Errorf("schema_version: got %v, want \"1\"", out["schema_version"])
	}
	if out["version"] == "" || out["version"] == nil {
		t.Errorf("version field missing or empty")
	}
}

func TestVersionCmd_CommitAndDateOmittedWhenEmpty(t *testing.T) {
	orig := commit
	origDate := date
	commit = ""
	date = ""
	defer func() { commit = orig; date = origDate }()

	var buf bytes.Buffer
	runVersion(&buf)

	var out map[string]any
	json.Unmarshal(buf.Bytes(), &out)
	if _, ok := out["commit"]; ok {
		t.Error("commit field should be absent when empty")
	}
	if _, ok := out["date"]; ok {
		t.Error("date field should be absent when empty")
	}
}

func TestVersionCmd_CommitAndDatePresentWhenSet(t *testing.T) {
	orig := commit
	origDate := date
	commit = "abc1234"
	date = "2026-03-27"
	defer func() { commit = orig; date = origDate }()

	var buf bytes.Buffer
	runVersion(&buf)

	var out map[string]any
	json.Unmarshal(buf.Bytes(), &out)
	if out["commit"] != "abc1234" {
		t.Errorf("commit: got %v, want abc1234", out["commit"])
	}
	if out["date"] != "2026-03-27" {
		t.Errorf("date: got %v, want 2026-03-27", out["date"])
	}
}

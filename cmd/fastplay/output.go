package main

import (
	"encoding/json"
	"fmt"
	"io"
)

// writeJSON serializes v to JSON with schema_version:"1" injected and writes to w.
func writeJSON(w io.Writer, v any) {
	data, err := json.Marshal(v)
	if err != nil {
		fmt.Fprintf(w, `{"schema_version":"1","error":"internal serialization error"}`+"\n")
		return
	}

	var m map[string]any
	if err := json.Unmarshal(data, &m); err != nil || m == nil {
		m = map[string]any{"data": v}
	}
	m["schema_version"] = "1"

	out, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return
	}
	fmt.Fprintf(w, "%s\n", out)
}

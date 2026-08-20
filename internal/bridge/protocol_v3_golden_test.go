package bridge

import (
	"encoding/json"
	"os"
	"testing"
)

func TestProtocol3GoldenRequest(t *testing.T) {
	data, err := os.ReadFile("../../unity/com.testplay.bridge/Tests/Editor/TestData/protocol-v3-request.json")
	if err != nil {
		t.Fatal(err)
	}
	var req requestFile
	if err := json.Unmarshal(data, &req); err != nil {
		t.Fatal(err)
	}
	if req.BridgeProtocolVersion != ProtocolVersion || req.CapabilityKind != string(CapabilityWarmTest) ||
		req.WorkspaceID != "workspace-golden" || req.EditorPID != 4242 || req.BridgeSessionID != "session-golden" {
		t.Fatalf("golden request drifted: %+v", req)
	}
	roundTrip, err := json.Marshal(req)
	if err != nil {
		t.Fatal(err)
	}
	var keys map[string]json.RawMessage
	if err := json.Unmarshal(roundTrip, &keys); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"bridge_protocol_version", "workspace_id", "editor_pid", "bridge_session_id", "capability_kind"} {
		if _, ok := keys[key]; !ok {
			t.Fatalf("Go DTO omitted golden key %q", key)
		}
	}
}

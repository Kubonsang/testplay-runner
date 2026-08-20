using System;

namespace TestPlay.Bridge
{
    /// <summary>
    /// Wire protocol shared with the Go client (internal/bridge). Field names are
    /// snake_case to match Go's json tags so Unity's JsonUtility (de)serialization
    /// is byte-compatible with encoding/json. DTOs use public fields (JsonUtility
    /// ignores properties) and are [Serializable].
    /// </summary>
    internal static class BridgeProtocol
    {
        // MUST equal internal/bridge.ProtocolVersion on the Go side.
        public const int Version = 3;

        public const string CapabilityCompile = "compile";
        public const string CapabilityWarmTest = "warm-test";

        public static bool IsRequestForSession(RequestDto request, string currentSessionId)
        {
            return request != null &&
                   !string.IsNullOrEmpty(currentSessionId) &&
                   string.Equals(request.bridge_session_id, currentSessionId, StringComparison.Ordinal);
        }

        public static bool IsKnownCapability(RequestDto request)
        {
            return request != null &&
                   (request.capability_kind == CapabilityCompile ||
                    request.capability_kind == CapabilityWarmTest);
        }

		public static bool RequiresTestRun(RequestDto request)
		{
			return request != null && request.capability_kind == CapabilityWarmTest;
		}

        public static bool IsRequestForEditor(
            RequestDto request,
            string currentSessionId,
            string currentWorkspaceId,
            int currentEditorPid)
        {
            return IsRequestForSession(request, currentSessionId) &&
                   request.editor_pid > 0 &&
                   request.editor_pid == currentEditorPid &&
                   string.Equals(request.workspace_id ?? "", currentWorkspaceId ?? "", StringComparison.Ordinal);
        }

        public static bool IsCancellationForRequest(CancellationMarkerDto marker, RequestDto request)
        {
            return marker != null && request != null &&
                   marker.bridge_protocol_version == Version &&
                   marker.run_id == request.run_id &&
                   marker.bridge_session_id == request.bridge_session_id &&
                   marker.workspace_id == request.workspace_id &&
                   marker.editor_pid == request.editor_pid;
        }

        // editor_state values.
        public const string StateIdle = "idle";
        public const string StateCompiling = "compiling";
        public const string StateImporting = "importing";
        public const string StateInPlayMode = "in_playmode";
        public const string StateRunningTests = "running_tests";

        // outcome values.
        public const string OutcomeCompleted = "completed";
        public const string OutcomeCompileFailed = "compile_failed";
        public const string OutcomeBuildFailed = "build_failed";
        public const string OutcomeBusy = "busy";
        public const string OutcomeRejected = "rejected";
        public const string OutcomeIndeterminate = "indeterminate";

        public const string ExecutionNotStarted = "not_started";
        public const string ExecutionPossiblyStarted = "possibly_started";
    }

    [Serializable]
    internal class HandshakeDto
    {
        public string schema_version = "1";
        public int bridge_protocol_version = BridgeProtocol.Version;
        public string project_path;
        public string project_path_real;
        public string unity_version;
        public int editor_pid;
        public string workspace_id;
        public string bridge_session_id;
        public string updated_at;
        public string editor_state;
        public string active_run_id = "";
    }

    [Serializable]
    internal class RequestDto
    {
        public string schema_version;
        public int bridge_protocol_version;
        public string run_id;
        public string bridge_session_id;
        public string workspace_id;
        public int editor_pid;
        public string capability_kind;
        public string test_platform;
        public string filter;
        public string category;
        public string results_xml;
        public string status_ndjson;
        public string compile_errors_json;
        public long idle_deadline_ms;
    }

    [Serializable]
    internal class ResponseDto
    {
        public string schema_version = "1";
        public int bridge_protocol_version = BridgeProtocol.Version;
        public string run_id;
        public string bridge_session_id;
        public string workspace_id;
        public int editor_pid;
		public string capability_kind;
        public string outcome;
        public bool results_xml_written;
        public bool compile_failed;
        public int compile_error_count;
        public string[] non_pristine = new string[0];
        public string finished_at;
    }

    [Serializable]
    internal class TombstoneDto
    {
        public string schema_version = "1";
        public int bridge_protocol_version = BridgeProtocol.Version;
        public string run_id;
        public string bridge_session_id;
        public string workspace_id;
        public int editor_pid;
        public string execution_state;
        public string reason;
        public string created_at;
    }

    [Serializable]
    internal class CancellationMarkerDto
    {
        public string schema_version = "1";
        public int bridge_protocol_version = BridgeProtocol.Version;
        public string run_id;
		public string bridge_session_id;
		public string workspace_id;
		public int editor_pid;
        public string owned_run_guid;
        public string execution_state;
        public string reason;
        public string created_at;
    }

    [Serializable]
    internal class ProgressDto
    {
        public string phase;
        public int total;
        public int passed;
        public int failed;
        public string current_test;
    }

    [Serializable]
    internal class CompileErrorDto
    {
        public string file;
        public string absolute_path;
        public int line;
        public int column;
        public string message;
    }

    [Serializable]
    internal class CompileErrorsFileDto
    {
        public string schema_version = "1";
        public CompileErrorDto[] errors = new CompileErrorDto[0];
    }
}

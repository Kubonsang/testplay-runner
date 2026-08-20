using System;
using System.Diagnostics;
using System.Globalization;
using UnityEditor;
using UnityEngine;

namespace TestPlay.Bridge
{
    /// <summary>
    /// Writes the liveness/identity handshake the Go client's Probe gate reads.
    /// The handshake is refreshed on a throttled heartbeat so a stale file
    /// (editor crashed/closed) makes Probe fall back to the cold path.
    /// </summary>
    internal static class HandshakeWriter
    {
        private static readonly string s_workspaceId =
            Environment.GetEnvironmentVariable("HONEYBEE_WORKSPACE_ID") ?? "";

        public static string WorkspaceId => s_workspaceId;
        public static int EditorPid => SafePid();

        public static void Write(string sessionId, string editorState, string activeRunId)
        {
            var dto = new HandshakeDto
            {
                project_path = BridgePaths.ProjectRoot,
                project_path_real = BridgePaths.ProjectRoot,
                unity_version = Application.unityVersion,
                editor_pid = SafePid(),
                workspace_id = s_workspaceId,
                bridge_session_id = sessionId,
                updated_at = DateTime.UtcNow.ToString("yyyy-MM-ddTHH:mm:ssZ", CultureInfo.InvariantCulture),
                editor_state = editorState,
                active_run_id = activeRunId ?? "",
            };
            AtomicFile.WriteAllText(BridgePaths.Handshake, JsonUtility.ToJson(dto, true));
        }

        /// <summary>
        /// Reports the editor's current readiness as a protocol editor_state.
        /// The Go Probe uses this only as a soft pre-filter; the C#-side
        /// PristineGate is the authoritative correctness bar at run time.
        /// </summary>
        public static string CurrentState(bool runningTests)
        {
            if (runningTests)
                return BridgeProtocol.StateRunningTests;
            if (EditorApplication.isPlayingOrWillChangePlaymode || EditorApplication.isPlaying)
                return BridgeProtocol.StateInPlayMode;
            if (EditorApplication.isCompiling)
                return BridgeProtocol.StateCompiling;
            if (EditorApplication.isUpdating)
                return BridgeProtocol.StateImporting;
            return BridgeProtocol.StateIdle;
        }

        private static int SafePid()
        {
            try
            {
                return Process.GetCurrentProcess().Id;
            }
            catch
            {
                return 0;
            }
        }
    }
}

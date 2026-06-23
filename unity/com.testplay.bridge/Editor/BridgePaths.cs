using System.IO;
using UnityEngine;

namespace TestPlay.Bridge
{
    /// <summary>
    /// Canonical locations of the file-based bridge protocol, mirroring the Go
    /// side's internal/bridge.BridgeDir layout under &lt;project&gt;/.testplay/bridge/.
    /// </summary>
    internal static class BridgePaths
    {
        // Application.dataPath is &lt;project&gt;/Assets; the project root is its parent.
        public static string ProjectRoot => Path.GetFullPath(Path.Combine(Application.dataPath, ".."));

        public static string BridgeDir => Path.Combine(ProjectRoot, ".testplay", "bridge");
        public static string Handshake => Path.Combine(BridgeDir, "handshake.json");
        public static string EnableSentinel => Path.Combine(BridgeDir, "ENABLE");
        public static string RequestsDir => Path.Combine(BridgeDir, "requests");
        public static string ResponsesDir => Path.Combine(BridgeDir, "responses");
        public static string RunsDir => Path.Combine(BridgeDir, "runs");

        public static string RequestPath(string runId) => Path.Combine(RequestsDir, runId + ".req.json");
        public static string CancelPath(string runId) => Path.Combine(RequestsDir, runId + ".cancel");
        public static string ResponsePath(string runId) => Path.Combine(ResponsesDir, runId + ".resp.json");
        public static string RunDir(string runId) => Path.Combine(RunsDir, runId);
    }
}

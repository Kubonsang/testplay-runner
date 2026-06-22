using System;
using System.Globalization;
using System.IO;
using UnityEditor;
using UnityEditor.TestTools.TestRunner.Api;
using UnityEngine;

namespace TestPlay.Bridge
{
    /// <summary>
    /// The warm-editor bridge server. Driven by EditorApplication.update on the
    /// main thread, it heartbeats the handshake and processes one run request at
    /// a time through a small state machine. Because settling compilation can
    /// trigger a domain reload (which wipes in-memory state), per-run progress is
    /// persisted in SessionState (survives reloads, clears on editor restart).
    ///
    /// State machine per active run:
    ///   new        → gate; refuse/busy, or stream compiling + AssetDatabase.Refresh → refreshing
    ///   refreshing → wait for compile to settle; compile errors → compile_failed;
    ///                else start the TestRunnerApi run → running
    ///   running    → TestRunController writes results.xml + completed response on finish
    ///
    /// Spike-gated behaviors (see RELEASE-PLAN / plan): TestRunnerApi cancellation,
    /// results.xml fidelity, compile-settle reliability.
    /// </summary>
    internal static class BridgeServer
    {
        private const string KeySession = "testplay.bridge.session";
        private const string KeyActive = "testplay.bridge.activeRun";
        private const string KeyPhase = "testplay.bridge.phase";
        private const string KeyDeadline = "testplay.bridge.deadlineTicks";
        private const string KeyNonPristine = "testplay.bridge.nonPristine";

        private const string PhaseNew = "new";
        private const string PhaseRefreshing = "refreshing";
        private const string PhaseRunning = "running";

        private const double HeartbeatSeconds = 1.0;

        private static bool s_started;
        private static double s_lastBeat;
        private static bool s_runningTests;

        // Valid only within the domain that started the run (lost on reload; we
        // detect that case as an orphan and fall back).
        private static TestRunnerApi s_api;
        private static TestRunController s_controller;

        public static void Start()
        {
            if (s_started)
                return;
            s_started = true;

            if (string.IsNullOrEmpty(SessionState.GetString(KeySession, "")))
                SessionState.SetString(KeySession, GenerateSessionId());

            CompileErrorSidecar.Hook();

            // A run that was mid-flight when the domain reloaded cannot be
            // recovered; mark it orphaned so the Go side falls back to cold.
            if (SessionState.GetString(KeyPhase, "") == PhaseRunning && s_controller == null)
            {
                string orphan = SessionState.GetString(KeyActive, "");
                if (!string.IsNullOrEmpty(orphan))
                    WriteTerminal(orphan, BridgeProtocol.OutcomeRejected, false, 0, "run orphaned by a domain reload");
                ClearActive();
            }

            EditorApplication.update -= Update;
            EditorApplication.update += Update;
        }

        private static void Update()
        {
            try
            {
                Heartbeat();
                Pump();
            }
            catch (Exception e)
            {
                Debug.LogError($"[TestPlay.Bridge] update error: {e}");
            }
        }

        private static void Heartbeat()
        {
            double now = EditorApplication.timeSinceStartup;
            if (now - s_lastBeat < HeartbeatSeconds)
                return;
            s_lastBeat = now;
            HandshakeWriter.Write(
                SessionState.GetString(KeySession, ""),
                HandshakeWriter.CurrentState(s_runningTests),
                SessionState.GetString(KeyActive, ""));
        }

        private static void Pump()
        {
            string active = SessionState.GetString(KeyActive, "");
            if (string.IsNullOrEmpty(active))
            {
                ClaimNextRequest();
                return;
            }
            Advance(active);
        }

        private static void ClaimNextRequest()
        {
            if (!Directory.Exists(BridgePaths.RequestsDir))
                return;

            string[] files = Directory.GetFiles(BridgePaths.RequestsDir, "*.req.json");
            Array.Sort(files, StringComparer.Ordinal); // run-id order == chronological
            foreach (string file in files)
            {
                string runId = Path.GetFileName(file);
                runId = runId.Substring(0, runId.Length - ".req.json".Length);

                if (File.Exists(BridgePaths.ResponsePath(runId)))
                    continue; // already answered

                var req = ReadRequest(file);
                if (req == null)
                    continue;

                if (req.bridge_protocol_version != BridgeProtocol.Version)
                {
                    WriteTerminal(runId, BridgeProtocol.OutcomeRejected, false, 0,
                        $"protocol version mismatch (request={req.bridge_protocol_version}, bridge={BridgeProtocol.Version})");
                    continue;
                }

                // Claim it.
                SessionState.SetString(KeyActive, runId);
                SessionState.SetString(KeyPhase, PhaseNew);
                long deadline = DateTime.UtcNow.AddMilliseconds(Math.Max(1000, req.idle_deadline_ms)).Ticks;
                SessionState.SetString(KeyDeadline, deadline.ToString(CultureInfo.InvariantCulture));
                return;
            }
        }

        private static void Advance(string runId)
        {
            string reqPath = BridgePaths.RequestPath(runId);
            var req = ReadRequest(reqPath);
            if (req == null || File.Exists(BridgePaths.ResponsePath(runId)))
            {
                ClearActive();
                return;
            }

            string phase = SessionState.GetString(KeyPhase, PhaseNew);
            switch (phase)
            {
                case PhaseNew:
                    AdvanceNew(req);
                    break;
                case PhaseRefreshing:
                    AdvanceRefreshing(req);
                    break;
                case PhaseRunning:
                    AdvanceRunning(req);
                    break;
                default:
                    ClearActive();
                    break;
            }
        }

        private static void AdvanceNew(RequestDto req)
        {
            var gate = PristineGate.Evaluate(req);
            switch (gate.Decision)
            {
                case GateDecision.Refuse:
                    WriteTerminal(req.run_id, BridgeProtocol.OutcomeRejected, false, 0, gate.RefuseReason);
                    ClearActive();
                    return;
                case GateDecision.Wait:
                    if (DeadlinePassed())
                    {
                        WriteTerminal(req.run_id, BridgeProtocol.OutcomeBusy, false, 0, "editor did not become idle in time");
                        ClearActive();
                    }
                    return;
                case GateDecision.Proceed:
                    SessionState.SetString(KeyNonPristine, string.Join("\n", gate.NonPristine));
                    new StatusStream(req.status_ndjson).Compiling();
                    SessionState.SetString(KeyPhase, PhaseRefreshing);
                    // May trigger import + compilation (+ a domain reload). The
                    // sidecar (hooked at Start) captures any resulting errors.
                    AssetDatabase.Refresh();
                    return;
            }
        }

        private static void AdvanceRefreshing(RequestDto req)
        {
            if (EditorApplication.isCompiling || EditorApplication.isUpdating)
            {
                if (DeadlinePassed())
                {
                    WriteTerminal(req.run_id, BridgeProtocol.OutcomeBusy, false, 0, "compilation did not settle in time");
                    ClearActive();
                }
                return; // still settling; re-check next tick (survives reloads)
            }

            // Compilation settled. A failed compile does not reload the domain,
            // so any captured errors are still present.
            if (CompileErrorSidecar.HasErrors)
            {
                CompileErrorSidecar.Write(req.compile_errors_json);
                WriteTerminal(req.run_id, BridgeProtocol.OutcomeCompileFailed, true, CompileErrorSidecar.Count, null);
                ClearActive();
                return;
            }

            // A late transition into Play Mode invalidates equivalence.
            if (EditorApplication.isPlaying || EditorApplication.isPlayingOrWillChangePlaymode)
            {
                WriteTerminal(req.run_id, BridgeProtocol.OutcomeRejected, false, 0, "editor entered Play Mode");
                ClearActive();
                return;
            }

            StartRun(req);
        }

        private static void AdvanceRunning(RequestDto req)
        {
            // The controller writes the completed response on RunFinished. If the
            // controller is gone (domain reloaded mid-run), the run is orphaned.
            if (s_controller == null)
            {
                WriteTerminal(req.run_id, BridgeProtocol.OutcomeRejected, false, 0, "run orphaned by a domain reload");
                ClearActive();
            }
            // Cancellation (requests/<runId>.cancel) is best-effort: the Go side
            // owns the authoritative timeout/interrupt result, so no action is
            // required here for v0.10.0.
        }

        private static void StartRun(RequestDto req)
        {
            string session = SessionState.GetString(KeySession, "");
            string[] nonPristine = SplitNonEmpty(SessionState.GetString(KeyNonPristine, ""));
            var stream = new StatusStream(req.status_ndjson);

            var filter = new Filter { testMode = TestMode.EditMode };
            if (!string.IsNullOrEmpty(req.filter))
                filter.groupNames = new[] { req.filter }; // regex over full names, matching CLI -testFilter
            if (!string.IsNullOrEmpty(req.category))
                filter.categoryNames = new[] { req.category };

            s_api = ScriptableObject.CreateInstance<TestRunnerApi>();
            s_controller = new TestRunController(req, session, nonPristine, stream, OnRunFinished);
            s_api.RegisterCallbacks(s_controller);

            s_runningTests = true;
            SessionState.SetString(KeyPhase, PhaseRunning);
            s_api.Execute(new ExecutionSettings(filter));
        }

        private static void OnRunFinished()
        {
            s_runningTests = false;
            s_controller = null;
            s_api = null;
            CompileErrorSidecar.Clear(); // next run starts from a clean error slate
            ClearActive();
        }

        // ── helpers ─────────────────────────────────────────────────────────

        private static RequestDto ReadRequest(string path)
        {
            try
            {
                if (!File.Exists(path))
                    return null;
                string json = File.ReadAllText(path);
                var req = JsonUtility.FromJson<RequestDto>(json);
                return string.IsNullOrEmpty(req?.run_id) ? null : req;
            }
            catch
            {
                return null; // partial write; retry next tick
            }
        }

        private static void WriteTerminal(string runId, string outcome, bool compileFailed, int errorCount, string disclosure)
        {
            var resp = new ResponseDto
            {
                run_id = runId,
                bridge_session_id = SessionState.GetString(KeySession, ""),
                outcome = outcome,
                results_xml_written = false,
                compile_failed = compileFailed,
                compile_error_count = errorCount,
                non_pristine = string.IsNullOrEmpty(disclosure) ? new string[0] : new[] { disclosure },
                finished_at = DateTime.UtcNow.ToString("yyyy-MM-ddTHH:mm:ssZ", CultureInfo.InvariantCulture),
            };
            AtomicFile.WriteAllText(BridgePaths.ResponsePath(runId), JsonUtility.ToJson(resp, true));
        }

        private static bool DeadlinePassed()
        {
            string raw = SessionState.GetString(KeyDeadline, "");
            if (long.TryParse(raw, NumberStyles.Integer, CultureInfo.InvariantCulture, out long ticks))
                return DateTime.UtcNow.Ticks > ticks;
            return false;
        }

        private static void ClearActive()
        {
            SessionState.SetString(KeyActive, "");
            SessionState.SetString(KeyPhase, "");
            SessionState.SetString(KeyDeadline, "");
            SessionState.SetString(KeyNonPristine, "");
        }

        private static string[] SplitNonEmpty(string joined)
        {
            if (string.IsNullOrEmpty(joined))
                return new string[0];
            return joined.Split(new[] { '\n' }, StringSplitOptions.RemoveEmptyEntries);
        }

        private static string GenerateSessionId()
        {
            string ts = DateTime.UtcNow.ToString("yyyyMMdd-HHmmss", CultureInfo.InvariantCulture);
            var bytes = new byte[4];
            using (var rng = System.Security.Cryptography.RandomNumberGenerator.Create())
                rng.GetBytes(bytes);
            return ts + "-" + BitConverter.ToString(bytes).Replace("-", "").ToLowerInvariant();
        }
    }
}

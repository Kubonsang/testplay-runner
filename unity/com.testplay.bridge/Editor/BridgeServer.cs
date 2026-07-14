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
        private const string KeyOwnedRunGuid = "testplay.bridge.ownedRunGuid";
        private const string KeyCancelReason = "testplay.bridge.cancelReason";
        private const string KeyCancelNotBefore = "testplay.bridge.cancelNotBeforeTicks";
        private const string KeyCancelNextProbe = "testplay.bridge.cancelNextProbeTicks";
        private const string KeyCancelInactiveCount = "testplay.bridge.cancelInactiveCount";
        private const string KeyCancelTombstoneOnly = "testplay.bridge.cancelTombstoneOnly";
        private const string KeyCancelRequireProbe = "testplay.bridge.cancelRequireProbe";

        private const string PhaseNew = "new";
        private const string PhaseRefreshing = "refreshing";
        private const string PhaseRunning = "running";
        private const string PhaseCancelling = "cancelling";

        private const double HeartbeatSeconds = 1.0;
        private const int DomainReloadCancelGraceMs = 500;
        private const int CancelProbeIntervalMs = 100;

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
            // recovered. Persist cancellation instead of publishing a terminal
            // result before the owned Test Framework job is demonstrably gone.
            if (SessionState.GetString(KeyPhase, "") == PhaseRunning && s_controller == null)
            {
                string orphan = SessionState.GetString(KeyActive, "");
                if (!string.IsNullOrEmpty(orphan))
                    BeginCancellation("run orphaned by a domain reload", true, false, true);
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
            // editor_state must reflect a claimed run even after a domain reload
            // reset the in-memory s_runningTests flag: report busy whenever
            // SessionState still holds an active run id, so the handshake's
            // editor_state and active_run_id never disagree (the Go Probe keys
            // on editor_state==idle).
            string active = SessionState.GetString(KeyActive, "");
            // A foreign run (Test Runner window) also makes the editor busy, so
            // the Go Probe declines instead of queueing a request we would refuse.
            bool activityKnown = RunActivityMonitor.TryGetIsRunActive(out bool frameworkRunActive);
            bool busy = s_runningTests || !string.IsNullOrEmpty(active) || !activityKnown || frameworkRunActive;
            HandshakeWriter.Write(
                SessionState.GetString(KeySession, ""),
                HandshakeWriter.CurrentState(busy),
                active);
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

                // Tombstones survive editor restarts, unlike SessionState, and
                // permanently seal transport failures/canceled requests.
                if (File.Exists(BridgePaths.TombstonePath(runId)))
                    continue;

                if (File.Exists(BridgePaths.CancelPath(runId)))
                {
                    string reason = ReadCancellationMarkerReason(runId);
                    WriteTombstone(runId,
                        string.IsNullOrEmpty(reason)
                            ? "request was canceled before the Unity bridge claimed it"
                            : reason);
                    continue;
                }

                var req = ReadRequest(file);
                if (req == null)
                    continue;

                if (req.bridge_protocol_version != BridgeProtocol.Version)
                {
                    TryWriteTerminal(runId, BridgeProtocol.OutcomeRejected, false, 0,
                        $"protocol version mismatch (request={req.bridge_protocol_version}, bridge={BridgeProtocol.Version})");
                    continue;
                }

                string currentSession = SessionState.GetString(KeySession, "");
                if (!BridgeProtocol.IsRequestForSession(req, currentSession))
                {
                    WriteTombstone(runId,
                        $"request belongs to bridge session '{req.bridge_session_id ?? ""}', current session is '{currentSession}'");
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
            string phase = SessionState.GetString(KeyPhase, PhaseNew);
            if (File.Exists(BridgePaths.ResponsePath(runId)))
            {
                DetachController();
                ClearActive();
                return;
            }

            // The Go context owns timeout/interrupt exit semantics. Unity owns
            // stopping any job it launched and sealing the request against
            // replay, so a cancel marker always enters the cancelling phase.
            if (File.Exists(BridgePaths.CancelPath(runId)) && phase != PhaseCancelling)
            {
                BeginCancellation("request was canceled by the Go client", false, true, phase == PhaseRunning);
                if (SessionState.GetString(KeyActive, "") == runId &&
                    SessionState.GetString(KeyPhase, "") == PhaseCancelling)
                    AdvanceCancelling(runId);
                return;
            }

            string reqPath = BridgePaths.RequestPath(runId);
            var req = ReadRequest(reqPath);
            if (req == null)
            {
                if (phase != PhaseCancelling)
                    BeginCancellation("claimed request disappeared before completion", false, true, phase == PhaseRunning);
                if (SessionState.GetString(KeyActive, "") == runId &&
                    SessionState.GetString(KeyPhase, "") == PhaseCancelling)
                    AdvanceCancelling(runId);
                return;
            }

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
                case PhaseCancelling:
                    AdvanceCancelling(runId);
                    break;
                default:
                    BeginCancellation($"unknown bridge phase '{phase}'", true, true, true);
                    break;
            }
        }

        private static void AdvanceNew(RequestDto req)
        {
            var gate = PristineGate.Evaluate(req);
            switch (gate.Decision)
            {
                case GateDecision.Refuse:
                    FinishTerminal(req.run_id, BridgeProtocol.OutcomeRejected, false, 0, gate.RefuseReason);
                    return;
                case GateDecision.Wait:
                    if (DeadlinePassed())
                        FinishTerminal(req.run_id, BridgeProtocol.OutcomeBusy, false, 0, "editor did not become idle in time");
                    return;
                case GateDecision.Proceed:
                    SessionState.SetString(KeyNonPristine, string.Join("\n", gate.NonPristine));
                    new StatusStream(req.status_ndjson).Compiling();
                    SessionState.SetString(KeyPhase, PhaseRefreshing);
                    // Clear any compile errors collected before this run so the
                    // sidecar reflects ONLY this run's recompile — otherwise a
                    // stale error from a prior (changed) compile would be reported
                    // alongside, or instead of, the current one.
                    CompileErrorSidecar.Clear();
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
                    FinishTerminal(req.run_id, BridgeProtocol.OutcomeBusy, false, 0, "compilation did not settle in time");
                return; // still settling; re-check next tick (survives reloads)
            }

            // Compilation settled. A failed compile does not reload the domain,
            // so any captured errors are still present.
            if (CompileErrorSidecar.HasErrors)
            {
                CompileErrorSidecar.Write(req.compile_errors_json);
                FinishTerminal(req.run_id, BridgeProtocol.OutcomeCompileFailed, true, CompileErrorSidecar.Count, null);
                return;
            }

            // Sidecar is empty but the editor's last compilation FAILED: the
            // loaded domain still runs stale last-good assemblies (the broken
            // state predates this request, and the Refresh was a no-op). Running
            // warm here would return green where cold exits 2 — reject so the
            // cold path produces the authoritative compile errors.
            if (EditorUtility.scriptCompilationFailed)
            {
                FinishTerminal(req.run_id, BridgeProtocol.OutcomeRejected, false, 0,
                    "script compilation is failing in this editor (pre-existing broken state); cold run will report the compile errors");
                return;
            }

            // A late transition into Play Mode invalidates equivalence.
            if (EditorApplication.isPlaying || EditorApplication.isPlayingOrWillChangePlaymode)
            {
                FinishTerminal(req.run_id, BridgeProtocol.OutcomeRejected, false, 0, "editor entered Play Mode");
                return;
            }

            // Gate parity: a foreign run started during the settle window is as
            // disqualifying as one caught at gate time — starting ours now would
            // interleave global callbacks with the human's run.
            if (!RunActivityMonitor.TryGetIsRunActive(out bool runActive))
            {
                FinishTerminal(req.run_id, BridgeProtocol.OutcomeRejected, false, 0,
                    "cannot verify Test Framework active-run state after compilation");
                return;
            }

            if (runActive)
            {
                FinishTerminal(req.run_id, BridgeProtocol.OutcomeRejected, false, 0,
                    "another test run started while compilation was settling");
                return;
            }

            StartRun(req);
        }

        private static void AdvanceRunning(RequestDto req)
        {
            // The controller writes the completed response on RunFinished. If the
            // controller is gone (domain reloaded mid-run), the run is orphaned.
            if (s_controller == null)
                BeginCancellation("run controller disappeared before the owned job completed", true, false, true);
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

            try
            {
                s_api = ScriptableObject.CreateInstance<TestRunnerApi>();
                s_controller = new TestRunController(
                    req,
                    session,
                    nonPristine,
                    stream,
                    reason => BeginCancellation(reason, false, false, true),
                    OnRunCompleted,
                    OnRunPersistenceFailure);
                s_api.RegisterCallbacks(s_controller);

                s_runningTests = true;
                SessionState.SetString(KeyPhase, PhaseRunning);
                string ownedGuid = TestRunnerApiCompat.ExecuteOwned(s_api, new ExecutionSettings(filter));
                // A tiny test run may finish synchronously inside Execute.
                // Its callback already wrote the response and cleared ownership.
                if (s_controller == null || SessionState.GetString(KeyActive, "") != req.run_id)
                    return;
                SessionState.SetString(KeyOwnedRunGuid, ownedGuid);
                s_controller.SetOwnedRunGuid(ownedGuid);
            }
            catch (Exception e)
            {
                string reason = $"failed to start an owned TestRunnerApi run: {e.Message}";
                // Execute may register a job before throwing and before its GUID
                // is returned. Never publish terminal merely because the GUID is
                // empty; use global authoritative inactivity after a grace.
                BeginCancellation(reason, true, false, true);
            }
        }

        private static void OnRunCompleted()
        {
            DetachController();
            ClearActive();
        }

        private static void OnRunPersistenceFailure(string reason)
        {
            string runId = SessionState.GetString(KeyActive, "");
            DetachController();
            if (!string.IsNullOrEmpty(runId))
                FinishTerminal(runId, BridgeProtocol.OutcomeRejected, false, 0, reason);
        }

        private static void DetachController()
        {
            s_runningTests = false;
            // Callbacks are global: left registered, this controller would keep
            // receiving future runs' events (including human Test Runner runs)
            // and rewrite this run's results.xml/response with foreign results.
            if (s_api != null && s_controller != null)
                s_api.UnregisterCallbacks(s_controller);
            if (s_api != null)
                UnityEngine.Object.DestroyImmediate(s_api);
            s_controller = null;
            s_api = null;
            CompileErrorSidecar.Clear(); // next run starts from a clean error slate
        }

        // ── helpers ─────────────────────────────────────────────────────────

        private static void BeginCancellation(
            string reason,
            bool waitForReloadGrace,
            bool tombstoneOnly,
            bool requireActivityProbe)
        {
            string runId = SessionState.GetString(KeyActive, "");
            if (string.IsNullOrEmpty(runId))
                return;

            // CancelTestRun may synchronously emit RunFinished. Seal the global
            // callback receiver before any cancellation attempt so a canceled
            // request can never publish a completed response during that call.
            s_controller?.SealForCancellation();

            if (SessionState.GetString(KeyPhase, "") == PhaseCancelling)
            {
                if (tombstoneOnly)
                    SessionState.SetString(KeyCancelTombstoneOnly, "1");
                if (requireActivityProbe)
                    SessionState.SetString(KeyCancelRequireProbe, "1");
                return;
            }

            long now = DateTime.UtcNow.Ticks;
            long notBefore = waitForReloadGrace
                ? DateTime.UtcNow.AddMilliseconds(DomainReloadCancelGraceMs).Ticks
                : now;
            SessionState.SetString(KeyPhase, PhaseCancelling);
            SessionState.SetString(KeyCancelReason, string.IsNullOrEmpty(reason) ? "warm run cancellation requested" : reason);
            SessionState.SetString(KeyCancelNotBefore, notBefore.ToString(CultureInfo.InvariantCulture));
            SessionState.SetString(KeyCancelNextProbe, "0");
            SessionState.SetString(KeyCancelInactiveCount, "0");
            SessionState.SetString(KeyCancelTombstoneOnly, tombstoneOnly ? "1" : "0");
            SessionState.SetString(KeyCancelRequireProbe, requireActivityProbe ? "1" : "0");
            EnsureCancellationMarker(runId, reason);

            // Callback ambiguity and client cancellation begin cancellation
            // immediately. Reload recovery alone waits for the framework to
            // restore its run registry before the first cancellation attempt.
            if (!waitForReloadGrace)
            {
                string guid = SessionState.GetString(KeyOwnedRunGuid, "");
                if (!string.IsNullOrEmpty(guid))
                    TestRunnerApiCompat.TryCancel(guid);
            }
        }

        private static void AdvanceCancelling(string runId)
        {
            string reason = SessionState.GetString(KeyCancelReason, "warm run cancellation requested");
            if (!EnsureCancellationMarker(runId, reason))
                return;

            // Stop receiving the global callback stream while cancellation is
            // pending. The persisted GUID remains the sole ownership key.
            DetachController();

            bool tombstoneOnly = SessionState.GetString(KeyCancelTombstoneOnly, "0") == "1";
            bool requireProbe = SessionState.GetString(KeyCancelRequireProbe, "0") == "1";
            string guid = SessionState.GetString(KeyOwnedRunGuid, "");

            // New/refreshing requests have no Test Framework job to prove
            // inactive. Seal a Go-canceled request without a late response;
            // its context already owns exit 4/8.
            if (string.IsNullOrEmpty(guid) && !requireProbe)
            {
                if (tombstoneOnly)
                {
                    if (WriteTombstone(runId, reason))
                        ClearActive();
                }
                else
                {
                    FinishTerminal(runId, BridgeProtocol.OutcomeRejected, false, 0, reason);
                }
                return;
            }

            long now = DateTime.UtcNow.Ticks;
            long notBefore = ReadSessionLong(KeyCancelNotBefore);
            long nextProbe = ReadSessionLong(KeyCancelNextProbe);
            if (now < notBefore || now < nextProbe)
                return;

            SessionState.SetString(
                KeyCancelNextProbe,
                DateTime.UtcNow.AddMilliseconds(CancelProbeIntervalMs).Ticks.ToString(CultureInfo.InvariantCulture));

            // If Execute failed before returning a GUID, a job may still have
            // registered. In that case the global probe is the only
            // authoritative evidence available; unknown stays busy forever.
            bool isActive;
            bool known = string.IsNullOrEmpty(guid)
                ? RunActivityMonitor.TryGetIsRunActive(out isActive)
                : TestRunnerApiCompat.TryGetRunActive(guid, out isActive);
            int inactiveCount = ReadSessionInt(KeyCancelInactiveCount);
            var decision = CancellationStateMachine.Evaluate(now >= notBefore, known, isActive, inactiveCount);
            SessionState.SetString(
                KeyCancelInactiveCount,
                decision.InactiveConfirmations.ToString(CultureInfo.InvariantCulture));

            if (!known || decision.Action == CancellationAction.RetryCancel)
            {
                // Retrying against the persisted owned GUID is safe. Without a
                // GUID we can only stay busy until the global probe confirms
                // inactivity; never cancel an unrelated global run.
                if (!string.IsNullOrEmpty(guid))
                    TestRunnerApiCompat.TryCancel(guid);
                return;
            }

            if (decision.Action != CancellationAction.Complete)
                return;

            if (tombstoneOnly)
            {
                if (WriteTombstone(runId, reason))
                    ClearActive();
                return;
            }

            FinishTerminal(runId, BridgeProtocol.OutcomeRejected, false, 0, reason);
        }

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

        private static void FinishTerminal(string runId, string outcome, bool compileFailed, int errorCount, string disclosure)
        {
            if (TryWriteTerminal(runId, outcome, compileFailed, errorCount, disclosure))
            {
                ClearActive();
                return;
            }

            // Neither response nor tombstone was durable. Keep the request
            // claimed and retry the tombstone instead of replaying work.
            BeginCancellation(
                string.IsNullOrEmpty(disclosure)
                    ? $"terminal {outcome} result could not be persisted"
                    : disclosure,
                false,
                true,
                false);
        }

        private static bool TryWriteTerminal(string runId, string outcome, bool compileFailed, int errorCount, string disclosure)
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
            try
            {
                AtomicFile.WriteAllText(BridgePaths.ResponsePath(runId), JsonUtility.ToJson(resp, true));
                return true;
            }
            catch (Exception e)
            {
                string reason = $"terminal response for intended outcome '{outcome}' could not be persisted: {e.Message}";
                Debug.LogError($"[TestPlay.Bridge] {reason}; writing durable request tombstone");
                return WriteTombstone(runId, reason);
            }
        }

        private static bool WriteTombstone(string runId, string reason)
        {
            if (string.IsNullOrEmpty(runId))
                return false;

            string path = BridgePaths.TombstonePath(runId);
            if (File.Exists(path))
                return true;

            var tombstone = new TombstoneDto
            {
                run_id = runId,
                reason = string.IsNullOrEmpty(reason) ? "bridge transport failed" : reason,
                created_at = DateTime.UtcNow.ToString("yyyy-MM-ddTHH:mm:ssZ", CultureInfo.InvariantCulture),
            };
            try
            {
                AtomicFile.WriteAllText(path, JsonUtility.ToJson(tombstone, true));
                return true;
            }
            catch (Exception e)
            {
                Debug.LogError($"[TestPlay.Bridge] durable tombstone for {runId} could not be written; bridge remains busy: {e}");
                return false;
            }
        }

        private static bool EnsureCancellationMarker(string runId, string reason)
        {
            string path = BridgePaths.CancelPath(runId);
            if (File.Exists(path))
                return true;

            var marker = new CancellationMarkerDto
            {
                run_id = runId,
                owned_run_guid = SessionState.GetString(KeyOwnedRunGuid, ""),
                reason = string.IsNullOrEmpty(reason) ? "warm run cancellation requested" : reason,
                created_at = DateTime.UtcNow.ToString("yyyy-MM-ddTHH:mm:ssZ", CultureInfo.InvariantCulture),
            };
            try
            {
                AtomicFile.WriteAllText(path, JsonUtility.ToJson(marker, true));
                return true;
            }
            catch (Exception e)
            {
                Debug.LogError($"[TestPlay.Bridge] durable cancellation marker for {runId} could not be written; bridge remains busy: {e}");
                return false;
            }
        }

        private static string ReadCancellationMarkerReason(string runId)
        {
            try
            {
                string json = File.ReadAllText(BridgePaths.CancelPath(runId));
                var marker = JsonUtility.FromJson<CancellationMarkerDto>(json);
                return marker != null && marker.run_id == runId ? marker.reason : null;
            }
            catch
            {
                return null; // Go-owned markers are intentionally plain text.
            }
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
            SessionState.SetString(KeyOwnedRunGuid, "");
            SessionState.SetString(KeyCancelReason, "");
            SessionState.SetString(KeyCancelNotBefore, "");
            SessionState.SetString(KeyCancelNextProbe, "");
            SessionState.SetString(KeyCancelInactiveCount, "");
            SessionState.SetString(KeyCancelTombstoneOnly, "");
            SessionState.SetString(KeyCancelRequireProbe, "");
        }

        private static long ReadSessionLong(string key)
        {
            string raw = SessionState.GetString(key, "0");
            return long.TryParse(raw, NumberStyles.Integer, CultureInfo.InvariantCulture, out long value) ? value : 0;
        }

        private static int ReadSessionInt(string key)
        {
            string raw = SessionState.GetString(key, "0");
            return int.TryParse(raw, NumberStyles.Integer, CultureInfo.InvariantCulture, out int value) ? value : 0;
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

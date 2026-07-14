using System;
using System.Globalization;
using UnityEditor.TestTools.TestRunner.Api;
using UnityEngine;

namespace TestPlay.Bridge
{
    /// <summary>
    /// Drives one owned warm run. TestRunnerApi callbacks are global, so this
    /// controller accepts a successful completion only when its callback stream
    /// contains exactly one RunStarted. A second start makes the stream
    /// ambiguous and forces a cold fallback instead of guessing ownership.
    /// </summary>
    internal sealed class TestRunController : IErrorCallbacks
    {
        private readonly RequestDto _req;
        private readonly string _sessionId;
        private readonly string[] _nonPristine;
        private readonly StatusStream _stream;
        private readonly Action<string> _beginCancellation;
        private readonly Action _onCompleted;
        private readonly Action<string> _onPersistenceFailure;
        private readonly RunOwnershipTracker _ownership = new RunOwnershipTracker();
        private int _total;
        private bool _done;

        public string OwnedRunGuid { get; private set; } = "";

        public TestRunController(
            RequestDto req,
            string sessionId,
            string[] nonPristine,
            StatusStream stream,
            Action<string> beginCancellation,
            Action onCompleted,
            Action<string> onPersistenceFailure)
        {
            _req = req;
            _sessionId = sessionId;
            _nonPristine = nonPristine ?? new string[0];
            _stream = stream;
            _beginCancellation = beginCancellation;
            _onCompleted = onCompleted;
            _onPersistenceFailure = onPersistenceFailure;
        }

        public void SetOwnedRunGuid(string guid)
        {
            if (string.IsNullOrEmpty(guid))
                throw new ArgumentException("owned run GUID must not be empty", nameof(guid));
            OwnedRunGuid = guid;
        }

        /// <summary>
        /// Permanently closes the callback gate before an external cancellation
        /// can synchronously emit RunFinished. Once sealed, this controller can
        /// never publish a completed response for the canceled request.
        /// </summary>
        internal void SealForCancellation()
        {
            _done = true;
        }

        public void RunStarted(ITestAdaptor testsToRun)
        {
            if (_done)
                return;

            _ownership.ObserveRunStarted();
            if (!_ownership.CanAcceptEvents)
            {
                BeginCancellation(
                    $"ambiguous TestRunnerApi callback stream (observed {_ownership.StartedCount} run starts); cold fallback required");
                return;
            }

            _total = testsToRun != null ? testsToRun.TestCaseCount : 0;
            _stream.Running(_total);
        }

        public void TestStarted(ITestAdaptor test)
        {
            if (_done || !_ownership.CanAcceptEvents)
                return;
            if (test != null && !test.IsSuite)
                _stream.CurrentTest(test.FullName, _total);
        }

        public void TestFinished(ITestResultAdaptor result)
        {
            // Per-case counts are derived from the final NUnit XML.
        }

        public void RunFinished(ITestResultAdaptor result)
        {
            if (_done)
                return;

            if (!_ownership.CanAcceptCompletion)
            {
                BeginCancellation(
                    $"ambiguous TestRunnerApi callback stream (observed {_ownership.StartedCount} run starts); cold fallback required");
                return;
            }

            _done = true;
            try
            {
                ResultXmlWriter.Write(_req.results_xml, result);

                var resp = new ResponseDto
                {
                    run_id = _req.run_id,
                    bridge_session_id = _sessionId,
                    outcome = BridgeProtocol.OutcomeCompleted,
                    results_xml_written = true,
                    compile_failed = false,
                    compile_error_count = 0,
                    non_pristine = _nonPristine,
                    finished_at = DateTime.UtcNow.ToString("yyyy-MM-ddTHH:mm:ssZ", CultureInfo.InvariantCulture),
                };
                AtomicFile.WriteAllText(BridgePaths.ResponsePath(_req.run_id), JsonUtility.ToJson(resp, true));
            }
            catch (Exception e)
            {
                Debug.LogError($"[TestPlay.Bridge] failed to write run results for {_req.run_id}: {e}");
                _onPersistenceFailure?.Invoke($"failed to persist owned warm-run result: {e.Message}");
                return;
            }

            _onCompleted?.Invoke();
        }

        public void OnError(string message)
        {
            if (_done)
                return;

            BeginCancellation(
                string.IsNullOrEmpty(message)
                    ? "TestRunnerApi reported a run-start failure"
                    : $"TestRunnerApi run-start failure: {message}");
        }

        private void BeginCancellation(string reason)
        {
            if (_done)
                return;

            _done = true;
            _beginCancellation?.Invoke(reason);
        }
    }
}

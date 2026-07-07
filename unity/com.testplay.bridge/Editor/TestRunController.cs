using System;
using System.Globalization;
using UnityEditor.TestTools.TestRunner.Api;
using UnityEngine;

namespace TestPlay.Bridge
{
    /// <summary>
    /// Drives a single warm test run via the TestRunnerApi and reports results.
    /// Streams running/current-test progress over status.ndjson; on RunFinished
    /// it writes results.xml (NUnit3) and the completed response, then signals
    /// the server. Cancellation is cooperative/best-effort — the Go side owns the
    /// authoritative timeout/interrupt classification regardless of whether the
    /// underlying run actually stops.
    /// </summary>
    internal sealed class TestRunController : ICallbacks
    {
        private readonly RequestDto _req;
        private readonly string _sessionId;
        private readonly string[] _nonPristine;
        private readonly StatusStream _stream;
        private readonly Action _onFinished;
        private int _total;
        private bool _done;

        public TestRunController(RequestDto req, string sessionId, string[] nonPristine, StatusStream stream, Action onFinished)
        {
            _req = req;
            _sessionId = sessionId;
            _nonPristine = nonPristine ?? new string[0];
            _stream = stream;
            _onFinished = onFinished;
        }

        public void RunStarted(ITestAdaptor testsToRun)
        {
            _total = testsToRun != null ? testsToRun.TestCaseCount : 0;
            _stream.Running(_total);
        }

        public void TestStarted(ITestAdaptor test)
        {
            if (test != null && !test.IsSuite)
                _stream.CurrentTest(test.FullName, _total);
        }

        public void TestFinished(ITestResultAdaptor result)
        {
            // Per-case running counts are derivable from the final XML; nothing
            // to stream here. Kept for interface completeness.
        }

        public void RunFinished(ITestResultAdaptor result)
        {
            // Callbacks are global to the test runner; a leaked or late event
            // from a subsequent run must never rewrite this run's outcome.
            if (_done)
                return;
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
            }
            finally
            {
                _onFinished?.Invoke();
            }
        }
    }
}

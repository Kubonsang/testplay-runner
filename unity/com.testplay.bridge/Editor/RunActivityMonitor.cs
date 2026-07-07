using UnityEditor.TestTools.TestRunner.Api;
using UnityEngine;

namespace TestPlay.Bridge
{
    /// <summary>
    /// Tracks whether ANY test run is in flight in this editor — including runs
    /// a human starts from the Test Runner window. TestRunnerApi callbacks are
    /// global (every registered ICallbacks receives every run's events), so
    /// without this signal the bridge could claim a request while a foreign run
    /// is active and capture that run's results as its own. The Pristine Gate
    /// refuses while IsRunActive, and the handshake reports busy.
    ///
    /// The flag is in-memory only: a domain reload both wipes it and (for the
    /// bridge) orphans any in-flight run, so a stale sticky flag cannot occur.
    /// </summary>
    internal static class RunActivityMonitor
    {
        private static bool s_hooked;
        private static TestRunnerApi s_api;
        private static Callbacks s_callbacks;

        public static bool IsRunActive { get; private set; }

        public static void Hook()
        {
            if (s_hooked)
                return;
            s_hooked = true;
            s_api = ScriptableObject.CreateInstance<TestRunnerApi>();
            s_callbacks = new Callbacks();
            s_api.RegisterCallbacks(s_callbacks);
        }

        private sealed class Callbacks : ICallbacks
        {
            public void RunStarted(ITestAdaptor testsToRun) { IsRunActive = true; }
            public void RunFinished(ITestResultAdaptor result) { IsRunActive = false; }
            public void TestStarted(ITestAdaptor test) { }
            public void TestFinished(ITestResultAdaptor result) { }
        }
    }
}

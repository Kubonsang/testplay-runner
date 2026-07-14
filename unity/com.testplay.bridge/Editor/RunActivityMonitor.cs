namespace TestPlay.Bridge
{
    /// <summary>
    /// Queries Test Framework's persisted job registry so an active or resumed
    /// run is never mistaken for idle. Callback counters are intentionally not
    /// used: some framework error paths omit RunFinished and would leave an
    /// in-memory counter stuck until the editor restarts.
    /// </summary>
    internal static class RunActivityMonitor
    {
        /// <summary>
        /// Returns true only when Test Framework can authoritatively report its
        /// persisted active-job state. An unknown probe is deliberately not
        /// converted into "idle".
        /// </summary>
        public static bool TryGetIsRunActive(out bool isActive)
        {
            if (!TestRunnerApiCompat.TryGetAnyRunActive(out isActive))
            {
                isActive = true;
                return false;
            }
            return true;
        }
    }
}

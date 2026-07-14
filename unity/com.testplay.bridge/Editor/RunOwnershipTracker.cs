namespace TestPlay.Bridge
{
    /// <summary>
    /// A callback stream is trustworthy only when exactly one RunStarted was
    /// observed after the bridge proved the editor idle and before completion.
    /// Any additional start means global callbacks are interleaved.
    /// </summary>
    internal sealed class RunOwnershipTracker
    {
        public int StartedCount { get; private set; }
        public bool IsAmbiguous => StartedCount > 1;
        public bool CanAcceptEvents => StartedCount == 1 && !IsAmbiguous;
        public bool CanAcceptCompletion => CanAcceptEvents;

        public void ObserveRunStarted()
        {
            StartedCount++;
        }
    }
}

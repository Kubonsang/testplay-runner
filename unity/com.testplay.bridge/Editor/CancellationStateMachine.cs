namespace TestPlay.Bridge
{
    internal enum CancellationAction
    {
        Wait,
        RetryCancel,
        ObserveInactive,
        Complete,
    }

    internal readonly struct CancellationDecision
    {
        public CancellationDecision(CancellationAction action, int inactiveConfirmations)
        {
            Action = action;
            InactiveConfirmations = inactiveConfirmations;
        }

        public CancellationAction Action { get; }
        public int InactiveConfirmations { get; }
    }

    /// <summary>
    /// Pure cancellation transition logic. A cancel never becomes terminal on
    /// an unknown probe, and two authoritative inactive observations are
    /// required after the reload grace period before ownership is released.
    /// </summary>
    internal static class CancellationStateMachine
    {
        public const int RequiredInactiveConfirmations = 2;

        public static CancellationDecision Evaluate(
            bool graceElapsed,
            bool activityKnown,
            bool isActive,
            int inactiveConfirmations)
        {
            if (!graceElapsed || !activityKnown)
                return new CancellationDecision(CancellationAction.Wait, 0);

            if (isActive)
                return new CancellationDecision(CancellationAction.RetryCancel, 0);

            int next = inactiveConfirmations + 1;
            return next >= RequiredInactiveConfirmations
                ? new CancellationDecision(CancellationAction.Complete, next)
                : new CancellationDecision(CancellationAction.ObserveInactive, next);
        }
    }
}

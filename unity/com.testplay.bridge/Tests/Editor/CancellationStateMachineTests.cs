using NUnit.Framework;

namespace TestPlay.Bridge.Tests
{
    public sealed class CancellationStateMachineTests
    {
        [Test]
        public void BeforeGraceOrUnknown_RemainsBusy()
        {
            var beforeGrace = CancellationStateMachine.Evaluate(false, true, false, 1);
            var unknown = CancellationStateMachine.Evaluate(true, false, false, 1);

            Assert.That(beforeGrace.Action, Is.EqualTo(CancellationAction.Wait));
            Assert.That(unknown.Action, Is.EqualTo(CancellationAction.Wait));
            Assert.That(beforeGrace.InactiveConfirmations, Is.Zero);
            Assert.That(unknown.InactiveConfirmations, Is.Zero);
        }

        [Test]
        public void ActiveRun_RequestsCancelAndResetsInactiveEvidence()
        {
            var decision = CancellationStateMachine.Evaluate(true, true, true, 1);

            Assert.That(decision.Action, Is.EqualTo(CancellationAction.RetryCancel));
            Assert.That(decision.InactiveConfirmations, Is.Zero);
        }

        [Test]
        public void TwoAuthoritativeInactiveObservations_CompleteCancellation()
        {
            var first = CancellationStateMachine.Evaluate(true, true, false, 0);
            var second = CancellationStateMachine.Evaluate(true, true, false, first.InactiveConfirmations);

            Assert.That(first.Action, Is.EqualTo(CancellationAction.ObserveInactive));
            Assert.That(second.Action, Is.EqualTo(CancellationAction.Complete));
        }
    }
}

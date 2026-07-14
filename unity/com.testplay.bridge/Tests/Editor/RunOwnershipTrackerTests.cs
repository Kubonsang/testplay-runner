using NUnit.Framework;

namespace TestPlay.Bridge.Tests
{
    public sealed class RunOwnershipTrackerTests
    {
        [Test]
        public void NoRunStart_CannotAcceptCompletion()
        {
            var tracker = new RunOwnershipTracker();

            Assert.That(tracker.CanAcceptCompletion, Is.False);
        }

        [Test]
        public void ExactlyOneRunStart_CanAcceptEventsAndCompletion()
        {
            var tracker = new RunOwnershipTracker();

            tracker.ObserveRunStarted();

            Assert.That(tracker.StartedCount, Is.EqualTo(1));
            Assert.That(tracker.CanAcceptEvents, Is.True);
            Assert.That(tracker.CanAcceptCompletion, Is.True);
        }

        [Test]
        public void SecondRunStart_PermanentlyMarksStreamAmbiguous()
        {
            var tracker = new RunOwnershipTracker();
            tracker.ObserveRunStarted();
            tracker.ObserveRunStarted();

            Assert.That(tracker.StartedCount, Is.EqualTo(2));
            Assert.That(tracker.IsAmbiguous, Is.True);
            Assert.That(tracker.CanAcceptEvents, Is.False);
            Assert.That(tracker.CanAcceptCompletion, Is.False);
        }

        [Test]
        public void FrameworkActivityProbe_IsAvailableInSupportedTestFramework()
        {
            Assert.That(TestRunnerApiCompat.TryGetAnyRunActive(out _), Is.True,
                "Unknown activity must disable the warm bridge, so supported environments need an authoritative probe.");
        }

        [Test]
        public void OwnedRunActivityProbe_IsAvailableInSupportedTestFramework()
        {
            Assert.That(TestRunnerApiCompat.TryGetRunActive("testplay-nonexistent-probe-guid", out bool isActive), Is.True,
                "Cancellation needs an authoritative per-GUID inactive observation.");
            Assert.That(isActive, Is.False);
        }

        [Test]
        public void SecondRunStarted_BeginsCancellationImmediately()
        {
            int cancellationCount = 0;
            string cancellationReason = null;
            var controller = new TestRunController(
                new RequestDto { run_id = "ownership-test" },
                "session",
                new string[0],
                new StatusStream(""),
                reason =>
                {
                    cancellationCount++;
                    cancellationReason = reason;
                },
                () => { },
                _ => { });

            controller.RunStarted(null);
            controller.RunStarted(null);

            Assert.That(cancellationCount, Is.EqualTo(1));
            Assert.That(cancellationReason, Does.Contain("2 run starts"));
        }
    }
}

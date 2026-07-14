using NUnit.Framework;

namespace TestPlay.Bridge.Tests
{
    public sealed class TestRunControllerTests
    {
        [Test]
        public void SealedControllerIgnoresSynchronousRunFinished()
        {
            int completed = 0;
            int persistenceFailures = 0;
            var controller = new TestRunController(
                new RequestDto { run_id = "run-a" },
                "session-a",
                new string[0],
                null,
                _ => { },
                () => completed++,
                _ => persistenceFailures++);

            controller.SealForCancellation();
            controller.RunFinished(null);

            Assert.That(completed, Is.Zero);
            Assert.That(persistenceFailures, Is.Zero);
        }
    }
}

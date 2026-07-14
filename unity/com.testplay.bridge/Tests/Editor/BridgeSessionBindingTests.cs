using NUnit.Framework;

namespace TestPlay.Bridge.Tests
{
    public sealed class BridgeSessionBindingTests
    {
        [Test]
        public void RequestMatchesOnlyTheExpectedEditorSession()
        {
            var request = new RequestDto { bridge_session_id = "session-a" };

            Assert.That(BridgeProtocol.IsRequestForSession(request, "session-a"), Is.True);
            Assert.That(BridgeProtocol.IsRequestForSession(request, "session-b"), Is.False);
            Assert.That(BridgeProtocol.IsRequestForSession(request, ""), Is.False);
            Assert.That(BridgeProtocol.IsRequestForSession(null, "session-a"), Is.False);
        }
    }
}

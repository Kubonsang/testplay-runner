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

        [Test]
        public void Protocol3RequestMatchesOnlyExactWorkspacePidAndSession()
        {
            var request = new RequestDto
            {
                bridge_session_id = "session-a",
                workspace_id = "workspace-a",
                editor_pid = 4242,
            };

            Assert.That(BridgeProtocol.IsRequestForEditor(request, "session-a", "workspace-a", 4242), Is.True);
            Assert.That(BridgeProtocol.IsRequestForEditor(request, "session-b", "workspace-a", 4242), Is.False);
            Assert.That(BridgeProtocol.IsRequestForEditor(request, "session-a", "workspace-b", 4242), Is.False);
            Assert.That(BridgeProtocol.IsRequestForEditor(request, "session-a", "workspace-a", 4243), Is.False);
            Assert.That(BridgeProtocol.IsRequestForEditor(request, "session-a", "workspace-a", 0), Is.False);
        }

        [Test]
		public void CancellationMatchesExactRequestIdentity()
		{
			var request = new RequestDto
			{
				run_id = "run-a", bridge_session_id = "session-a", workspace_id = "workspace-a", editor_pid = 4242,
			};
			var marker = new CancellationMarkerDto
			{
				run_id = request.run_id, bridge_session_id = request.bridge_session_id,
				workspace_id = request.workspace_id, editor_pid = request.editor_pid,
			};
			Assert.That(BridgeProtocol.IsCancellationForRequest(marker, request), Is.True);
			marker.workspace_id = "foreign";
			Assert.That(BridgeProtocol.IsCancellationForRequest(marker, request), Is.False);
		}
    }
}

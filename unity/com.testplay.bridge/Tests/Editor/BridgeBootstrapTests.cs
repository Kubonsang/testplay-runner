using NUnit.Framework;

namespace TestPlay.Bridge.Tests
{
    public class BridgeBootstrapTests
    {
        [Test]
        public void HoneyBeeWorkspaceIdentityActivatesBridge()
        {
            Assert.That(BridgeBootstrap.EnvironmentOptedIn(null, "workspace-1"), Is.True);
            Assert.That(BridgeBootstrap.EnvironmentOptedIn(null, ""), Is.False);
            Assert.That(BridgeBootstrap.EnvironmentOptedIn("1", ""), Is.True);
        }
    }
}

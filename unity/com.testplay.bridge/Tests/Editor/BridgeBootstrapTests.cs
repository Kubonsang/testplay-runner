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

        [Test]
        public void BatchmodeActivationStillRequiresExplicitOptIn()
        {
            // BridgeBootstrap intentionally uses the same explicit opt-in gate
            // in interactive and batchmode Editors. A plain cold batchmode run
            // therefore remains dormant, while a harness-owned workspace can
            // start protocol 3 headlessly.
            Assert.That(BridgeBootstrap.EnvironmentOptedIn(null, null), Is.False);
            Assert.That(BridgeBootstrap.EnvironmentOptedIn("0", ""), Is.False);
            Assert.That(BridgeBootstrap.EnvironmentOptedIn(null, "honeybee-workspace"), Is.True);
        }
    }
}

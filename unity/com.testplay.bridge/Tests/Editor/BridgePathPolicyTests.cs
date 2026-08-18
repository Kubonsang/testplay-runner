using System.IO;
using NUnit.Framework;

namespace TestPlay.Bridge.Tests
{
    public class BridgePathPolicyTests
    {
        [Test]
        public void RejectsReparseEntryInsideBridgeRuntimePath()
        {
			string root = Path.Combine(Path.GetTempPath(), "testplay-project-" + System.Guid.NewGuid().ToString("N"));
            string bridge = Path.Combine(root, ".testplay", "bridge");
			Directory.CreateDirectory(bridge);
			try
			{
            bool ok = BridgePathPolicy.Validate(
                root,
                bridge,
                path => path.EndsWith(".testplay") ? FileAttributes.Directory | FileAttributes.ReparsePoint : FileAttributes.Directory,
                out string reason);
            Assert.That(ok, Is.False);
            StringAssert.Contains("reparse point", reason);
			}
			finally
			{
				Directory.Delete(root, true);
			}
        }

        [Test]
        public void AcceptsRealComponentsInsideProject()
        {
			string root = Path.Combine(Path.GetTempPath(), "testplay-project-" + System.Guid.NewGuid().ToString("N"));
            string bridge = Path.Combine(root, ".testplay", "bridge");
			Directory.CreateDirectory(bridge);
			try
			{
            bool ok = BridgePathPolicy.Validate(root, bridge, _ => FileAttributes.Directory, out string reason);
            Assert.That(ok, Is.True, reason);
			}
			finally
			{
				Directory.Delete(root, true);
			}
        }
    }
}

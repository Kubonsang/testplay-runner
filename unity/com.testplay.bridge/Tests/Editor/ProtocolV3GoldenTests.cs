using System.IO;
using NUnit.Framework;
using UnityEngine;

namespace TestPlay.Bridge.Tests
{
    public class ProtocolV3GoldenTests
    {
        [Test]
        public void GoldenRequest_UsesExactProtocol3FieldNamesAndEnums()
        {
            string path = Path.GetFullPath(
                "Packages/com.testplay.bridge/Tests/Editor/TestData/protocol-v3-request.json");
            Assert.That(File.Exists(path), Is.True, path);

            var request = JsonUtility.FromJson<RequestDto>(File.ReadAllText(path));
            Assert.That(request.bridge_protocol_version, Is.EqualTo(BridgeProtocol.Version));
            Assert.That(request.bridge_protocol_version, Is.EqualTo(3));
            Assert.That(request.workspace_id, Is.EqualTo("workspace-golden"));
            Assert.That(request.editor_pid, Is.EqualTo(4242));
            Assert.That(request.bridge_session_id, Is.EqualTo("session-golden"));
            Assert.That(request.capability_kind, Is.EqualTo(BridgeProtocol.CapabilityWarmTest));

            string roundTrip = JsonUtility.ToJson(request);
            StringAssert.Contains("\"workspace_id\"", roundTrip);
            StringAssert.Contains("\"editor_pid\"", roundTrip);
            StringAssert.Contains("\"bridge_session_id\"", roundTrip);
            StringAssert.Contains("\"capability_kind\"", roundTrip);
        }

        [Test]
        public void CapabilityKinds_AreLimitedToCompileAndWarmTest()
        {
            Assert.That(BridgeProtocol.IsKnownCapability(new RequestDto
            {
                capability_kind = BridgeProtocol.CapabilityCompile,
            }), Is.True);
            Assert.That(BridgeProtocol.IsKnownCapability(new RequestDto
            {
                capability_kind = BridgeProtocol.CapabilityWarmTest,
            }), Is.True);
            Assert.That(BridgeProtocol.IsKnownCapability(new RequestDto
            {
                capability_kind = "cold-test",
            }), Is.False);
			Assert.That(BridgeProtocol.RequiresTestRun(new RequestDto
			{
				capability_kind = BridgeProtocol.CapabilityCompile,
			}), Is.False);
			Assert.That(BridgeProtocol.RequiresTestRun(new RequestDto
			{
				capability_kind = BridgeProtocol.CapabilityWarmTest,
			}), Is.True);
        }
    }
}

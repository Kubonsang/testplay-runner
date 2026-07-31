using System.Collections;
using NUnit.Framework;
using UnityEngine;
using UnityEngine.TestTools;

namespace TestPlayFixture.Tests
{
    public sealed class DeterministicPlayModeTests
    {
        [UnityTest]
        public IEnumerator DeterministicPlayModeSmokeTest()
        {
            var gameObject = new GameObject("TestPlayVHDXFixture");
            var probe = gameObject.AddComponent<DeterministicProbe>();

            yield return null;

            Assert.That(probe.Value, Is.EqualTo(DeterministicState.ExpectedValue));
            Object.Destroy(gameObject);
            yield return null;
            Assert.That(gameObject == null, Is.True);
        }
    }
}

using System.Collections;
using NUnit.Framework;
using UnityEngine.TestTools;

namespace FastPlaySmoke
{
    public class PlayModeSmoke
    {
        [UnityTest]
        public IEnumerator AlwaysPassesInPlayMode()
        {
            // Wait one frame to exercise the PlayMode test runner lifecycle.
            yield return null;
            Assert.AreEqual(2, 1 + 1, "Basic arithmetic must hold in PlayMode");
        }
    }
}

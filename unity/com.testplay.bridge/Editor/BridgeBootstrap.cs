using System;
using System.IO;
using UnityEditor;
using UnityEngine;

namespace TestPlay.Bridge
{
    /// <summary>
    /// Activation gate. The bridge is DORMANT by default: installing the package
    /// changes nothing about a normal Editor session. It activates only when
    /// explicitly opted in. A normal batchmode run remains dormant, while an
    /// explicitly bound HoneyBee workspace may activate the protocol-3 bridge
    /// in a harness-owned headless Editor.
    ///
    /// Opt-in (either):
    ///   • TESTPLAY_BRIDGE_ENABLE=1 environment variable (developer dogfood), or
    ///   • a .testplay/bridge/ENABLE sentinel file in the project (declarative;
    ///     create the empty file to opt in).
    /// </summary>
    [InitializeOnLoad]
    internal static class BridgeBootstrap
    {
        static BridgeBootstrap()
        {
            if (!OptedIn())
                return;

            BridgeServer.Start();
        }

        private static bool OptedIn()
        {
            try
            {
				if (EnvironmentOptedIn(
						Environment.GetEnvironmentVariable("TESTPLAY_BRIDGE_ENABLE"),
						Environment.GetEnvironmentVariable("HONEYBEE_WORKSPACE_ID")))
                    return true;
                return File.Exists(BridgePaths.EnableSentinel);
            }
            catch
            {
                return false;
            }
        }

		internal static bool EnvironmentOptedIn(string testPlayEnable, string honeyBeeWorkspaceId)
		{
			return testPlayEnable == "1" || !string.IsNullOrWhiteSpace(honeyBeeWorkspaceId);
		}
    }
}

using System;
using System.IO;
using System.Threading;
using NUnit.Framework;
using UnityEngine;

namespace TestPlayFixture.Tests
{
    public sealed class LibraryMountTests
    {
        [Test]
        public void LibraryMountWriteReadTest()
        {
            string marker = Environment.GetEnvironmentVariable("TESTPLAY_UNITY_FIXTURE_MARKER");
            Assert.That(marker, Is.Not.Null.And.Not.Empty);

            string projectRoot = Path.GetFullPath(Path.Combine(Application.dataPath, ".."));
            string libraryRoot = Path.GetFullPath(Path.Combine(projectRoot, "Library"));
            string markerRoot = Path.Combine(libraryRoot, "TestPlayVHDX");
            string markerPath = Path.GetFullPath(Path.Combine(markerRoot, "marker.txt"));
            string libraryPrefix = libraryRoot.TrimEnd(Path.DirectorySeparatorChar) + Path.DirectorySeparatorChar;

            Assert.That(markerPath.StartsWith(libraryPrefix, StringComparison.OrdinalIgnoreCase), Is.True);
            Directory.CreateDirectory(markerRoot);
            File.WriteAllText(markerPath, marker);
            Assert.That(File.ReadAllText(markerPath), Is.EqualTo(marker));
        }

        [Test]
        public void DeterministicRuntimeStateTest()
        {
            Assert.That(DeterministicState.Combine(4, 2), Is.EqualTo(42));
        }

        [Test]
        public void RebootRecoveryHoldTest()
        {
            string marker = Environment.GetEnvironmentVariable("TESTPLAY_UNITY_FIXTURE_MARKER");
            string readyPath = Environment.GetEnvironmentVariable("TESTPLAY_UNITY_FIXTURE_REBOOT_READY_FILE");
            string releasePath = Environment.GetEnvironmentVariable("TESTPLAY_UNITY_FIXTURE_REBOOT_RELEASE_FILE");
            Assert.That(marker, Is.Not.Null.And.Not.Empty);
            Assert.That(readyPath, Is.Not.Null.And.Not.Empty);
            Assert.That(releasePath, Is.Not.Null.And.Not.Empty);

            string projectRoot = Path.GetFullPath(Path.Combine(Application.dataPath, ".."));
            string markerRoot = Path.Combine(projectRoot, "Library", "TestPlayVHDX");
            Directory.CreateDirectory(markerRoot);
            File.WriteAllText(Path.Combine(markerRoot, "reboot-marker.txt"), marker);
            File.WriteAllText(readyPath, marker);

            DateTime deadline = DateTime.UtcNow.AddMinutes(30);
            while (!File.Exists(releasePath) && DateTime.UtcNow < deadline)
            {
                Thread.Sleep(100);
            }
            Assert.That(File.Exists(releasePath), Is.True, "reboot recovery hold timed out");
        }
    }
}

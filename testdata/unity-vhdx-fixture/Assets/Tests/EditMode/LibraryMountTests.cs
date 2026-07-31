using System;
using System.IO;
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
    }
}

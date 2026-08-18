using System;
using System.IO;

namespace TestPlay.Bridge
{
    internal static class BridgePathPolicy
    {
        public static bool Validate(out string reason)
        {
            return Validate(
                BridgePaths.ProjectRoot,
                BridgePaths.BridgeDir,
                path => File.GetAttributes(path),
                out reason);
        }

        internal static bool Validate(
            string projectRoot,
            string bridgeDir,
            Func<string, FileAttributes> attributes,
            out string reason)
        {
            reason = "";
            string root = Path.GetFullPath(projectRoot)
                .TrimEnd(Path.DirectorySeparatorChar, Path.AltDirectorySeparatorChar);
            string target = Path.GetFullPath(bridgeDir);
			string rootedPrefix = root + Path.DirectorySeparatorChar;
			if (!target.Equals(root, StringComparison.OrdinalIgnoreCase) &&
				!target.StartsWith(rootedPrefix, StringComparison.OrdinalIgnoreCase))
			{
				reason = "bridge directory is outside the project root";
				return false;
			}
            string relative = target.Substring(root.Length)
                .TrimStart(Path.DirectorySeparatorChar, Path.AltDirectorySeparatorChar);

            string current = root;
            foreach (string component in relative.Split(
                         new[] { Path.DirectorySeparatorChar, Path.AltDirectorySeparatorChar },
                         StringSplitOptions.RemoveEmptyEntries))
            {
                current = Path.Combine(current, component);
                if (!Directory.Exists(current) && !File.Exists(current))
                    continue;
                if ((attributes(current) & FileAttributes.ReparsePoint) != 0)
                {
                    reason = current + " is a reparse point";
                    return false;
                }
            }
            return true;
        }
    }
}

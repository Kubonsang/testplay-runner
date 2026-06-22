using System.IO;
using System.Text;

namespace TestPlay.Bridge
{
    /// <summary>
    /// Crash-safe file writes used by the bridge. Snapshot files (handshake,
    /// response, results.xml, compile-errors.json) are written to a sibling
    /// .tmp and atomically renamed so a reader on the Go side never observes a
    /// partial document — the same tmp+rename discipline used by the Go
    /// internal/status writer. Progress is append-only NDJSON.
    /// </summary>
    internal static class AtomicFile
    {
        private static readonly UTF8Encoding Utf8NoBom = new UTF8Encoding(false);

        public static void WriteAllText(string path, string content)
        {
            Directory.CreateDirectory(Path.GetDirectoryName(path));
            string tmp = path + ".tmp";
            File.WriteAllText(tmp, content, Utf8NoBom);
            // File.Replace requires the destination to exist; fall back to a
            // delete+move when it does not (first write of this run).
            if (File.Exists(path))
            {
                File.Replace(tmp, path, null);
            }
            else
            {
                File.Move(tmp, path);
            }
        }

        public static void AppendLine(string path, string line)
        {
            Directory.CreateDirectory(Path.GetDirectoryName(path));
            // Append is atomic for lines smaller than the OS pipe buffer; the Go
            // reader tolerates and re-reads partial trailing lines regardless.
            File.AppendAllText(path, line + "\n", Utf8NoBom);
        }
    }
}

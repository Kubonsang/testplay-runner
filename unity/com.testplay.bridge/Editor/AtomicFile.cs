using System.IO;
using System.Text;
using System.Threading;

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

            // Retry the replace to tolerate Windows sharing violations: replacing
            // a destination the Go-side poller has briefly opened throws
            // IOException ("being used by another process") until that handle
            // closes. The reader holds it only briefly, so a bounded backoff
            // resolves the race. (File.Replace requires the destination to exist;
            // fall back to Move on the first write of a run.)
            const int maxAttempts = 100; // ~1s total at 10ms spacing
            for (int attempt = 0; ; attempt++)
            {
                try
                {
                    if (File.Exists(path))
                        File.Replace(tmp, path, null);
                    else
                        File.Move(tmp, path);
                    return;
                }
                catch (IOException) when (attempt < maxAttempts)
                {
                    Thread.Sleep(10);
                }
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

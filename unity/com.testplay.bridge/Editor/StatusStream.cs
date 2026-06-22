using UnityEngine;

namespace TestPlay.Bridge
{
    /// <summary>
    /// Appends typed progress lines to the per-run status.ndjson. The Go client
    /// tails this file and translates each line into a single status.Writer
    /// snapshot, so the bridge must NOT write testplay-status.json itself (that
    /// would fork the monotonic seq counter). Only non-terminal phases
    /// (compiling/running) are streamed; the terminal "done" phase is owned by
    /// the Go side after it parses results.xml.
    /// </summary>
    internal sealed class StatusStream
    {
        private readonly string _path;

        public StatusStream(string statusNdjsonPath)
        {
            _path = statusNdjsonPath;
        }

        public void Compiling()
        {
            Append(new ProgressDto { phase = BridgeProtocol.StateCompiling });
        }

        public void Running(int total)
        {
            Append(new ProgressDto { phase = "running", total = total });
        }

        public void CurrentTest(string fullName, int total)
        {
            Append(new ProgressDto { phase = "running", total = total, current_test = fullName });
        }

        private void Append(ProgressDto dto)
        {
            if (string.IsNullOrEmpty(_path))
                return;
            AtomicFile.AppendLine(_path, JsonUtility.ToJson(dto));
        }
    }
}

using System.Collections.Generic;
using System.IO;
using System.Text.RegularExpressions;
using UnityEditor.Compilation;
using UnityEngine;

namespace TestPlay.Bridge
{
    /// <summary>
    /// Captures C# compile errors from the CompilationPipeline and writes them as
    /// the compile-errors.json sidecar the Go bridge executor maps 1:1 onto
    /// history.CompileError (→ exit 2). This is the warm-editor analog of the
    /// batchmode stderr scrape; the message is normalized to "CSxxxx: text" so it
    /// is byte-identical to the Go stderr regex's captured group.
    ///
    /// A failed compilation does NOT trigger a domain reload, so errors captured
    /// in this static list during compilation survive until the server inspects
    /// them. A successful compilation reloads the domain and clears the list,
    /// which is correct (there were no errors).
    /// </summary>
    internal static class CompileErrorSidecar
    {
        // Matches the body the Go stderr regex captures:
        //   ^(.+\.cs)\((\d+),(\d+)\): error (CS\d+: .+)$   → group 4
        private static readonly Regex BodyRe = new Regex(@"error (CS\d+: .+)", RegexOptions.Compiled);

        private static readonly List<CompileErrorDto> Collected = new List<CompileErrorDto>();
        private static bool _hooked;

        public static void Hook()
        {
            if (_hooked)
                return;
            CompilationPipeline.assemblyCompilationFinished += OnAssemblyCompiled;
            _hooked = true;
        }

        public static void Clear()
        {
            Collected.Clear();
        }

        public static bool HasErrors => Collected.Count > 0;

        private static void OnAssemblyCompiled(string assemblyPath, CompilerMessage[] messages)
        {
            foreach (var msg in messages)
            {
                if (msg.type != CompilerMessageType.Error)
                    continue;

                string firstLine = (msg.message ?? "").Split('\n')[0].Trim();
                var match = BodyRe.Match(firstLine);
                string body = match.Success ? match.Groups[1].Value.Trim() : firstLine;

                string abs = "";
                if (!string.IsNullOrEmpty(msg.file))
                {
                    abs = Path.GetFullPath(msg.file).Replace('\\', '/');
                }

                Collected.Add(new CompileErrorDto
                {
                    file = (msg.file ?? "").Replace('\\', '/'),
                    absolute_path = abs,
                    line = msg.line,
                    column = msg.column,
                    message = body,
                });
            }
        }

        public static void Write(string path)
        {
            var file = new CompileErrorsFileDto { errors = Collected.ToArray() };
            AtomicFile.WriteAllText(path, JsonUtility.ToJson(file, true));
        }

        public static int Count => Collected.Count;
    }
}

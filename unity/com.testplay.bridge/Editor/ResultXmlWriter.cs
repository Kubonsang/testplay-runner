using System.Globalization;
using System.Text;
using UnityEditor.TestTools.TestRunner.Api;

namespace TestPlay.Bridge
{
    /// <summary>
    /// Serializes a finished test run to the Go-assigned results.xml path in the
    /// NUnit3 &lt;test-run&gt; shape internal/parser.Parse expects: a &lt;test-run&gt; root
    /// carrying total/passed/failed/skipped/duration, wrapping the result
    /// adaptor's own &lt;test-suite&gt; tree (preserving suite type, document order,
    /// and the "(at file.cs:line)" stack traces the parser extracts).
    ///
    /// PARITY RISK (validated by spike #2 / e2e bridge_parity_test): the exact
    /// attribute set and suite nesting produced by ITestResultAdaptor.ToXml()
    /// must round-trip through parser.Parse identically to a cold batchmode
    /// -testResults file. If a divergence is found, fix it here (or fall back to
    /// cold for the affected shape) rather than loosening the parser.
    /// </summary>
    internal static class ResultXmlWriter
    {
        public static void Write(string path, ITestResultAdaptor result)
        {
            int passed = result.PassCount;
            int failed = result.FailCount;
            int skipped = result.SkipCount;
            int inconclusive = result.InconclusiveCount;
            int total = passed + failed + skipped + inconclusive;

            var sb = new StringBuilder();
            sb.Append("<?xml version=\"1.0\" encoding=\"utf-8\"?>\n");
            sb.AppendFormat(
                CultureInfo.InvariantCulture,
                "<test-run total=\"{0}\" passed=\"{1}\" failed=\"{2}\" skipped=\"{3}\" inconclusive=\"{4}\" duration=\"{5}\">\n",
                total, passed, failed, skipped, inconclusive,
                result.Duration.ToString("0.######", CultureInfo.InvariantCulture));
            sb.Append(result.ToXml().OuterXml);
            sb.Append("\n</test-run>\n");

            AtomicFile.WriteAllText(path, sb.ToString());
        }
    }
}

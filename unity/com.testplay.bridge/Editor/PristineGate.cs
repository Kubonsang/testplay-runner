using System.Collections.Generic;
using UnityEditor;
using UnityEditor.SceneManagement;

namespace TestPlay.Bridge
{
    internal enum GateDecision
    {
        Proceed, // run now (NonPristine may carry disclosures)
        Wait,    // editor busy (compiling/importing); re-check next tick
        Refuse,  // cannot run a trustworthy warm result; Go falls back to cold
    }

    internal sealed class GateResult
    {
        public GateDecision Decision;
        public string RefuseReason = "";
        public readonly List<string> NonPristine = new List<string>();
    }

    /// <summary>
    /// Enforces hermeticity: a warm result is returned only when the warm domain
    /// is observationally equivalent to a fresh cold domain for the code under
    /// test. The gate refuses (→ cold fallback) on any state that could change
    /// RESULTS, waits out transient busy states, and discloses (but still runs)
    /// states that do not change results.
    /// </summary>
    internal static class PristineGate
    {
        public static GateResult Evaluate(RequestDto req)
        {
            var r = new GateResult();

            // EditMode-only this version — PlayMode-warm is deferred (C1-class
            // state-leakage hazard). Refuse so play_mode runs go cold.
            if (req.test_platform == "play_mode")
            {
                r.Decision = GateDecision.Refuse;
                r.RefuseReason = "PlayMode tests are not supported by the warm bridge in this version";
                return r;
            }

            // C1 — in/entering Play Mode: hard refuse.
            if (EditorApplication.isPlaying || EditorApplication.isPlayingOrWillChangePlaymode)
            {
                r.Decision = GateDecision.Refuse;
                r.RefuseReason = "editor is in Play Mode";
                return r;
            }

            // C2 — compile/import in flight: wait for it to settle (bounded by
            // the request's idle deadline, enforced by the server).
            if (EditorApplication.isCompiling || EditorApplication.isUpdating)
            {
                r.Decision = GateDecision.Wait;
                return r;
            }

            // C4 — dirty scenes / unsaved assets: disclose, never auto-save.
            int dirtyScenes = CountDirtyScenes();
            if (dirtyScenes > 0)
            {
                r.NonPristine.Add(
                    $"editor had unsaved changes in {dirtyScenes} scene(s); results reflect in-memory editor state, not disk");
            }

            r.Decision = GateDecision.Proceed;
            return r;
        }

        private static int CountDirtyScenes()
        {
            int dirty = 0;
            for (int i = 0; i < EditorSceneManager.sceneCount; i++)
            {
                if (EditorSceneManager.GetSceneAt(i).isDirty)
                    dirty++;
            }
            return dirty;
        }
    }
}

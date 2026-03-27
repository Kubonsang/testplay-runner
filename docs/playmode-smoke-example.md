# PlayMode Smoke Test Example

This document shows a minimal PlayMode smoke pattern that works well with
`fastplay` when you want to verify a small piece of gameplay behavior from a
real Unity project.

The example below uses a simple "player moves to the right" test, but the same
shape works for camera follow, gravity, trigger entry, or basic combat
interactions.

## Why This Pattern

For local smoke tests, the most reliable path is:

1. Create the fixture in code inside the test.
2. Keep the test self-contained.
3. Run it through `fastplay run`.

This avoids several common sources of friction:

- No temporary scene needs to be added to Build Settings.
- No extra editor tooling or bridge package is required.
- The test stays small enough to diagnose from `stdout.log`, `results.xml`, and
  `summary.json`.

## Example Test

```csharp
using System.Collections;
using NUnit.Framework;
using UnityEngine;
using UnityEngine.TestTools;

internal sealed class MovementSmokeComponent : MonoBehaviour
{
    public float speed = 4f;
    public Vector3 direction = Vector3.right;

    private void Update()
    {
        var move = direction;
        if (move.sqrMagnitude > 1f)
        {
            move.Normalize();
        }

        transform.position += move * speed * Time.deltaTime;
    }
}

public class MovementSmokeTest
{
    [UnityTest]
    public IEnumerator TestPlayer_MovesRight_InPlayMode()
    {
        var player = GameObject.CreatePrimitive(PrimitiveType.Capsule);
        player.name = "TestPlayer";
        player.transform.position = new Vector3(0f, 1f, 0f);

        var controller = player.AddComponent<MovementSmokeComponent>();
        controller.speed = 4f;
        controller.direction = Vector3.right;

        var start = player.transform.position;

        for (int i = 0; i < 20; i++)
        {
            yield return null;
        }

        var end = player.transform.position;
        Object.Destroy(player);

        Assert.Greater(end.x, start.x + 0.01f,
            $"Expected TestPlayer to move right. start={start}, end={end}");
    }
}
```

## How To Run

Prepare `fastplay.json` for the target project, then run:

```bash
fastplay run --filter TestPlayer_MovesRight_InPlayMode
```

Expected success shape:

```json
{
  "exit_code": 0,
  "passed": 1,
  "failed": 0,
  "total": 1,
  "tests": [
    {
      "name": "MovementSmokeTest.TestPlayer_MovesRight_InPlayMode",
      "result": "Passed"
    }
  ]
}
```

Artifacts are written under:

```text
.fastplay/runs/<run_id>/
  results.xml
  summary.json
  manifest.json
  stdout.log
  stderr.log
  events.ndjson
```

## Practical Notes

- Today, `fastplay` runs Unity in batch mode. If the same Unity project is
  already open in the Editor, close it before running this exact workflow.
- This pattern is intentionally scene-free. It is a good fit for movement,
  component wiring, and short interaction smoke tests.
- If your test truly depends on an authored scene, keep that as a separate
  pattern and document the scene-loading rule explicitly.
- For larger gameplay coverage, start with one or two small smoke tests like
  this before introducing heavier fixture scenes.

## When To Use This Example

Use this pattern when you want:

- a fast local PlayMode sanity check
- a reproducible `fastplay` run with small artifacts
- a test that does not depend on Build Settings changes
- a simple example for onboarding other developers to `fastplay`

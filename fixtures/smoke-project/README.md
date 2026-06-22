# smoke-project

Minimal Unity project for TestPlay Runner smoke verification.

**Do not run `testplay` directly from this directory.**
`testplay.json` has empty `unity_path` and `project_path` by design — they are
filled in at runtime by `scripts/smoke.sh`.

To run the smoke tests:

```bash
UNITY_PATH=/path/to/Unity ../../scripts/smoke.sh
```

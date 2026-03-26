# smoke-project

Minimal Unity project for FastPlay Runner smoke verification.

**Do not run `fastplay` directly from this directory.**
`fastplay.json` has empty `unity_path` and `project_path` by design — they are
filled in at runtime by `scripts/smoke.sh`.

To run the smoke tests:

```bash
UNITY_PATH=/path/to/Unity ../../scripts/smoke.sh
```

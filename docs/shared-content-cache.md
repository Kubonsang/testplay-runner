# Shared-content Library cache (opt-in)

Add `workspace.cache_mode: "shared-content"` to an existing TestPlay config.
Missing or `local` preserves historical behavior. Optional `workspace.cache_root`
is an absolute directory outside the project; otherwise the user cache directory
contains `TestPlay/Cache/v1` (`%LOCALAPPDATA%` on Windows).

This changes persistence for the legacy shadow provider, not backend selection.
Bridge/process runs stay Bridge/process runs. Explicit image/VHDX/auto providers
cannot be combined with shared-content. The v0.11.0 release has neither these
settings nor the following commands.

Each project path has an independent manifest and compatibility key covering
Unity executable contents, Unity version, packages manifest/lock and project
settings. Immutable SHA-256 blobs deduplicate equal bytes across manifests.
Restore verifies bytes and writes independent regular files. Unity never opens
the shared blobs directly. Publication and restore use eight IO workers. New
blobs are flushed before the complete manifest replaces its predecessor.

OS shared/exclusive locks protect the whole restore, publication, invalidation
and GC transaction. Process termination releases locks. A corrupt manifest or
blob fails explicitly; an absent/incompatible manifest is a cache miss. Failed
updates retain the prior manifest; unreferenced blobs may remain for later GC.
After a successful publication, the runner records allocated store size before
collecting blobs unreferenced by every project manifest. Maintenance failure is
reported as a warning; the committed manifest remains usable. Explicit prune is
also available after interrupted runs. Windows run metrics use actual filesystem
allocation, including sparse/compressed files, rather than logical file lengths.
Directory batches return allocation without opening every ordinary file. This
keeps measurement overhead low for both local and shared mode; unusual files use
individual queries and unsupported directory APIs fall back to a file walk.
Successful legacy exit codes 0 and 3 retain their existing write-back semantics;
scenario runs still skip write-back. No automatic conversion or deletion of old
local caches takes place.

```text
testplay cache usage --json
testplay cache prune --dry-run
testplay cache prune --execute
testplay cache retire-local --config ./testplay.json --dry-run
testplay cache retire-local --config ./testplay.json --execute
```

Usage/prune accept `--store-root` and report logical byte counts, not allocated
clusters. They do not infer the root from the current project's config. Prune
without `--execute` only previews. It retains all referenced blobs and rejects
unsafe entries or invalid manifests. Directory metadata and manifest files are
outside its blob-size totals; include the entire store when measuring savings.

Retire-local verifies the new shared cache and the old local key before touching
`.testplay/cache`. It refuses tracked files, links, special files, unexpected
top-level cache entries, a Unity lock or outstanding shadow directories. Its
default is a preview. Keep recovery copies separately; do not retire a cache
needed as recovery evidence. `--clear-cache` only invalidates the selected
project manifest, never the whole shared store.

Changing `cache_mode` back to `local` restores the old behavior. If its old cache
was explicitly retired, the next shadow run rebuilds it. Shared data remains
available until explicitly invalidated/pruned. This feature stays opt-in until
native repeated-run timing and disk-usage gates are met.

# Plan: collapse the snapshot machinery into a plain store

## Diagnosis

`lsp/cache` is a mini-port of gopls' architecture: `Cache → Session → View →
Snapshot`, copy-on-write maps (`cowMap`), a COW `IncludeGraph`, refcounted
snapshots (`Acquire`/release), an `IsCurrent` pointer check, and a transitive
`ForgetFile` that drops parse caches for every dependent on every change.

That machinery protects an invariant this codebase does not have.

**Key fact: `ParsedFile` is purely content-derived** (ast, mapper, errs,
tokens, index — all computed from the file's own bytes). Cross-file resolution
happens at request time through live reads; `source.Index` memoizes only
within a single request. So when include X changes, its dependents' cached
parses are *not* stale, and dropping them (ForgetFile's transitive cascade)
is wasted work inherited from gopls, where type-check results genuinely embed
cross-file data.

Other redundancy found while reading:

- `Snapshot.files` double-caches file handles. `memoizedFS` already memoizes
  disk reads by inode+mtime; `overlayFS` already holds overlays.
- `snapshotFS` (the resolver's fs.FS) re-routes overlay-first/disk-second,
  duplicating `overlayFS`, and is subtly less correct: it misses overlays
  created after the snapshot was taken.
- `IncludeDeps.Register` returns the union of old+new dependents;
  `View.FileChange` ignores it and recomputes via `Dependents`.
- `knownFiles` is a parallel structure to "has a store entry"; both are
  populated by exactly ReadFile / FileChange / walk.
- Three separate staleness mechanisms for one job: refcounting (release
  discipline), `IsCurrent` (drop superseded diagnostics), COW shared flags.

## Target design

One mutable store per view, guarded by one RWMutex; one atomic generation
counter. No snapshots, no cloning, no refcounting.

```go
// view.go (new internals)
type viewEntry struct {
    fh     FileHandle   // immutable once set
    parsed *ParsedFile  // derived from fh, immutable once set
}

type View struct {
    folder       uri.URI
    fs           FileSource        // overlayFS over memoizedFS (unchanged)
    includePaths []string
    config       options.Patch

    mu       sync.RWMutex
    entries  map[uri.URI]*viewEntry          // replaces files+parsedCache+knownFiles
    includes map[uri.URI][]uri.URI           // sorted direct out-edges
    includers map[uri.URI]map[uri.URI]struct{} // reverse edges

    gen atomic.Uint64   // bumped by FileChange; drops stale async diagnostics
}
```

Semantics:

- **ReadFile** delegates straight to `fs` (overlay first, memoized disk
  second). No per-view handle cache.
- **Parse**: return entry if present; else read + parse + store entry and
  refresh include edges under one lock. Entries are replaced wholesale on
  reparse, so nothing can go stale mid-flight.
- **Includes/Includers/Dependents**: graph queries over the edge maps.
  Dependents = cycle-safe BFS over reverse edges, sorted (same contract as
  today).
- **FileChange**: drop entries for changed URIs → eager re-parse of changed
  files (refreshes edges before requests observe them) → compute affected =
  changed ∪ transitive dependents → bump gen → run postFns asynchronously;
  each postFn compares captured gen against current and drops if superseded.
- **Resolver** wraps `fs` directly; `snapshotFS` dies.
- **Session, Cache, the three file sources, ParsedFile, FileIndex,
  FileChange plumbing: unchanged.**

`*cache.Snapshot` disappears from every signature in `lsp/source` and `lsp`;
handlers receive `*cache.View`. The acquire/release dance in `withSnapshot`
collapses to `session.ViewOf`.

Deleted: cow.go, graph.go (COW mechanics), context.go (IncludeDeps),
refcounting, clone(), IsCurrent, snapshotFS/snapshotFile/snapshotFileInfo,
FilesMap/ParseCaches aliases. Roughly −700 LOC, +250 LOC.

Behavioral deltas (intentional, all benign):

1. Dependents' parse caches survive an include change instead of being
   dropped. Their content-derived data was never stale; cross-file results
   recompute at request time regardless.
2. A request spanning a concurrent didChange may mix pre/post-change reads
   across files. Today a held snapshot froze already-read handles but still
   read live overlays for unread ones, so the old guarantee was already
   leaky; request handlers finish in milliseconds.
3. Diagnostics staleness moves from snapshot identity to generation counter —
   same observable behavior.

## Steps (each ends build + tests green, one jj commit)

1. **`cache: replace snapshot cow with plain store`**
   Rewrite View internals onto the store; keep `Snapshot` as a dumb
   delegating struct (`{view}`) so `lsp/` and `lsp/source` compile untouched.
   Delete cow.go, graph.go, context.go, snapshotFS, refcounting.
   `FileChange` switches to gen-based staleness internally.
   Rewrite tests that pin deleted internals:
   - `invalidation_test.go`: keep affected-set assertions; replace
     parsedCache/files reach-ins with public-behavior assertions (changed
     file reparsed with new content, unaffected file's parse survives).
   - `graph_test.go`, `context_test.go`, `cow_test.go`: fold coverage into
     new store tests (edges, dedupe, self-include, cycles, dependents BFS);
     drop what becomes redundant.

2. **`lsp: use views directly, drop snapshot facade`**
   Mechanical rename across `lsp` + `lsp/source` (+ their tests):
   `ss *cache.Snapshot` → `view *cache.View`; delete `Snapshot`,
   `BuildSnapshotForTest{,WithPaths}` → `BuildViewForTest{,WithPaths}`;
   collapse `withSnapshot`/`withFile` into a single helper; `postDiagnostics`
   captures a generation instead of acquiring/releasing.

3. **Full verification**: `go test -race ./...`, `go vet`, fuzz smoke on
   parser/formatter (untouched, but confirm), manual smoke via
   `thrift-ls lsp` against tests/made-in-abyss corpus if practical.

## Out of scope (flagged, not doing here)

- `options → formatter` coupling (drags formatter into every importer).
- Deducing the ~6 duplicated AST-walk switches.
- Sharing one `source.Index` across a diagnostic batch.
- Making eager re-parse lazy (kept to preserve edge-freshness semantics).

## Risks

- Subtle ordering bugs in the async postFn/gen interaction → covered by
  `-race`, existing didchange/session tests, plus new gen unit test.
- invalidation_test.go currently pins eager-parse behavior implicitly;
  rewrite must assert edges are fresh after FileChange (Dependents query
  reflects new includes immediately).

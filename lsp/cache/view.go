package cache

import (
	"context"
	"log/slog"
	"slices"
	"strings"
	"sync"
	"sync/atomic"

	"go.lsp.dev/uri"

	"github.com/karitham/thrift-ls/options"
	"github.com/karitham/thrift-ls/syntax"
)

// viewEntry is one file's store record: its content handle and the parse
// derived from it. Entries are replaced wholesale when content changes, so
// a live entry never goes stale.
type viewEntry struct {
	fh     FileHandle
	parsed *ParsedFile // nil until parsed
}

// View is one workspace folder's file store: parsed files, the include
// graph between them, and the configuration that applies to them.
//
// Concurrency: entries and edges are guarded by mu; reads share immutable
// values, writes replace entries wholesale. gen bumps on every FileChange;
// asynchronous work compares its captured generation against View.IsCurrent
// to drop superseded results.
type View struct {
	// folder is the tree root: a workspace folder, or the opened file's
	// directory in single-file mode.
	folder uri.URI

	fs FileSource

	includePaths []string

	// config is the folder's base configuration, fixed at creation;
	// workspace settings overlay it per request.
	config options.Patch

	mu        sync.RWMutex
	entries   map[uri.URI]*viewEntry
	includes  map[uri.URI][]uri.URI            // sorted direct include edges
	includers map[uri.URI]map[uri.URI]struct{} // reverse edges

	gen atomic.Uint64
}

func NewView(folder uri.URI, fs FileSource, includePaths []string, config options.Patch) *View {
	return &View{
		folder:       folder,
		fs:           fs,
		includePaths: includePaths,
		config:       config,
		entries:      make(map[uri.URI]*viewEntry),
		includes:     make(map[uri.URI][]uri.URI),
		includers:    make(map[uri.URI]map[uri.URI]struct{}),
	}
}

func (v *View) ContainsFile(uri uri.URI) bool {
	// folder: file:///workdir/
	// file: file:///workdir/file.idl
	folder := v.folder.Path()
	file := uri.Path()

	if !strings.HasPrefix(file, folder) {
		return false
	}

	folder = strings.TrimSuffix(folder, "/")
	file = strings.TrimPrefix(file, folder)

	return strings.HasPrefix(file, "/")
}

// Folder returns the workspace folder the view covers.
func (v *View) Folder() uri.URI {
	return v.folder
}

// Config returns the view's base configuration, without the client's
// workspace settings overlay.
func (v *View) Config() options.Patch {
	return v.config
}

// WalkFiles enumerates the view's file source under root: the disk in
// production, the in-memory tree in tests.
func (v *View) WalkFiles(ctx context.Context, root uri.URI, fn func(uri.URI) error) error {
	return v.fs.WalkFiles(ctx, root, fn)
}

// ReadFile returns the current content of uri: the editor overlay for open
// files, the memoized disk content otherwise. Content is not cached on the
// view — the file sources already memoize it.
func (v *View) ReadFile(ctx context.Context, u uri.URI) (FileHandle, error) {
	slog.Debug("view read file", "uri", u)

	return v.fs.ReadFile(ctx, u)
}

// Parse returns the cached parse of uri, parsing it on first use. A parse
// failure (unreadable file) is not cached; syntax errors are carried on the
// ParsedFile itself.
func (v *View) Parse(ctx context.Context, u uri.URI) (*ParsedFile, error) {
	if pf := v.parsed(u); pf != nil {
		return pf, nil
	}

	fh, err := v.ReadFile(ctx, u)
	if err != nil {
		return nil, err
	}

	pf, err := Parse(fh)
	if err != nil {
		slog.Debug("view parse failed", "err", err)

		return nil, err
	}

	var includes []uri.URI
	if pf.AST() != nil {
		includes = resolveIncludes(u, pf.AST().Includes(), v.Resolver().ResolveInclude)
	}

	v.setEntry(u, &viewEntry{fh: fh, parsed: pf}, includes)

	return pf, nil
}

// Resolver returns a resolver for the view's include paths.
func (v *View) Resolver() *Resolver {
	return newResolver(v.includePaths, v.fs)
}

// parsed returns the cached parse of uri, or nil.
func (v *View) parsed(u uri.URI) *ParsedFile {
	v.mu.RLock()
	defer v.mu.RUnlock()

	e, ok := v.entries[u]
	if !ok || e.parsed == nil {
		return nil
	}

	return e.parsed
}

// setEntry stores the file's entry and replaces its include edges.
func (v *View) setEntry(u uri.URI, e *viewEntry, includes []uri.URI) {
	v.mu.Lock()
	defer v.mu.Unlock()

	v.removeEdgesLocked(u)
	v.entries[u] = e

	for _, inc := range includes {
		v.includes[u] = append(v.includes[u], inc)

		// A self-include records its own reverse edge, so the file is its
		// own dependent.
		set := v.includers[inc]
		if set == nil {
			set = make(map[uri.URI]struct{})
			v.includers[inc] = set
		}

		set[u] = struct{}{}
	}
}

// removeEdgesLocked drops u's out-edges and their reverse entries.
// Callers must hold mu.
func (v *View) removeEdgesLocked(u uri.URI) {
	for _, inc := range v.includes[u] {
		set := v.includers[inc]
		if set == nil {
			continue
		}

		delete(set, u)
		if len(set) == 0 {
			delete(v.includers, inc)
		}
	}

	delete(v.includes, u)
}

// Includes returns the files file includes directly, sorted ascending by
// URI.
func (v *View) Includes(file uri.URI) []uri.URI {
	v.mu.RLock()
	defer v.mu.RUnlock()

	return slices.Clone(v.includes[file])
}

// Includers returns the files that include file directly, sorted ascending
// by URI.
func (v *View) Includers(file uri.URI) []uri.URI {
	v.mu.RLock()
	defer v.mu.RUnlock()

	set := v.includers[file]
	if len(set) == 0 {
		return nil
	}

	out := make([]uri.URI, 0, len(set))
	for u := range set {
		out = append(out, u)
	}

	slices.Sort(out)

	return out
}

// Dependents returns every file that directly or transitively includes
// file, including file itself when a cycle leads back to it. The result is
// sorted ascending by URI.
func (v *View) Dependents(file uri.URI) []uri.URI {
	v.mu.RLock()
	defer v.mu.RUnlock()

	deps := make([]uri.URI, 0)
	seen := make(map[uri.URI]struct{})
	queue := []uri.URI{file}

	for len(queue) > 0 {
		u := queue[0]
		queue = queue[1:]

		for dependent := range v.includers[u] {
			if _, ok := seen[dependent]; ok {
				continue
			}

			seen[dependent] = struct{}{}
			deps = append(deps, dependent)
			queue = append(queue, dependent)
		}
	}

	slices.Sort(deps)

	return deps
}

// TokensForFile returns the identifier tokens of file and its transitively
// included files. Only already-parsed files contribute; nothing is forced.
func (v *View) TokensForFile(file uri.URI) map[string]struct{} {
	tokens := make(map[string]struct{})
	visited := make(map[uri.URI]bool)

	var collect func(f uri.URI)

	collect = func(f uri.URI) {
		if visited[f] {
			return
		}

		visited[f] = true

		if pf := v.parsed(f); pf != nil {
			for token := range pf.Tokens() {
				tokens[token] = struct{}{}
			}
		}

		for _, inc := range v.Includes(f) {
			collect(inc)
		}
	}

	collect(file)

	return tokens
}

// FileKnown reports whether the view tracks uri.
func (v *View) FileKnown(u uri.URI) bool {
	v.mu.RLock()
	defer v.mu.RUnlock()

	_, ok := v.entries[u]

	return ok
}

// KnownFiles returns the tracked file URIs of the view, sorted for
// deterministic iteration.
func (v *View) KnownFiles() []uri.URI {
	v.mu.RLock()
	defer v.mu.RUnlock()

	files := make([]uri.URI, 0, len(v.entries))
	for file := range v.entries {
		files = append(files, file)
	}

	slices.Sort(files)

	return files
}

// Generation returns the view's change counter. It increments on every
// FileChange; asynchronous work captures it and re-checks before publishing.
func (v *View) Generation() uint64 {
	return v.gen.Load()
}

// IsCurrent reports whether gen is still the view's latest generation.
// Used by asynchronous work to drop results that a newer change superseded.
func (v *View) IsCurrent(gen uint64) bool {
	return v.Generation() == gen
}

// FileChange applies changes to the view: it invalidates the changed files'
// entries, re-parses them so their include edges are fresh before requests
// observe the change, then runs postFns asynchronously with the generation
// of this change and the affected URIs (changed files plus their transitive
// dependents). The request thread never blocks on postFns, so
// diagnostics-heavy work does not stall the editor. A postFn whose
// generation is no longer current can drop its results.
func (v *View) FileChange(ctx context.Context, changes []*FileChange, postFns ...func(gen uint64, affected []uri.URI)) {
	uris := make([]uri.URI, 0, len(changes))
	for _, change := range changes {
		uris = append(uris, change.URI)
	}

	slices.Sort(uris)
	uris = slices.Compact(uris)

	v.mu.Lock()
	for _, u := range uris {
		v.removeEdgesLocked(u)

		// Invalidate the old entry unconditionally, and track the file even
		// if the parse below fails, so routing and KnownFiles see it.
		v.entries[u] = &viewEntry{}
	}
	v.mu.Unlock()

	// Parse is lazy and cached, so requests racing ahead of this loop
	// simply parse on demand.
	for _, u := range uris {
		if _, err := v.Parse(ctx, u); err != nil {
			slog.Warn("parse error", "err", err)
		}
	}

	affected := v.affected(uris)

	gen := v.gen.Add(1)

	go func() {
		for i := range postFns {
			postFns[i](gen, affected)
		}
	}()
}

// affected returns uris plus the transitive dependents of each, deduped,
// computed after the eager re-parse so the edges reflect the change.
// Changes come first, in order; dependents follow, sorted by URI.
func (v *View) affected(uris []uri.URI) []uri.URI {
	affected := slices.Clone(uris)
	seen := make(map[uri.URI]struct{}, len(uris))
	for _, u := range affected {
		seen[u] = struct{}{}
	}

	for _, u := range uris {
		for _, dep := range v.Dependents(u) {
			if _, ok := seen[dep]; ok {
				continue
			}

			seen[dep] = struct{}{}
			affected = append(affected, dep)
		}
	}

	return affected
}

// resolveIncludes dedupes include statements by path text (first wins) and
// resolves each to a URI, sorted ascending.
func resolveIncludes(file uri.URI, includes []*syntax.Include, resolve func(uri.URI, string) uri.URI) []uri.URI {
	seen := make(map[string]struct{}, len(includes))
	uris := make([]uri.URI, 0, len(includes))

	for _, inc := range includes {
		if inc.Path == nil {
			continue
		}

		text := strings.Trim(inc.Path.Text, "\"'")
		if _, ok := seen[text]; ok {
			continue
		}

		seen[text] = struct{}{}

		uris = append(uris, resolve(file, text))
	}

	slices.Sort(uris)

	return uris
}

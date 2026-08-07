package cache

import (
	"context"
	"log/slog"
	"math/rand"
	"sort"
	"strings"
	"sync"

	"go.lsp.dev/uri"
)

type View struct {
	id int64

	// name is the user-specified name of this view.
	name string

	// TODO(jpf): view 的设计并不合理
	// workspace folder
	folder uri.URI

	fs FileSource

	knownFilesMu sync.Mutex
	knownFiles   map[uri.URI]bool

	includePaths []string

	// Track the latest snapshot via the snapshot field, guarded by snapshotMu.
	//
	// Invariant: whenever the snapshot field is overwritten, destroy(snapshot)
	// is called on the previous (overwritten) snapshot while snapshotMu is held,
	// incrementing snapshotWG. During shutdown the final snapshot is
	// overwritten with nil and destroyed, guaranteeing that all observed
	// snapshots have been destroyed via the destroy method, and snapshotWG may
	// be waited upon to let these destroy operations complete.
	snapshotMu      sync.Mutex
	snapshot        *Snapshot // latest snapshot; nil after shutdown has been called
	snapshotRelease func()
}

func NewView(name string, folder uri.URI, fs FileSource, includePaths []string) *View {
	view := &View{
		id:           rand.Int63(),
		name:         name,
		folder:       folder,
		fs:           fs,
		knownFiles:   make(map[uri.URI]bool),
		includePaths: includePaths,
	}

	view.snapshot = NewSnapshot(view, includePaths)

	view.snapshotRelease = view.snapshot.Acquire()

	asyncRelease := view.snapshot.Acquire()
	go func() {
		defer asyncRelease()

		view.snapshotMu.Lock()
		view.snapshot.Initialize(context.Background())
		view.snapshotMu.Unlock()
	}()

	return view
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

func (v *View) MarkFileKnown(fileURI uri.URI) {
	v.knownFilesMu.Lock()
	defer v.knownFilesMu.Unlock()

	if v.knownFiles == nil {
		v.knownFiles = make(map[uri.URI]bool)
	}

	v.knownFiles[fileURI] = true
}

func (v *View) FileKnown(uri uri.URI) bool {
	v.knownFilesMu.Lock()
	defer v.knownFilesMu.Unlock()

	return v.knownFiles[uri]
}

// FileChange applies changes to the view: it swaps in a new snapshot (an
// O(1) copy-on-write clone) and re-parses the changed files, then runs the
// postFns asynchronously with the affected URIs (changed files plus their
// transitive dependents). The request thread never blocks on postFns, so
// diagnostics-heavy work (semantic analysis) does not stall the editor.
func (v *View) FileChange(ctx context.Context, changes []*FileChange, postFns ...func(affected []uri.URI)) {
	for _, change := range changes {
		v.MarkFileKnown(change.URI)
	}

	// Swap in the new snapshot.
	newSnapshot, release := v.snapshot.clone()
	v.snapshotRelease()
	v.snapshotMu.Lock()

	v.snapshot = newSnapshot
	for _, change := range changes {
		v.snapshot.ForgetFile(change.URI)
	}
	v.snapshotMu.Unlock()
	v.snapshotRelease = release

	asyncRelease := v.snapshot.Acquire()

	// Re-parse the changed files so the snapshot's include edges and
	// parsed caches reflect the change before any request observes it.
	// Parse is lazy and cached, so requests racing ahead of this loop
	// simply parse on demand.
	uris := make([]uri.URI, 0, len(changes))
	for _, change := range changes {
		uris = append(uris, change.URI)
	}
	sort.Slice(uris, func(i, j int) bool { return uris[i] < uris[j] })

	for _, uri := range uris {
		if _, err := v.snapshot.Parse(ctx, uri); err != nil {
			slog.Error("parse error", "err", err)
		}
	}

	affected := v.affectedFiles(changes)

	go func() {
		defer asyncRelease()

		for i := range postFns {
			postFns[i](affected)
		}
	}()
}

// affectedFiles returns the changed URIs plus the transitive dependents of
// each, deduped. Dependents are computed on the current snapshot, after the
// changes were applied and re-parsed, so the edges reflect the change.
// Changes come first, in order; dependents follow, sorted by URI.
func (v *View) affectedFiles(changes []*FileChange) []uri.URI {
	affected := make([]uri.URI, 0, len(changes))
	seen := make(map[uri.URI]struct{}, len(changes))

	for _, change := range changes {
		if _, ok := seen[change.URI]; ok {
			continue
		}

		seen[change.URI] = struct{}{}
		affected = append(affected, change.URI)

		v.snapshotMu.Lock()
		deps := v.snapshot.Dependents(change.URI)
		v.snapshotMu.Unlock()

		for _, dep := range deps {
			if _, ok := seen[dep]; ok {
				continue
			}

			seen[dep] = struct{}{}
			affected = append(affected, dep)
		}
	}

	return affected
}

func (v *View) Snapshot() (*Snapshot, func()) {
	v.snapshotMu.Lock()
	defer v.snapshotMu.Unlock()

	// The snapshot is created in NewView and only set to nil on shutdown.
	return v.snapshot, v.snapshot.Acquire()
}

// IsCurrent reports whether ss is the view's latest snapshot. Used by
// asynchronous work to drop results that a newer change superseded.
func (v *View) IsCurrent(ss *Snapshot) bool {
	v.snapshotMu.Lock()
	defer v.snapshotMu.Unlock()

	return v.snapshot == ss
}

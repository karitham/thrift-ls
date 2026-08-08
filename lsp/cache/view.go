package cache

import (
	"context"
	"log/slog"
	"slices"
	"strings"
	"sync"

	"go.lsp.dev/uri"
)

type View struct {
	// TODO(jpf): view 的设计并不合理
	// workspace folder
	folder uri.URI

	fs FileSource

	knownFilesMu sync.Mutex
	knownFiles   map[uri.URI]bool

	includePaths []string

	// Track the latest snapshot via the snapshot field, guarded by
	// snapshotMu. The swap in FileChange releases the previous snapshot's
	// ref under the same lock.
	snapshotMu      sync.Mutex
	snapshot        *Snapshot // latest snapshot
	snapshotRelease func()
}

func NewView(folder uri.URI, fs FileSource, includePaths []string) *View {
	view := &View{
		folder:       folder,
		fs:           fs,
		knownFiles:   make(map[uri.URI]bool),
		includePaths: includePaths,
	}

	view.snapshot = NewSnapshot(view, includePaths)

	view.snapshotRelease = view.snapshot.Acquire()

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

// Folder returns the workspace folder the view covers.
func (v *View) Folder() uri.URI {
	return v.folder
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

// KnownFiles returns the known file URIs of the view, sorted for
// deterministic iteration.
func (v *View) KnownFiles() []uri.URI {
	v.knownFilesMu.Lock()
	defer v.knownFilesMu.Unlock()

	files := make([]uri.URI, 0, len(v.knownFiles))
	for file := range v.knownFiles {
		files = append(files, file)
	}

	slices.Sort(files)

	return files
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

	// Swap in the new snapshot. The pointer and its release are a single
	// unit guarded by snapshotMu: FileChange runs concurrently (request
	// handlers, the workspace walk), and an unlocked swap could release the
	// same snapshot twice (negative WaitGroup panic) or let a reader see a
	// released snapshot.
	v.snapshotMu.Lock()
	newSnapshot, release := v.snapshot.clone()
	v.snapshotRelease()

	v.snapshot = newSnapshot
	for _, change := range changes {
		newSnapshot.ForgetFile(change.URI)
	}

	v.snapshotRelease = release
	v.snapshotMu.Unlock()

	asyncRelease := newSnapshot.Acquire()

	// Re-parse the changed files so the snapshot's include edges and
	// parsed caches reflect the change before any request observes it.
	// Parse is lazy and cached, so requests racing ahead of this loop
	// simply parse on demand.
	uris := make([]uri.URI, 0, len(changes))
	for _, change := range changes {
		uris = append(uris, change.URI)
	}

	slices.Sort(uris)

	for _, uri := range uris {
		if _, err := newSnapshot.Parse(ctx, uri); err != nil {
			slog.Error("parse error", "err", err)
		}
	}

	affected := v.affectedFiles(changes, newSnapshot)

	go func() {
		defer asyncRelease()

		for i := range postFns {
			postFns[i](affected)
		}
	}()
}

// affectedFiles returns the changed URIs plus the transitive dependents of
// each, deduped. Dependents are computed on ss, the snapshot the changes
// were applied to, so the edges reflect the change.
// Changes come first, in order; dependents follow, sorted by URI.
func (v *View) affectedFiles(changes []*FileChange, ss *Snapshot) []uri.URI {
	affected := make([]uri.URI, 0, len(changes))
	seen := make(map[uri.URI]struct{}, len(changes))

	for _, change := range changes {
		if _, ok := seen[change.URI]; ok {
			continue
		}

		seen[change.URI] = struct{}{}
		affected = append(affected, change.URI)

		deps := ss.Dependents(change.URI)

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

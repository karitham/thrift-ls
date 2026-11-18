package store

// Compares session routing against the documented rule: memo hit, longest
// containing folder, first view with the file in its known set, then the
// first view.

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"testing"

	"go.lsp.dev/uri"

	"github.com/karitham/thrift-ls/internal/proptest"
)

// ppContainsFolder is the documented containment test: the file lives strictly
// under the folder, so a folder never contains itself and a sibling sharing a
// name prefix is a different tree.
func ppContainsFolder(folder, file uri.URI) bool {
	return strings.HasPrefix(file.Path(), strings.TrimSuffix(folder.Path(), "/")+"/")
}

// ppSessionView is one registered view in the model.
type ppSessionView struct {
	folder uri.URI
	view   *View
	known  map[uri.URI]bool
}

// ppSessionModel is the routing model: the views in registration order plus
// the memo ViewOf keeps.
type ppSessionModel struct {
	views []*ppSessionView
	memo  map[uri.URI]uri.URI
}

func newPPSessionModel() *ppSessionModel {
	return &ppSessionModel{memo: make(map[uri.URI]uri.URI)}
}

func (m *ppSessionModel) index(folder uri.URI) int {
	for i, v := range m.views {
		if v.folder == folder {
			return i
		}
	}

	return -1
}

// insert registers a view, mirroring AddView's deduplication and its
// invalidation of the routing memo.
func (m *ppSessionModel) insert(folder uri.URI, view *View) *ppSessionView {
	if i := m.index(folder); i >= 0 {
		return m.views[i]
	}

	entry := &ppSessionView{folder: folder, view: view, known: make(map[uri.URI]bool)}
	m.views = append(m.views, entry)
	m.memo = make(map[uri.URI]uri.URI)

	return entry
}

// remove drops the view and every memo entry that pointed at it.
func (m *ppSessionModel) remove(folder uri.URI) *ppSessionView {
	i := m.index(folder)
	if i < 0 {
		return nil
	}

	gone := m.views[i]
	m.views = append(m.views[:i], m.views[i+1:]...)

	for file, target := range m.memo {
		if target == folder {
			delete(m.memo, file)
		}
	}

	return gone
}

// route applies the documented rule and records the memo entry ViewOf would
// have recorded.
func (m *ppSessionModel) route(file uri.URI) (uri.URI, bool) {
	if len(m.views) == 0 {
		return "", false
	}

	if target, hit := m.memo[file]; hit {
		return target, true
	}

	best := -1

	for i, v := range m.views {
		if !ppContainsFolder(v.folder, file) {
			continue
		}

		if best < 0 || len(v.folder.Path()) > len(m.views[best].folder.Path()) {
			best = i
		}
	}

	if best >= 0 {
		m.memo[file] = m.views[best].folder

		return m.views[best].folder, true
	}

	for _, v := range m.views {
		if v.known[file] {
			m.memo[file] = v.folder

			return v.folder, true
		}
	}

	// The documented fallback: the first view, and deliberately not memoized.
	return m.views[0].folder, true
}

// ppSessionOpKind is one operation on the session.
type ppSessionOpKind int

const (
	ppSessionAddView    ppSessionOpKind = iota // Session.AddView
	ppSessionRemoveView                        // Session.RemoveView
	ppSessionPlace                             // a view learns a file (Update)
	ppSessionRoute                             // Session.ViewOf
	ppSessionWalk                              // Session.WalkFiles
	ppSessionObserve                           // reads only
)

func (k ppSessionOpKind) String() string {
	switch k {
	case ppSessionAddView:
		return "add-view"
	case ppSessionRemoveView:
		return "remove-view"
	case ppSessionPlace:
		return "place"
	case ppSessionRoute:
		return "route"
	case ppSessionWalk:
		return "walk"
	case ppSessionObserve:
		return "observe"
	default:
		return fmt.Sprintf("op(%d)", int(k))
	}
}

type ppSessionOp struct {
	kind   ppSessionOpKind
	folder int // index into the folder pool
	file   int // index into the file pool
	view   int // index into the model's views, for Place
}

func (o ppSessionOp) String() string {
	return fmt.Sprintf("%s(folder=%d file=%d view=%d)", o.kind, o.folder, o.file, o.view)
}

// ppSessionHarness drives a session over an in-memory tree plus the model.
type ppSessionHarness struct {
	sess    *Session
	folders []uri.URI
	files   []uri.URI
	disk    map[uri.URI][]byte
	model   *ppSessionModel
	step    int
}

// ppNewSessionHarness draws the workspace shape: 1-4 nested folders (one of
// which may be spelled with a trailing slash, covering the same files), and
// files placed inside folders, in deeper directories under them, and outside
// every folder.
func ppNewSessionHarness(t *proptest.T) *ppSessionHarness {
	t.Helper()

	root := "/proptest/ws"
	folders := []uri.URI{uri.File(root)}

	for range proptest.Int(t, 0, 3) {
		parent := folders[proptest.Int(t, 0, len(folders)-1)]
		segment := proptest.Pick(t, []string{"a", "b", "c"})
		candidate := uri.File(parent.Path() + "/" + segment)

		if slices.Contains(folders, candidate) {
			continue
		}

		folders = append(folders, candidate)
	}

	if proptest.Bool(t) {
		variant := uri.URI(string(folders[proptest.Int(t, 0, len(folders)-1)]) + "/")
		if !slices.Contains(folders, variant) {
			folders = append(folders, variant)
		}
	}

	disk := make(map[uri.URI][]byte)

	var files []uri.URI

	for i := range proptest.Int(t, 1, 5) {
		name := fmt.Sprintf("f%d.thrift", i)
		folder := folders[proptest.Int(t, 0, len(folders)-1)]

		var path string

		switch proptest.Int(t, 0, 2) {
		case 0: // directly inside the folder
			path = folder.Path() + "/" + name
		case 1: // in a directory under the folder: still inside it
			path = folder.Path() + "/sub/" + name
		default: // outside every folder
			path = "/proptest/elsewhere/" + name
		}

		u := uri.File(path)
		if slices.Contains(files, u) {
			continue
		}

		files = append(files, u)
		disk[u] = []byte(ppStubDoc(fmt.Sprintf("S%d", i)))
	}

	return &ppSessionHarness{
		sess:    NewSession(NewMemFS(disk)),
		folders: folders,
		files:   files,
		disk:    disk,
		model:   newPPSessionModel(),
	}
}

func ppSessionOpGen(folders, files int) proptest.Gen[ppSessionOp] {
	return func(t *proptest.T) ppSessionOp {
		op := ppSessionOp{
			kind: proptest.Pick(t, []ppSessionOpKind{
				ppSessionAddView, ppSessionAddView, ppSessionAddView,
				ppSessionRemoveView, ppSessionRemoveView,
				ppSessionPlace, ppSessionPlace, ppSessionPlace,
				ppSessionRoute, ppSessionRoute,
				ppSessionWalk,
				ppSessionObserve,
			}),
			folder: proptest.Int(t, 0, folders-1),
			file:   proptest.Int(t, 0, files-1),
			view:   proptest.Int(t, 0, folders-1),
		}

		return op
	}
}

func (h *ppSessionHarness) apply(ctx context.Context, t *proptest.T, op ppSessionOp) {
	t.Helper()

	switch op.kind {
	case ppSessionAddView:
		folder := h.folders[op.folder]
		before := -1
		if i := h.model.index(folder); i >= 0 {
			before = i
		}

		view := h.sess.AddView(folder, nil)
		entry := h.model.insert(folder, view)

		// Re-adding a folder returns the same view, not a second one.
		if before >= 0 && entry.view != h.model.views[before].view {
			t.Fatalf("step %d: AddView(%s) returned a new view for a tracked folder", h.step+1, folder)
		}

	case ppSessionRemoveView:
		folder := h.folders[op.folder]
		gone := h.model.remove(folder)
		if gone == nil {
			h.sess.RemoveView(folder)

			return
		}

		genBefore := gone.view.Generation()
		h.sess.RemoveView(folder)

		// RemoveView evicts the view's files and advances its generation.
		if known := gone.view.KnownFiles(); len(known) != 0 {
			t.Fatalf("step %d: RemoveView(%s) left %v tracked", h.step+1, folder, known)
		}

		if gone.view.Generation() <= genBefore {
			t.Fatalf("step %d: RemoveView(%s) did not advance the evicted view's generation", h.step+1, folder)
		}

	case ppSessionPlace:
		if len(h.model.views) == 0 {
			return
		}

		entry := h.model.views[op.view%len(h.model.views)]
		file := h.files[op.file]

		entry.view.Update(ctx, &FileChange{URI: file, From: FileChangeTypeInitialize})
		entry.known[file] = true

	case ppSessionRoute:
		// The check performs the routing sweep and keeps the model's memo in
		// step with the session's.

	case ppSessionWalk:
		root := h.folders[op.folder]

		var walked []uri.URI
		if err := h.sess.WalkFiles(ctx, root, func(u uri.URI) error {
			walked = append(walked, u)

			return nil
		}); err != nil {
			t.Fatalf("step %d: WalkFiles(%s): %v", h.step+1, root, err)
		}

		var want []uri.URI
		for u := range h.disk {
			if ppContainsFolder(root, u) {
				want = append(want, u)
			}
		}

		slices.Sort(want)

		ppEqual(t, h.step+1, "WalkFiles("+string(root)+")", ppURIs(want), ppURIs(walked))

	case ppSessionObserve:
		// Reads only; the check performs them.
	}
}

// check compares the session's routing model and view registration against the
// model after one operation.
func (h *ppSessionHarness) check(ctx context.Context, t *proptest.T) {
	t.Helper()

	h.step++
	step := h.step

	views := h.sess.Views()

	wantFolders := make([]uri.URI, 0, len(h.model.views))
	for _, v := range h.model.views {
		wantFolders = append(wantFolders, v.folder)
	}

	gotFolders := make([]uri.URI, 0, len(views))
	for _, v := range views {
		gotFolders = append(gotFolders, v.Folder())
	}

	ppEqual(t, step, "Views", ppURIs(wantFolders), ppURIs(gotFolders))

	for i, v := range views {
		entry := h.model.views[i]

		if v != entry.view {
			t.Fatalf("step %d: Views()[%d] is not the registered view for %s", step, i, entry.folder)
		}

		want := make([]uri.URI, 0, len(entry.known))
		for u := range entry.known {
			want = append(want, u)
		}

		slices.Sort(want)
		ppEqual(t, step, "KnownFiles("+string(entry.folder)+")", ppURIs(want), ppURIs(v.KnownFiles()))
	}

	// The routing sweep: every file, through ViewOf, against the model rule.
	for _, file := range h.files {
		view, err := h.sess.ViewOf(file)
		wantFolder, ok := h.model.route(file)

		if !ok {
			if err == nil {
				t.Fatalf("step %d: ViewOf(%s) with no views returned %s", step, file, view.Folder())
			}

			continue
		}

		if err != nil {
			t.Fatalf("step %d: ViewOf(%s): %v", step, file, err)
		}

		ppEqual(t, step, "ViewOf("+string(file)+")", wantFolder, view.Folder())

		// Routing is stable between structure changes: the memo makes it so.
		again, err := h.sess.ViewOf(file)
		if err != nil || again != view {
			t.Fatalf("step %d: ViewOf(%s) is not stable across calls", step, file)
		}
	}

	// ViewOf on a file no view has in its known set is total whenever a view
	// exists, and an
	// error exactly when none does.
	outside := uri.File("/outside/stranger.thrift")

	view, err := h.sess.ViewOf(outside)
	if len(h.model.views) == 0 {
		if err == nil {
			t.Fatalf("step %d: ViewOf with no views must fail", step)
		}
	} else {
		if err != nil {
			t.Fatalf("step %d: ViewOf(%s): %v", step, outside, err)
		}

		wantFolder, _ := h.model.route(outside)
		ppEqual(t, step, "ViewOf(outside)", wantFolder, view.Folder())
	}
}

// FuzzPropertySessionRouting drives add/remove/place/route/walk sequences and
// compares every routing decision with the model.
func FuzzPropertySessionRouting(f *testing.F) {
	proptest.Fuzz(f, proptest.Corpus{
		[]byte("route"),
		[]byte("s1"),
		[]byte("sessions"),
		[]byte(""),
	}, func(t *proptest.T) {
		h := ppNewSessionHarness(t)
		ctx := t.Context()

		trace := proptest.Slice(t, ppSessionOpGen(len(h.folders), len(h.files)), 1, 24)

		// The first step registers a view about half the time: routing with no
		// views registered must be an error, never a panic.
		h.check(ctx, t)

		for _, op := range trace {
			h.apply(ctx, t, op)
			h.check(ctx, t)
		}
	})
}

// FuzzPropertySessionNestedPrecedence checks the longest-prefix rule on a
// nested folder tree, including a trailing-slash spelling of one folder.
func FuzzPropertySessionNestedPrecedence(f *testing.F) {
	proptest.Fuzz(f, proptest.Corpus{
		[]byte("nested"),
		[]byte("p1"),
		[]byte("slash"),
		[]byte(""),
	}, func(t *proptest.T) {
		root := "/proptest/nested"
		depth := proptest.Int(t, 1, 3)

		folders := make([]uri.URI, 0, depth+1)
		folders = append(folders, uri.File(root))

		var path strings.Builder
		path.WriteString(root)
		for i := range depth {
			fmt.Fprintf(&path, "/l%d", i)
			folders = append(folders, uri.File(path.String()))
		}

		// A trailing-slash twin of one folder covers the same files.
		slashFolder := folders[proptest.Int(t, 0, len(folders)-1)]
		folders = append(folders, uri.URI(string(slashFolder)+"/"))

		disk := map[uri.URI][]byte{}

		var files []uri.URI

		for i, folder := range folders {
			file := uri.File(folder.Path() + fmt.Sprintf("/file%d.thrift", i))
			files = append(files, file)
			disk[file] = []byte(ppStubDoc(fmt.Sprintf("S%d", i)))
		}

		deepest := uri.File(path.String() + "/deepest.thrift")
		files = append(files, deepest)
		disk[deepest] = []byte(ppStubDoc("Deepest"))

		sess := NewSession(NewMemFS(disk))

		// Register in a shuffled order: registration order must only matter
		// for ties.
		perm := proptest.Permute(t, slices.Clone(folders))

		model := newPPSessionModel()
		for _, folder := range perm {
			model.insert(folder, sess.AddView(folder, nil))
		}

		h := &ppSessionHarness{sess: sess, folders: folders, files: files, disk: disk, model: model}
		h.check(t.Context(), t)

		// Every file routes to the deepest folder that contains it; the
		// trailing-slash twin wins ties by path length, as documented.
		for _, file := range files {
			view, err := sess.ViewOf(file)
			if err != nil {
				t.Fatalf("ViewOf(%s): %v", file, err)
			}

			wantFolder, _ := model.route(file)
			ppEqual(t, 1, "ViewOf("+string(file)+")", wantFolder, view.Folder())
		}
	})
}

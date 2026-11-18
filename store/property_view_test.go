package store

// Compares View's parse cache, include graph, and generations against a
// model of the documented contract, including eviction bookkeeping.

import (
	"context"
	"fmt"
	"math/rand"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"sync"
	"testing"

	"go.lsp.dev/uri"

	"github.com/karitham/thrift-ls/internal/proptest"
	"github.com/karitham/thrift-ls/syntax"
)

// ppGhostName is an include target that is never seeded on disk or in the
// overlay, so generated histories include files that do not exist.
const ppGhostName = "ghost.thrift"

// ppDefNames is the definition-name pool: rewrites pick a fresh name, so a
// stale parse cache shows up as a token mismatch on nearly every change.
var ppDefNames = []string{
	"Amuro", "Char", "Sayla", "Bright", "Garma", "Kycilia", "Usso", "Bernard",
}

// ppStubDoc renders a small valid document with one struct.
func ppStubDoc(name string) string {
	return fmt.Sprintf("struct %s {\n\t1: optional string %s_name\n}\n", name, name)
}

// ppDocKind selects the shape of a generated document.
type ppDocKind int

const (
	ppDocEmpty  ppDocKind = iota // empty file: parses with no definitions
	ppDocValid                   // a struct with generated fields
	ppDocBroken                  // a struct body the parser reports errors on
)

// ppInclude is one include statement: which file, which spelling, repeated or
// not. Dotted spellings ("./f1.thrift") resolve to the same URI as the plain
// form, so they exercise edge deduplication.
type ppInclude struct {
	target int
	dotted bool
	dup    bool
}

// ppDoc is a generated thrift document.
type ppDoc struct {
	kind     ppDocKind
	def      string
	fields   int
	includes []ppInclude
}

// ppDocGen generates a document. Include targets are drawn from names, which
// ends with ppGhostName, so generated graphs contain chains, diamonds, cycles,
// self-includes and includes of missing files.
func ppDocGen(names int) proptest.Gen[ppDoc] {
	return func(t *proptest.T) ppDoc {
		doc := ppDoc{
			kind:   proptest.Pick(t, []ppDocKind{ppDocEmpty, ppDocValid, ppDocValid, ppDocBroken}),
			def:    proptest.Pick(t, ppDefNames),
			fields: proptest.Int(t, 0, 2),
		}
		for range proptest.Int(t, 0, 3) {
			doc.includes = append(doc.includes, ppInclude{
				target: proptest.Int(t, 0, names-1),
				dotted: proptest.Bool(t),
				dup:    proptest.Bool(t),
			})
		}

		return doc
	}
}

func (d ppDoc) render(names []string) string {
	if d.kind == ppDocEmpty {
		return ""
	}

	var b strings.Builder

	for _, inc := range d.includes {
		path := names[inc.target]
		if inc.dotted {
			path = "./" + path
		}

		fmt.Fprintf(&b, "include %q\n", path)
		if inc.dup {
			fmt.Fprintf(&b, "include %q\n", path)
		}
	}

	b.WriteString("\n")

	switch d.kind {
	case ppDocValid:
		fmt.Fprintf(&b, "struct %s {\n", d.def)
		for i := 1; i <= d.fields; i++ {
			fmt.Fprintf(&b, "\t%d: optional string %s_f%d\n", i, d.def, i)
		}
		b.WriteString("}\n")
	case ppDocBroken:
		fmt.Fprintf(&b, "struct %s {\n\t1: required string\n}\n", d.def)
	}

	return b.String()
}

// ppTokenList returns a sorted, comparable view of an identifier set.
func ppTokenList(tokens map[string]struct{}) []string {
	out := make([]string, 0, len(tokens))
	for tok := range tokens {
		out = append(out, tok)
	}

	slices.Sort(out)

	return out
}

// ppContentIncludes resolves a document's include statements the way the view
// does with no include paths configured: the resolver's documented fallback,
// the file's own directory joined with the include text, deduplicated by text
// first and then by resolved URI, sorted. Nothing is read from disk, so this
// stays a function of content alone.
func ppContentIncludes(file uri.URI, content string) []uri.URI {
	out := []uri.URI{}
	ast, _ := syntax.Parse([]byte(content))
	if ast == nil {
		return out
	}

	dir := filepath.Dir(file.FsPath())
	seen := make(map[string]bool)

	for _, inc := range ast.Includes() {
		if inc.Path == nil {
			continue
		}

		text := strings.Trim(inc.Path.Text, "\"'")
		if seen[text] {
			continue
		}

		seen[text] = true
		out = append(out, uri.File(filepath.Join(dir, text)))
	}

	slices.Sort(out)

	return slices.Compact(out)
}

// ppRefStore is the reference model: the disk tree, the open overlays, the
// content each tracked file was last parsed from, and the include graph.
type ppRefStore struct {
	disk    map[uri.URI]string
	overlay map[uri.URI]string
	cached  map[uri.URI]string
	tracked map[uri.URI]bool

	forward  map[uri.URI]map[uri.URI]bool // file -> the files it includes
	backward map[uri.URI]map[uri.URI]bool // file -> the files that include it

	tokens map[uri.URI][]string
	errs   map[uri.URI][]syntax.Error
}

func newPPRefStore() *ppRefStore {
	return &ppRefStore{
		disk:     make(map[uri.URI]string),
		overlay:  make(map[uri.URI]string),
		cached:   make(map[uri.URI]string),
		tracked:  make(map[uri.URI]bool),
		forward:  make(map[uri.URI]map[uri.URI]bool),
		backward: make(map[uri.URI]map[uri.URI]bool),
		tokens:   make(map[uri.URI][]string),
		errs:     make(map[uri.URI][]syntax.Error),
	}
}

// effective is the content a read of u observes: the overlay when open, the
// disk copy otherwise.
func (m *ppRefStore) effective(u uri.URI) (string, bool) {
	if text, ok := m.overlay[u]; ok {
		return text, true
	}

	text, ok := m.disk[u]

	return text, ok
}

func (m *ppRefStore) knownSorted() []uri.URI {
	out := make([]uri.URI, 0, len(m.tracked))
	for u := range m.tracked {
		out = append(out, u)
	}

	slices.Sort(out)

	return out
}

// setForward replaces u's out-edges, maintaining the reverse image.
func (m *ppRefStore) setForward(u uri.URI, targets []uri.URI) {
	for t := range m.forward[u] {
		delete(m.backward[t], u)
		if len(m.backward[t]) == 0 {
			delete(m.backward, t)
		}
	}

	edges := make(map[uri.URI]bool, len(targets))
	for _, t := range targets {
		edges[t] = true
		if m.backward[t] == nil {
			m.backward[t] = make(map[uri.URI]bool)
		}

		m.backward[t][u] = true
	}

	m.forward[u] = edges
}

// recordParse records the parse the view must have cached for u: the modelled
// content's include statements become u's edges, and its tokens and errors are
// what a fresh parse reports.
func (m *ppRefStore) recordParse(u uri.URI) {
	text, ok := m.cached[u]
	if !ok {
		// A failed read leaves no parse and no edges behind.
		m.setForward(u, nil)
		delete(m.tokens, u)
		delete(m.errs, u)

		return
	}

	pf, err := Parse(NewOverlay(u, []byte(text), 0))
	if err != nil {
		panic("parse overlay: " + err.Error())
	}

	m.setForward(u, ppContentIncludes(u, text))
	m.tokens[u] = ppTokenList(pf.Tokens())
	m.errs[u] = pf.Errors()
}

// recordEvict drops the file's entry and every edge that names it, in both
// directions: the evicted file is no longer part of the graph.
func (m *ppRefStore) recordEvict(u uri.URI) {
	for t := range m.forward[u] {
		delete(m.backward[t], u)
		if len(m.backward[t]) == 0 {
			delete(m.backward, t)
		}
	}

	delete(m.forward, u)

	for includer := range m.backward[u] {
		delete(m.forward[includer], u)
	}

	delete(m.backward, u)
	delete(m.tracked, u)
	delete(m.cached, u)
	delete(m.tokens, u)
	delete(m.errs, u)
}

func (m *ppRefStore) includes(u uri.URI) []uri.URI {
	out := make([]uri.URI, 0, len(m.forward[u]))
	for t := range m.forward[u] {
		out = append(out, t)
	}

	slices.Sort(out)

	return out
}

func (m *ppRefStore) includers(u uri.URI) []uri.URI {
	out := make([]uri.URI, 0, len(m.backward[u]))
	for r := range m.backward[u] {
		out = append(out, r)
	}

	slices.Sort(out)

	return out
}

func (m *ppRefStore) dependents(u uri.URI) []uri.URI {
	out := []uri.URI{}
	seen := make(map[uri.URI]bool)
	queue := []uri.URI{u}

	for head := 0; head < len(queue); head++ {
		for r := range m.backward[queue[head]] {
			if seen[r] {
				continue
			}

			seen[r] = true
			out = append(out, r)
			queue = append(queue, r)
		}
	}

	slices.Sort(out)

	return out
}

// graphFiles is every URI in the model's graph plus every tracked
// file: the universe the per-file assertions sweep.
func (m *ppRefStore) graphFiles() []uri.URI {
	seen := make(map[uri.URI]bool)

	for _, u := range m.knownSorted() {
		seen[u] = true
	}

	for u, targets := range m.forward {
		seen[u] = true
		for t := range targets {
			seen[t] = true
		}
	}

	out := make([]uri.URI, 0, len(seen))
	for u := range seen {
		out = append(out, u)
	}

	slices.Sort(out)

	return out
}

// tokensFor is TokensForFile from the model: identifiers of every file
// reachable through forward edges that has a cached parse.
func (m *ppRefStore) tokensFor(file uri.URI) []string {
	tokens := make(map[string]struct{})
	visited := make(map[uri.URI]bool)

	var collect func(f uri.URI)

	collect = func(f uri.URI) {
		if visited[f] {
			return
		}

		visited[f] = true

		for _, tok := range m.tokens[f] {
			tokens[tok] = struct{}{}
		}

		for t := range m.forward[f] {
			collect(t)
		}
	}

	collect(file)

	return ppTokenList(tokens)
}

// ppOpKind is one operation on the store, drawn independently so an operation
// history is plain data: replayable from the seed that drew it.
type ppOpKind int

const (
	ppOpOpen              ppOpKind = iota // overlay write + Update (didOpen)
	ppOpChange                            // overlay write + Update (didChange)
	ppOpClose                             // overlay drop + Update (didClose)
	ppOpDiskChange                        // disk write + Update
	ppOpDiskWriteNoUpdate                 // disk write, no Update: the cache must stay warm
	ppOpEvict                             // View.Evict
	ppOpReparse                           // a read: View.Parse, which may populate the cache
	ppOpUpdateBatch                       // Update over 1-3 files, possibly duplicated
	ppOpObserve                           // reads only: observations must not change state
)

func (k ppOpKind) String() string {
	switch k {
	case ppOpOpen:
		return "open"
	case ppOpChange:
		return "change"
	case ppOpClose:
		return "close"
	case ppOpDiskChange:
		return "disk-change"
	case ppOpDiskWriteNoUpdate:
		return "disk-write-no-update"
	case ppOpEvict:
		return "evict"
	case ppOpReparse:
		return "reparse"
	case ppOpUpdateBatch:
		return "update-batch"
	case ppOpObserve:
		return "observe"
	default:
		return fmt.Sprintf("op(%d)", int(k))
	}
}

// ppOp is one step: an operation kind, the files it touches, and (for content
// writes) the document to store. text, when set, is written verbatim instead of
// rendering doc, so corpus documents can drive the same histories.
type ppOp struct {
	kind  ppOpKind
	files []int
	doc   ppDoc
	text  *string
}

func (o ppOp) String() string {
	return fmt.Sprintf("%s%v", o.kind, o.files)
}

// ppOpGen generates one operation. File indices address the real files;
// include targets may also name ppGhostName.
func ppOpGen(files, names int) proptest.Gen[ppOp] {
	return func(t *proptest.T) ppOp {
		kind := proptest.Pick(t, []ppOpKind{
			ppOpOpen, ppOpOpen,
			ppOpChange, ppOpChange, ppOpChange,
			ppOpClose,
			ppOpDiskChange, ppOpDiskChange,
			ppOpDiskWriteNoUpdate,
			ppOpEvict, ppOpEvict,
			ppOpReparse,
			ppOpUpdateBatch,
			ppOpObserve, ppOpObserve,
		})

		op := ppOp{kind: kind, files: []int{proptest.Int(t, 0, files-1)}}

		if kind == ppOpUpdateBatch {
			for range proptest.Int(t, 1, 3) {
				op.files = append(op.files, proptest.Int(t, 0, files-1))
			}
		}

		switch kind {
		case ppOpOpen, ppOpChange, ppOpDiskChange, ppOpDiskWriteNoUpdate:
			op.doc = ppDocGen(names)(t)
		}

		return op
	}
}

// ppLockedFS guards an in-memory tree with a mutex. The concurrency test
// mutates the disk while readers read, so the test filesystem must not race;
// the store's own synchronisation is what is under test.
type ppLockedFS struct {
	mu  sync.RWMutex
	fs  FileSource
	mem map[uri.URI][]byte
}

func newPPLockedFS(files map[uri.URI][]byte) *ppLockedFS {
	return &ppLockedFS{fs: NewMemFS(files), mem: files}
}

func (f *ppLockedFS) ReadFile(ctx context.Context, u uri.URI) (FileHandle, error) {
	f.mu.RLock()
	defer f.mu.RUnlock()

	return f.fs.ReadFile(ctx, u)
}

func (f *ppLockedFS) WalkFiles(ctx context.Context, root uri.URI, fn func(uri.URI) error) error {
	f.mu.RLock()
	defer f.mu.RUnlock()

	return f.fs.WalkFiles(ctx, root, fn)
}

func (f *ppLockedFS) Exists(ctx context.Context, path string) bool {
	f.mu.RLock()
	defer f.mu.RUnlock()

	if checker, ok := f.fs.(Checker); ok {
		return checker.Exists(ctx, path)
	}

	return false
}

func (f *ppLockedFS) write(u uri.URI, content []byte) {
	f.mu.Lock()
	defer f.mu.Unlock()

	f.mem[u] = content
}

// ppViewHarness drives one live view over a memFS plus the model that mirrors
// it. names holds the include-renderable names (real files first, then
// ppGhostName); uris holds the real files' URIs.
type ppViewHarness struct {
	names   []string
	uris    []uri.URI
	disk    map[uri.URI][]byte
	diskFS  *ppLockedFS
	fs      *OverlayFS
	view    *View
	model   *ppRefStore
	version int32
	step    int
}

// ppNewViewHarness builds the test driver over names. seeds maps a universe
// index to
// the initial disk content; unseeded files exist only if an operation creates
// them.
func ppNewViewHarness(t *proptest.T, folder uri.URI, names []string, seeds map[int]string) *ppViewHarness {
	t.Helper()

	h := &ppViewHarness{names: names, model: newPPRefStore()}
	for i := 0; i < len(names)-1; i++ {
		h.uris = append(h.uris, uri.File(folder.Path()+"/"+names[i]))
	}

	for i := range h.uris {
		if text, ok := seeds[i]; ok {
			h.model.disk[h.uris[i]] = text
		}
	}

	h.disk = make(map[uri.URI][]byte, len(h.model.disk))
	for u, text := range h.model.disk {
		h.disk[u] = []byte(text)
	}

	h.diskFS = newPPLockedFS(h.disk)
	h.fs = NewOverlayFS(h.diskFS)
	h.view = NewView(folder, h.fs, nil)

	return h
}

// ppGeneratedHarness draws a universe of 2-6 files with a mix of seeded
// content: valid documents with generated include graphs, empty files, broken
// documents, and files with no disk copy at all.
func ppGeneratedHarness(t *proptest.T) *ppViewHarness {
	t.Helper()

	folder := uri.File("/proptest/view")
	count := proptest.Int(t, 2, 6)

	names := make([]string, 0, count+1)
	for i := range count {
		names = append(names, fmt.Sprintf("f%d.thrift", i))
	}
	names = append(names, ppGhostName)

	seeds := make(map[int]string, count)
	for i := range count {
		switch proptest.Int(t, 0, 3) {
		case 0: // no disk copy: only an overlay can make it readable
		case 1:
			seeds[i] = ppDocGen(len(names))(t).render(names)
		case 2:
			seeds[i] = ""
		case 3:
			seeds[i] = ppStubDoc(proptest.Pick(t, ppDefNames))
		}
	}

	return ppNewViewHarness(t, folder, names, seeds)
}

// opText is the content a content-writing operation stores.
func (h *ppViewHarness) opText(op ppOp) string {
	if op.text != nil {
		return *op.text
	}

	return op.doc.render(h.names)
}

// apply performs one operation on the live view and on the model, returning
// the change result for Update operations.
func (h *ppViewHarness) apply(ctx context.Context, t *proptest.T, op ppOp) *ChangeResult {
	t.Helper()

	switch op.kind {
	case ppOpOpen, ppOpChange, ppOpClose:
		return h.applyOverlayOp(ctx, t, op)
	case ppOpDiskChange, ppOpDiskWriteNoUpdate:
		return h.applyDiskOp(ctx, t, op)
	case ppOpEvict:
		u := h.uris[op.files[0]]
		h.view.Evict(u)
		h.model.recordEvict(u)
	case ppOpReparse:
		h.applyReparse(ctx, op)
	case ppOpUpdateBatch:
		return h.applyBatch(ctx, op)
	case ppOpObserve:
		// Reads only; the step's check performs them.
	}

	return nil
}

func (h *ppViewHarness) applyOverlayOp(ctx context.Context, t *proptest.T, op ppOp) *ChangeResult {
	t.Helper()

	u := h.uris[op.files[0]]

	from := FileChangeTypeDidOpen
	switch op.kind {
	case ppOpChange:
		from = FileChangeTypeDidChange
	case ppOpClose:
		from = FileChangeTypeDidClose
	}

	if op.kind == ppOpClose {
		if err := h.fs.Update(ctx, []*FileChange{{URI: u, From: FileChangeTypeDidClose}}); err != nil {
			t.Fatalf("close overlay: %v", err)
		}
	} else {
		text := h.opText(op)
		if err := h.fs.Update(ctx, []*FileChange{{URI: u, Content: []byte(text), Version: int(h.version), From: from}}); err != nil {
			t.Fatalf("%s overlay: %v", from, err)
		}
	}

	res := h.view.Update(ctx, &FileChange{URI: u, From: from})

	h.model.tracked[u] = true

	switch op.kind {
	case ppOpClose:
		delete(h.model.overlay, u)
		if text, ok := h.model.disk[u]; ok {
			h.model.cached[u] = text
		} else {
			delete(h.model.cached, u)
		}
	default:
		text := h.opText(op)
		h.model.overlay[u] = text
		h.model.cached[u] = text
	}

	h.model.recordParse(u)

	return &res
}

func (h *ppViewHarness) applyDiskOp(ctx context.Context, t *proptest.T, op ppOp) *ChangeResult {
	t.Helper()

	u := h.uris[op.files[0]]
	text := h.opText(op)

	h.diskFS.write(u, []byte(text))
	h.model.disk[u] = text

	if op.kind == ppOpDiskWriteNoUpdate {
		// The view keeps serving its cached parse: no Update, no re-read.
		return nil
	}

	res := h.view.Update(ctx, &FileChange{URI: u, From: FileChangeTypeInitialize})
	h.model.tracked[u] = true

	if over, ok := h.model.overlay[u]; ok {
		h.model.cached[u] = over
	} else {
		h.model.cached[u] = text
	}

	h.model.recordParse(u)

	return &res
}

// applyReparse models a bare read: a tracked file is a cache hit, an untracked
// or unreadable one re-reads its current content and caches it when the read
// succeeds.
func (h *ppViewHarness) applyReparse(ctx context.Context, op ppOp) {
	u := h.uris[op.files[0]]

	_, err := h.view.Parse(ctx, u)

	if _, cached := h.model.cached[u]; cached {
		return
	}

	if text, ok := h.model.effective(u); ok {
		h.model.tracked[u] = true
		h.model.cached[u] = text
		h.model.recordParse(u)
	}

	_ = err
}

func (h *ppViewHarness) applyBatch(ctx context.Context, op ppOp) *ChangeResult {
	changes := make([]*FileChange, 0, len(op.files))
	uris := make([]uri.URI, 0, len(op.files))

	for _, idx := range op.files {
		uris = append(uris, h.uris[idx])
		changes = append(changes, &FileChange{URI: h.uris[idx], From: FileChangeTypeInitialize})
	}

	res := h.view.Update(ctx, changes...)

	for _, u := range uris {
		h.model.tracked[u] = true
		if text, ok := h.model.effective(u); ok {
			h.model.cached[u] = text
		} else {
			delete(h.model.cached, u)
		}

		h.model.recordParse(u)
	}

	return &res
}

// ppEqual fails the fuzz case when want and got differ, naming the step.
func ppEqual[T any](t *proptest.T, step int, what string, want, got T) {
	t.Helper()

	if !reflect.DeepEqual(want, got) {
		t.Fatalf("step %d %s: want %v, got %v", step, what, want, got)
	}
}

// ppURIs canonicalises a URI slice: an accessor's empty result may be nil or
// empty, which is not a behavioural difference.
func ppURIs(s []uri.URI) []uri.URI {
	return append([]uri.URI{}, s...)
}

// ppExpectedAffected recomputes Update's Affected from the model graph:
// changed files in sorted deduped order, then their transitive dependents.
func ppExpectedAffected(model *ppRefStore, changed []uri.URI) []uri.URI {
	uris := slices.Clone(changed)
	slices.Sort(uris)
	uris = slices.Compact(uris)

	affected := slices.Clone(uris)
	seen := make(map[uri.URI]bool, len(uris))
	for _, u := range uris {
		seen[u] = true
	}

	for _, u := range uris {
		for _, dep := range model.dependents(u) {
			if seen[dep] {
				continue
			}

			seen[dep] = true
			affected = append(affected, dep)
		}
	}

	return affected
}

// check compares the live view against the model after one operation.
func (h *ppViewHarness) check(ctx context.Context, t *proptest.T, op ppOp, genBefore uint64, res *ChangeResult) {
	t.Helper()

	h.step++
	step := h.step

	// KnownFiles/FileKnown track exactly the modelled entries.
	want := h.model.knownSorted()
	ppEqual(t, step, "KnownFiles", ppURIs(want), ppURIs(h.view.KnownFiles()))

	for _, u := range h.model.graphFiles() {
		ppEqual(t, step, "FileKnown("+string(u)+")", h.model.tracked[u], h.view.FileKnown(u))
	}

	// Parses, edges and dependents recomputed from the modelled content.
	for _, u := range h.model.graphFiles() {
		ppEqual(t, step, "Includes("+string(u)+")", ppURIs(h.model.includes(u)), ppURIs(h.view.Includes(u)))
		ppEqual(t, step, "Includers("+string(u)+")", ppURIs(h.model.includers(u)), ppURIs(h.view.Includers(u)))
		ppEqual(t, step, "Dependents("+string(u)+")", ppURIs(h.model.dependents(u)), ppURIs(h.view.Dependents(u)))
		ppEqual(t, step, "TokensForFile("+string(u)+")", h.model.tokensFor(u), ppTokenList(h.view.TokensForFile(u)))
	}

	// Content freshness: a cached parse reports the modelled content's tokens
	// and errors, never an older parse's.
	for _, u := range want {
		if _, cached := h.model.cached[u]; !cached {
			continue
		}

		pf, err := h.view.Parse(ctx, u)
		if err != nil {
			t.Fatalf("step %d: Parse(%s) failed for a cached file: %v", step, u, err)
		}

		ppEqual(t, step, "Tokens("+string(u)+")", h.model.tokens[u], ppTokenList(pf.Tokens()))
		ppEqual(t, step, "Errors("+string(u)+")", h.model.errs[u], pf.Errors())
		ppEqual(t, step, "URI("+string(u)+")", u, pf.URI())
	}

	// Structural invariant: the graph is its own reverse image.
	for _, x := range want {
		for _, inc := range h.view.Includes(x) {
			if !slices.Contains(h.view.Includers(inc), x) {
				t.Fatalf("step %d: reverse image broken: %s includes %s, but %s is not an includer of %s",
					step, x, inc, x, inc)
			}
		}
	}

	// Structural invariant: Dependents is the closure over forward edges.
	includers := map[uri.URI][]uri.URI{}

	for _, x := range want {
		for _, inc := range h.view.Includes(x) {
			includers[inc] = append(includers[inc], x)
		}
	}

	for _, target := range h.model.graphFiles() {
		ppEqual(t, step, "Dependents-closure("+string(target)+")",
			ppURIs(ppClosure(includers, target)), ppURIs(h.view.Dependents(target)))
	}

	// Overlay seam: reads see open content, then fall back to the delegate.
	for _, u := range h.uris {
		wantText, wantOK := h.model.effective(u)

		fh, err := h.fs.ReadFile(ctx, u)
		if err != nil {
			t.Fatalf("step %d: ReadFile(%s): %v", step, u, err)
		}

		content, cerr := fh.Content()
		if wantOK {
			if cerr != nil || string(content) != wantText {
				t.Fatalf("step %d: ReadFile(%s) = %q, %v; want %q", step, u, content, cerr, wantText)
			}
		} else if cerr == nil {
			t.Fatalf("step %d: ReadFile(%s) succeeded for a missing file", step, u)
		}

		ppEqual(t, step, "Exists("+string(u)+")", wantOK, h.fs.Exists(ctx, u.Path()))

		_, open := h.model.overlay[u]
		ppEqual(t, step, "HasOverlay("+string(u)+")", open, h.fs.HasOverlay(u))
	}

	// Generation: mutations advance it, reads never do.
	genAfter := h.view.Generation()

	switch op.kind {
	case ppOpOpen, ppOpChange, ppOpClose, ppOpDiskChange, ppOpUpdateBatch, ppOpEvict:
		if genAfter <= genBefore {
			t.Fatalf("step %d: %s must advance the generation: %d -> %d", step, op.kind, genBefore, genAfter)
		}
	default:
		if genAfter != genBefore {
			t.Fatalf("step %d: %s must not advance the generation: %d -> %d", step, op.kind, genBefore, genAfter)
		}
	}

	if res == nil {
		return
	}

	ppEqual(t, step, "ChangeResult.Gen", genAfter, res.Gen)

	changed := make([]uri.URI, 0, len(op.files))
	for _, idx := range op.files {
		changed = append(changed, h.uris[idx])
	}

	ppEqual(t, step, "ChangeResult.Affected", ppURIs(ppExpectedAffected(h.model, changed)), ppURIs(res.Affected))
}

// ppClosure is Dependents computed from a forward-edge model: everything that
// reaches target by following reverse edges, cycle-safe and sorted.
func ppClosure(includers map[uri.URI][]uri.URI, target uri.URI) []uri.URI {
	out := []uri.URI{}
	seen := map[uri.URI]bool{}
	queue := []uri.URI{target}

	for head := 0; head < len(queue); head++ {
		for _, dependent := range includers[queue[head]] {
			if seen[dependent] {
				continue
			}

			seen[dependent] = true
			out = append(out, dependent)
			queue = append(queue, dependent)
		}
	}

	slices.Sort(out)

	return out
}

// runViewTrace applies a whole operation history, checking after every step.
func ppRunViewTrace(ctx context.Context, t *proptest.T, h *ppViewHarness, trace []ppOp) string {
	t.Helper()

	for _, op := range trace {
		genBefore := h.view.Generation()
		res := h.apply(ctx, t, op)
		h.check(ctx, t, op, genBefore, res)
	}

	return h.snapshot()
}

// snapshot is the canonical observation of a view: what a replay must
// reproduce exactly.
func (h *ppViewHarness) snapshot() string {
	var b strings.Builder

	fmt.Fprintf(&b, "gen=%d\n", h.view.Generation())

	known := h.view.KnownFiles()
	fmt.Fprintf(&b, "known=%v\n", known)

	for _, u := range known {
		fmt.Fprintf(&b, "%s includes=%v includers=%v dependents=%v tokens=%v\n",
			u, h.view.Includes(u), h.view.Includers(u), h.view.Dependents(u),
			ppTokenList(h.view.TokensForFile(u)))
	}

	return b.String()
}

// FuzzPropertyViewOperationsMatchModel is the core state machine property: any
// sequence of overlay writes, disk writes, updates, evictions and reads leaves
// the view equal to the model.
func FuzzPropertyViewOperationsMatchModel(f *testing.F) {
	proptest.Fuzz(f, proptest.Corpus{
		[]byte("a"),
		[]byte("view-1"),
		[]byte("trace"),
		[]byte("mixed-ops"),
		[]byte(""),
	}, func(t *proptest.T) {
		h := ppGeneratedHarness(t)
		ctx := t.Context()

		trace := proptest.Slice(t, ppOpGen(len(h.uris), len(h.names)), 1, 24)
		ppRunViewTrace(ctx, t, h, trace)
	})
}

// FuzzPropertyViewChainPatterns checks chains and diamonds through reparse,
// eviction, and reopen.
func FuzzPropertyViewChainPatterns(f *testing.F) {
	proptest.Fuzz(f, proptest.Corpus{
		[]byte("chain"),
		[]byte("b"),
		[]byte("diamond"),
		[]byte("dotted"),
		[]byte(""),
	}, func(t *proptest.T) {
		names := []string{"a.thrift", "b.thrift", "c.thrift", "d.thrift", ppGhostName}
		folder := uri.File("/proptest/chain")

		// a includes b; b includes c and d; c includes d (a diamond under a
		// chain); d is the leaf everyone reaches.
		seeds := map[int]string{
			0: "include \"b.thrift\"\n\n" + ppStubDoc("Alpha"),
			1: "include \"c.thrift\"\ninclude \"d.thrift\"\n\n" + ppStubDoc("Beta"),
			2: "include \"d.thrift\"\n\n" + ppStubDoc("Gamma"),
			3: ppStubDoc("Delta"),
		}

		if proptest.Bool(t) {
			seeds[3] = "include \"d.thrift\"\n\n" + ppStubDoc("Delta")
		}

		if proptest.Bool(t) {
			seeds[2] = "include \"d.thrift\"\ninclude \"" + ppGhostName + "\"\n\n" + ppStubDoc("Gamma")
		}

		if proptest.Bool(t) {
			seeds[1] = "include \"c.thrift\"\ninclude \"./c.thrift\"\ninclude \"d.thrift\"\n\n" + ppStubDoc("Beta")
		}

		h := ppNewViewHarness(t, folder, names, seeds)
		ctx := t.Context()

		observe := ppOp{kind: ppOpObserve}

		// Cold reads first: parse the whole chain, then check everything.
		cold := []ppOp{
			{kind: ppOpReparse, files: []int{0}},
			{kind: ppOpReparse, files: []int{1}},
			{kind: ppOpReparse, files: []int{2}},
			{kind: ppOpReparse, files: []int{3}},
			{kind: ppOpUpdateBatch, files: []int{0, 1, 2, 3}},
			observe,
		}

		// Change the middle of the chain to drop its includes.
		dropIncludes := ppOp{
			kind:  ppOpChange,
			files: []int{1},
			doc:   ppDoc{kind: ppDocValid, def: proptest.Pick(t, ppDefNames)},
		}

		// A size-preserving rewrite that only renames a definition.
		rename := ppOp{
			kind:  ppOpDiskChange,
			files: []int{2},
			doc:   ppDoc{kind: ppDocValid, def: proptest.Pick(t, ppDefNames)},
		}

		// Evict the middle, observe the leftovers, then re-open it.
		evict := ppOp{kind: ppOpEvict, files: []int{1}}
		reopen := ppOp{
			kind:  ppOpOpen,
			files: []int{1},
			doc:   ppDoc{kind: ppDocValid, def: proptest.Pick(t, ppDefNames)},
		}

		script := make([]ppOp, 0, len(cold)+8)
		script = append(script, cold...)
		script = append(script, dropIncludes, observe)
		script = append(script, rename, observe)
		script = append(script, evict, observe)
		script = append(script, ppOp{kind: ppOpClose, files: []int{1}}, reopen)
		script = append(script, ppOp{kind: ppOpUpdateBatch, files: []int{0, 1, 2, 3}})

		if proptest.Bool(t) {
			script = append(script, ppOp{kind: ppOpEvict, files: []int{3}}, observe)
		}

		ppRunViewTrace(ctx, t, h, script)
	})
}

// FuzzPropertyViewReplayDeterminism checks that replaying a history on a fresh
// store produces identical observations.
func FuzzPropertyViewReplayDeterminism(f *testing.F) {
	proptest.Fuzz(f, proptest.Corpus{
		[]byte("replay"),
		[]byte("r2"),
		[]byte("same-history"),
		[]byte(""),
	}, func(t *proptest.T) {
		// Draw the universe and history once, then replay them twice.
		folder := uri.File("/proptest/replay")
		count := proptest.Int(t, 2, 5)

		names := make([]string, 0, count+1)
		for i := range count {
			names = append(names, fmt.Sprintf("f%d.thrift", i))
		}
		names = append(names, ppGhostName)

		seeds := make(map[int]string, count)
		for i := range count {
			if proptest.Bool(t) {
				seeds[i] = ppDocGen(len(names))(t).render(names)
			}
		}

		trace := proptest.Slice(t, ppOpGen(count, len(names)), 1, 12)

		first := ppRunViewTrace(t.Context(), t, ppNewViewHarness(t, folder, names, seeds), trace)
		second := ppRunViewTrace(t.Context(), t, ppNewViewHarness(t, folder, names, seeds), trace)

		ppEqual(t, 0, "replay determinism", first, second)
	})
}

// ppUnsorted reports why uris is not sorted and deduplicated: the documented
// shape of every graph accessor, which must hold for each individual call.
func ppUnsorted(uris []uri.URI) string {
	for i := 1; i < len(uris); i++ {
		if strings.Compare(string(uris[i-1]), string(uris[i])) >= 0 {
			return fmt.Sprintf("not sorted and deduped: %v", uris)
		}
	}

	return ""
}

// FuzzPropertyViewConcurrentReaders is the -race variant: 2-4 goroutines read
// the view while one goroutine drives updates and evictions. Only single-call
// invariants are asserted while racing; afterwards the state is reconciled by
// re-parsing every file and compared against the model.
func FuzzPropertyViewConcurrentReaders(f *testing.F) {
	proptest.Fuzz(f, proptest.Corpus{
		[]byte("race"),
		[]byte("c1"),
		[]byte("readers"),
		[]byte(""),
	}, func(t *proptest.T) {
		h := ppGeneratedHarness(t)
		ctx := t.Context()

		// Warm every readable file so readers mostly hit the cache.
		for _, u := range h.uris {
			if _, err := h.view.Parse(ctx, u); err == nil {
				if text, ok := h.model.effective(u); ok {
					h.model.tracked[u] = true
					h.model.cached[u] = text
					h.model.recordParse(u)
				}
			}
		}

		writes := proptest.Slice(t, ppOpGen(len(h.uris), len(h.names)), 4, 16)
		readers := proptest.Int(t, 2, 4)
		readerSeed := proptest.Int64(t)

		stop := make(chan struct{})

		var (
			wg         sync.WaitGroup
			mu         sync.Mutex
			violations []string
		)

		for r := range readers {
			wg.Add(1)

			go func() {
				defer wg.Done()

				rng := rand.New(rand.NewSource(readerSeed + int64(r)))
				files := h.uris

				for {
					select {
					case <-stop:
						return
					default:
					}

					u := files[rng.Intn(len(files))]

					if pf, err := h.view.Parse(ctx, u); err == nil {
						// A single call's result is well-formed even while
						// other goroutines mutate the view.
						_ = ppTokenList(pf.Tokens())
					}

					checks := []struct {
						what string
						uris []uri.URI
					}{
						{"KnownFiles", h.view.KnownFiles()},
						{"Includes", h.view.Includes(u)},
						{"Includers", h.view.Includers(u)},
						{"Dependents", h.view.Dependents(u)},
					}

					for _, c := range checks {
						if msg := ppUnsorted(c.uris); msg != "" {
							mu.Lock()
							violations = append(violations, c.what+": "+msg)
							mu.Unlock()
						}
					}
				}
			}()
		}

		for _, op := range writes {
			h.apply(ctx, t, op)
		}

		close(stop)
		wg.Wait()

		if len(violations) > 0 {
			t.Fatalf("concurrent reads observed malformed results: %v", violations[:min(len(violations), 3)])
		}

		// Reconcile: one Update over everything makes the final state a pure
		// function of the current content, then the model applies.
		changes := make([]*FileChange, 0, len(h.uris))
		for _, u := range h.uris {
			changes = append(changes, &FileChange{URI: u, From: FileChangeTypeInitialize})
		}

		if len(changes) > 0 {
			h.view.Update(ctx, changes...)
		}

		for _, u := range h.uris {
			h.model.tracked[u] = true
			if text, ok := h.model.effective(u); ok {
				h.model.cached[u] = text
			} else {
				delete(h.model.cached, u)
			}

			h.model.recordParse(u)
		}

		h.check(ctx, t, ppOp{kind: ppOpObserve}, h.view.Generation(), nil)
	})
}

// ppLoadCorpus reads every top-level *.thrift file of the given corpus
// directories, so the suite's histories carry real-world documents. Names are
// returned sorted, with contents index-aligned.
func ppLoadCorpus(t testing.TB, dirs ...string) ([]string, []string) {
	t.Helper()

	type entry struct{ name, content string }

	var entries []entry

	for _, dir := range dirs {
		items, err := os.ReadDir(dir)
		if err != nil {
			t.Fatalf("read corpus %s: %v", dir, err)
		}

		for _, item := range items {
			if item.IsDir() || !strings.HasSuffix(item.Name(), ".thrift") {
				continue
			}

			data, err := os.ReadFile(filepath.Join(dir, item.Name()))
			if err != nil {
				t.Fatalf("read %s: %v", item.Name(), err)
			}

			entries = append(entries, entry{name: item.Name(), content: string(data)})
		}
	}

	slices.SortFunc(entries, func(a, b entry) int { return strings.Compare(a.name, b.name) })

	names := make([]string, len(entries))
	contents := make([]string, len(entries))

	for i, e := range entries {
		names[i] = e.name
		contents[i] = e.content
	}

	return names, contents
}

// FuzzPropertyViewCorpusHistories drives the same operation histories with
// real-world corpus documents: the include shapes come from the corpora, not
// from the generated templates.
func FuzzPropertyViewCorpusHistories(f *testing.F) {
	names, contents := ppLoadCorpus(f,
		filepath.Join("..", "tests", "made-in-abyss"),
		filepath.Join("..", "tests", "galaxy-thrift-api", "sds"),
	)

	proptest.Fuzz(f, proptest.Corpus{
		[]byte("corpus"),
		[]byte("abyss"),
		[]byte("sds"),
		[]byte(""),
	}, func(t *proptest.T) {
		folder := uri.File("/proptest/corpus")
		allNames := append(slices.Clone(names), ppGhostName)

		seeds := make(map[int]string, len(names))
		for i := range names {
			if proptest.Bool(t) {
				seeds[i] = contents[i]
			}
		}

		h := ppNewViewHarness(t, folder, allNames, seeds)

		trace := proptest.Slice(t, ppOpGen(len(names), len(allNames)), 1, 12)

		// Half the content writes store another corpus document, which keeps
		// the include targets real but changes which definitions a file has.
		for i := range trace {
			switch trace[i].kind {
			case ppOpOpen, ppOpChange, ppOpDiskChange, ppOpDiskWriteNoUpdate:
				if proptest.Bool(t) {
					text := contents[proptest.Int(t, 0, len(contents)-1)]
					trace[i].text = &text
				}
			}
		}

		ppRunViewTrace(t.Context(), t, h, trace)
	})
}

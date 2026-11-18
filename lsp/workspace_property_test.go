package lsp

import (
	"context"
	"fmt"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"go.lsp.dev/protocol"
	"go.lsp.dev/uri"

	"github.com/karitham/thrift-ls/internal/fuzzgen"
	"github.com/karitham/thrift-ls/internal/proptest"
	"github.com/karitham/thrift-ls/options"
	"github.com/karitham/thrift-ls/store"
)

// Folder snapshots; /ws/inner nests inside /ws.
var workspacePropertyFolders = []uri.URI{
	uri.File("/ws"),
	uri.File("/ws/inner"),
	uri.File("/other"),
}

// Project roots, deliberately unrelated to the folder set: a project root is
// whatever the build system reports. /shared/deep nests inside /shared.
var workspacePropertyRoots = []uri.URI{
	uri.File("/ws"),
	uri.File("/ws/inner"),
	uri.File("/shared"),
	uri.File("/shared/deep"),
	uri.File("/deps"),
}

// Files: inside roots, inside nested roots, and outside every root.
var workspacePropertyFiles = []uri.URI{
	uri.File("/ws/a.thrift"),
	uri.File("/ws/inner/b.thrift"),
	uri.File("/shared/c.thrift"),
	uri.File("/shared/deep/d.thrift"),
	uri.File("/deps/e.thrift"),
	uri.File("/loose/f.thrift"),
	uri.File("/ws/inner/g.thrift"),
}

// Disk contents, one per file (by index). Entry 0 is clean, the others each
// carry exactly one defect, so a view that indexes its files publishes
// diagnostics and the suite can observe ownership through the publish log.
var workspacePropertyContents = []string{
	"struct Clean { 1: i32 value }\n",
	"struct Duplicate { 1: i32 value, 2: i32 value }\n",
	"struct Undefined { 1: Missing value }\n",
	"struct OutOfRange { 0: i32 value }\n",
}

func workspacePropertyContent(index int) string {
	return workspacePropertyContents[index%len(workspacePropertyContents)]
}

func workspacePropertyHasDiagnostic(index int) bool {
	return index%len(workspacePropertyContents) != 0
}

// recordedPublish is one PublishDiagnostics call, canonically formatted so
// two runs of the same trace compare byte for byte.
type recordedPublish struct {
	URI         uri.URI
	Diagnostics []protocol.Diagnostic
}

func (p recordedPublish) String() string {
	parts := make([]string, 0, len(p.Diagnostics))

	for _, d := range p.Diagnostics {
		parts = append(parts, fmt.Sprintf("%d %d:%d-%d:%d %s",
			d.Severity,
			d.Range.Start.Line, d.Range.Start.Character,
			d.Range.End.Line, d.Range.End.Character,
			fmt.Sprint(d.Message),
		))
	}

	slices.Sort(parts)

	return fmt.Sprintf("%s | %s", p.URI, strings.Join(parts, "; "))
}

// difference returns the members of a that b lacks.
func difference(a, b []uri.URI) []uri.URI {
	have := make(map[uri.URI]struct{}, len(b))
	for _, v := range b {
		have[v] = struct{}{}
	}

	var out []uri.URI

	for _, v := range a {
		if _, ok := have[v]; !ok {
			out = append(out, v)
		}
	}

	return out
}

// workspaceRecordingClient records every publish in arrival order and keeps
// the latest diagnostics per URI.
type workspaceRecordingClient struct {
	protocol.Client

	publishes []recordedPublish
	lastByURI map[uri.URI][]protocol.Diagnostic
	calls     map[uri.URI]int
}

func (c *workspaceRecordingClient) PublishDiagnostics(_ context.Context, params *protocol.PublishDiagnosticsParams) error {
	if c.lastByURI == nil {
		c.lastByURI = make(map[uri.URI][]protocol.Diagnostic)
		c.calls = make(map[uri.URI]int)
	}

	c.publishes = append(c.publishes, recordedPublish{
		URI:         params.URI,
		Diagnostics: append([]protocol.Diagnostic(nil), params.Diagnostics...),
	})
	c.lastByURI[params.URI] = append([]protocol.Diagnostic(nil), params.Diagnostics...)
	c.calls[params.URI]++

	return nil
}

func (c *workspaceRecordingClient) last(file uri.URI) []protocol.Diagnostic {
	return c.lastByURI[file]
}

// log returns each URI's publishes in arrival order. Order across URIs is
// not part of the contract: a reconcile that evicts from several views
// clears them in map order, so the determinism comparison is per URI.
func (c *workspaceRecordingClient) log() map[uri.URI][]string {
	out := make(map[uri.URI][]string)

	for _, publish := range c.publishes {
		out[publish.URI] = append(out[publish.URI], publish.String())
	}

	return out
}

func (c *workspaceRecordingClient) ShowMessage(context.Context, *protocol.ShowMessageParams) error {
	return nil
}

func (c *workspaceRecordingClient) LogMessage(context.Context, *protocol.LogMessageParams) error {
	return nil
}

func (c *workspaceRecordingClient) RegisterCapability(context.Context, *protocol.RegistrationParams) error {
	return nil
}

// installPropertySnapshots is the test-side installSnapshots for a fuzz stream: one
// reconcile over the folder snapshots, validated as the commit path validates
// them. It is separate from the *testing.T helper.
func installPropertySnapshots(tb require.TestingT, srv *Server, snaps map[uri.URI]WorkspaceSnapshot) {
	w := srv.workspace
	w.mu.Lock()
	defer w.mu.Unlock()

	for folder, snap := range snaps {
		state, ok := w.folders[folder]
		if !ok {
			state = &workspaceFolder{}
			w.folders[folder] = state
		}

		state.snapshot = validateWorkspaceSnapshot(folder, cloneWorkspaceSnapshot(snap))
		state.cancel = nil
	}

	w.reconcileLocked(context.Background())
}

// workspacePropertyModel is the reference routing state: the folder snapshots
// as installed, the open documents, and the routing derived from both. It
// follows the documented contract:
//
//   - invalid projects are rejected into issues and never routed,
//   - the first project per root wins, and a later project on the same root
//     with different include paths becomes a conflict issue and is dropped,
//   - targets route to the longest containing root, else to their project root,
//   - open documents route through their containing root,
//   - owned files are a root's targets plus the documents it owns.
type workspacePropertyModel struct {
	folders   map[uri.URI]WorkspaceSnapshot
	documents map[uri.URI]struct{}

	roots   map[uri.URI]Project
	targets map[uri.URI]uri.URI

	// issues counts the workspace issues published per URI. Counts, not
	// messages: the message text belongs to the validator that produced it,
	// while attribution to a URI and accumulation across folders are the
	// routing contract.
	issues map[uri.URI]int

	// rejected counts projects validation threw out, and conflicts counts
	// same-root projects with different include paths. They let the test
	// prove the generator reaches those classes.
	rejected  int
	conflicts int
}

func newWorkspacePropertyModel(folders []uri.URI) *workspacePropertyModel {
	model := &workspacePropertyModel{
		folders:   make(map[uri.URI]WorkspaceSnapshot, len(folders)),
		documents: make(map[uri.URI]struct{}),
		roots:     make(map[uri.URI]Project),
		targets:   make(map[uri.URI]uri.URI),
		issues:    make(map[uri.URI]int),
	}

	for _, folder := range folders {
		model.folders[folder] = WorkspaceSnapshot{}
	}

	model.reconcile()

	return model
}

// propertyValidFileURI re-derives project URI validation from its documented
// rule: file scheme, non-empty path.
func propertyValidFileURI(value uri.URI) bool {
	if value == "" {
		return false
	}

	parsed, err := uri.ParseStrict(string(value))
	if err != nil {
		return false
	}

	return parsed.IsFile() && parsed.Path() != ""
}

func propertyProjectValid(project Project) bool {
	if !propertyValidFileURI(project.RootURI) || !propertyValidFileURI(project.ConfigURI) {
		return false
	}

	for _, target := range project.TargetFiles {
		if !propertyValidFileURI(target) {
			return false
		}
	}

	return true
}

func propertyIssueURI(folder uri.URI, project Project) uri.URI {
	if propertyValidFileURI(project.ConfigURI) {
		return project.ConfigURI
	}

	if propertyValidFileURI(folder) {
		return folder
	}

	return ""
}

func (m *workspacePropertyModel) reconcile() {
	m.roots = make(map[uri.URI]Project)
	m.targets = make(map[uri.URI]uri.URI)
	m.issues = make(map[uri.URI]int)

	var accepted []Project

	for _, folder := range sortedURIs(m.folders) {
		snapshot := m.folders[folder]
		for _, project := range snapshot.Projects {
			if !propertyProjectValid(project) {
				m.rejected++

				if issueURI := propertyIssueURI(folder, project); issueURI != "" {
					m.issues[issueURI]++
				}

				continue
			}

			previous, exists := m.roots[project.RootURI]
			if exists && !slices.Equal(previous.IncludePaths, project.IncludePaths) {
				m.conflicts++
				m.issues[project.ConfigURI]++

				continue
			}

			if !exists {
				m.roots[project.RootURI] = project
			}

			accepted = append(accepted, project)
		}

		for _, issue := range snapshot.Issues {
			m.issues[issue.URI]++
		}
	}

	for _, project := range accepted {
		for _, target := range project.TargetFiles {
			if _, routed := m.targets[target]; routed {
				continue
			}

			if root, ok := m.rootFor(target); ok {
				m.targets[target] = root
			} else {
				m.targets[target] = project.RootURI
			}
		}
	}
}

func (m *workspacePropertyModel) rootFor(file uri.URI) (uri.URI, bool) {
	best := uri.URI("")

	for root := range m.roots {
		if !propertyContainsURI(root, file) {
			continue
		}

		if best == "" || len(root.Path()) > len(best.Path()) {
			best = root
		}
	}

	if best != "" {
		return best, true
	}

	root, ok := m.targets[file]

	return root, ok
}

// propertyContainsURI re-derives the documented containment rule: the file's
// path sits under the root's directory, boundary at a separator. A root of
// "/" therefore contains every absolute path.
func propertyContainsURI(root, file uri.URI) bool {
	return strings.HasPrefix(file.Path(), strings.TrimSuffix(root.Path(), "/")+"/")
}

func (m *workspacePropertyModel) ownerOf(file uri.URI) (uri.URI, bool) {
	if root, ok := m.targets[file]; ok {
		return root, true
	}

	if _, open := m.documents[file]; open {
		return m.rootFor(file)
	}

	return "", false
}

func (m *workspacePropertyModel) ownedFiles(root uri.URI) []uri.URI {
	var files []uri.URI

	for target, owner := range m.targets {
		if owner == root {
			files = append(files, target)
		}
	}

	for document := range m.documents {
		if owner, ok := m.ownerOf(document); ok && owner == root {
			files = append(files, document)
		}
	}

	slices.Sort(files)

	return slices.Compact(files)
}

type workspacePropertyOpKind int

const (
	opInstallSnapshot workspacePropertyOpKind = iota
	opChangeFolders
	opOpenDocument
	opChangeDocument
	opCloseDocument
	// opLifecycle opens and immediately closes a file inside a root that no
	// project targets: the exact history that must evict the overlay.
	opLifecycle
)

type workspacePropertyOp struct {
	kind     workspacePropertyOpKind
	folder   int
	snapshot WorkspaceSnapshot
	loadFail bool
	added    []int
	removed  []int
	file     int
	revision int
}

func drawWorkspacePropertyProject(t *fuzzgen.T, name string) Project {
	project := Project{
		ConfigURI: uri.File("/cfg/" + name + ".json"),
		RootURI:   fuzzgen.SampledFrom(workspacePropertyRoots).Draw(t),
		TargetFiles: fuzzgen.SliceOfNDistinct(
			fuzzgen.SampledFrom(workspacePropertyFiles), 0, 3, func(u uri.URI) uri.URI { return u },
		).Draw(t),
	}

	if fuzzgen.Bool().Draw(t) {
		project.IncludePaths = fuzzgen.SampledFrom([][]string{
			{"/inc"},
			{"/inc", "/inc2"},
		}).Draw(t)
	}

	// One in six projects is deliberately invalid: validation must reject it
	// into an issue while every valid project in the same snapshot survives.
	switch fuzzgen.IntRange(0, 5).Draw(t) {
	case 0:
		project.RootURI = uri.URI("not a uri")
	case 1:
		project.ConfigURI = uri.URI("https://example.test/config.json")
	case 2:
		project.ConfigURI = ""
	case 3:
		project.TargetFiles = append(slices.Clone(project.TargetFiles), uri.URI(""))
	case 4:
		project.RootURI = uri.URI("file://")
	}

	return project
}

func drawWorkspacePropertySnapshot(t *fuzzgen.T, name string) WorkspaceSnapshot {
	var snapshot WorkspaceSnapshot

	count := fuzzgen.IntRange(0, 3).Draw(t)
	for i := 0; i < count; i++ {
		snapshot.Projects = append(snapshot.Projects, drawWorkspacePropertyProject(t, fmt.Sprintf("%s.p%d", name, i)))
	}

	if fuzzgen.Bool().Draw(t) {
		snapshot.Issues = []WorkspaceIssue{{
			URI:     uri.File("/issue/" + name + ".json"),
			Message: "discovery failed",
		}}
	}

	return snapshot
}

func drawWorkspacePropertyOp(t *fuzzgen.T, name string) workspacePropertyOp {
	switch fuzzgen.IntRange(0, 9).Draw(t) {
	case 0, 1, 2, 3:
		op := workspacePropertyOp{
			kind:     opInstallSnapshot,
			folder:   fuzzgen.IntRange(0, len(workspacePropertyFolders)-1).Draw(t),
			snapshot: drawWorkspacePropertySnapshot(t, name+".snapshot"),
			loadFail: fuzzgen.IntRange(0, 5).Draw(t) == 0,
		}

		return op
	case 4, 5:
		op := workspacePropertyOp{
			kind:    opChangeFolders,
			added:   fuzzgen.SliceOfNDistinct(fuzzgen.IntRange(0, len(workspacePropertyFolders)-1), 0, 2, func(i int) int { return i }).Draw(t),
			removed: fuzzgen.SliceOfNDistinct(fuzzgen.IntRange(0, len(workspacePropertyFolders)-1), 0, 2, func(i int) int { return i }).Draw(t),
		}

		return op
	case 6, 7:
		return workspacePropertyOp{
			kind:     opOpenDocument,
			file:     fuzzgen.IntRange(0, len(workspacePropertyFiles)-1).Draw(t),
			revision: fuzzgen.IntRange(0, 3).Draw(t),
		}
	case 8:
		switch fuzzgen.IntRange(0, 2).Draw(t) {
		case 0:
			return workspacePropertyOp{kind: opLifecycle, file: fuzzgen.IntRange(0, len(workspacePropertyFiles)-1).Draw(t)}
		default:
			return workspacePropertyOp{
				kind:     opChangeDocument,
				file:     fuzzgen.IntRange(0, len(workspacePropertyFiles)-1).Draw(t),
				revision: fuzzgen.IntRange(0, 3).Draw(t),
			}
		}
	default:
		return workspacePropertyOp{
			kind: opCloseDocument,
			file: fuzzgen.IntRange(0, len(workspacePropertyFiles)-1).Draw(t),
		}
	}
}

// workspacePropertyCoverage aggregates which routing classes a run reached.
// The suite asserts that the interesting ones occur, so a generator
// rewrite cannot silently stop producing the cases the assertions need.
type workspacePropertyCoverage struct {
	cases            int
	installs         int
	loadFailures     int
	reusedViews      int
	recreatedViews   int
	evictedFiles     int
	ownedDocuments   int
	unownedDocuments int
	closedOverlays   int
	conflictIssues   int
	rejectedProjects int
}

type workspacePropertyDriver struct {
	tb       require.TestingT
	srv      *Server
	client   *workspaceRecordingClient
	model    *workspacePropertyModel
	views    map[uri.URI]*store.View
	known    map[uri.URI]map[uri.URI]struct{}
	roots    map[uri.URI]Project
	coverage *workspacePropertyCoverage

	// files is the URI universe this driver routes; diagnostics records
	// which of them carry a finding on disk. seeded is non-nil only for the
	// implicit-folder driver, where the default loader derives each folder's
	// snapshot from the memFS tree instead of a stub.
	files       []uri.URI
	diagnostics map[uri.URI]bool
	seeded      map[uri.URI]string
	implicit    bool
}

func newWorkspacePropertyDriver(tb require.TestingT, folders []uri.URI) *workspacePropertyDriver {
	seeded := make(map[uri.URI]string, len(workspacePropertyFiles))
	diagnostics := make(map[uri.URI]bool, len(workspacePropertyFiles))

	for i, file := range workspacePropertyFiles {
		seeded[file] = workspacePropertyContent(i)
		diagnostics[file] = workspacePropertyHasDiagnostic(i)
	}

	return newWorkspacePropertyDriverWith(tb, folders, workspacePropertyFiles, seeded, diagnostics, true)
}

// newWorkspacePropertyDriverWith builds the server and driver over one file
// universe, leaving the server's loader as the default one so the
// implicit-folder driver can use it.
func newWorkspacePropertyDriverWith(tb require.TestingT, folders []uri.URI, universe []uri.URI, seeded map[uri.URI]string, diagnostics map[uri.URI]bool, stubLoader bool) *workspacePropertyDriver {
	files := make(map[uri.URI][]byte, len(universe))
	for file, content := range seeded {
		files[file] = []byte(content)
	}

	client := &workspaceRecordingClient{}
	srv := newSyncServerWithOptions(client, files, Options{ConfigSource: options.PinnedSource(nil)})

	// The stub-loader driver owns its snapshots: no implicit folders, no
	// recursive default scan.
	if stubLoader {
		srv.workspace = newWorkspace(srv, nil)
	}

	_, err := srv.Initialize(context.Background(), testInitializeParams(foldersFromURIs(folders)))
	require.NoError(tb, err)

	return &workspacePropertyDriver{
		tb:          tb,
		srv:         srv,
		client:      client,
		model:       newWorkspacePropertyModel(folders),
		views:       map[uri.URI]*store.View{},
		known:       map[uri.URI]map[uri.URI]struct{}{},
		roots:       map[uri.URI]Project{},
		coverage:    &workspacePropertyCoverage{},
		files:       universe,
		diagnostics: diagnostics,
		seeded:      seeded,
		implicit:    !stubLoader,
	}
}

func (d *workspacePropertyDriver) content(index, revision int) string {
	return fmt.Sprintf("// rev %d\nstruct Overlay%d { 1: i32 value }\n", revision, index)
}

func (d *workspacePropertyDriver) install(folder int, snapshot WorkspaceSnapshot, loadFail bool) {
	target := workspacePropertyFolders[folder]

	// A failed load keeps the folder's previous snapshot: commit skips the
	// result and the state is left untouched.
	if loadFail {
		d.coverage.loadFailures++
		d.observe(false)

		return
	}

	d.coverage.installs++
	d.model.folders[target] = snapshot
	d.model.reconcile()
	installPropertySnapshots(d.tb, d.srv, map[uri.URI]WorkspaceSnapshot{target: snapshot})

	d.observe(true)
}

func (d *workspacePropertyDriver) changeFolders(added, removed []int) {
	addedURIs := make([]protocol.WorkspaceFolder, 0, len(added))
	for _, index := range added {
		addedURIs = append(addedURIs, protocol.WorkspaceFolder{URI: workspacePropertyFolders[index]})
	}

	removedURIs := make([]protocol.WorkspaceFolder, 0, len(removed))
	for _, index := range removed {
		removedURIs = append(removedURIs, protocol.WorkspaceFolder{URI: workspacePropertyFolders[index]})
	}

	require.NoError(d.tb, d.srv.DidChangeWorkspaceFolders(context.Background(), &protocol.DidChangeWorkspaceFoldersParams{
		Event: protocol.WorkspaceFoldersChangeEvent{Added: addedURIs, Removed: removedURIs},
	}))

	removedAny := false
	for _, index := range removed {
		folder := workspacePropertyFolders[index]
		if _, ok := d.model.folders[folder]; ok {
			delete(d.model.folders, folder)
			removedAny = true
		}
	}

	if removedAny {
		d.model.reconcile()
	}

	for _, index := range added {
		folder := workspacePropertyFolders[index]
		if _, ok := d.model.folders[folder]; ok {
			continue
		}

		d.model.folders[folder] = WorkspaceSnapshot{}
	}

	d.observe(removedAny)
}

func (d *workspacePropertyDriver) open(file int) {
	reconciled := false
	target := d.files[file]

	// With the default loader, opening a file no project owns and no root
	// contains loads its directory as an implicit folder, which reconciles
	// before the overlay is applied. The snapshot the default loader hands
	// back is the folder itself plus every known *.thrift file beneath it.
	if d.implicit && !d.modelOwns(target) && !d.modelContains(target) {
		folder := uri.File(filepath.Dir(target.FsPath()))
		d.model.folders[folder] = defaultLoaderSnapshot(folder, d.seeded)
		d.model.reconcile()
		reconciled = true
	}

	require.NoError(d.tb, d.srv.DidOpen(context.Background(), &protocol.DidOpenTextDocumentParams{
		TextDocument: protocol.TextDocumentItem{
			URI:        target,
			LanguageID: LanguageIDThrift,
			Version:    0,
			Text:       d.content(file, 0),
		},
	}))

	d.model.documents[target] = struct{}{}

	d.observe(reconciled)
}

// lifecycle opens and closes one file in a single step. It prefers a file
// inside a known root that no project targets and that is not already open,
// because closing that file is what exercises the eviction path; the choice
// depends only on routing state both sides share, so it stays deterministic.
func (d *workspacePropertyDriver) lifecycle(fallback int) {
	chosen := fallback

	for i, file := range d.files {
		if _, isTarget := d.model.targets[file]; isTarget {
			continue
		}

		if _, owned := d.model.ownerOf(file); !owned {
			continue
		}

		chosen = i

		break
	}

	d.open(chosen)
	d.close(chosen)
}

func (d *workspacePropertyDriver) modelOwns(file uri.URI) bool {
	_, ok := d.model.ownerOf(file)

	return ok
}

func (d *workspacePropertyDriver) modelContains(file uri.URI) bool {
	_, ok := d.model.rootFor(file)

	return ok
}

// defaultLoaderSnapshot re-derives defaultLoader's snapshot: one project
// rooted at the folder, targeting every known *.thrift file under it.
func defaultLoaderSnapshot(folder uri.URI, seeded map[uri.URI]string) WorkspaceSnapshot {
	var targets []uri.URI

	for file := range seeded {
		if propertyContainsURI(folder, file) && strings.HasSuffix(file.Path(), ".thrift") {
			targets = append(targets, file)
		}
	}

	slices.Sort(targets)

	return WorkspaceSnapshot{Projects: []Project{{
		ConfigURI:   uri.File(filepath.Join(folder.FsPath(), options.ConfigFileName)),
		RootURI:     folder,
		TargetFiles: targets,
	}}}
}

func (d *workspacePropertyDriver) change(file, revision int) {
	target := d.files[file]
	require.NoError(d.tb, d.srv.DidChange(context.Background(), &protocol.DidChangeTextDocumentParams{
		TextDocument: protocol.VersionedTextDocumentIdentifier{
			TextDocumentIdentifier: protocol.TextDocumentIdentifier{URI: target},
			Version:                int32(revision + 1), //nolint:gosec // small revisions
		},
		ContentChanges: []protocol.TextDocumentContentChangeEvent{
			&protocol.TextDocumentContentChangeWholeDocument{Text: d.content(file, revision)},
		},
	}))

	d.model.documents[target] = struct{}{}

	d.observe(false)
}

func (d *workspacePropertyDriver) close(file int) {
	target := d.files[file]

	// Closing an owned document is what makes a view forget it.
	if _, isTarget := d.model.targets[target]; !isTarget {
		if _, open := d.model.documents[target]; open {
			if _, owned := d.model.ownerOf(target); owned {
				d.coverage.closedOverlays++
			}
		}
	}

	require.NoError(d.tb, d.srv.DidClose(context.Background(), &protocol.DidCloseTextDocumentParams{
		TextDocument: protocol.TextDocumentIdentifier{URI: target},
	}))

	delete(d.model.documents, target)

	d.observe(false)
}

// observe checks the live server against the model. reconciled reports
// whether the event recomputed the routing; when it did not, the model and
// the server's views must be untouched.
func (d *workspacePropertyDriver) observe(reconciled bool) {
	w := d.srv.workspace

	w.mu.Lock()
	gotRoots := make(map[uri.URI]Project, len(w.model.roots))
	for root, project := range w.model.roots {
		gotRoots[root] = project
	}

	gotTargets := make(map[uri.URI]uri.URI, len(w.model.targets))
	for target, owner := range w.model.targets {
		gotTargets[target] = owner
	}

	gotIssues := make(map[uri.URI]int, len(w.model.issues))
	for issueURI, issues := range w.model.issues {
		gotIssues[issueURI] = len(issues)
	}

	gotViews := make(map[uri.URI]*store.View, len(w.views))
	for root, view := range w.views {
		gotViews[root] = view
	}

	gotDocuments := make(map[uri.URI]struct{}, len(w.documents))
	for document := range w.documents {
		gotDocuments[document] = struct{}{}
	}
	w.mu.Unlock()

	require.Equal(d.tb, d.model.roots, gotRoots, "accepted root projects")
	require.Equal(d.tb, d.model.targets, gotTargets, "target routing")
	require.Equal(d.tb, d.model.issues, gotIssues, "workspace issue counts per URI")
	require.Equal(d.tb, d.model.documents, gotDocuments, "open document set")
	require.Equal(d.tb, sortedURIs(d.model.roots), sortedURIs(gotViews), "one view per accepted root")

	// View identity: a view is reused exactly when its root survived the
	// reconcile with unchanged include paths, and recreated otherwise.
	for root, view := range gotViews {
		previous, had := d.views[root]
		if !had {
			continue
		}

		previousProject, hadRoot := d.roots[root]
		reused := !reconciled || (hadRoot && slices.Equal(previousProject.IncludePaths, d.model.roots[root].IncludePaths))
		if reused {
			d.coverage.reusedViews++

			require.Same(d.tb, previous, view, "view for %s must be reused", root)
		} else {
			d.coverage.recreatedViews++

			require.NotSame(d.tb, previous, view, "view for %s must be recreated", root)
		}
	}

	// Each root's view holds exactly the files the model says it owns, and
	// nothing else: a closed document or a lost target must leave the view.
	known := make(map[uri.URI]struct{})
	knownByRoot := make(map[uri.URI]map[uri.URI]struct{}, len(gotViews))

	for root, view := range gotViews {
		require.Equal(d.tb, root, view.Folder(), "view folder")

		files := view.KnownFiles()
		slices.Sort(files)

		owned := d.model.ownedFiles(root)
		if !slices.Equal(owned, files) {
			extra := difference(files, owned)
			missing := difference(owned, files)

			require.Failf(d.tb, "view ownership mismatch",
				"root %s: view knows %v, model owns %v (stale %v, missing %v)", root, files, owned, extra, missing)
		}

		knownByRoot[root] = make(map[uri.URI]struct{}, len(files))

		for _, file := range files {
			known[file] = struct{}{}
			knownByRoot[root][file] = struct{}{}
		}
	}

	// Ownership through the server's own entry points agrees with the model.
	for _, file := range d.files {
		wantOwner, wantOwned := d.model.ownerOf(file)

		require.Equal(d.tb, wantOwned, d.srv.workspace.owns(file), "owns(%s)", file)

		view, err := d.srv.workspace.viewOf(file)
		if !wantOwned {
			require.Error(d.tb, err, "viewOf(%s) must fail for an unowned file", file)

			continue
		}

		require.NoError(d.tb, err, "viewOf(%s)", file)
		require.NotNil(d.tb, view)
		require.Equal(d.tb, wantOwner, view.Folder(), "owner of %s", file)

		require.Equal(d.tb, func() bool {
			_, open := d.model.documents[file]
			return open
		}(), d.srv.session.HasOverlay(file), "overlay presence for %s", file)
	}

	// Evictions: a file present in a view before and absent now left it,
	// which is what a closed document or a lost target must produce.
	for root, before := range d.known {
		after := knownByRoot[root]

		for file := range before {
			if _, still := after[file]; !still {
				d.coverage.evictedFiles++
			}
		}
	}

	for document := range gotDocuments {
		if _, owned := known[document]; owned {
			d.coverage.ownedDocuments++
		} else {
			d.coverage.unownedDocuments++
		}
	}

	// A file no view owns keeps no diagnostics: stale findings must be
	// cleared when a root loses a file.
	for _, file := range d.files {
		if _, owned := known[file]; owned {
			continue
		}

		require.Empty(d.tb, d.client.last(file), "diagnostics for unowned %s must be cleared", file)
	}

	// An owned file that is not open shows exactly its disk findings.
	for _, file := range d.files {
		if _, owned := known[file]; !owned {
			continue
		}

		if _, open := d.model.documents[file]; open {
			continue
		}

		require.Equal(d.tb, d.diagnostics[file], len(d.client.last(file)) > 0,
			"diagnostics for owned %s must match its disk content", file)
	}

	d.views = gotViews
	d.known = knownByRoot
	d.roots = gotRoots
}

// runWorkspacePropertyTrace replays a whole history and returns the final
// server, client, and driver for the determinism comparison.
func runWorkspacePropertyTrace(tb require.TestingT, folders []uri.URI, initial map[uri.URI]WorkspaceSnapshot, ops []workspacePropertyOp, coverage *workspacePropertyCoverage) *workspacePropertyDriver {
	driver := newWorkspacePropertyDriver(tb, folders)
	driver.coverage = coverage

	snaps := make(map[uri.URI]WorkspaceSnapshot, len(initial))
	for folder, snapshot := range initial {
		snaps[folder] = snapshot
	}

	for _, folder := range folders {
		driver.model.folders[folder] = snaps[folder]
	}

	driver.model.reconcile()
	installPropertySnapshots(tb, driver.srv, snaps)
	driver.observe(true)

	for _, op := range ops {
		switch op.kind {
		case opInstallSnapshot:
			driver.install(op.folder, op.snapshot, op.loadFail)
		case opChangeFolders:
			driver.changeFolders(op.added, op.removed)
		case opOpenDocument:
			driver.open(op.file)
		case opChangeDocument:
			driver.change(op.file, op.revision)
		case opCloseDocument:
			driver.close(op.file)
		case opLifecycle:
			driver.lifecycle(op.file)
		}
	}

	return driver
}

// workspaceRoutingCase runs one generated routing history: it checks the
// server against the model after every event and replays the sequence on a
// fresh server, accumulating the classes the corpus reached in coverage.
func workspaceRoutingCase(t *fuzzgen.T, coverage *workspacePropertyCoverage) {
	coverage.cases++

	folderCount := fuzzgen.IntRange(1, len(workspacePropertyFolders)).Draw(t)
	folders := workspacePropertyFolders[:folderCount]

	initial := make(map[uri.URI]WorkspaceSnapshot, len(folders))
	for i, folder := range folders {
		initial[folder] = drawWorkspacePropertySnapshot(t, fmt.Sprintf("initial%d", i))
	}

	opCount := fuzzgen.IntRange(1, 8).Draw(t)
	ops := make([]workspacePropertyOp, 0, opCount+1)
	for i := 0; i < opCount; i++ {
		ops = append(ops, drawWorkspacePropertyOp(t, fmt.Sprintf("op%d", i)))
	}

	// Every history ends by closing an overlay-only file, so the
	// eviction path runs whatever the drawn operations did.
	ops = append(ops, workspacePropertyOp{kind: opLifecycle})

	first := runWorkspacePropertyTrace(t, folders, initial, ops, coverage)
	second := runWorkspacePropertyTrace(t, folders, initial, ops, coverage)

	// Determinism: the same history on a fresh server routes the same
	// files and publishes the same diagnostics, with no map-order leak.
	require.Equal(t, first.model.roots, second.model.roots, "replayed routing roots")
	require.Equal(t, first.model.targets, second.model.targets, "replayed target routing")
	require.Equal(t, first.client.log(), second.client.log(), "replayed publish log")

	coverage.conflictIssues += first.model.conflicts
	coverage.rejectedProjects += first.model.rejected
}

// workspaceRoutingSeeds is the routing fuzz target's seed corpus; the
// deterministic coverage run derives a larger corpus from the same shape.
var workspaceRoutingSeeds = proptest.Corpus{
	[]byte(""),
	[]byte("one folder"),
	[]byte("two folders"),
	[]byte("installs and closes"),
	[]byte("open documents"),
	[]byte("folder changes"),
}

// FuzzWorkspaceRoutingModelProperty is the routing suite: generated folder
// snapshots and document histories, checked against the reference model after
// every event, then replayed on a fresh server to prove the sequence is
// deterministic (same routing, same publish log).
func FuzzWorkspaceRoutingModelProperty(f *testing.F) {
	proptest.Fuzz(f, workspaceRoutingSeeds, func(tb *testing.T, seed []byte) {
		workspaceRoutingCase(fuzzgen.New(tb, seed), &workspacePropertyCoverage{})
	})
}

// workspaceCoverageCases is how many derived seeds the deterministic coverage
// runs replay: enough that every class the routing assertions name occurs.
const workspaceCoverageCases = 60

// runSeedCorpus runs one case per derived seed, so a coverage assertion holds
// on the seed run of `go test` without depending on how far fuzzing got.
func runSeedCorpus(t *testing.T, count int, run func(*fuzzgen.T)) {
	t.Helper()

	proptest.Range(count, func(i int) string {
		return fmt.Sprintf("coverage case %d", i)
	}).Run(t, run)
}

// TestWorkspaceRoutingCoverage replays a fixed corpus through the routing case
// and asserts the classes the suite depends on: a generator rewrite that stops
// producing them fails here instead of silently weakening the routing checks.
func TestWorkspaceRoutingCoverage(t *testing.T) {
	coverage := &workspacePropertyCoverage{}

	runSeedCorpus(t, workspaceCoverageCases, func(t *fuzzgen.T) {
		workspaceRoutingCase(t, coverage)
	})

	t.Logf("workspace routing coverage: %+v", *coverage)

	require.Positive(t, coverage.reusedViews, "generated history must reuse a view across reconciles")
	require.Positive(t, coverage.evictedFiles, "generated history must evict a file from a view")
	require.Positive(t, coverage.rejectedProjects, "generated history must produce a rejected project")
	require.Positive(t, coverage.ownedDocuments, "generated history must own some open documents")
	require.Positive(t, coverage.closedOverlays, "generated history must close an owned overlay-only document")
	require.Positive(t, coverage.unownedDocuments, "generated history must leave some open documents unowned")
	require.Positive(t, coverage.loadFailures, "generated history must include a failed folder load")
}

// Implicit-folder routing: with the default loader, opening a file no
// project owns loads its directory as a folder. The server's real loadSync
// path runs here (no stub snapshot), so the model re-derives defaultLoader's
// snapshot from the seeded tree and checks routing after every event.

var implicitFolderUniverse = []uri.URI{
	uri.File("/alpha/one.thrift"),
	uri.File("/alpha/two.thrift"), // never written to disk: overlay only
	uri.File("/alpha/nested/three.thrift"),
	uri.File("/beta/four.thrift"),
	uri.File("/gamma/five.thrift"), // never written to disk: overlay only
}

func implicitFolderDiskContents() (map[uri.URI]string, map[uri.URI]bool) {
	seeded := map[uri.URI]string{
		uri.File("/alpha/one.thrift"):          workspacePropertyContents[0],
		uri.File("/alpha/nested/three.thrift"): workspacePropertyContents[1],
		uri.File("/beta/four.thrift"):          workspacePropertyContents[2],
	}
	diagnostics := map[uri.URI]bool{
		uri.File("/alpha/one.thrift"):          false,
		uri.File("/alpha/nested/three.thrift"): true,
		uri.File("/beta/four.thrift"):          true,
	}

	return seeded, diagnostics
}

func drawImplicitFolderOp(t *fuzzgen.T) workspacePropertyOp {
	if fuzzgen.Bool().Draw(t) {
		return workspacePropertyOp{
			kind: opCloseDocument,
			file: fuzzgen.IntRange(0, len(implicitFolderUniverse)-1).Draw(t),
		}
	}

	return workspacePropertyOp{
		kind:     opOpenDocument,
		file:     fuzzgen.IntRange(0, len(implicitFolderUniverse)-1).Draw(t),
		revision: fuzzgen.IntRange(0, 2).Draw(t),
	}
}

// implicitFolderRoutingCase opens and closes one generated history against
// the server's real loader.
func implicitFolderRoutingCase(t *fuzzgen.T, coverage *workspacePropertyCoverage, seeded map[uri.URI]string, diagnostics map[uri.URI]bool) {
	coverage.cases++

	driver := newWorkspacePropertyDriverWith(t, nil, implicitFolderUniverse, seeded, diagnostics, false)
	driver.coverage = coverage
	driver.observe(false)

	opCount := fuzzgen.IntRange(1, 6).Draw(t)
	for i := 0; i < opCount; i++ {
		op := drawImplicitFolderOp(t)

		switch op.kind {
		case opOpenDocument:
			driver.open(op.file)

			require.NotEmpty(t, driver.model.roots,
				"opening %s must load a directory as a folder", implicitFolderUniverse[op.file])
		case opCloseDocument:
			driver.close(op.file)
		default:
			t.Fatalf("unexpected op %v", op.kind)
		}
	}
}

// implicitFolderSeeds is the implicit-folder fuzz target's seed corpus.
var implicitFolderSeeds = proptest.Corpus{
	[]byte(""),
	[]byte("alpha only"),
	[]byte("nested directory"),
	[]byte("two directories"),
	[]byte("overlay only"),
	[]byte("reopen after close"),
}

// FuzzImplicitFolderRouting drives the default loader through generated
// open/close histories: every first open of a directory loads that
// directory as a folder (one reconcile), every later open joins it, and
// closing an overlay-only file evicts it from the view it joined.
func FuzzImplicitFolderRouting(f *testing.F) {
	proptest.Fuzz(f, implicitFolderSeeds, func(tb *testing.T, seed []byte) {
		seeded, diagnostics := implicitFolderDiskContents()

		implicitFolderRoutingCase(fuzzgen.New(tb, seed), &workspacePropertyCoverage{}, seeded, diagnostics)
	})
}

// TestImplicitFolderCoverage replays a fixed corpus through the implicit-
// folder case and asserts the two classes it must reach.
func TestImplicitFolderCoverage(t *testing.T) {
	coverage := &workspacePropertyCoverage{}
	seeded, diagnostics := implicitFolderDiskContents()

	runSeedCorpus(t, workspaceCoverageCases, func(t *fuzzgen.T) {
		implicitFolderRoutingCase(t, coverage, seeded, diagnostics)
	})

	t.Logf("implicit folder coverage: %+v", *coverage)

	require.Positive(t, coverage.reusedViews, "a second open in a loaded directory must reuse its view")
	require.Positive(t, coverage.evictedFiles, "closing an overlay-only file must evict it from its view")
}

// TestViewRecreationAndConflictIssues pins the two reconcile decisions the
// generated histories reach only occasionally, deterministically: a
// surviving root whose include paths changed is rebuilt, and a second
// project on one root with different include paths becomes an issue at its
// config URI while the first project's view keeps serving the root.
func TestViewRecreationAndConflictIssues(t *testing.T) {
	root := uri.File("/ws")
	other := uri.File("/other")
	target := uri.File("/ws/a.thrift")
	config := uri.File("/ws/cfg.json")
	loserConfig := uri.File("/ws/cfg.json")
	client := &workspaceRecordingClient{}

	srv := newSyncServerWithOptions(client,
		map[uri.URI][]byte{target: []byte("struct A {}\n")},
		Options{ConfigSource: options.PinnedSource(nil)})
	srv.workspace = newWorkspace(srv, nil)

	_, err := srv.Initialize(context.Background(), testInitializeParams(foldersFromURIs([]uri.URI{root, other})))
	require.NoError(t, err)

	installPropertySnapshots(t, srv, map[uri.URI]WorkspaceSnapshot{
		root: {Projects: []Project{{
			ConfigURI:   config,
			RootURI:     root,
			TargetFiles: []uri.URI{target},
		}}},
	})

	first, err := srv.workspace.viewOf(target)
	require.NoError(t, err)
	require.True(t, first.FileKnown(target))

	// Same root, different include paths: the view must be rebuilt, not
	// silently kept with stale resolution settings.
	installPropertySnapshots(t, srv, map[uri.URI]WorkspaceSnapshot{
		root: {Projects: []Project{{
			ConfigURI:    config,
			RootURI:      root,
			TargetFiles:  []uri.URI{target},
			IncludePaths: []string{"/inc"},
		}}},
	})

	second, err := srv.workspace.viewOf(target)
	require.NoError(t, err)
	require.NotSame(t, first, second, "a changed include path must rebuild the view")
	require.Equal(t, []uri.URI{target}, second.KnownFiles())

	// "/other" sorts before "/ws", so its project claims the root first and
	// the later project with different include paths is dropped into an
	// issue at its own config URI.
	installPropertySnapshots(t, srv, map[uri.URI]WorkspaceSnapshot{
		other: {Projects: []Project{{
			ConfigURI:    uri.File("/other/cfg.json"),
			RootURI:      root,
			TargetFiles:  []uri.URI{target},
			IncludePaths: []string{"/other-inc"},
		}}},
		root: {Projects: []Project{{
			ConfigURI:    config,
			RootURI:      root,
			TargetFiles:  []uri.URI{target},
			IncludePaths: []string{"/inc"},
		}}},
	})

	third, err := srv.workspace.viewOf(target)
	require.NoError(t, err)
	require.True(t, third.FileKnown(target), "the winning project's view keeps serving the root")

	msgs := diagMessages(client.last(loserConfig))
	require.Len(t, msgs, 1, "exactly one conflict issue at the losing config URI")
	require.Contains(t, msgs[0], "different include paths")
}

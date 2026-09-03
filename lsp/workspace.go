package lsp

import (
	"context"
	"fmt"
	"path/filepath"
	"slices"
	"strings"
	"sync"

	"go.lsp.dev/protocol"
	"go.lsp.dev/uri"

	"github.com/karitham/thrift-ls/options"
	"github.com/karitham/thrift-ls/store"
	"github.com/karitham/thrift-ls/vfs"
)

// WorkspaceLoader discovers the projects in one LSP workspace folder. A
// returned error means discovery failed for the folder. Non-fatal project
// failures belong in WorkspaceSnapshot.Issues so valid projects can still be
// indexed.
type WorkspaceLoader func(ctx context.Context, folder uri.URI) (WorkspaceSnapshot, error)

// WorkspaceSnapshot is one immutable result of workspace discovery. The
// loader must not mutate it after returning.
type WorkspaceSnapshot struct {
	Projects []Project
	Issues   []WorkspaceIssue
}

// Project describes one independently configured Thrift project. It is
// the build-system seam: discovery hands thrift-ls roots, files, and
// include paths, and nothing else. Formatting and lint come from the
// config document and CLI layers, never from here.
type Project struct {
	// ConfigURI is the stable identity and source location of the project
	// configuration.
	ConfigURI uri.URI
	// RootURI is the directory whose files use this project's view.
	RootURI uri.URI
	// TargetFiles are the Thrift files to index for the project.
	TargetFiles []uri.URI
	// IncludePaths are the compiler-equivalent include roots for the
	// project. A non-empty list is authoritative over the config document
	// and CLI, because the build system owns resolution. Empty means
	// resolve includes via the server ConfigSource for the root.
	IncludePaths []string
}

// WorkspaceIssue is a non-fatal discovery problem publishable at URI.
type WorkspaceIssue struct {
	URI     uri.URI
	Message string
}

type workspace struct {
	server *Server
	loader WorkspaceLoader

	// implicitFolders lets didOpen grow the workspace: opening a file no
	// project owns loads its directory as a folder. Only the default
	// loader works this way; custom loaders own their files exclusively.
	implicitFolders bool

	mu        sync.Mutex
	folders   map[uri.URI]*workspaceFolder
	model     workspaceModel
	views     map[uri.URI]*store.View
	documents map[uri.URI]struct{}
}

type workspaceFolder struct {
	cancel   context.CancelFunc
	snapshot WorkspaceSnapshot
}

type workspaceLoadResult struct {
	folder   uri.URI
	state    *workspaceFolder
	cancel   context.CancelFunc
	snapshot WorkspaceSnapshot
	err      error
}

type workspaceModel struct {
	roots   map[uri.URI]Project
	targets map[uri.URI]uri.URI
	issues  map[uri.URI][]WorkspaceIssue
}

func newWorkspace(server *Server, loader WorkspaceLoader) *workspace {
	return &workspace{
		server:    server,
		loader:    loader,
		folders:   make(map[uri.URI]*workspaceFolder),
		model:     emptyWorkspaceModel(),
		views:     make(map[uri.URI]*store.View),
		documents: make(map[uri.URI]struct{}),
	}
}

func emptyWorkspaceModel() workspaceModel {
	return workspaceModel{
		roots:   make(map[uri.URI]Project),
		targets: make(map[uri.URI]uri.URI),
		issues:  make(map[uri.URI][]WorkspaceIssue),
	}
}

func cloneWorkspaceSnapshot(snapshot WorkspaceSnapshot) WorkspaceSnapshot {
	out := WorkspaceSnapshot{
		Projects: make([]Project, len(snapshot.Projects)),
		Issues:   slices.Clone(snapshot.Issues),
	}

	for i, project := range snapshot.Projects {
		project.TargetFiles = slices.Clone(project.TargetFiles)
		project.IncludePaths = slices.Clone(project.IncludePaths)
		out.Projects[i] = project
	}

	return out
}

// projectIncludesEqual reports whether two projects resolve includes
// identically. A view is reused only when its include paths are unchanged.
func projectIncludesEqual(a, b []string) bool {
	return slices.Equal(a, b)
}

func validateWorkspaceSnapshot(folder uri.URI, snapshot WorkspaceSnapshot) WorkspaceSnapshot {
	projects := make([]Project, 0, len(snapshot.Projects))

	for _, project := range snapshot.Projects {
		if err := validateProject(project); err != nil {
			logError("workspace project rejected", Expected(fmt.Errorf("workspace %s: %w", folder, err)))

			if issueURI := projectIssueURI(folder, project); issueURI != "" {
				snapshot.Issues = append(snapshot.Issues, WorkspaceIssue{
					URI:     issueURI,
					Message: fmt.Sprintf("project rejected: %v", err),
				})
			}

			continue
		}

		projects = append(projects, project)
	}

	snapshot.Projects = projects

	return snapshot
}

func validateProject(project Project) error {
	if err := validateProjectURI("root URI", project.RootURI); err != nil {
		return err
	}

	if err := validateProjectURI("config URI", project.ConfigURI); err != nil {
		return err
	}

	for i, target := range project.TargetFiles {
		if err := validateProjectURI(fmt.Sprintf("target file %d", i), target); err != nil {
			return err
		}
	}

	return nil
}

func validateProjectURI(name string, value uri.URI) error {
	if value == "" {
		return fmt.Errorf("%s is empty", name)
	}

	parsed, err := uri.ParseStrict(string(value))
	if err != nil {
		return fmt.Errorf("%s is invalid: %w", name, err)
	}

	if !parsed.IsFile() {
		return fmt.Errorf("%s must use the file URI scheme", name)
	}

	if parsed.Path() == "" {
		return fmt.Errorf("%s has an empty path", name)
	}

	return nil
}

func projectIssueURI(folder uri.URI, project Project) uri.URI {
	if validateProjectURI("config URI", project.ConfigURI) == nil {
		return project.ConfigURI
	}

	if validateProjectURI("workspace folder", folder) == nil {
		return folder
	}

	return ""
}

// workspaceModelOf derives all routing and diagnostics ownership from the
// accepted folder snapshots. Folder order makes shared roots deterministic.
func workspaceModelOf(snapshots map[uri.URI]WorkspaceSnapshot) workspaceModel {
	model := emptyWorkspaceModel()
	folders := sortedURIs(snapshots)
	var projects []Project

	for _, folder := range folders {
		snapshot := snapshots[folder]
		for _, project := range snapshot.Projects {
			previous, exists := model.roots[project.RootURI]
			if exists && !projectIncludesEqual(previous.IncludePaths, project.IncludePaths) {
				model.issues[project.ConfigURI] = append(model.issues[project.ConfigURI], WorkspaceIssue{
					URI: project.ConfigURI,
					Message: fmt.Sprintf(
						"project conflicts with %s: root %s has different include paths",
						previous.ConfigURI, project.RootURI,
					),
				})

				continue
			}

			if !exists {
				model.roots[project.RootURI] = project
			}

			projects = append(projects, project)
		}

		for _, issue := range snapshot.Issues {
			model.issues[issue.URI] = append(model.issues[issue.URI], issue)
		}
	}

	for _, project := range projects {
		for _, target := range project.TargetFiles {
			if _, exists := model.targets[target]; exists {
				continue
			}

			root, ok := model.rootFor(target)
			if !ok {
				root = project.RootURI
			}
			model.targets[target] = root
		}
	}

	return model
}

func (m workspaceModel) rootFor(file uri.URI) (uri.URI, bool) {
	var best uri.URI

	for root := range m.roots {
		if !containsURI(root, file) {
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

func (m workspaceModel) ownerOf(file uri.URI, documents map[uri.URI]struct{}) (uri.URI, bool) {
	if root, ok := m.targets[file]; ok {
		return root, true
	}

	if _, ok := documents[file]; ok {
		return m.rootFor(file)
	}

	return "", false
}

func (m workspaceModel) ownedFiles(root uri.URI, documents map[uri.URI]struct{}) []uri.URI {
	files := make([]uri.URI, 0)
	for target, owner := range m.targets {
		if owner == root {
			files = append(files, target)
		}
	}
	for document := range documents {
		owner, ok := m.ownerOf(document, documents)
		if ok && owner == root {
			files = append(files, document)
		}
	}

	slices.Sort(files)

	return slices.Compact(files)
}

func containsURI(root, file uri.URI) bool {
	folder := strings.TrimSuffix(root.Path(), "/")

	return strings.HasPrefix(file.Path(), folder+"/")
}

func (w *workspace) initialize(folders []uri.URI) {
	w.mu.Lock()
	defer w.mu.Unlock()

	for _, folder := range folders {
		if _, exists := w.folders[folder]; !exists {
			w.folders[folder] = &workspaceFolder{}
		}
	}
}

func (w *workspace) start() {
	w.mu.Lock()
	defer w.mu.Unlock()

	w.loadLocked(sortedURIs(w.folders))
}

func (w *workspace) shutdown() {
	w.mu.Lock()
	defer w.mu.Unlock()

	w.loader = nil
	for _, folder := range w.folders {
		if folder.cancel != nil {
			folder.cancel()
			folder.cancel = nil
		}
	}
}

func (w *workspace) changeFolders(added, removed []uri.URI) {
	w.mu.Lock()
	defer w.mu.Unlock()

	removedAny := false
	for _, folder := range removed {
		state, exists := w.folders[folder]
		if !exists {
			continue
		}
		if state.cancel != nil {
			state.cancel()
		}

		delete(w.folders, folder)
		removedAny = true
	}

	if removedAny {
		w.reconcileLocked(context.Background())
	}

	toLoad := make([]uri.URI, 0, len(added))
	for _, folder := range added {
		if _, exists := w.folders[folder]; exists {
			continue
		}

		w.folders[folder] = &workspaceFolder{}
		toLoad = append(toLoad, folder)
	}

	w.loadLocked(toLoad)
}

// loadLocked starts one asynchronous batch. The batch is committed only after
// every loader returns, so every discovered view exists before target routing.
func (w *workspace) loadLocked(folders []uri.URI) {
	if w.loader == nil || len(folders) == 0 {
		return
	}

	loader := w.loader
	results := make([]workspaceLoadResult, 0, len(folders))
	contexts := make([]context.Context, 0, len(folders))

	for _, folder := range folders {
		state, active := w.folders[folder]
		if !active || state.cancel != nil {
			continue
		}

		ctx, cancel := context.WithCancel(context.Background())
		state.cancel = cancel
		results = append(results, workspaceLoadResult{folder: folder, state: state, cancel: cancel})
		contexts = append(contexts, ctx)
	}

	if len(results) == 0 {
		return
	}

	go func() {
		for i := range results {
			snapshot, err := loader(contexts[i], results[i].folder)
			results[i].cancel()
			results[i].snapshot = snapshot
			results[i].err = err
		}

		w.commit(results)
	}()
}

// loadSync runs the loader for folders inline and commits the results. It
// backs implicit folder creation on didOpen and keeps tests deterministic.
func (w *workspace) loadSync(ctx context.Context, folders []uri.URI) {
	w.mu.Lock()
	for _, folder := range folders {
		if _, ok := w.folders[folder]; !ok {
			w.folders[folder] = &workspaceFolder{}
		}
	}
	states := make(map[uri.URI]*workspaceFolder, len(folders))
	for _, folder := range folders {
		states[folder] = w.folders[folder]
	}
	loader := w.loader
	w.mu.Unlock()

	if loader == nil {
		return
	}

	results := make([]workspaceLoadResult, 0, len(folders))
	for _, folder := range folders {
		snapshot, err := loader(ctx, folder)
		results = append(results, workspaceLoadResult{folder: folder, state: states[folder], snapshot: snapshot, err: err})
	}

	w.commit(results)
}

// defaultLoader treats each workspace folder as one project: every
// *.thrift file under the folder is a target, with no opinions of its own
// so each root resolves through the ConfigSource. It keeps folder-based
// sessions working with the workspace as the only workspace.
func defaultLoader(src vfs.FileSource) WorkspaceLoader {
	return func(ctx context.Context, folder uri.URI) (WorkspaceSnapshot, error) {
		var targets []uri.URI

		err := src.WalkFiles(ctx, folder, func(fileURI uri.URI) error {
			if strings.HasSuffix(fileURI.Path(), ".thrift") {
				targets = append(targets, fileURI)
			}

			return nil
		})
		if err != nil {
			return WorkspaceSnapshot{}, err
		}

		slices.Sort(targets)

		return WorkspaceSnapshot{Projects: []Project{{
			ConfigURI:   uri.File(filepath.Join(folder.FsPath(), options.ConfigFileName)),
			RootURI:     folder,
			TargetFiles: targets,
		}}}, nil
	}
}

func (w *workspace) commit(results []workspaceLoadResult) {
	w.mu.Lock()
	defer w.mu.Unlock()

	if w.loader == nil {
		return
	}

	accepted := false
	for _, result := range results {
		folder, active := w.folders[result.folder]
		if !active || folder != result.state {
			continue
		}

		folder.cancel = nil
		accepted = true
		if result.err != nil {
			logError("workspace loading failed", fmt.Errorf("load workspace %s: %w", result.folder, result.err))

			continue
		}

		// Clone crossing into the model: the loader must not mutate its
		// result after returning, and neither path hereafter aliases it.
		folder.snapshot = validateWorkspaceSnapshot(result.folder, cloneWorkspaceSnapshot(result.snapshot))
	}

	if accepted {
		w.reconcileLocked(context.Background())
	}
}

func (w *workspace) reconcileLocked(ctx context.Context) {
	snapshots := make(map[uri.URI]WorkspaceSnapshot)
	for folder, state := range w.folders {
		snapshots[folder] = state.snapshot
	}

	next := workspaceModelOf(snapshots)
	beforeIssues := w.model.issues
	known := make(map[uri.URI][]uri.URI, len(w.views))
	for root, view := range w.views {
		known[root] = view.KnownFiles()
	}

	for root := range w.views {
		project, exists := next.roots[root]
		previous := w.model.roots[root]
		if exists && projectIncludesEqual(previous.IncludePaths, project.IncludePaths) {
			var lost []uri.URI
			for _, file := range w.model.ownedFiles(root, w.documents) {
				owner, owned := next.ownerOf(file, w.documents)
				if !owned || owner != root {
					lost = append(lost, file)
				}
			}

			if len(lost) > 0 {
				w.views[root].Evict(lost...)
				w.server.clearDiagnostics(ctx, lost...)
			}

			continue
		}

		w.views[root].Evict(known[root]...)
		w.server.clearDiagnostics(ctx, known[root]...)
		w.server.removeView(root)
		delete(w.views, root)
	}

	for _, root := range sortedURIs(next.roots) {
		if w.views[root] == nil {
			w.views[root] = w.server.addProjectView(next.roots[root])
		}
	}

	w.model = next

	changes := make(map[uri.URI][]uri.URI, len(w.views))
	for target, root := range next.targets {
		changes[root] = append(changes[root], target)
	}
	for document := range w.documents {
		if root, ok := next.ownerOf(document, w.documents); ok {
			changes[root] = append(changes[root], document)
		}
	}

	w.updateViewsLocked(ctx, changes)
	w.publishIssueChangesLocked(ctx, beforeIssues, next.issues)
}

func (w *workspace) updateViewsLocked(ctx context.Context, changes map[uri.URI][]uri.URI) {
	for _, root := range sortedURIs(changes) {
		view := w.views[root]
		if view == nil {
			continue
		}

		files := changes[root]
		slices.Sort(files)
		files = slices.Compact(files)
		updates := make([]*vfs.FileChange, len(files))
		for i, file := range files {
			updates[i] = &vfs.FileChange{URI: file, From: vfs.FileChangeTypeInitialize}
		}

		w.server.postDiagnostics(ctx, view, view.Update(ctx, updates...))
	}
}

func (w *workspace) applyChanges(ctx context.Context, changes []*vfs.FileChange, overlay bool) error {
	w.mu.Lock()
	defer w.mu.Unlock()

	if overlay {
		if err := w.server.session.Update(ctx, changes); err != nil {
			return err
		}
	}

	byRoot := make(map[uri.URI][]uri.URI)
	var evicted []uri.URI
	for _, change := range changes {
		previousRoot, previouslyOwned := w.model.ownerOf(change.URI, w.documents)
		if overlay {
			if change.From == vfs.FileChangeTypeDidClose {
				delete(w.documents, change.URI)
			} else {
				w.documents[change.URI] = struct{}{}
			}
		}

		if root, ok := w.model.ownerOf(change.URI, w.documents); ok {
			byRoot[root] = append(byRoot[root], change.URI)
			continue
		}

		if change.From == vfs.FileChangeTypeDidClose && previouslyOwned {
			if view := w.views[previousRoot]; view != nil {
				view.Evict(change.URI)
				evicted = append(evicted, change.URI)
			}
		}
	}

	if len(evicted) > 0 {
		w.server.clearDiagnostics(ctx, evicted...)
	}
	w.updateViewsLocked(ctx, byRoot)

	return nil
}

func (w *workspace) viewOf(file uri.URI) (*store.View, error) {
	w.mu.Lock()
	defer w.mu.Unlock()

	root, ok := w.model.ownerOf(file, w.documents)
	if !ok || w.views[root] == nil {
		return nil, fmt.Errorf("no workspace project owns %s", file)
	}

	return w.views[root], nil
}

func (w *workspace) owns(file uri.URI) bool {
	w.mu.Lock()
	defer w.mu.Unlock()

	_, ok := w.model.ownerOf(file, w.documents)

	return ok
}

// rootContains reports whether any known project root contains file,
// regardless of target or document ownership. didOpen uses it to keep new
// files inside loaded roots instead of splitting off inner folders.
func (w *workspace) rootContains(file uri.URI) bool {
	w.mu.Lock()
	defer w.mu.Unlock()

	_, ok := w.model.rootFor(file)

	return ok
}

func (w *workspace) files(view *store.View) []uri.URI {
	w.mu.Lock()
	defer w.mu.Unlock()

	if w.views[view.Folder()] != view {
		return nil
	}

	return w.model.ownedFiles(view.Folder(), w.documents)
}

func (w *workspace) publishIssueChangesLocked(ctx context.Context, before, after map[uri.URI][]WorkspaceIssue) {
	changed := make(map[uri.URI]struct{}, len(before)+len(after))
	for issueURI := range before {
		changed[issueURI] = struct{}{}
	}
	for issueURI := range after {
		changed[issueURI] = struct{}{}
	}

	for _, issueURI := range sortedURIs(changed) {
		if slices.Equal(before[issueURI], after[issueURI]) {
			continue
		}

		w.server.publishWorkspaceIssues(ctx, issueURI, after[issueURI])
	}
}

func (s *Server) publishWorkspaceIssues(ctx context.Context, issueURI uri.URI, issues []WorkspaceIssue) {
	if s.client == nil {
		return
	}

	diagnostics := make([]protocol.Diagnostic, len(issues))
	for i, issue := range issues {
		diagnostics[i] = protocol.Diagnostic{
			Range: protocol.Range{
				Start: protocol.Position{},
				End:   protocol.Position{},
			},
			Severity: protocol.DiagnosticSeverityError,
			Source:   protocol.NewOptional("thrift-ls"),
			Message:  protocol.String(issue.Message),
		}
	}

	if err := s.client.PublishDiagnostics(ctx, &protocol.PublishDiagnosticsParams{
		URI:         issueURI,
		Diagnostics: diagnostics,
	}); err != nil {
		logError("workspace issue diagnostic failed", err, "uri", issueURI)
	}
}

func sortedURIs[V any](values map[uri.URI]V) []uri.URI {
	result := make([]uri.URI, 0, len(values))
	for value := range values {
		result = append(result, value)
	}
	slices.Sort(result)

	return result
}

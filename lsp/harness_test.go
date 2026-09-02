package lsp

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.lsp.dev/protocol"
	"go.lsp.dev/uri"

	"github.com/karitham/thrift-ls/lsp/cache"
)

// testClient records everything the server pushes: diagnostics per URI,
// publish call counts, capability registrations, and log messages. One
// type covers all suites. Servers run with diagSync in tests, so all
// calls are inline with no concurrency.
type testClient struct {
	protocol.Client

	mu       sync.Mutex
	diags    map[uri.URI][]protocol.Diagnostic
	calls    map[uri.URI]int
	regs     []protocol.Registration
	messages []protocol.LogMessageParams
}

func (c *testClient) PublishDiagnostics(_ context.Context, params *protocol.PublishDiagnosticsParams) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.diags == nil {
		c.diags = make(map[uri.URI][]protocol.Diagnostic)
	}
	if c.calls == nil {
		c.calls = make(map[uri.URI]int)
	}

	c.diags[params.URI] = append([]protocol.Diagnostic(nil), params.Diagnostics...)
	c.calls[params.URI]++

	return nil
}

func (c *testClient) RegisterCapability(_ context.Context, params *protocol.RegistrationParams) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.regs = append(c.regs, params.Registrations...)

	return nil
}

func (c *testClient) LogMessage(_ context.Context, params *protocol.LogMessageParams) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.messages = append(c.messages, *params)

	return nil
}

func (c *testClient) ShowMessage(_ context.Context, _ *protocol.ShowMessageParams) error {
	return nil
}

func (c *testClient) reset() {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.diags = make(map[uri.URI][]protocol.Diagnostic)
	c.calls = make(map[uri.URI]int)
}

func (c *testClient) last(file uri.URI) []protocol.Diagnostic {
	c.mu.Lock()
	defer c.mu.Unlock()

	return append([]protocol.Diagnostic(nil), c.diags[file]...)
}

// count reports how many times diagnostics were published for file.
func (c *testClient) count(file uri.URI) int {
	c.mu.Lock()
	defer c.mu.Unlock()

	return c.calls[file]
}

// logs returns a copy of the recorded log messages.
func (c *testClient) logs() []protocol.LogMessageParams {
	c.mu.Lock()
	defer c.mu.Unlock()

	return append([]protocol.LogMessageParams(nil), c.messages...)
}

// watchers returns the glob patterns of registered file watchers.
func (c *testClient) watchers() []string {
	c.mu.Lock()
	defer c.mu.Unlock()

	var globs []string

	for _, r := range c.regs {
		if r.Method != protocol.MethodWorkspaceDidChangeWatchedFiles {
			continue
		}

		var raw struct {
			Watchers []struct {
				GlobPattern string `json:"globPattern"`
			} `json:"watchers"`
		}
		if err := json.Unmarshal(r.RegisterOptions, &raw); err != nil {
			continue
		}

		for _, w := range raw.Watchers {
			globs = append(globs, w.GlobPattern)
		}
	}

	return globs
}

func diagMessages(diags []protocol.Diagnostic) []string {
	msgs := make([]string, 0, len(diags))
	for _, d := range diags {
		msgs = append(msgs, fmt.Sprint(d.Message))
	}

	return msgs
}

func symbolNames(syms protocol.SymbolInformationSlice) []string {
	names := make([]string, len(syms))
	for i, s := range syms {
		names[i] = s.Name
	}

	return names
}

// Server construction. All test servers run with diagSync so
// DidOpen/DidChange/applyChanges publish inline with no goroutines.

// newTestServer returns an in-memory server with synchronous diagnostics.
func newTestServer(client protocol.Client) *Server {
	srv := NewServer(client, Options{Files: cache.NewMemFS(nil)})
	srv.diagSync = true

	return srv
}

// newMemServer returns an in-memory server seeded with files.
func newMemServer(files map[uri.URI][]byte) *Server {
	srv := NewServer(nil, Options{Files: cache.NewMemFS(files)})
	srv.diagSync = true

	return srv
}

// newSyncServerWithOptions builds a synchronous server over a MemFS seed.
// opts.Files is always the MemFS; other fields carry through.
func newSyncServerWithOptions(client protocol.Client, files map[uri.URI][]byte, opts Options) *Server {
	opts.Files = cache.NewMemFS(files)
	srv := NewServer(client, opts)
	srv.diagSync = true

	return srv
}

// seedFiles builds a MemFS seed from absolute path to content. Paths must
// be absolute so FileConfigSource discovery (filepath.Abs passthrough)
// and MemFS walks resolve against memory.
func seedFiles(entries map[string]string) map[uri.URI][]byte {
	out := make(map[uri.URI][]byte, len(entries))
	for p, c := range entries {
		out[uri.File(p)] = []byte(c)
	}

	return out
}

// configURI returns the MemFS URI of the thrift-ls.json in dir.
func configURI(dir string) uri.URI {
	return uri.File(filepath.Join(dir, "thrift-ls.json"))
}

func openDocument(t *testing.T, srv *Server, fileURI uri.URI, content string) {
	t.Helper()

	err := srv.DidOpen(t.Context(), &protocol.DidOpenTextDocumentParams{
		TextDocument: protocol.TextDocumentItem{
			URI:        fileURI,
			LanguageID: LanguageIDThrift,
			Version:    0,
			Text:       content,
		},
	})
	assert.NoError(t, err)
}

// Initialize helpers. Stock walks run synchronously via walkWorkspaceFolders;
// custom workspaces only record folders here and wait for installSnapshot.

func testInitializeParams(folders []protocol.WorkspaceFolder) *protocol.InitializeParams {
	params := &protocol.InitializeParams{}
	params.WorkspaceFolders = protocol.NewNullable(folders)

	return params
}

func testCompletionParams(file uri.URI, line, character uint32) *protocol.CompletionParams {
	params := &protocol.CompletionParams{
		Context: protocol.CompletionContext{
			TriggerKind: protocol.CompletionTriggerKindInvoked,
		},
	}
	params.TextDocument = protocol.TextDocumentIdentifier{URI: file}
	params.Position = protocol.Position{Line: line, Character: character}
	params.WorkDoneToken = protocol.String("")
	params.PartialResultToken = protocol.String("")

	return params
}

func foldersFromURIs(uris []uri.URI) []protocol.WorkspaceFolder {
	folders := make([]protocol.WorkspaceFolder, 0, len(uris))
	for _, u := range uris {
		folders = append(folders, protocol.WorkspaceFolder{URI: u})
	}

	return folders
}

// initWorkspace initializes srv and runs the stock walk synchronously.
// For custom workspaces it only records folders; call installSnapshot next.
func initWorkspace(t *testing.T, srv *Server, folders []uri.URI, initializationOptions []byte) {
	t.Helper()

	params := testInitializeParams(foldersFromURIs(folders))
	params.InitializationOptions = protocol.LSPAny(initializationOptions)
	_, err := srv.Initialize(t.Context(), params)
	require.NoError(t, err)

	if srv.workspace != nil {
		return
	}

	srv.walkWorkspaceFolders(folders)
}

// initCustomFolders records folders on a custom-workspace server without
// starting any loader. Snapshots arrive via installSnapshot.
func initCustomFolders(t *testing.T, srv *Server, folders []uri.URI) {
	t.Helper()

	if srv.workspace == nil {
		srv.workspace = newCustomWorkspace(srv, nil)
	}

	_, err := srv.Initialize(t.Context(), testInitializeParams(foldersFromURIs(folders)))
	require.NoError(t, err)
}

// installSnapshot validates snap and reconciles it synchronously. With
// diagSync, view updates publish inline.
func installSnapshot(t *testing.T, srv *Server, folder uri.URI, snap WorkspaceSnapshot) {
	t.Helper()
	installSnapshots(t, srv, map[uri.URI]WorkspaceSnapshot{folder: snap})
}

// installSnapshots installs several folder snapshots, then reconciles once.
func installSnapshots(t *testing.T, srv *Server, snaps map[uri.URI]WorkspaceSnapshot) {
	t.Helper()

	if srv.workspace == nil {
		srv.workspace = newCustomWorkspace(srv, nil)
	}

	w := srv.workspace
	w.mu.Lock()
	defer w.mu.Unlock()

	for folder, snap := range snaps {
		state, ok := w.folders[folder]
		if !ok {
			state = &workspaceFolder{}
			w.folders[folder] = state
		}
		state.snapshot = validateWorkspaceSnapshot(folder, snap)
		state.cancel = nil
	}

	w.reconcileLocked(t.Context())
}

// Format helpers.

func openAndFormat(t *testing.T, srv *Server, file string) string {
	t.Helper()

	require.NoError(t, srv.DidOpen(t.Context(), &protocol.DidOpenTextDocumentParams{
		TextDocument: protocol.TextDocumentItem{
			URI:        uri.File(file),
			LanguageID: "thrift",
			Version:    0,
			Text:       probe,
		},
	}))

	edits, err := srv.Formatting(t.Context(), &protocol.DocumentFormattingParams{
		TextDocument: protocol.TextDocumentIdentifier{URI: uri.File(file)},
	})
	require.NoError(t, err)
	require.Len(t, edits, 1)

	return edits[0].NewText
}

func formatText(t *testing.T, srv *Server, file uri.URI) string {
	t.Helper()

	edits, err := srv.Formatting(t.Context(), &protocol.DocumentFormattingParams{
		TextDocument: protocol.TextDocumentIdentifier{URI: file},
	})
	require.NoError(t, err)
	require.Len(t, edits, 1)

	return edits[0].NewText
}

// diagnosePair runs the synchronous in-process diagnosis for file so code
// actions pair with the server's own diagnostics without async waits.
func diagnosePair(t *testing.T, srv *Server, file uri.URI) {
	t.Helper()

	_, err := withFile(t.Context(), srv.session.ViewOf, file, func(view *cache.View, _ cache.FileHandle) (struct{}, error) {
		srv.diagnose(t.Context(), view, []uri.URI{file})

		return struct{}{}, nil
	})
	require.NoError(t, err)
}

// codeActionTitles requests code actions at rng and returns title to kind.
func codeActionTitles(t *testing.T, srv *Server, file uri.URI, rng protocol.Range, only ...protocol.CodeActionKind) map[string]protocol.CodeActionKind {
	t.Helper()

	actions, err := srv.codeAction(t.Context(), &protocol.CodeActionParams{
		TextDocument: protocol.TextDocumentIdentifier{URI: file},
		Range:        rng,
		Context:      protocol.CodeActionContext{Only: only},
	})
	require.NoError(t, err)

	got := make(map[string]protocol.CodeActionKind, len(actions))
	for _, a := range actions {
		ca, ok := a.(*protocol.CodeAction)
		require.True(t, ok, "expected a code action, got %T", a)
		require.NotNil(t, ca.Kind)
		got[ca.Title] = *ca.Kind
	}

	return got
}

// codeActionTitleList returns only the titles, for suites that assert
// presence without kinds.
func codeActionTitleList(t *testing.T, srv *Server, file uri.URI, rng protocol.Range) []string {
	t.Helper()

	actions, err := srv.codeAction(t.Context(), &protocol.CodeActionParams{
		TextDocument: protocol.TextDocumentIdentifier{URI: file},
		Range:        rng,
	})
	require.NoError(t, err)

	titles := make([]string, 0, len(actions))
	for _, a := range actions {
		ca, ok := a.(*protocol.CodeAction)
		require.True(t, ok)
		titles = append(titles, ca.Title)
	}

	return titles
}

func completionLabels(t *testing.T, srv *Server, file uri.URI, line, character uint32) []string {
	t.Helper()

	result, err := srv.Completion(t.Context(), testCompletionParams(file, line, character))
	require.NoError(t, err)

	list, ok := result.(*protocol.CompletionList)
	require.True(t, ok)

	labels := make([]string, len(list.Items))
	for i, item := range list.Items {
		labels[i] = item.Label
	}

	return labels
}

func workspaceSymbolNames(t *testing.T, srv *Server) []string {
	t.Helper()

	result, err := srv.Symbols(t.Context(), &protocol.WorkspaceSymbolParams{Query: ""})
	require.NoError(t, err)

	symbols, ok := result.(protocol.SymbolInformationSlice)
	require.True(t, ok)

	return symbolNames(symbols)
}

func assertViewPresent(t *testing.T, srv *Server, folder uri.URI) {
	t.Helper()

	for _, view := range srv.session.Views() {
		if view.Folder() == folder {
			return
		}
	}

	t.Errorf("view %s is missing", folder)
}

func assertViewMissing(t *testing.T, srv *Server, folder uri.URI) {
	t.Helper()

	for _, view := range srv.session.Views() {
		if view.Folder() == folder {
			t.Errorf("view %s is still present", folder)
		}
	}
}

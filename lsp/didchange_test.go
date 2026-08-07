package lsp

import (
	"context"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"testing/synctest"

	"github.com/stretchr/testify/assert"
	"go.lsp.dev/protocol"
	"go.lsp.dev/uri"

	"github.com/karitham/thrift-ls/lsp/cache"
	"github.com/karitham/thrift-ls/options"
)

// recordingClient records PublishDiagnostics calls per URI; every other
// client method is a no-op.
type recordingClient struct {
	protocol.Client

	mu        sync.Mutex
	published map[uri.URI]int
}

func (c *recordingClient) PublishDiagnostics(ctx context.Context, params *protocol.PublishDiagnosticsParams) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.published == nil {
		c.published = make(map[uri.URI]int)
	}

	c.published[params.URI]++

	return nil
}

func (c *recordingClient) reset() {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.published = nil
}

func (c *recordingClient) count(file uri.URI) int {
	c.mu.Lock()
	defer c.mu.Unlock()

	return c.published[file]
}

func newTestServer(client protocol.Client) *Server {
	return NewServer(cache.New(nil), client, options.Patch{})
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()

	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
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

// Test_DidChangeRepublishesDependentsDiagnostics is the user-visible
// behavior: editing federation.gundam re-publishes diagnostics for
// strike_rouge, which includes it. Diagnostics publish asynchronously, so
// the test runs in a synctest bubble and waits for the goroutines.
func Test_DidChangeRepublishesDependentsDiagnostics(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		dir := t.TempDir()
		writeFile(t, filepath.Join(dir, "federation.gundam.thrift"), `struct Gundam {
	1: required string Name
}`)
		writeFile(t, filepath.Join(dir, "strike_rouge.thrift"), `include "federation.gundam.thrift"

exception BayFull {
	1: string message
}`)

		client := &recordingClient{}
		srv := newTestServer(client)

		aURI := uri.File(filepath.Join(dir, "strike_rouge.thrift"))
		bURI := uri.File(filepath.Join(dir, "federation.gundam.thrift"))

		openDocument(t, srv, aURI, `include "federation.gundam.thrift"

exception BayFull {
	1: string message
}`)
		openDocument(t, srv, bURI, `struct Gundam {
	1: required string Name
}`)

		client.reset()

		err := srv.DidChange(t.Context(), &protocol.DidChangeTextDocumentParams{
			TextDocument: protocol.VersionedTextDocumentIdentifier{
				TextDocumentIdentifier: protocol.TextDocumentIdentifier{URI: bURI},
				Version:                1,
			},
			ContentChanges: []protocol.TextDocumentContentChangeEvent{
				&protocol.TextDocumentContentChangeWholeDocument{
					Text: `struct Gundam {
	1: required string Name,
	2: optional i32 SerialNumber
}`,
				},
			},
		})
		assert.NoError(t, err)

		// Let the asynchronous diagnostics goroutine finish.
		synctest.Wait()

		assert.GreaterOrEqual(t, client.count(bURI), 1, "changed file gets diagnostics")
		assert.GreaterOrEqual(t, client.count(aURI), 1, "dependent of changed file gets diagnostics")
	})
}

// Test_DidCloseDropsOverlay: closing a document removes its overlay; content
// falls back to disk and dependents keep their include edges.
func Test_DidCloseDropsOverlay(t *testing.T) {
	dir := t.TempDir()
	bDisk := "struct Gundam {\n\t1: required string Name\n}"
	bOverlay := "struct Gundam {\n\t1: required string Name,\n\t2: optional i32 SerialNumber\n}"

	writeFile(t, filepath.Join(dir, "federation.gundam.thrift"), bDisk)
	writeFile(t, filepath.Join(dir, "strike_rouge.thrift"), `include "federation.gundam.thrift"

exception BayFull {
	1: string message
}`)

	srv := newTestServer(nil)

	aURI := uri.File(filepath.Join(dir, "strike_rouge.thrift"))
	bURI := uri.File(filepath.Join(dir, "federation.gundam.thrift"))

	openDocument(t, srv, aURI, `include "federation.gundam.thrift"

exception BayFull {
	1: string message
}`)
	openDocument(t, srv, bURI, bOverlay)

	ctx := t.Context()

	fh, err := srv.session.ReadFile(ctx, bURI)
	assert.NoError(t, err)
	content, err := fh.Content()
	assert.NoError(t, err)
	assert.Equal(t, bOverlay, string(content))

	err = srv.DidClose(ctx, &protocol.DidCloseTextDocumentParams{
		TextDocument: protocol.TextDocumentIdentifier{URI: bURI},
	})
	assert.NoError(t, err)

	// the overlay is gone: reads fall back to disk content
	fh, err = srv.session.ReadFile(ctx, bURI)
	assert.NoError(t, err)
	content, err = fh.Content()
	assert.NoError(t, err)
	assert.Equal(t, bDisk, string(content))

	// the dependent still parses and keeps its include edge
	view, err := srv.session.ViewOf(aURI)
	assert.NoError(t, err)

	ss, release := view.Snapshot()
	defer release()

	_, err = ss.Parse(ctx, aURI)
	assert.NoError(t, err)
	assert.Equal(t, []uri.URI{aURI}, ss.Dependents(bURI))
}

// Test_DidChangeWatchedFilesRefreshesDiskContent: disk events outside the
// editor (git pull, other editors) refresh content and invalidate dependents.
func Test_DidChangeWatchedFilesRefreshesDiskContent(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		dir := t.TempDir()
		writeFile(t, filepath.Join(dir, "federation.gundam.thrift"), `struct Gundam {
	1: required string Name
}`)
		writeFile(t, filepath.Join(dir, "strike_rouge.thrift"), `include "federation.gundam.thrift"

exception BayFull {
	1: string message
}`)

		client := &recordingClient{}
		srv := newTestServer(client)

		aURI := uri.File(filepath.Join(dir, "strike_rouge.thrift"))
		bURI := uri.File(filepath.Join(dir, "federation.gundam.thrift"))

		openDocument(t, srv, aURI, `include "federation.gundam.thrift"

exception BayFull {
	1: string message
}`)

		// external edit, e.g. git pull
		writeFile(t, filepath.Join(dir, "federation.gundam.thrift"), `struct Gundam {
	1: required string Name,
	2: optional i32 SerialNumber
}`)

		client.reset()

		err := srv.DidChangeWatchedFiles(t.Context(), &protocol.DidChangeWatchedFilesParams{
			Changes: []protocol.FileEvent{
				{URI: bURI, Type: protocol.FileChangeTypeChanged},
			},
		})
		assert.NoError(t, err)

		// Let the asynchronous diagnostics goroutine finish.
		synctest.Wait()

		assert.GreaterOrEqual(t, client.count(bURI), 1, "changed file gets diagnostics")
		assert.GreaterOrEqual(t, client.count(aURI), 1, "dependent of changed file gets diagnostics")

		// the refreshed content is visible through the session
		fh, err := srv.session.ReadFile(t.Context(), bURI)
		assert.NoError(t, err)
		content, err := fh.Content()
		assert.NoError(t, err)
		assert.Contains(t, string(content), "SerialNumber")
	})
}

// Test_DidChangeWatchedFilesIgnoresOpenFiles: disk events for open documents
// are ignored, the overlay stays authoritative.
func Test_DidChangeWatchedFilesIgnoresOpenFiles(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "federation.gundam.thrift"), `struct Gundam {
	1: required string Name
}`)
	writeFile(t, filepath.Join(dir, "strike_rouge.thrift"), `include "federation.gundam.thrift"

exception BayFull {
	1: string message
}`)

	srv := newTestServer(nil)

	bURI := uri.File(filepath.Join(dir, "federation.gundam.thrift"))

	overlayContent := `struct Gundam {
	1: required string Name,
	2: optional i32 SerialNumber
}`
	openDocument(t, srv, bURI, overlayContent)

	// disk differs from the overlay
	writeFile(t, filepath.Join(dir, "federation.gundam.thrift"), `struct Gundam {
	1: required string Name
}`)

	err := srv.DidChangeWatchedFiles(t.Context(), &protocol.DidChangeWatchedFilesParams{
		Changes: []protocol.FileEvent{
			{URI: bURI, Type: protocol.FileChangeTypeChanged},
		},
	})
	assert.NoError(t, err)

	fh, err := srv.session.ReadFile(t.Context(), bURI)
	assert.NoError(t, err)
	content, err := fh.Content()
	assert.NoError(t, err)
	assert.Equal(t, overlayContent, string(content))
}

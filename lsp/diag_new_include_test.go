package lsp

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"sync"
	"testing"
	"testing/synctest"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.lsp.dev/protocol"
	"go.lsp.dev/uri"
)

// diagClient records the last published diagnostics per URI and the
// capability registrations the server asks for.
type diagClient struct {
	protocol.Client

	mu    sync.Mutex
	diags map[uri.URI][]protocol.Diagnostic

	regsMu sync.Mutex
	regs   []protocol.Registration
}

func (c *diagClient) PublishDiagnostics(ctx context.Context, params *protocol.PublishDiagnosticsParams) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.diags == nil {
		c.diags = make(map[uri.URI][]protocol.Diagnostic)
	}

	c.diags[params.URI] = append([]protocol.Diagnostic(nil), params.Diagnostics...)

	return nil
}

func (c *diagClient) RegisterCapability(ctx context.Context, params *protocol.RegistrationParams) error {
	c.regsMu.Lock()
	defer c.regsMu.Unlock()

	c.regs = append(c.regs, params.Registrations...)

	return nil
}

// watchers returns the glob patterns of the registered file watchers.
func (c *diagClient) watchers() []string {
	c.regsMu.Lock()
	defer c.regsMu.Unlock()

	var globs []string
	for _, r := range c.regs {
		if r.Method != protocol.MethodWorkspaceDidChangeWatchedFiles {
			continue
		}

		// Parse the wire JSON directly: the generated protocol types
		// marshal with jsontext, so encoding/json cannot round-trip them.
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

func (c *diagClient) reset() {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.diags = make(map[uri.URI][]protocol.Diagnostic)
}

func (c *diagClient) last(file uri.URI) []protocol.Diagnostic {
	c.mu.Lock()
	defer c.mu.Unlock()

	return append([]protocol.Diagnostic(nil), c.diags[file]...)
}

func diagMessages(diags []protocol.Diagnostic) []string {
	msgs := make([]string, 0, len(diags))
	for _, d := range diags {
		msgs = append(msgs, fmt.Sprint(d.Message))
	}

	return msgs
}

const (
	aContent = "include \"B.thrift\"\nstruct A {\n\t1: required B b\n}\n"
	bContent = "struct B {\n\t1: required string x\n}\n"
)

// Test_DiagIncludeCreatedInEditor: A includes B, which does not exist when
// A opens — the missing type is flagged. B is then created in the editor;
// A's diagnostics must clear.
func Test_DiagIncludeCreatedInEditor(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		dir := t.TempDir()
		aURI := uri.File(filepath.Join(dir, "A.thrift"))
		bURI := uri.File(filepath.Join(dir, "B.thrift"))

		client := &diagClient{}
		srv := newTestServer(client)

		openDocument(t, srv, aURI, aContent)

		// Let the diagnostics goroutine finish.
		synctest.Wait()

		assert.Contains(t, diagMessages(client.last(aURI)), "field type doesn't exist")

		client.reset()

		openDocument(t, srv, bURI, bContent)

		synctest.Wait()

		assert.Empty(t, client.last(aURI), "A's diagnostics must clear once B exists")
	})
}

// Test_DiagIncludeCreatedOnDisk: same as above, but B is created outside the
// editor (terminal, git) and reported through didChangeWatchedFiles.
func Test_DiagIncludeCreatedOnDisk(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		dir := t.TempDir()
		aURI := uri.File(filepath.Join(dir, "A.thrift"))
		bURI := uri.File(filepath.Join(dir, "B.thrift"))

		client := &diagClient{}
		srv := newTestServer(client)

		openDocument(t, srv, aURI, aContent)

		synctest.Wait()

		assert.Contains(t, diagMessages(client.last(aURI)), "field type doesn't exist")

		client.reset()

		writeFile(t, filepath.Join(dir, "B.thrift"), bContent)

		require.NoError(t, srv.DidChangeWatchedFiles(t.Context(), &protocol.DidChangeWatchedFilesParams{
			Changes: []protocol.FileEvent{
				{URI: bURI, Type: protocol.FileChangeTypeCreated},
			},
		}))

		synctest.Wait()

		assert.Empty(t, client.last(aURI), "A's diagnostics must clear once B exists")
	})
}

// Test_DiagIncludeCreatedFullSession is the end-to-end user scenario:
// initialize registers the file watcher (so disk events reach the server),
// a new include is created on disk during the session, and the dependent's
// diagnostics clear.
func Test_DiagIncludeCreatedFullSession(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		dir := t.TempDir()
		aURI := uri.File(filepath.Join(dir, "A.thrift"))
		bURI := uri.File(filepath.Join(dir, "B.thrift"))

		client := &diagClient{}
		srv := newTestServer(client)

		_, err := srv.Initialize(t.Context(), &protocol.InitializeParams{})
		require.NoError(t, err)

		// The server must ask the client to watch thrift files; without it,
		// disk-created includes never reach the server.
		watchers := client.watchers()
		require.NotEmpty(t, watchers, "file watcher must be registered at initialize")
		assert.Contains(t, watchers, "**/*.thrift")

		openDocument(t, srv, aURI, aContent)

		synctest.Wait()

		assert.Contains(t, diagMessages(client.last(aURI)), "field type doesn't exist")

		client.reset()

		writeFile(t, filepath.Join(dir, "B.thrift"), bContent)

		require.NoError(t, srv.DidChangeWatchedFiles(t.Context(), &protocol.DidChangeWatchedFilesParams{
			Changes: []protocol.FileEvent{
				{URI: bURI, Type: protocol.FileChangeTypeCreated},
			},
		}))

		synctest.Wait()

		assert.Empty(t, client.last(aURI), "A's diagnostics must clear once B exists")
	})
}

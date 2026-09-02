package lsp

import (
	"log/slog"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.lsp.dev/protocol"
	"go.lsp.dev/uri"
)

// TestLoggerForwardsToClientAfterHandshake verifies that log records reach
// the client as window/logMessage notifications only once the initialize
// handshake is answered — before that, Helix discards notifications from
// an uninitialized server.
func TestLoggerForwardsToClientAfterHandshake(t *testing.T) {
	InitLogger(5)
	defer setLogClient(nil)

	client := &testClient{}
	srv := newSyncServerWithOptions(client, nil, Options{})

	slog.Info("pre-handshake")
	assert.Empty(t, client.logs())

	_, err := srv.Initialize(t.Context(), &protocol.InitializeParams{})
	require.NoError(t, err)

	slog.Info("between handshake and initialized")
	assert.Empty(t, client.logs())

	// Initialized wires the client; call it directly to stay synchronous.
	setLogClient(client)

	slog.Error("post-handshake boom")

	got := client.logs()
	assert.Contains(t, got, protocol.LogMessageParams{
		Type:    protocol.MessageTypeError,
		Message: "post-handshake boom",
	})
	for _, m := range got {
		assert.NotEqual(t, "pre-handshake", m.Message)
		assert.NotEqual(t, "between handshake and initialized", m.Message)
	}
}

// TestLoggerForwardingSurvivesConfigRelevel verifies that re-leveling the
// logger for a view config's logLevel keeps the client wiring.
func TestLoggerForwardingSurvivesConfigRelevel(t *testing.T) {
	InitLogger(3)
	defer setLogClient(nil)

	files := seedFiles(map[string]string{"/ws/proj/thrift-ls.json": `{"logLevel": 5}`})

	client := &testClient{}
	srv := newSyncServerWithOptions(client, files, Options{})

	_, err := srv.Initialize(t.Context(), testInitializeParams([]protocol.WorkspaceFolder{{URI: uri.File("/ws/proj")}}))
	require.NoError(t, err)
	// Wire before the walk, as Initialized does; the walk's re-level must keep it.
	setLogClient(client)
	srv.walkWorkspaceFolders([]uri.URI{uri.File("/ws/proj")})

	slog.Error("after config re-level")

	assert.Contains(t, client.logs(), protocol.LogMessageParams{
		Type:    protocol.MessageTypeError,
		Message: "after config re-level",
	})
}

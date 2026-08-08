package lsp

import (
	"context"
	"log/slog"
	"sync"
	"testing"
	"testing/synctest"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.lsp.dev/protocol"
	"go.lsp.dev/uri"

	"github.com/karitham/thrift-ls/lsp/cache"
)

// logClient records window/logMessage notifications; every other client
// method is a no-op.
type logClient struct {
	protocol.Client

	mu       sync.Mutex
	messages []protocol.LogMessageParams
}

func (c *logClient) LogMessage(ctx context.Context, params *protocol.LogMessageParams) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.messages = append(c.messages, *params)

	return nil
}

func (c *logClient) RegisterCapability(ctx context.Context, params *protocol.RegistrationParams) error {
	return nil // the file watcher registration
}

func (c *logClient) got() []protocol.LogMessageParams {
	c.mu.Lock()
	defer c.mu.Unlock()

	return append([]protocol.LogMessageParams(nil), c.messages...)
}

// TestLoggerForwardsToClientAfterHandshake verifies that log records reach
// the client as window/logMessage notifications only once the initialize
// handshake is answered — before that, Helix discards notifications from
// an uninitialized server.
func TestLoggerForwardsToClientAfterHandshake(t *testing.T) {
	InitLogger(5) // debug enabled
	defer setLogClient(nil)

	client := &logClient{}
	srv := NewServer(cache.New(), client, Options{})

	slog.Info("pre-handshake")
	assert.Empty(t, client.got())

	_, err := srv.Initialize(t.Context(), &protocol.InitializeParams{})
	require.NoError(t, err)

	// The handshake is answered but not complete: the client sends
	// Initialized only after receiving the initialize response.
	slog.Info("between handshake and initialized")
	assert.Empty(t, client.got())

	require.NoError(t, srv.Initialized(t.Context(), &protocol.InitializedParams{}))

	slog.Error("post-handshake boom")

	got := client.got()
	// The gate is open at Initialized: the boom record is forwarded, and
	// nothing recorded before it (like the pre-handshake info) is.
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
// logger for a view config's logLevel keeps the client wiring: the
// handshake wires the client, then the workspace walk re-inits the logger,
// and records must still reach the client.
func TestLoggerForwardingSurvivesConfigRelevel(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		InitLogger(3)
		defer setLogClient(nil)

		dir := t.TempDir()
		writeConfig(t, dir, `{"logLevel": 5}`)

		client := &logClient{}
		srv := NewServer(cache.New(), client, Options{})
		initWorkspace(t, srv, []uri.URI{uri.File(dir)}, nil)

		slog.Error("after config re-level")

		assert.Contains(t, client.got(), protocol.LogMessageParams{
			Type:    protocol.MessageTypeError,
			Message: "after config re-level",
		})
	})
}

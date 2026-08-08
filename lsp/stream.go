package lsp

import (
	"context"

	"go.lsp.dev/jsonrpc2"
	"go.lsp.dev/protocol"

	"github.com/karitham/thrift-ls/lsp/cache"
	"github.com/karitham/thrift-ls/options"
)

type StreamServer struct {
	cache  *cache.Cache
	config *Options
}

// Options configures the stream server. Config is the startup
// configuration (defaults + config file + CLI). When ConfigPath pins an
// explicit file every view uses Config; otherwise each view resolves its
// own config from its workspace folder at creation, with CLI overlaid.
type Options struct {
	Config options.Patch
	// ConfigPath pins an explicit --config file, skipping per-folder
	// discovery.
	ConfigPath string
	// CLI is the CLI-only overlay, applied on top of every view's config.
	CLI options.Patch
}

func NewStreamServer(opts *Options) *StreamServer {
	return &StreamServer{
		cache:  cache.New(),
		config: opts,
	}
}

func (s *StreamServer) ServeStream(ctx context.Context, conn jsonrpc2.Conn) error {
	client := protocol.ClientDispatcher(conn)

	server := NewServer(s.cache, client, *s.config)
	// Clients may or may not send a shutdown message. Make sure the server is
	// shut down.
	defer func() {
		_ = server.Shutdown(ctx)
	}()

	ctx = protocol.WithClient(ctx, client)
	conn.Go(ctx,
		DebugHandler(
			protocol.Handlers(
				protocol.ServerHandler(server, jsonrpc2.MethodNotFoundHandler),
			),
		))
	<-conn.Done()

	return conn.Err()
}

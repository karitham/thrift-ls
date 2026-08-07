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
	config options.Patch
}

// Options configures the stream server. Config is the base configuration —
// defaults overlaid with the config file and CLI flags — which workspace
// settings from the client overlay at initialize time.
type Options struct {
	IncludePaths []string
	Config       options.Patch
}

func NewStreamServer(opts *Options) *StreamServer {
	return &StreamServer{
		cache:  cache.New(opts.IncludePaths),
		config: opts.Config,
	}
}

func (s *StreamServer) ServeStream(ctx context.Context, conn jsonrpc2.Conn) error {
	client := protocol.ClientDispatcher(conn)

	server := NewServer(s.cache, client, s.config)
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

package lsp

import (
	"context"

	"go.lsp.dev/jsonrpc2"
	"go.lsp.dev/protocol"

	"github.com/karitham/thrift-ls/formatter"
	"github.com/karitham/thrift-ls/lsp/cache"
)

type StreamServer struct {
	cache      *cache.Cache
	formatOpts formatter.Options
}

type Options struct {
	IncludePaths []string
	Format       formatter.Options
}

func NewStreamServer(opts *Options) *StreamServer {
	return &StreamServer{
		cache:      cache.New(opts.IncludePaths),
		formatOpts: opts.Format,
	}
}

func (s *StreamServer) ServeStream(ctx context.Context, conn jsonrpc2.Conn) error {
	client := protocol.ClientDispatcher(conn)

	server := NewServer(s.cache, client, s.formatOpts)
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

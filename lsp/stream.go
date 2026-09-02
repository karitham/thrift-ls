package lsp

import (
	"context"
	"errors"
	"io"

	"go.lsp.dev/jsonrpc2"
	"go.lsp.dev/pkg/fakenet"
	"go.lsp.dev/protocol"
)

type StreamServer struct {
	config *Options
}

// ServeStdio serves the language server over input and output until the
// context is canceled or the input closes. End-of-file is a clean shutdown.
// A nil opts uses the server defaults.
func ServeStdio(ctx context.Context, opts *Options, input io.Reader, output io.Writer) error {
	if opts == nil {
		opts = &Options{}
	}

	server := NewStreamServer(opts)
	stream := jsonrpc2.NewStream(fakenet.NewConn("stdio", io.NopCloser(input), nopWriteCloser{output}))
	err := server.ServeStream(ctx, jsonrpc2.NewConn(stream))
	if errors.Is(err, io.EOF) {
		return nil
	}

	return err
}

type nopWriteCloser struct {
	io.Writer
}

func (nopWriteCloser) Close() error { return nil }

func NewStreamServer(opts *Options) *StreamServer {
	return &StreamServer{
		config: opts,
	}
}

func (s *StreamServer) ServeStream(ctx context.Context, conn jsonrpc2.Conn) error {
	client := protocol.ClientDispatcher(conn)

	server := NewServer(client, *s.config)
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

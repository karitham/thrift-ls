package lsp

import (
	"context"
	"errors"
	"io"

	"go.lsp.dev/jsonrpc2"
	"go.lsp.dev/pkg/fakenet"
	"go.lsp.dev/protocol"

	"github.com/karitham/thrift-ls/lsp/cache"
	"github.com/karitham/thrift-ls/options"
	"github.com/karitham/thrift-ls/sema"
)

type StreamServer struct {
	fs     cache.FileSource
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
	// ConfigFinder resolves an implicit config for each view root. A nil
	// finder uses options.FindConfig.
	ConfigFinder func(string) (string, error)
	// WorkspaceLoader replaces the default recursive workspace scan when set.
	WorkspaceLoader WorkspaceLoader
	// Analyzers are appended to thrift-ls's built-in semantic analyzers.
	Analyzers []sema.Analyzer
	// Version is reported in the initialize result. An empty value uses
	// ServerVersion.
	Version string
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
		fs:     cache.NewMemoizedFS(),
		config: opts,
	}
}

func (s *StreamServer) ServeStream(ctx context.Context, conn jsonrpc2.Conn) error {
	client := protocol.ClientDispatcher(conn)

	server := NewServer(s.fs, client, *s.config)
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

// Package lsptest is an e2e harness for the thrift-ls language server. It
// launches the built binary as a subprocess, speaks LSP over stdio through
// go.lsp.dev/jsonrpc2 and go.lsp.dev/protocol, and exposes editor-shaped
// operations whose diagnostics waits keep tests deterministic.
package lsptest

// server.go is the LSP facade over the transport. Each method names an
// editor operation, uses a typed protocol.ServerDispatcher call, and owns
// its own waiting policy.

import (
	"context"
	"fmt"
	"path/filepath"
	"sync"
	"time"

	"go.lsp.dev/jsonrpc2"
	"go.lsp.dev/protocol"
	"go.lsp.dev/uri"

	"github.com/karitham/thrift-ls/lsp/mapper"
)

const (
	defaultCallTimeout    = 20 * time.Second
	defaultStartupTimeout = 30 * time.Second
	defaultPublishTimeout = 10 * time.Second
)

// Options tune a session. Zero value is usable.
type Options struct {
	// CallTimeout bounds each request/response round trip after startup.
	CallTimeout time.Duration

	// StartupTimeout bounds the initialize/initialized handshake.
	// Workspace indexing starts after initialized, outside this bound.
	StartupTimeout time.Duration

	// PublishTimeout bounds how long document operations wait for the
	// diagnostics they provoke.
	PublishTimeout time.Duration
}

func (o Options) fillDefaults() Options {
	if o.CallTimeout == 0 {
		o.CallTimeout = defaultCallTimeout
	}

	if o.StartupTimeout == 0 {
		o.StartupTimeout = defaultStartupTimeout
	}

	if o.PublishTimeout == 0 {
		o.PublishTimeout = defaultPublishTimeout
	}

	return o
}

// Server is one attached language-server session; see [New].
type Server struct {
	conn jsonrpc2.Conn
	disp protocol.Server
	proc *proc
	dir  string
	opts Options

	mu sync.Mutex

	// diags holds the latest publish per document path; docs hold the text
	// the server was most recently told about (formattings are applied to
	// them); versions drive didChange increments.
	diags    map[string][]Diagnostic
	docs     map[string]string
	versions map[string]int

	// publishWaiters get one buffered send per arrived publish for their
	// document; registering before the triggering notification means no
	// publish can slip past unobserved.
	publishWaiters map[string][]chan struct{}

	windowMsgs []windowMessage
}

// windowMessage is one window/showMessage or window/logMessage record.
type windowMessage struct {
	kind    string // "show" or "log"
	level   int    // LSP MessageType: 1 error, 2 warning, 3 info, 4 log
	message string
}

// New launches command with cwd set to dir, attaches over stdio as an LSP
// client rooted at dir, and completes the initialization handshake.
//
// The inbound handler goroutine starts before any request is sent, so
// server-initiated traffic during initialize (registerCapability) is answered.
//
// An error return means the session is unusable: either the process would
// not start or it died before answering initialize. Startup failures carry
// the server's stderr tail, which is usually all a dying server has to say.
// A returned *Server must be released with Close even on later errors.
func New(command []string, dir string, opts Options) (*Server, error) {
	opts = opts.fillDefaults()

	p, err := newProc(command, dir)
	if err != nil {
		return nil, fmt.Errorf("lsptest: start %s: %w", command[0], err)
	}

	s := &Server{
		proc:           p,
		dir:            dir,
		opts:           opts,
		diags:          make(map[string][]Diagnostic),
		docs:           make(map[string]string),
		versions:       make(map[string]int),
		publishWaiters: make(map[string][]chan struct{}),
	}

	s.conn = jsonrpc2.NewConn(jsonrpc2.NewStream(p.stdio()), jsonrpc2.WithCodec(lspWireCodec{}))
	s.disp = protocol.ServerDispatcher(s.conn)

	root := uri.File(dir)
	s.conn.Go(context.Background(), protocol.ClientHandler(
		&client{srv: s},
		jsonrpc2.MethodNotFoundHandler,
	))

	initCtx, cancel := context.WithTimeout(context.Background(), opts.StartupTimeout)
	defer cancel()

	initParams := &protocol.InitializeParams{
		RootURI:      &root,
		Capabilities: protocol.ClientCapabilities{},
		WorkspaceFolders: protocol.NewNullable([]protocol.WorkspaceFolder{
			{URI: root, Name: filepath.Base(dir)},
		}),
	}

	if _, ierr := s.disp.Initialize(initCtx, initParams); ierr != nil {
		_ = s.Close()

		return nil, fmt.Errorf("lsptest: initialize failed (%s died?): %w\nstderr tail:\n%s",
			command[0], ierr, p.tail.String())
	}

	if nerr := s.disp.Initialized(initCtx, &protocol.InitializedParams{}); nerr != nil {
		_ = s.Close()

		return nil, fmt.Errorf("lsptest: initialized failed (%s died?): %w\nstderr tail:\n%s",
			command[0], nerr, p.tail.String())
	}

	return s, nil
}

// client implements the subset of protocol.Client the harness observes.
// Unoverridden request methods answer with a classified not-implemented
// error instead of silence, which keeps server-initiated requests from
// wedging the connection.
type client struct {
	protocol.UnimplementedClient

	srv *Server
}

func (c *client) PublishDiagnostics(ctx context.Context, params *protocol.PublishDiagnosticsParams) error {
	c.srv.recordDiagnostics(params)

	return nil
}

func (c *client) ShowMessage(ctx context.Context, params *protocol.ShowMessageParams) error {
	c.srv.recordWindowMessage("show", int(params.Type), params.Message)

	return nil
}

func (c *client) LogMessage(ctx context.Context, params *protocol.LogMessageParams) error {
	c.srv.recordWindowMessage("log", int(params.Type), params.Message)

	return nil
}

// RegisterCapability answers null so the server's file-watcher registration
// succeeds; a refusal degrades watch functionality.
func (c *client) RegisterCapability(ctx context.Context, params *protocol.RegistrationParams) error {
	return nil
}

// callCtx returns a context bounded by the session's round-trip timeout.
func (s *Server) callCtx() (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.Background(), s.opts.CallTimeout)
}

// waitToken is a registered interest in the next diagnostics publish for
// one document. Register it before the notification that provokes the
// publish, so nothing slips past unobserved.
type waitToken struct {
	path string
	ch   chan struct{}
}

// expectPublish registers for the next publish of path.
func (s *Server) expectPublish(path string) waitToken {
	t := waitToken{path: path, ch: make(chan struct{}, 4)}

	s.mu.Lock()
	s.publishWaiters[t.path] = append(s.publishWaiters[t.path], t.ch)
	s.mu.Unlock()

	return t
}

// quietWindow is how long settled waits after a publish before deciding no
// newer publish will follow it.
const quietWindow = 250 * time.Millisecond

// settled waits for the diagnostics activity provoked by the triggering
// notification to finish, then removes the registration. The server
// publishes in background goroutines, so an older operation's publish can
// land after the trigger; therefore the wait does not stop at the first
// publish but at the first one followed by the quiet window, and the latest
// state wins.
func (s *Server) settled(t waitToken) ([]Diagnostic, error) {
	defer func() {
		s.mu.Lock()
		defer s.mu.Unlock()

		for i, w := range s.publishWaiters[t.path] {
			if w == t.ch {
				s.publishWaiters[t.path] = append(s.publishWaiters[t.path][:i], s.publishWaiters[t.path][i+1:]...)

				break
			}
		}
	}()

	timeout := time.NewTimer(s.opts.PublishTimeout)
	defer timeout.Stop()

	select {
	case <-t.ch:
	case <-timeout.C:
		return nil, fmt.Errorf("lsptest: no diagnostics published for %s within %s",
			filepath.Base(t.path), s.opts.PublishTimeout)
	case <-s.conn.Done():
		return nil, fmt.Errorf("lsptest: connection closed while waiting for diagnostics of %s (%v)",
			filepath.Base(t.path), s.conn.Err())
	}

	quiet := time.NewTimer(quietWindow)
	defer quiet.Stop()

	for {
		select {
		case <-t.ch:
			if !quiet.Stop() {
				select {
				case <-quiet.C:
				default:
				}
			}
			quiet.Reset(quietWindow)
		case <-quiet.C:
			return s.LatestDiagnostics(t.path), nil
		case <-timeout.C:
			return nil, fmt.Errorf("lsptest: diagnostics for %s did not settle within %s",
				filepath.Base(t.path), s.opts.PublishTimeout)
		case <-s.conn.Done():
			return nil, fmt.Errorf("lsptest: connection closed while waiting for diagnostics of %s (%v)",
				filepath.Base(t.path), s.conn.Err())
		}
	}
}

// recordDiagnostics stores one publish for its document and wakes waiters.
// It runs on the connection's read goroutine; keep it non-blocking.
func (s *Server) recordDiagnostics(p *protocol.PublishDiagnosticsParams) {
	path, ok := uriPath(string(p.URI))
	if !ok {
		return
	}

	ds := make([]Diagnostic, 0, len(p.Diagnostics))

	for _, d := range p.Diagnostics {
		source := ""
		if v, ok := d.Source.Get(); ok {
			source = v
		}

		ds = append(ds, Diagnostic{
			Range:    toModelRange(d.Range),
			Severity: int(d.Severity),
			Source:   source,
			Message:  messageText(d.Message),
		})
	}

	s.mu.Lock()
	s.diags[path] = ds

	waiters := append([]chan struct{}(nil), s.publishWaiters[path]...)
	s.mu.Unlock()

	for _, w := range waiters {
		select {
		case w <- struct{}{}:
		default:
		}
	}
}

// recordWindowMessage keeps window messages for Messages().
func (s *Server) recordWindowMessage(kind string, level int, message string) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.windowMsgs = append(s.windowMsgs, windowMessage{kind: kind, level: level, message: message})
}

func (s *Server) docURI(path string) uri.URI {
	return uri.File(absJoin(s.dir, path))
}

// textDocumentPos builds the shared position-bearing request parameters.
func (s *Server) textDocumentPos(path string, pos Position) protocol.TextDocumentPositionParams {
	return protocol.TextDocumentPositionParams{
		TextDocument: protocol.TextDocumentIdentifier{URI: s.docURI(path)},
		Position:     toProtoPos(pos),
	}
}

// absJoin resolves path against the session root; absolute paths pass
// through cleaned.
func absJoin(dir, path string) string {
	if filepath.IsAbs(path) {
		return filepath.Clean(path)
	}

	return filepath.Join(dir, path)
}

// uriPath converts a wire URI to a filesystem path; non-file URIs (or
// garbage) report false so callers can skip them.
func uriPath(u string) (string, bool) {
	parsed := uri.URI(u)

	if !parsed.IsFile() {
		return "", false
	}

	return parsed.Path(), true
}

// Open tells the server a document was opened with content and returns once
// the diagnostics it provokes have settled.
func (s *Server) Open(path, text string) ([]Diagnostic, error) {
	full := absJoin(s.dir, path)
	t := s.expectPublish(full)

	s.mu.Lock()
	s.docs[full] = text
	s.versions[full] = 1
	s.mu.Unlock()

	ctx, cancel := s.callCtx()
	defer cancel()

	err := s.disp.DidOpen(ctx, &protocol.DidOpenTextDocumentParams{
		TextDocument: protocol.TextDocumentItem{
			URI:        s.docURI(path),
			LanguageID: "thrift",
			Version:    1,
			Text:       text,
		},
	})
	if err != nil {
		return nil, fmt.Errorf("didOpen %s: %w", filepath.Base(path), err)
	}

	return s.settled(t)
}

// Change pushes new full-document content and returns once the diagnostics
// it provokes have settled.
func (s *Server) Change(path, text string) ([]Diagnostic, error) {
	full := absJoin(s.dir, path)

	t := s.expectPublish(full)

	s.mu.Lock()
	version := s.versions[full] + 1
	s.versions[full] = version
	s.docs[full] = text
	s.mu.Unlock()

	ctx, cancel := s.callCtx()
	defer cancel()

	err := s.disp.DidChange(ctx, &protocol.DidChangeTextDocumentParams{
		TextDocument: protocol.VersionedTextDocumentIdentifier{
			TextDocumentIdentifier: protocol.TextDocumentIdentifier{URI: s.docURI(path)},
			Version:                int32(version),
		},
		ContentChanges: []protocol.TextDocumentContentChangeEvent{
			&protocol.TextDocumentContentChangeWholeDocument{Text: text},
		},
	})
	if err != nil {
		return nil, fmt.Errorf("didChange %s: %w", filepath.Base(path), err)
	}

	return s.settled(t)
}

// LatestDiagnostics returns the most recent diagnostics published for path,
// or nil when none arrived yet.
func (s *Server) LatestDiagnostics(path string) []Diagnostic {
	full := absJoin(s.dir, path)

	s.mu.Lock()
	defer s.mu.Unlock()

	return append([]Diagnostic(nil), s.diags[full]...)
}

// Hover asks what is under pos and returns hover markdown, or nil when the
// server has nothing to say at that position.
func (s *Server) Hover(path string, pos Position) (*string, error) {
	full := absJoin(s.dir, path)

	ctx, cancel := s.callCtx()
	defer cancel()

	h, err := s.disp.Hover(ctx, &protocol.HoverParams{
		TextDocumentPositionParams: s.textDocumentPos(path, pos),
	})
	if err != nil {
		return nil, fmt.Errorf("hover %s: %w", filepath.Base(full), err)
	}

	text, ok := hoverText(h)
	if !ok {
		return nil, nil
	}

	return &text, nil
}

// Definition resolves the symbol under pos to definition locations. An
// empty result means nothing was there to resolve.
func (s *Server) Definition(path string, pos Position) ([]Location, error) {
	full := absJoin(s.dir, path)

	ctx, cancel := s.callCtx()
	defer cancel()

	res, err := s.disp.Definition(ctx, &protocol.DefinitionParams{
		TextDocumentPositionParams: s.textDocumentPos(path, pos),
	})
	if err != nil {
		return nil, fmt.Errorf("definition %s: %w", filepath.Base(full), err)
	}

	var locs []Location

	switch v := res.(type) {
	case nil:
	case *protocol.Location:
		locs = locationsFrom([]protocol.Location{*v})
	case protocol.LocationSlice:
		locs = locationsFrom(v)
	case protocol.DefinitionLinkSlice:
		for _, link := range v {
			if link.TargetURI.IsFile() {
				locs = append(locs, Location{
					Path:  link.TargetURI.Path(),
					Range: toModelRange(link.TargetSelectionRange),
				})
			}
		}
	}

	return locs, nil
}

// Completion asks for completions at pos.
func (s *Server) Completion(path string, pos Position) ([]CompletionItem, error) {
	full := absJoin(s.dir, path)

	ctx, cancel := s.callCtx()
	defer cancel()

	res, err := s.disp.Completion(ctx, &protocol.CompletionParams{
		TextDocumentPositionParams: s.textDocumentPos(path, pos),
	})
	if err != nil {
		return nil, fmt.Errorf("completion %s: %w", filepath.Base(full), err)
	}

	var items []protocol.CompletionItem

	switch v := res.(type) {
	case nil:
	case protocol.CompletionItemSlice:
		items = v
	case *protocol.CompletionList:
		items = v.Items
	}

	out := make([]CompletionItem, 0, len(items))

	for _, i := range items {
		detail := ""
		if v, ok := i.Detail.Get(); ok {
			detail = v
		}

		out = append(out, CompletionItem{Label: i.Label, Detail: detail})
	}

	return out, nil
}

// Format formats the document and returns the formatted text. The document
// must have been Opened first.
func (s *Server) Format(path string) (string, error) {
	full := absJoin(s.dir, path)

	s.mu.Lock()
	text, ok := s.docs[full]
	s.mu.Unlock()

	if !ok {
		return "", fmt.Errorf("%w: format %s without opening it", ErrNotFound, filepath.Base(path))
	}

	ctx, cancel := s.callCtx()
	defer cancel()

	edits, err := s.disp.Formatting(ctx, &protocol.DocumentFormattingParams{
		TextDocument: protocol.TextDocumentIdentifier{URI: s.docURI(path)},
		Options:      protocol.FormattingOptions{TabSize: 4, InsertSpaces: true},
	})
	if err != nil {
		return "", fmt.Errorf("formatting %s: %w", filepath.Base(path), err)
	}

	out, err := mapper.NewMapper([]byte(text)).ApplyEdits(edits)
	if err != nil {
		return "", fmt.Errorf("apply formatting: %w", err)
	}

	formatted := string(out)

	s.mu.Lock()
	s.docs[full] = formatted
	s.mu.Unlock()

	return formatted, nil
}

// Messages returns the window messages the server has emitted so far,
// newest last. Levels follow LSP MessageType numbering.
func (s *Server) Messages() []string {
	s.mu.Lock()
	defer s.mu.Unlock()

	out := make([]string, 0, len(s.windowMsgs))

	for _, m := range s.windowMsgs {
		out = append(out, fmt.Sprintf("[%s %d] %s", m.kind, m.level, m.message))
	}

	return out
}

// Alive reports whether the process is still connected.
func (s *Server) Alive() bool {
	select {
	case <-s.conn.Done():
		return false
	default:
		return true
	}
}

// Stderr returns the bounded tail of everything the server wrote to stderr,
// which is where dead servers leave their last words.
func (s *Server) Stderr() string {
	return s.proc.tail.String()
}

// Close shuts the session down: stop the process (close stdin, wait briefly,
// kill if it overstays), then tear down the framed connection and mark it
// dead. conn.Close is idempotent, so repeated calls are safe.
func (s *Server) Close() error {
	s.proc.stop()

	return s.conn.Close()
}

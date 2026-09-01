package lsp

import (
	"context"
	"log/slog"
	"os"
	"sync"

	"go.lsp.dev/protocol"
)

// InitLogger configures the process-wide slog logger with the given level
// (1 fatal .. 6 trace) and redirects it to a temp file so LSP traffic on
// stdio is never polluted. Records are also forwarded to the LSP client as
// window/logMessage once the handshake is done (see setLogClient).
//
// Re-calling InitLogger re-levels the existing handler in place: the file
// is opened once and a client wired after the handshake keeps receiving
// records.
func InitLogger(level int) {
	if logger == nil {
		file := os.TempDir() + "/thrift-ls.log"

		logFile, err := os.OpenFile(file, os.O_RDWR|os.O_CREATE|os.O_APPEND, 0o766)
		if err != nil {
			panic(err)
		}

		logger = &logHandler{file: logFile}
		slog.SetDefault(slog.New(logger))
	}

	logger.mu.Lock()
	logger.inner = slog.NewTextHandler(logger.file, &slog.HandlerOptions{
		Level: SlogLevel(level),
	})
	logger.mu.Unlock()
}

// logger is the handler InitLogger installed, so setLogClient can wire the
// client without type-asserting the process's current default handler
// (which tests and library users may have replaced).
var logger *logHandler

// logHandler writes every record to the temp file and, once a client is
// set, forwards it to the client as a window/logMessage notification.
type logHandler struct {
	file *os.File

	mu     sync.RWMutex
	inner  slog.Handler
	client protocol.Client
}

func (h *logHandler) Enabled(ctx context.Context, level slog.Level) bool {
	h.mu.RLock()
	defer h.mu.RUnlock()

	return h.inner.Enabled(ctx, level)
}

func (h *logHandler) Handle(ctx context.Context, r slog.Record) error {
	h.mu.RLock()
	inner, client := h.inner, h.client
	h.mu.RUnlock()

	if err := inner.Handle(ctx, r); err != nil {
		return err
	}

	if client == nil {
		return nil
	}

	return client.LogMessage(ctx, &protocol.LogMessageParams{
		Type:    logMessageType(r.Level),
		Message: r.Message,
	})
}

func (h *logHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	h.mu.RLock()
	defer h.mu.RUnlock()

	return &logHandler{file: h.file, inner: h.inner.WithAttrs(attrs), client: h.client}
}

func (h *logHandler) WithGroup(name string) slog.Handler {
	h.mu.RLock()
	defer h.mu.RUnlock()

	return &logHandler{file: h.file, inner: h.inner.WithGroup(name), client: h.client}
}

// setLogClient forwards subsequent log records to the client. It must only
// run after the initialize handshake is answered: Helix discards (or
// stalls on) notifications from an uninitialized server. A nil client
// unwires forwarding.
func setLogClient(client protocol.Client) {
	if logger == nil {
		return // InitLogger never ran; nothing to wire
	}

	logger.mu.Lock()
	logger.client = client
	logger.mu.Unlock()
}

// logMessageType maps a slog level to the LSP message type.
func logMessageType(level slog.Level) protocol.MessageType {
	switch {
	case level >= slog.LevelError:
		return protocol.MessageTypeError
	case level >= slog.LevelWarn:
		return protocol.MessageTypeWarning
	case level >= slog.LevelInfo:
		return protocol.MessageTypeInfo
	default:
		return protocol.MessageTypeLog
	}
}

func SlogLevel(level int) slog.Level {
	switch {
	case level >= 5: // logrus Debug / Trace
		return slog.LevelDebug
	case level == 4: // logrus Info
		return slog.LevelInfo
	case level == 3: // logrus Warn
		return slog.LevelWarn
	default: // logrus Fatal / Error / Panic
		return slog.LevelError
	}
}

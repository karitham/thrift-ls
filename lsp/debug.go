package lsp

import (
	"context"
	"log/slog"

	"go.lsp.dev/jsonrpc2"
)

// DebugHandler wraps a jsonrpc2.Handler with request/response debug logging
// and panic recovery.
func DebugHandler(handler jsonrpc2.Handler) jsonrpc2.Handler {
	return func(ctx context.Context, req *jsonrpc2.Request) (result any, err error) {
		if req != nil {
			slog.Debug("jsonrpc request", "method", req.Method(), "params", string(req.Params()))
		}

		defer func() {
			if r := recover(); r != nil {
				slog.Error("recovered from panic", "panic", r)
			}
		}()

		return handler(ctx, req)
	}
}

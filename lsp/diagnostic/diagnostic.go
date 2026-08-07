package diagnostic

import (
	"context"
	"errors"
	"log/slog"

	"go.lsp.dev/protocol"
	"go.lsp.dev/uri"

	"github.com/karitham/thrift-ls/lsp/cache"
	"github.com/karitham/thrift-ls/syntax"
)

var registry []Interface

func init() {
	registry = []Interface{
		&CycleCheck{},
		&Parse{},
		&FieldIDCheck{},
		&SemanticAnalysis{},
	}
}

type Interface interface {
	Diagnostic(ctx context.Context, ss *cache.Snapshot, changeFiles []uri.URI) (DiagnosticResult, error)
	Name() string
}

type Diagnostic struct{}

func NewDiagnostic() Interface {
	return &Diagnostic{}
}

func (d *Diagnostic) Diagnostic(ctx context.Context, ss *cache.Snapshot, changeFiles []uri.URI) (DiagnosticResult, error) {
	res := make(DiagnosticResult)

	var errs []error

	for _, impl := range registry {
		slog.Debug("diagnostic called", "impl", impl.Name())

		diagRes, err := impl.Diagnostic(ctx, ss, changeFiles)
		if err != nil {
			errs = append(errs, err)
		}

		for key, items := range diagRes {
			res[key] = append(res[key], items...)
		}
	}

	if len(errs) > 0 {
		return res, errors.Join(errs...)
	}

	return res, nil
}

func (d *Diagnostic) Name() string {
	return "Diagnostic"
}

type DiagnosticResult map[uri.URI][]protocol.Diagnostic

// tokenRange converts a token's span to an LSP range.
func tokenRange(doc *syntax.Document, tok *syntax.Token) protocol.Range {
	if tok == nil {
		return protocol.Range{}
	}

	start := doc.TokenPosition(tokIndex(doc, tok))
	end := doc.TokenEndPosition(tokIndex(doc, tok))

	return protocol.Range{
		Start: protocol.Position{
			Line:      uint32(start.Line - 1),
			Character: uint32(start.Col - 1),
		},
		End: protocol.Position{
			Line:      uint32(end.Line - 1),
			Character: uint32(end.Col - 1),
		},
	}
}

// nodeRange converts a node's span to an LSP range.
func nodeRange(doc *syntax.Document, node syntax.Node) protocol.Range {
	start, end := doc.Range(node)

	return protocol.Range{
		Start: protocol.Position{
			Line:      uint32(start.Line - 1),
			Character: uint32(start.Col - 1),
		},
		End: protocol.Position{
			Line:      uint32(end.Line - 1),
			Character: uint32(end.Col - 1),
		},
	}
}

// tokIndex finds the index of a token pointer in the document's token
// stream by its offset.
func tokIndex(doc *syntax.Document, tok *syntax.Token) int {
	if tok == nil {
		return 0
	}
	// Token pointers are stable: the first token with a matching offset is
	// the one.
	for i, t := range doc.Tokens {
		if t.Offset == tok.Offset {
			return i
		}
	}

	return 0
}

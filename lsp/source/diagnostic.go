package source

import (
	"context"
	"errors"
	"log/slog"
	"unicode/utf8"

	"go.lsp.dev/protocol"
	"go.lsp.dev/uri"

	"github.com/karitham/thrift-ls/lsp/cache"
	"github.com/karitham/thrift-ls/syntax"
)

var registry []Checker

func init() {
	registry = []Checker{
		&CycleCheck{},
		&Parse{},
		&FieldIDCheck{},
		&DuplicateCheck{},
		&EnumValueCheck{},
		&UnusedIncludeCheck{},
		&SemanticAnalysis{},
	}
}

type Checker interface {
	Diagnostic(ctx context.Context, view *cache.View, changeFiles []uri.URI) (DiagnosticResult, error)
	Name() string
}

type Diagnostic struct{}

func NewDiagnostic() Checker {
	return &Diagnostic{}
}

func (d *Diagnostic) Diagnostic(ctx context.Context, view *cache.View, changeFiles []uri.URI) (DiagnosticResult, error) {
	res := make(DiagnosticResult)

	var errs []error

	for _, impl := range registry {
		slog.Debug("diagnostic called", "impl", impl.Name())

		diagRes, err := impl.Diagnostic(ctx, view, changeFiles)
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
func tokenRange(pf *cache.ParsedFile, tok *syntax.Token) protocol.Range {
	if tok == nil {
		return protocol.Range{}
	}

	start := syntax.Position{Line: tok.Line, Col: tok.Col, Offset: tok.Offset}
	end := syntax.Position{Line: tok.Line, Col: tok.Col + utf8.RuneCountInString(tok.Text), Offset: tok.Offset + len(tok.Text)}

	return toLSPRange(pf, start, end)
}

package sema

import (
	"testing"

	"github.com/stretchr/testify/require"
	"go.lsp.dev/uri"

	"github.com/karitham/thrift-ls/store"
)

var _ func(Config) *Pipeline = DefaultPipeline

// runOne runs a single analyzer over view and files: the shape check
// tests use to pin one check's behavior in isolation.
func runOne(t *testing.T, a Analyzer, view *store.View, files ...uri.URI) Report {
	t.Helper()

	report, err := New(Config{}, []Analyzer{a}).Run(t.Context(), view, files)
	require.NoError(t, err)

	return report
}

// diagCmp reduces a diagnostic to the parts tests assert on: the span's
// line and rune column, severity, code, and message. Byte offsets are
// omitted — they are mapper input, not meaning.
type diagCmp struct {
	StartLine, StartCol int
	EndLine, EndCol     int
	Severity            Severity
	Code                string
	Message             string
}

func cmp(d Diagnostic) diagCmp {
	return diagCmp{
		StartLine: d.Span.Start.Line,
		StartCol:  d.Span.Start.Col,
		EndLine:   d.Span.End.Line,
		EndCol:    d.Span.End.Col,
		Severity:  d.Severity,
		Code:      d.Code,
		Message:   d.Message,
	}
}

func cmpAll(ds []Diagnostic) []diagCmp {
	if len(ds) == 0 {
		return nil
	}

	out := make([]diagCmp, 0, len(ds))
	for _, d := range ds {
		out = append(out, cmp(d))
	}

	return out
}

// analyzeOne runs the full semantic analysis over one file.
func analyzeOne(t *testing.T, view *store.View, file uri.URI) []Diagnostic {
	t.Helper()

	return runOne(t, EachFile(&SemanticAnalysis{}), view, file)[file]
}

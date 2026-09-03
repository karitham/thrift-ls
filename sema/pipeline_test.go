package sema

import (
	"os"
	"path/filepath"
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

// buildFolderSnapshotForTest builds a snapshot whose view root is folder,
// with the given files opened in the overlay.
func buildFolderSnapshotForTest(t *testing.T, folder string, files []*store.FileChange) *store.View {
	t.Helper()

	c := store.NewDiskFS()
	fs := store.NewOverlayFS(c)
	_ = fs.Update(t.Context(), files)

	view := store.NewView(uri.File(folder), fs, nil)

	for _, f := range files {
		_, _ = view.Parse(t.Context(), f.URI)
	}

	return view
}

// writeThrift writes content to a .thrift file under folder.
func writeThrift(t *testing.T, folder, name, content string) string {
	t.Helper()

	p := filepath.Join(folder, name)
	require.NoError(t, os.WriteFile(p, []byte(content), 0o644))

	return p
}

// analyzeOne runs the full semantic analysis over one file.
func analyzeOne(t *testing.T, view *store.View, file uri.URI) []Diagnostic {
	t.Helper()

	return runOne(t, EachFile(&SemanticAnalysis{}), view, file)[file]
}

// Package analyzertest runs analyzer tests against in-memory files.
//
// The package provides view construction, single-analyzer runs, fixpoint
// runs, and diagnostic projections. Tests in package analyzers use it, and
// third-party analyzer authors can use it too. It imports sema and store
// only, so it cannot form an import cycle.
package analyzertest

import (
	"context"
	"sort"
	"testing"

	"github.com/stretchr/testify/require"
	"go.lsp.dev/uri"

	"github.com/karitham/thrift-ls/sema"
	"github.com/karitham/thrift-ls/store"
)

// root prefixes every test file name. "user.thrift" resolves to
// file:///tmp/user.thrift. Includes resolve relative to the referencing
// file, so files in one map include each other by bare name.
const root = "/tmp/"

// URI returns the in-memory URI for a slash-separated test file name.
func URI(name string) uri.URI {
	return uri.File(root + name)
}

// View builds an in-memory view from files, which maps test file names to
// contents.
func View(t *testing.T, files map[string]string) *store.View {
	t.Helper()

	names := make([]string, 0, len(files))
	for name := range files {
		names = append(names, name)
	}

	sort.Strings(names)

	changes := make([]*store.FileChange, 0, len(files))
	for _, name := range names {
		changes = append(changes, &store.FileChange{
			URI:     URI(name),
			Content: []byte(files[name]),
			From:    store.FileChangeTypeDidOpen,
		})
	}

	return store.BuildViewForTest(changes)
}

// RunOnView runs one analyzer over targets and returns the full report.
// The run shares one index across all targets.
func RunOnView(t *testing.T, a sema.Analyzer, view sema.Graph, targets ...uri.URI) sema.Report {
	t.Helper()

	report, err := sema.New(sema.Config{}, []sema.Analyzer{a}).Run(t.Context(), view, targets)
	require.NoError(t, err)

	return report
}

// Run builds a view from files and runs one analyzer over targets.
// Targets name files in the map. An empty target list analyzes every file
// in sorted name order.
func Run(t *testing.T, a sema.Analyzer, files map[string]string, targets ...string) sema.Report {
	t.Helper()

	return RunOnView(t, a, View(t, files), targetURIs(files, targets)...)
}

// RunFixAll runs the pipeline fixpoint loop over targets and writes each
// applied result back into files. An empty target list fixes every file in
// sorted name order.
//
// The view uses a memfs file source, not the overlay that View builds.
// FixAll requires persist to write content through to the view's file
// source before returning, and the shared map is the only file source a
// test can write. Against an overlay view, re-parses read stale content
// and the loop never converges.
func RunFixAll(t *testing.T, p *sema.Pipeline, files map[string]string, targets ...string) sema.FixResult {
	t.Helper()

	contents := make(map[uri.URI][]byte, len(files))
	byURI := make(map[uri.URI]string, len(files))
	names := make([]string, 0, len(files))
	for name, content := range files {
		u := URI(name)
		contents[u] = []byte(content)
		byURI[u] = name
		names = append(names, name)
	}

	view := store.NewView(uri.File(root), store.NewMemFS(contents), nil)
	sort.Strings(names)
	for _, name := range names {
		_, err := view.Parse(t.Context(), URI(name))
		require.NoError(t, err)
	}

	result, err := p.FixAll(t.Context(), view, targetURIs(files, targets), func(_ context.Context, u uri.URI, content []byte) error {
		contents[u] = content
		files[byURI[u]] = string(content)

		return nil
	})
	require.NoError(t, err)

	return result
}

// targetURIs converts target names to URIs. An empty target list includes
// every file in sorted name order.
func targetURIs(files map[string]string, targets []string) []uri.URI {
	if len(targets) == 0 {
		targets = make([]string, 0, len(files))
		for name := range files {
			targets = append(targets, name)
		}

		sort.Strings(targets)
	}

	uris := make([]uri.URI, 0, len(targets))
	for _, name := range targets {
		uris = append(uris, URI(name))
	}

	return uris
}

// File parses a test file and returns the sema.File for direct Analyzer,
// Fixer, or ActionProvider calls. The returned File carries a fresh index
// over the view.
func File(t *testing.T, view sema.Graph, name string) sema.File {
	t.Helper()

	u := URI(name)

	pf, err := view.Parse(t.Context(), u)
	require.NoError(t, err)

	return sema.NewFile(u, pf, view)
}

// Diag holds the parts of a diagnostic that tests assert: span endpoints
// in line and rune columns, severity, code, and message. Byte offsets are
// excluded.
type Diag struct {
	StartLine, StartCol int
	EndLine, EndCol     int
	Severity            sema.Severity
	Code                string
	Message             string
}

// Simplify converts diagnostics to Diags. It returns nil for an empty
// input. Use Messages to compare message text only.
func Simplify(ds []sema.Diagnostic) []Diag {
	if len(ds) == 0 {
		return nil
	}

	out := make([]Diag, 0, len(ds))
	for _, d := range ds {
		out = append(out, Diag{
			StartLine: d.Span.Start.Line,
			StartCol:  d.Span.Start.Col,
			EndLine:   d.Span.End.Line,
			EndCol:    d.Span.End.Col,
			Severity:  d.Severity,
			Code:      d.Code,
			Message:   d.Message,
		})
	}

	return out
}

// Messages returns diagnostic messages in order. It returns nil for an
// empty input.
func Messages(ds []sema.Diagnostic) []string {
	var out []string
	for _, d := range ds {
		out = append(out, d.Message)
	}

	return out
}

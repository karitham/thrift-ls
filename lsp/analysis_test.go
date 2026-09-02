package lsp

import (
	"context"
	"fmt"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.lsp.dev/protocol"
	"go.lsp.dev/uri"

	"github.com/karitham/thrift-ls/options"
	"github.com/karitham/thrift-ls/sema"
	"github.com/karitham/thrift-ls/syntax"
)

type optionAnalyzer struct{}

func (optionAnalyzer) Name() string { return "option-analyzer" }

func (optionAnalyzer) Analyze(_ context.Context, run *sema.Run) error {
	pos := syntax.Position{Line: 1, Col: 1, Offset: 0}

	for _, file := range run.Files() {
		run.Add(file, sema.Diagnostic{
			Code:     "option-diagnostic",
			Severity: sema.SeverityError,
			Message:  "reported by option analyzer",
			Span:     sema.Span{Start: pos, End: pos},
			Fixes: []sema.Fix{{
				Title: "Apply option analyzer fix",
				Edits: []sema.Edit{{Span: sema.Span{Start: pos, End: pos}, NewText: "// fixed\n"}},
			}},
		})
	}

	return nil
}

type optionFixer struct{}

func (optionFixer) Fix(_ context.Context, _ sema.File, d sema.Diagnostic) []sema.Fix {
	if d.Code != "option-diagnostic" {
		return nil
	}

	pos := syntax.Position{Line: 1, Col: 1, Offset: 0}

	return []sema.Fix{{
		Title: "Apply option fixer fix",
		Edits: []sema.Edit{{Span: sema.Span{Start: pos, End: pos}, NewText: "// fixer\n"}},
	}}
}

type optionProvider struct{}

func (optionProvider) Actions(_ context.Context, f sema.File, _ sema.Span, _ sema.Report) []sema.Action {
	pos := syntax.Position{Line: 1, Col: 1, Offset: 0}

	return []sema.Action{{
		Title: "Apply option provider refactor",
		File:  f.URI,
		Edits: []sema.Edit{{Span: sema.Span{Start: pos, End: pos}, NewText: "// provider\n"}},
	}}
}

func openAnalysisDoc(t *testing.T, srv *Server, file uri.URI) {
	t.Helper()

	require.NoError(t, srv.DidOpen(t.Context(), &protocol.DidOpenTextDocumentParams{
		TextDocument: protocol.TextDocumentItem{
			URI:        file,
			LanguageID: LanguageIDThrift,
			Text:       "enum E { A, B = 1 }\n",
		},
	}))
}

func TestOptionsAnalyzersExtendDiagnosticsAndCodeActions(t *testing.T) {
	file := uri.File("/workspace/api.thrift")
	srv := newSyncServerWithOptions(nil, nil, Options{
		Analysis: Analysis{Analyzers: []sema.Analyzer{optionAnalyzer{}}},
	})

	openAnalysisDoc(t, srv, file)

	report := srv.reportFor(file)
	require.NotNil(t, report)

	var codes []string
	for _, diagnostic := range report[file] {
		codes = append(codes, diagnostic.Code)
	}
	assert.Contains(t, codes, "option-diagnostic")
	assert.Contains(t, codes, sema.CodeImplicitEnumValue, "built-in analyzers must remain enabled")

	titles := codeActionTitleList(t, srv, file, protocol.Range{
		Start: protocol.Position{Line: 0, Character: 0},
		End:   protocol.Position{Line: 0, Character: 0},
	})
	assert.Contains(t, titles, "Apply option analyzer fix")
	assert.Contains(t, titles, "Make enum values explicit", "built-in providers must remain enabled")
}

// TestOptionsFixersAndProvidersReachCodeActions verifies the full Analysis
// bundle is wired: a Fixer turning a reported diagnostic into a quickfix,
// and a Provider offering a refactor, both surface through codeAction
// alongside the analyzer's inline fix.
func TestOptionsFixersAndProvidersReachCodeActions(t *testing.T) {
	file := uri.File("/workspace/api.thrift")
	srv := newSyncServerWithOptions(nil, nil, Options{Analysis: Analysis{
		Analyzers: []sema.Analyzer{optionAnalyzer{}},
		Fixers:    []sema.Fixer{optionFixer{}},
		Providers: []sema.ActionProvider{optionProvider{}},
	}})

	openAnalysisDoc(t, srv, file)

	titles := codeActionTitleList(t, srv, file, protocol.Range{
		Start: protocol.Position{Line: 0, Character: 0},
		End:   protocol.Position{Line: 0, Character: 0},
	})
	assert.Contains(t, titles, "Apply option analyzer fix", "inline analyzer fix")
	assert.Contains(t, titles, "Apply option fixer fix", "injected fixer")
	assert.Contains(t, titles, "Apply option provider refactor", "injected provider")
}

type countingAnalyzer struct {
	calls atomic.Int32
}

func (a *countingAnalyzer) Name() string { return "counting-analyzer" }

func (a *countingAnalyzer) Analyze(_ context.Context, run *sema.Run) error {
	call := a.calls.Add(1)
	pos := syntax.Position{Line: 1, Col: 1, Offset: 0}
	for _, file := range run.Files() {
		run.Add(file, sema.Diagnostic{
			Code:     "counting-diagnostic",
			Severity: sema.SeverityError,
			Message:  fmt.Sprintf("analysis run %d", call),
			Span:     sema.Span{Start: pos, End: pos},
		})
	}

	return nil
}

// TestAnalyzerRunsAreSerializedAndStaleResultsAreDropped pins generation
// fencing without goroutines: a diagnoseAt for a superseded generation
// publishes nothing and runs no analysis, while the current generation
// publishes. Sequential DidOpen/DidChange prove inline runs never overlap.
func TestAnalyzerRunsAreSerializedAndStaleResultsAreDropped(t *testing.T) {
	file := uri.File("/workspace/api.thrift")
	client := &testClient{}
	analyzer := &countingAnalyzer{}
	srv := newSyncServerWithOptions(client, nil, Options{
		Analysis: Analysis{Analyzers: []sema.Analyzer{analyzer}},
	})

	require.NoError(t, srv.DidOpen(t.Context(), &protocol.DidOpenTextDocumentParams{
		TextDocument: protocol.TextDocumentItem{
			URI:        file,
			LanguageID: LanguageIDThrift,
			Text:       "struct VersionOne {}",
		},
	}))
	view, err := srv.session.ViewOf(file)
	require.NoError(t, err)
	staleGen := view.Generation()
	require.Equal(t, int32(1), analyzer.calls.Load())
	assert.Contains(t, diagMessages(client.last(file)), "analysis run 1")

	require.NoError(t, srv.DidChange(t.Context(), &protocol.DidChangeTextDocumentParams{
		TextDocument: protocol.VersionedTextDocumentIdentifier{
			TextDocumentIdentifier: protocol.TextDocumentIdentifier{URI: file},
			Version:                1,
		},
		ContentChanges: []protocol.TextDocumentContentChangeEvent{
			&protocol.TextDocumentContentChangeWholeDocument{Text: "struct VersionTwo {}"},
		},
	}))
	require.Equal(t, int32(2), analyzer.calls.Load(), "inline runs never overlap")
	assert.Contains(t, diagMessages(client.last(file)), "analysis run 2")

	srv.diagnoseAt(t.Context(), view, []uri.URI{file}, staleGen)
	assert.Equal(t, int32(2), analyzer.calls.Load(), "a superseded generation must not run analysis")
	assert.Contains(t, diagMessages(client.last(file)), "analysis run 2")
	assert.NotContains(t, diagMessages(client.last(file)), "analysis run 1")

	srv.diagnoseAt(t.Context(), view, []uri.URI{file}, view.Generation())
	assert.Equal(t, int32(3), analyzer.calls.Load())
	assert.Contains(t, diagMessages(client.last(file)), "analysis run 3")
}

func TestRetiredViewDropsInFlightAnalysis(t *testing.T) {
	folder := uri.File("/workspace")
	root := uri.File("/workspace/project")
	file := uri.File("/workspace/project/api.thrift")
	client := &testClient{}
	analyzer := &countingAnalyzer{}
	srv := newSyncServerWithOptions(client,
		seedFiles(map[string]string{"/workspace/project/api.thrift": "struct VersionOne {}"}),
		Options{
			Analysis:     Analysis{Analyzers: []sema.Analyzer{analyzer}},
			ConfigSource: options.PinnedSource(nil),
		})
	initCustomFolders(t, srv, []uri.URI{folder})
	installSnapshot(t, srv, folder, WorkspaceSnapshot{Projects: []Project{{
		ConfigURI:   uri.File("/workspace/project.json"),
		RootURI:     root,
		TargetFiles: []uri.URI{file},
	}}})

	view, err := srv.session.ViewOf(file)
	require.NoError(t, err)
	staleGen := view.Generation()
	require.NotEmpty(t, client.last(file))

	require.NoError(t, srv.DidChangeWorkspaceFolders(t.Context(), &protocol.DidChangeWorkspaceFoldersParams{
		Event: protocol.WorkspaceFoldersChangeEvent{
			Removed: []protocol.WorkspaceFolder{{URI: folder}},
		},
	}))

	assert.Empty(t, srv.session.Views())
	assert.Empty(t, client.last(file))

	before := analyzer.calls.Load()
	srv.diagnoseAt(t.Context(), view, []uri.URI{file}, staleGen)
	assert.Equal(t, before, analyzer.calls.Load(), "a retired view must not run analysis")
	assert.Empty(t, client.last(file))
	assert.Nil(t, srv.reportFor(file))
}

package lsp

import (
	"context"
	"fmt"
	"sync/atomic"
	"testing"
	"testing/synctest"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.lsp.dev/protocol"
	"go.lsp.dev/uri"

	"github.com/karitham/thrift-ls/lsp/cache"
	"github.com/karitham/thrift-ls/sema"
	"github.com/karitham/thrift-ls/syntax"
)

type optionAnalyzer struct{}

func (optionAnalyzer) Name() string { return "option-analyzer" }

func (optionAnalyzer) Analyze(ctx context.Context, run *sema.Run) error {
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

func TestOptionsAnalyzersExtendDiagnosticsAndCodeActions(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		file := uri.File("/workspace/api.thrift")
		srv := NewServer(cache.NewMemFS(nil), nil, Options{
			Analyzers: []sema.Analyzer{optionAnalyzer{}},
		})

		require.NoError(t, srv.DidOpen(t.Context(), &protocol.DidOpenTextDocumentParams{
			TextDocument: protocol.TextDocumentItem{
				URI:        file,
				LanguageID: LanguageIDThrift,
				Text:       "enum E { A, B = 1 }\n",
			},
		}))
		synctest.Wait()

		report := srv.reportFor(file)
		require.NotNil(t, report)

		var codes []string
		for _, diagnostic := range report[file] {
			codes = append(codes, diagnostic.Code)
		}
		assert.Contains(t, codes, "option-diagnostic")
		assert.Contains(t, codes, sema.CodeImplicitEnumValue, "built-in analyzers must remain enabled")

		actions, err := srv.codeAction(t.Context(), &protocol.CodeActionParams{
			TextDocument: protocol.TextDocumentIdentifier{URI: file},
			Range: protocol.Range{
				Start: protocol.Position{Line: 0, Character: 0},
				End:   protocol.Position{Line: 0, Character: 0},
			},
		})
		require.NoError(t, err)

		titles := make([]string, 0, len(actions))
		for _, action := range actions {
			codeAction, ok := action.(*protocol.CodeAction)
			require.True(t, ok)
			titles = append(titles, codeAction.Title)
		}
		assert.Contains(t, titles, "Apply option analyzer fix")
		assert.Contains(t, titles, "Make enum values explicit", "built-in providers must remain enabled")
	})
}

type statefulAnalyzer struct {
	calls   atomic.Int32
	active  atomic.Int32
	max     atomic.Int32
	started chan struct{}
	release chan struct{}
}

func (a *statefulAnalyzer) Name() string { return "stateful-analyzer" }

func (a *statefulAnalyzer) Analyze(ctx context.Context, run *sema.Run) error {
	call := a.calls.Add(1)
	active := a.active.Add(1)
	for {
		max := a.max.Load()
		if active <= max || a.max.CompareAndSwap(max, active) {
			break
		}
	}
	defer a.active.Add(-1)

	if call == 1 {
		close(a.started)
		<-a.release
	}

	pos := syntax.Position{Line: 1, Col: 1, Offset: 0}
	for _, file := range run.Files() {
		run.Add(file, sema.Diagnostic{
			Code:     "stateful-diagnostic",
			Severity: sema.SeverityError,
			Message:  fmt.Sprintf("analysis run %d", call),
			Span:     sema.Span{Start: pos, End: pos},
		})
	}

	return nil
}

func TestAnalyzerRunsAreSerializedAndStaleResultsAreDropped(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		file := uri.File("/workspace/api.thrift")
		client := &diagClient{}
		analyzer := &statefulAnalyzer{
			started: make(chan struct{}),
			release: make(chan struct{}),
		}
		srv := NewServer(cache.NewMemFS(nil), client, Options{
			Analyzers: []sema.Analyzer{analyzer},
		})

		require.NoError(t, srv.DidOpen(t.Context(), &protocol.DidOpenTextDocumentParams{
			TextDocument: protocol.TextDocumentItem{
				URI:        file,
				LanguageID: LanguageIDThrift,
				Text:       "struct VersionOne {}",
			},
		}))
		<-analyzer.started

		require.NoError(t, srv.DidChange(t.Context(), &protocol.DidChangeTextDocumentParams{
			TextDocument: protocol.VersionedTextDocumentIdentifier{
				TextDocumentIdentifier: protocol.TextDocumentIdentifier{URI: file},
				Version:                1,
			},
			ContentChanges: []protocol.TextDocumentContentChangeEvent{
				&protocol.TextDocumentContentChangeWholeDocument{Text: "struct VersionTwo {}"},
			},
		}))

		close(analyzer.release)
		synctest.Wait()

		assert.Equal(t, int32(1), analyzer.max.Load(), "the injected analyzer instance must not run concurrently")
		messages := diagMessages(client.last(file))
		assert.Contains(t, messages, "analysis run 2")
		assert.NotContains(t, messages, "analysis run 1", "a superseded analysis must not be published")
	})
}

func TestRetiredViewDropsInFlightAnalysis(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		folder := uri.File("/workspace")
		root := uri.File("/workspace/project")
		file := uri.File("/workspace/project/api.thrift")
		client := &diagClient{}
		analyzer := &statefulAnalyzer{
			started: make(chan struct{}),
			release: make(chan struct{}),
		}
		loader := WorkspaceLoader(func(ctx context.Context, got uri.URI) (WorkspaceSnapshot, error) {
			return WorkspaceSnapshot{Projects: []Project{{
				ConfigURI:   uri.File("/workspace/project.json"),
				RootURI:     root,
				TargetFiles: []uri.URI{file},
			}}}, nil
		})
		srv := NewServer(cache.NewMemFS(map[uri.URI][]byte{
			file: []byte("struct VersionOne {}"),
		}), client, Options{
			WorkspaceLoader: loader,
			Analyzers:       []sema.Analyzer{analyzer},
			ConfigPath:      "pinned",
		})

		_, err := srv.Initialize(t.Context(), testInitializeParams([]protocol.WorkspaceFolder{{URI: folder}}))
		require.NoError(t, err)
		require.NoError(t, srv.Initialized(t.Context(), &protocol.InitializedParams{}))
		<-analyzer.started

		require.NoError(t, srv.DidChangeWorkspaceFolders(t.Context(), &protocol.DidChangeWorkspaceFoldersParams{
			Event: protocol.WorkspaceFoldersChangeEvent{
				Removed: []protocol.WorkspaceFolder{{URI: folder}},
			},
		}))
		close(analyzer.release)
		synctest.Wait()

		assert.Empty(t, srv.session.Views())
		assert.Empty(t, client.last(file))
		assert.Nil(t, srv.reportFor(file))
	})
}

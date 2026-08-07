package lsp

import (
	"os"
	"path/filepath"
	"testing"
	"testing/synctest"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.lsp.dev/protocol"
	"go.lsp.dev/uri"

	"github.com/karitham/thrift-ls/formatter"
	"github.com/karitham/thrift-ls/lsp/cache"
)

func Test_DidOpen(t *testing.T) {
	ctx := t.Context()
	fileURI, err := uri.Parse("file:///tmp/file.thrift")
	assert.NoError(t, err)

	fileContent := `
include "base.thrift"

struct Test {
	1: required string Name,
	2: optional i32 Age,
}`
	params := &protocol.DidOpenTextDocumentParams{
		TextDocument: protocol.TextDocumentItem{
			URI:        fileURI,
			LanguageID: "thrift",
			Version:    0,
			Text:       fileContent,
		},
	}

	cache := cache.New(nil)
	srv := NewServer(cache, nil, formatter.Options{})
	err = srv.DidOpen(ctx, params)
	assert.NoError(t, err)

	assert.NotNil(t, srv.session)

	fh, err := srv.session.ReadFile(ctx, fileURI)
	assert.NoError(t, err)
	assert.Equal(t, 0, int(fh.Version()))
	gotContent, err := fh.Content()
	assert.NoError(t, err)
	assert.Equal(t, gotContent, []byte(fileContent))
}

func Test_DidChange(t *testing.T) {
	ctx := t.Context()
	fileURI, err := uri.Parse("file:///tmp/file.thrift")
	assert.NoError(t, err)

	fileContentInit := `
include "base.thrift"

struct Test {
	1: required string Name,
	2: optional i32 Age,
}`
	fileContent := `
include "base.thrift"

struct Test {
	1: required string Name,
	2: optional i32 Age,
	3: required string Email,

}`
	openParams := &protocol.DidOpenTextDocumentParams{
		TextDocument: protocol.TextDocumentItem{
			URI:        fileURI,
			LanguageID: "thrift",
			Version:    0,
			Text:       fileContentInit,
		},
	}
	params := &protocol.DidChangeTextDocumentParams{
		TextDocument: protocol.VersionedTextDocumentIdentifier{
			TextDocumentIdentifier: protocol.TextDocumentIdentifier{
				URI: fileURI,
			},
			Version: 1,
		},
		ContentChanges: []protocol.TextDocumentContentChangeEvent{
			&protocol.TextDocumentContentChangeWholeDocument{
				Text: fileContent,
			},
		},
	}

	cache := cache.New(nil)
	srv := NewServer(cache, nil, formatter.Options{})

	err = srv.DidOpen(ctx, openParams)
	assert.NoError(t, err)

	err = srv.DidChange(ctx, params)
	assert.NoError(t, err)

	fh, err := srv.session.ReadFile(ctx, fileURI)
	assert.NoError(t, err)
	assert.Equal(t, 1, int(fh.Version()))
	gotContent, err := fh.Content()
	assert.NoError(t, err)
	assert.Equal(t, gotContent, []byte(fileContent))
}

func Test_Completion(t *testing.T) {
	ctx := t.Context()

	for _, tt := range []struct {
		name           string
		content        string
		line           uint32
		character      uint32
		wantLabel      string
		wantPreselect  bool
		wantNewText    string
		wantRangeStart protocol.Position
		wantRangeEnd   protocol.Position
	}{
		{
			name: "complete field name at end of line",
			content: `include "base.thrift"

struct Test {
	1: required string Name,
	2: optional i32 Age,
        3: required string N
}`,
			line:           5,
			character:      28,
			wantLabel:      "Name",
			wantPreselect:  true,
			wantNewText:    "Name",
			wantRangeStart: protocol.Position{Line: 5, Character: 27},
			wantRangeEnd:   protocol.Position{Line: 5, Character: 28},
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			fileURI, err := uri.Parse("file:///tmp/file.thrift")
			assert.NoError(t, err)

			openParams := &protocol.DidOpenTextDocumentParams{
				TextDocument: protocol.TextDocumentItem{
					URI:        fileURI,
					LanguageID: "thrift",
					Version:    0,
					Text:       tt.content,
				},
			}

			cache := cache.New(nil)
			srv := NewServer(cache, nil, formatter.Options{})
			err = srv.DidOpen(ctx, openParams)
			assert.NoError(t, err)

			completionParams := &protocol.CompletionParams{
				TextDocumentPositionParams: protocol.TextDocumentPositionParams{
					TextDocument: protocol.TextDocumentIdentifier{
						URI: fileURI,
					},
					Position: protocol.Position{
						Line:      tt.line,
						Character: tt.character,
					},
				},
				WorkDoneProgressParams: protocol.WorkDoneProgressParams{
					WorkDoneToken: protocol.String(""),
				},
				PartialResultParams: protocol.PartialResultParams{
					PartialResultToken: protocol.String(""),
				},
				Context: protocol.CompletionContext{
					TriggerKind: protocol.CompletionTriggerKindInvoked,
				},
			}

			completionResult, err := srv.Completion(ctx, completionParams)
			assert.NoError(t, err)

			assert.IsType(t, &protocol.CompletionList{}, completionResult)
			completionList := completionResult.(*protocol.CompletionList)
			assert.NotEmpty(t, completionList.Items)
			assert.LessOrEqual(t, len(completionList.Items), 10)
			assert.Equal(t, tt.wantLabel, completionList.Items[0].Label)
			preselect, _ := completionList.Items[0].Preselect.Get()
			assert.Equal(t, tt.wantPreselect, preselect)

			textEdit, ok := completionList.Items[0].TextEdit.(*protocol.TextEdit)
			assert.True(t, ok)
			assert.Equal(t, tt.wantNewText, textEdit.NewText)
			assert.Equal(t, protocol.Range{
				Start: tt.wantRangeStart,
				End:   tt.wantRangeEnd,
			}, textEdit.Range)
		})
	}
}

func Test_CompletionIncludeScope(t *testing.T) {
	ctx := t.Context()

	for _, tt := range []struct {
		name          string
		baseContent   string
		testContent   string
		wantLabels    []string
		includeSearch []string
	}{
		{
			name:        "completion includes enum from included file",
			baseContent: `enum Name { ONE, TWO }`,
			testContent: `include "base.thrift"

struct Test {
	1: required string Name,
	2: optional i32 Age,
        3: required string N
}`,
			wantLabels:    []string{"Name"},
			includeSearch: []string{"Name"},
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			baseURI, err := uri.Parse("file:///tmp/base.thrift")
			assert.NoError(t, err)

			baseParams := &protocol.DidOpenTextDocumentParams{
				TextDocument: protocol.TextDocumentItem{
					URI:        baseURI,
					LanguageID: "thrift",
					Version:    0,
					Text:       tt.baseContent,
				},
			}

			testURI, err := uri.Parse("file:///tmp/test.thrift")
			assert.NoError(t, err)

			testParams := &protocol.DidOpenTextDocumentParams{
				TextDocument: protocol.TextDocumentItem{
					URI:        testURI,
					LanguageID: "thrift",
					Version:    0,
					Text:       tt.testContent,
				},
			}

			cache := cache.New([]string{"/tmp"})
			srv := NewServer(cache, nil, formatter.Options{})

			err = srv.DidOpen(ctx, baseParams)
			assert.NoError(t, err)
			err = srv.DidOpen(ctx, testParams)
			assert.NoError(t, err)

			completionParams := &protocol.CompletionParams{
				TextDocumentPositionParams: protocol.TextDocumentPositionParams{
					TextDocument: protocol.TextDocumentIdentifier{
						URI: testURI,
					},
					Position: protocol.Position{
						Line:      5,
						Character: 28,
					},
				},
				WorkDoneProgressParams: protocol.WorkDoneProgressParams{
					WorkDoneToken: protocol.String(""),
				},
				PartialResultParams: protocol.PartialResultParams{
					PartialResultToken: protocol.String(""),
				},
				Context: protocol.CompletionContext{
					TriggerKind: protocol.CompletionTriggerKindInvoked,
				},
			}

			completionResult, err := srv.Completion(ctx, completionParams)
			assert.NoError(t, err)

			assert.IsType(t, &protocol.CompletionList{}, completionResult)
			completionList := completionResult.(*protocol.CompletionList)

			labels := make([]string, len(completionList.Items))
			for i, item := range completionList.Items {
				labels[i] = item.Label
			}

			for _, want := range tt.includeSearch {
				assert.Contains(t, labels, want, "Completion should include '%s' from included file", want)
			}
		})
	}
}

func Test_CompletionNoGlobalPollution(t *testing.T) {
	ctx := t.Context()

	for _, tt := range []struct {
		name          string
		file1Content  string
		file2Content  string
		file1URI      string
		file2URI      string
		completionURI string
		notWantLabels []string
	}{
		{
			name: "completions in file1 should not include items from file2",
			file1Content: `include "base.thrift"

struct Test {
	1: required string Name,
	2: optional i32 Age,
        3: required string N
}`,
			file2Content: `include "other.thrift"

struct Other {
	1: required string Field2,
	2: optional i32 Other,
        3: required string M
}`,
			file1URI:      "file:///tmp/file1.thrift",
			file2URI:      "file:///tmp/file2.thrift",
			completionURI: "file:///tmp/file1.thrift",
			notWantLabels: []string{"Field2", "Other"},
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			file1URI, err := uri.Parse(tt.file1URI)
			assert.NoError(t, err)

			file1Params := &protocol.DidOpenTextDocumentParams{
				TextDocument: protocol.TextDocumentItem{
					URI:        file1URI,
					LanguageID: "thrift",
					Version:    0,
					Text:       tt.file1Content,
				},
			}

			file2URI, err := uri.Parse(tt.file2URI)
			assert.NoError(t, err)

			file2Params := &protocol.DidOpenTextDocumentParams{
				TextDocument: protocol.TextDocumentItem{
					URI:        file2URI,
					LanguageID: "thrift",
					Version:    0,
					Text:       tt.file2Content,
				},
			}

			cache := cache.New(nil)
			srv := NewServer(cache, nil, formatter.Options{})

			err = srv.DidOpen(ctx, file1Params)
			assert.NoError(t, err)
			err = srv.DidOpen(ctx, file2Params)
			assert.NoError(t, err)

			completionURI, err := uri.Parse(tt.completionURI)
			assert.NoError(t, err)

			completionParams := &protocol.CompletionParams{
				TextDocumentPositionParams: protocol.TextDocumentPositionParams{
					TextDocument: protocol.TextDocumentIdentifier{
						URI: completionURI,
					},
					Position: protocol.Position{
						Line:      5,
						Character: 28,
					},
				},
				WorkDoneProgressParams: protocol.WorkDoneProgressParams{
					WorkDoneToken: protocol.String(""),
				},
				PartialResultParams: protocol.PartialResultParams{
					PartialResultToken: protocol.String(""),
				},
				Context: protocol.CompletionContext{
					TriggerKind: protocol.CompletionTriggerKindInvoked,
				},
			}

			completionResult, err := srv.Completion(ctx, completionParams)
			assert.NoError(t, err)

			assert.IsType(t, &protocol.CompletionList{}, completionResult)
			completionList := completionResult.(*protocol.CompletionList)

			labels := make([]string, len(completionList.Items))
			for i, item := range completionList.Items {
				labels[i] = item.Label
			}

			for _, notWant := range tt.notWantLabels {
				assert.NotContains(t, labels, notWant, "Completion should NOT include '%s' from unrelated file", notWant)
			}
		})
	}
}

func Test_DidChangeWorkspaceFolders(t *testing.T) {
	ctx := t.Context()

	dirA := t.TempDir()
	dirB := t.TempDir()

	require.NoError(t, os.WriteFile(filepath.Join(dirA, "a.thrift"), []byte("struct FromA {}"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(dirB, "b.thrift"), []byte("struct FromB {}"), 0o644))

	srv := NewServer(cache.New(nil), nil, formatter.Options{})

	// Adding folders walks them and registers their thrift files.
	err := srv.DidChangeWorkspaceFolders(ctx, &protocol.DidChangeWorkspaceFoldersParams{
		Event: protocol.WorkspaceFoldersChangeEvent{
			Added: []protocol.WorkspaceFolder{{URI: uri.File(dirA)}},
		},
	})
	require.NoError(t, err)

	err = srv.DidChangeWorkspaceFolders(ctx, &protocol.DidChangeWorkspaceFoldersParams{
		Event: protocol.WorkspaceFoldersChangeEvent{
			Added: []protocol.WorkspaceFolder{{URI: uri.File(dirB)}},
		},
	})
	require.NoError(t, err)

	assert.Len(t, srv.session.Views(), 2)

	files, err := srv.Symbols(ctx, &protocol.WorkspaceSymbolParams{Query: ""})
	require.NoError(t, err)

	syms, ok := files.(protocol.SymbolInformationSlice)
	require.True(t, ok)
	assert.Equal(t, []string{"FromA", "FromB"}, symbolNames(syms))

	// Removing a folder drops its view and its symbols.
	err = srv.DidChangeWorkspaceFolders(ctx, &protocol.DidChangeWorkspaceFoldersParams{
		Event: protocol.WorkspaceFoldersChangeEvent{
			Removed: []protocol.WorkspaceFolder{{URI: uri.File(dirA)}},
		},
	})
	require.NoError(t, err)

	assert.Len(t, srv.session.Views(), 1)

	files, err = srv.Symbols(ctx, &protocol.WorkspaceSymbolParams{Query: ""})
	require.NoError(t, err)

	syms, ok = files.(protocol.SymbolInformationSlice)
	require.True(t, ok)
	assert.Equal(t, []string{"FromB"}, symbolNames(syms))
}

func symbolNames(syms protocol.SymbolInformationSlice) []string {
	names := make([]string, len(syms))
	for i, s := range syms {
		names[i] = s.Name
	}

	return names
}

// Test_InitializeDefersTheWorkspaceWalk pins the startup flow: initialize
// returns without touching the workspace, and the walk runs once on the
// Initialized notification, registering every thrift file under the
// workspace folder.
func Test_InitializeDefersTheWorkspaceWalk(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		dir := t.TempDir()
		require.NoError(t, os.WriteFile(filepath.Join(dir, "a.thrift"), []byte("struct FromA {}"), 0o644))
		require.NoError(t, os.MkdirAll(filepath.Join(dir, "nested"), 0o755))
		require.NoError(t, os.WriteFile(filepath.Join(dir, "nested", "b.thrift"), []byte("struct FromB {}"), 0o644))

		srv := NewServer(cache.New(nil), nil, formatter.Options{})

		_, err := srv.Initialize(t.Context(), &protocol.InitializeParams{
			WorkspaceFoldersInitializeParams: protocol.WorkspaceFoldersInitializeParams{
				WorkspaceFolders: protocol.NewNullable([]protocol.WorkspaceFolder{{URI: uri.File(dir)}}),
			},
		})
		require.NoError(t, err)

		// The walk is deferred: nothing is known yet, and no view exists.
		assert.Empty(t, srv.session.Views())

		require.NoError(t, srv.Initialized(t.Context(), &protocol.InitializedParams{}))

		synctest.Wait()

		// The walk registered the folder as a view and marked both files
		// known — including the nested one.
		views := srv.session.Views()
		require.Len(t, views, 1)
		assert.Equal(t, uri.File(dir), views[0].Folder())

		known := views[0].KnownFiles()
		assert.Contains(t, known, uri.File(filepath.Join(dir, "a.thrift")))
		assert.Contains(t, known, uri.File(filepath.Join(dir, "nested", "b.thrift")))

		// Workspace symbols resolve from the walked files.
		files, err := srv.Symbols(t.Context(), &protocol.WorkspaceSymbolParams{Query: ""})
		require.NoError(t, err)

		syms, ok := files.(protocol.SymbolInformationSlice)
		require.True(t, ok)
		assert.Equal(t, []string{"FromA", "FromB"}, symbolNames(syms))
	})
}

// Test_CodeActionFormatDocument pins the format code action: an
// unformatted document yields a source.fixAll action with the full-document
// edit, and a formatted document yields no actions.
func Test_CodeActionFormatDocument(t *testing.T) {
	tests := []struct {
		name    string
		content string
		want    bool // whether an action is expected
	}{
		{"unformatted document offers formatting", "struct S{\n1:i32 a\n}", true},
		{"formatted document offers nothing", "struct S { 1: i32 a }\n", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fileURI := uri.File("/tmp/format.thrift")

			srv := NewServer(cache.New(nil), nil, formatter.Options{})
			require.NoError(t, srv.DidOpen(t.Context(), &protocol.DidOpenTextDocumentParams{
				TextDocument: protocol.TextDocumentItem{
					URI:        fileURI,
					LanguageID: "thrift",
					Version:    0,
					Text:       tt.content,
				},
			}))

			actions, err := srv.CodeAction(t.Context(), &protocol.CodeActionParams{
				TextDocument: protocol.TextDocumentIdentifier{URI: fileURI},
			})
			require.NoError(t, err)

			if !tt.want {
				assert.Empty(t, actions)

				return
			}

			require.Len(t, actions, 1)
			action, ok := actions[0].(*protocol.CodeAction)
			require.True(t, ok)
			require.NotNil(t, action)
			assert.Equal(t, protocol.CodeActionKindSourceFixAll, *action.Kind)
			require.NotNil(t, action.Edit)

			edits := action.Edit.Changes[fileURI]
			require.Len(t, edits, 1)
			assert.Contains(t, edits[0].NewText, "struct S {")
		})
	}
}

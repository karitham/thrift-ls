package lsp

import (
	"context"
	"testing"

	"github.com/joyme123/protocol"
	"github.com/joyme123/thrift-ls/lsp/cache"
	"github.com/joyme123/thrift-ls/lsp/memoize"
	"github.com/stretchr/testify/assert"
	"go.lsp.dev/uri"
)

func Test_DidOpen(t *testing.T) {
	ctx := context.TODO()
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

	store := &memoize.Store{}
	cache := cache.New(store, nil)
	srv := NewServer(cache, nil)
	err = srv.DidOpen(ctx, params)
	assert.NoError(t, err)

	assert.NotNil(t, srv.session)

	fh, err := srv.session.ReadFile(ctx, fileURI)
	assert.NoError(t, err)
	assert.Equal(t, int(fh.Version()), 0)
	gotContent, err := fh.Content()
	assert.NoError(t, err)
	assert.Equal(t, gotContent, []byte(fileContent))
}

func Test_DidChange(t *testing.T) {
	ctx := context.TODO()
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
			{
				Text: fileContent,
			},
		},
	}

	store := &memoize.Store{}
	cache := cache.New(store, nil)
	srv := NewServer(cache, nil)

	err = srv.DidOpen(ctx, openParams)
	assert.NoError(t, err)

	err = srv.DidChange(ctx, params)
	assert.NoError(t, err)

	fh, err := srv.session.ReadFile(ctx, fileURI)
	assert.NoError(t, err)
	assert.Equal(t, int(fh.Version()), 1)
	gotContent, err := fh.Content()
	assert.NoError(t, err)
	assert.Equal(t, gotContent, []byte(fileContent))
}

func Test_Completion(t *testing.T) {
	ctx := context.TODO()

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
			character:     28,
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

			store := &memoize.Store{}
			cache := cache.New(store, nil)
			srv := NewServer(cache, nil)
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
					WorkDoneToken: &protocol.ProgressToken{},
				},
				PartialResultParams: protocol.PartialResultParams{
					PartialResultToken: &protocol.ProgressToken{},
				},
				Context: &protocol.CompletionContext{
					TriggerKind: protocol.CompletionTriggerKindInvoked,
				},
			}

			completionList, err := srv.Completion(ctx, completionParams)
			assert.NoError(t, err)

			assert.True(t, len(completionList.Items) > 0)
			assert.True(t, len(completionList.Items) <= 10)
			assert.Equal(t, tt.wantLabel, completionList.Items[0].Label)
			assert.Equal(t, tt.wantPreselect, completionList.Items[0].Preselect)

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
	ctx := context.TODO()

	for _, tt := range []struct {
		name          string
		baseContent   string
		testContent   string
		wantLabels    []string
		includeSearch []string
	}{
		{
			name: "completion includes enum from included file",
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

			store := &memoize.Store{}
			cache := cache.New(store, []string{"/tmp"})
			srv := NewServer(cache, nil)

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
					WorkDoneToken: &protocol.ProgressToken{},
				},
				PartialResultParams: protocol.PartialResultParams{
					PartialResultToken: &protocol.ProgressToken{},
				},
				Context: &protocol.CompletionContext{
					TriggerKind: protocol.CompletionTriggerKindInvoked,
				},
			}

			completionList, err := srv.Completion(ctx, completionParams)
			assert.NoError(t, err)

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
	ctx := context.TODO()

	for _, tt := range []struct {
		name           string
		file1Content   string
		file2Content   string
		file1URI       string
		file2URI       string
		completionURI  string
		notWantLabels  []string
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

			store := &memoize.Store{}
			cache := cache.New(store, nil)
			srv := NewServer(cache, nil)

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
					WorkDoneToken: &protocol.ProgressToken{},
				},
				PartialResultParams: protocol.PartialResultParams{
					PartialResultToken: &protocol.ProgressToken{},
				},
				Context: &protocol.CompletionContext{
					TriggerKind: protocol.CompletionTriggerKindInvoked,
				},
			}

			completionList, err := srv.Completion(ctx, completionParams)
			assert.NoError(t, err)

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

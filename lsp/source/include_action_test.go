package source

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.lsp.dev/protocol"
	"go.lsp.dev/uri"

	"github.com/karitham/thrift-ls/lsp/cache"
	"github.com/karitham/thrift-ls/lsp/mapper"
	"github.com/karitham/thrift-ls/options"
)

// buildFolderSnapshotForTest builds a snapshot whose view root is folder,
// with the given files opened in the overlay.
func buildFolderSnapshotForTest(t *testing.T, folder string, files []*cache.FileChange) *cache.View {
	t.Helper()

	c := cache.NewMemoizedFS()
	fs := cache.NewOverlayFS(c)
	_ = fs.Update(t.Context(), files)

	view := cache.NewView(uri.File(folder), fs, nil, options.Patch{})

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

func Test_MakeRemoveUnusedIncludeAction(t *testing.T) {
	folder := t.TempDir()
	filePath := writeThrift(t, folder, "user.thrift", "include \"shared.thrift\"\nstruct S { 1: i32 a }\n")

	view := buildFolderSnapshotForTest(t, folder, []*cache.FileChange{
		{
			URI:     uri.File(filePath),
			Version: 0,
			Content: []byte("include \"shared.thrift\"\nstruct S { 1: i32 a }\n"),
			From:    cache.FileChangeTypeDidOpen,
		},
	})

	fh, err := view.ReadFile(t.Context(), uri.File(filePath))
	require.NoError(t, err)

	// The diagnostic the check produces, as the server would pass it.
	diags, err := (&UnusedIncludeCheck{}).diagnostic(t.Context(), NewBatch(view), uri.File(filePath))
	require.NoError(t, err)
	require.Len(t, diags, 1)

	act, err := MakeRemoveUnusedIncludeAction(t.Context(), view, fh, diags[0].Range, diags)
	require.NoError(t, err)
	require.NotNil(t, act)
	assert.Equal(t, protocol.CodeActionKindQuickFix, *act.Kind)

	edits := act.Edit.Changes[uri.File(filePath)]
	got, err := mapper.NewMapper([]byte("include \"shared.thrift\"\nstruct S { 1: i32 a }\n")).ApplyEdits(edits)
	require.NoError(t, err)
	assert.Equal(t, "struct S { 1: i32 a }\n", string(got))
}

func Test_MakeRemoveUnusedIncludeAction_NoDiagnostic(t *testing.T) {
	folder := t.TempDir()
	filePath := writeThrift(t, folder, "user.thrift", "include \"shared.thrift\"\nstruct S { 1: shared.User u }\n")

	view := buildFolderSnapshotForTest(t, folder, []*cache.FileChange{
		{
			URI:     uri.File(filePath),
			Version: 0,
			Content: []byte("include \"shared.thrift\"\nstruct S { 1: shared.User u }\n"),
			From:    cache.FileChangeTypeDidOpen,
		},
	})

	fh, err := view.ReadFile(t.Context(), uri.File(filePath))
	require.NoError(t, err)

	act, err := MakeRemoveUnusedIncludeAction(t.Context(), view, fh, pointRange(0, 0), nil)
	require.NoError(t, err)
	assert.Nil(t, act)
}

func Test_MakeAddMissingIncludeAction(t *testing.T) {
	folder := t.TempDir()
	_ = writeThrift(t, folder, "shared.thrift", "struct User {\n  1: i32 id,\n}\n")
	filePath := writeThrift(t, folder, "user.thrift", "struct S {\n  1: User u,\n}\n")

	view := buildFolderSnapshotForTest(t, folder, []*cache.FileChange{
		{
			URI:     uri.File(filePath),
			Version: 0,
			Content: []byte("struct S {\n  1: User u,\n}\n"),
			From:    cache.FileChangeTypeDidOpen,
		},
	})

	fh, err := view.ReadFile(t.Context(), uri.File(filePath))
	require.NoError(t, err)

	// The semantic diagnostic the server would pass, at the type position.
	diag := protocol.Diagnostic{
		Range:   protocol.Range{Start: protocol.Position{Line: 1, Character: 6}, End: protocol.Position{Line: 1, Character: 10}},
		Code:    protocol.String(CodeUndefinedType),
		Message: protocol.String("field type doesn't exist"),
	}

	act, err := MakeAddMissingIncludeAction(t.Context(), view, fh, diag.Range, []protocol.Diagnostic{diag})
	require.NoError(t, err)
	require.NotNil(t, act)
	assert.Equal(t, protocol.CodeActionKindQuickFix, *act.Kind)
	assert.Equal(t, `Add include "shared.thrift"`, act.Title)

	edits := act.Edit.Changes[uri.File(filePath)]
	got, err := mapper.NewMapper([]byte("struct S {\n  1: User u,\n}\n")).ApplyEdits(edits)
	require.NoError(t, err)
	assert.Equal(t, "include \"shared.thrift\"\nstruct S {\n  1: User u,\n}\n", string(got))
}

func Test_MakeAddMissingIncludeAction_InsertAfterExistingIncludes(t *testing.T) {
	folder := t.TempDir()
	_ = writeThrift(t, folder, "shared.thrift", "struct User {\n  1: i32 id,\n}\n")
	filePath := writeThrift(t, folder, "user.thrift", "include \"base.thrift\"\n\nstruct S {\n  1: User u,\n}\n")

	view := buildFolderSnapshotForTest(t, folder, []*cache.FileChange{
		{
			URI:     uri.File(filePath),
			Version: 0,
			Content: []byte("include \"base.thrift\"\n\nstruct S {\n  1: User u,\n}\n"),
			From:    cache.FileChangeTypeDidOpen,
		},
	})

	fh, err := view.ReadFile(t.Context(), uri.File(filePath))
	require.NoError(t, err)

	diag := protocol.Diagnostic{
		Range:   protocol.Range{Start: protocol.Position{Line: 3, Character: 6}, End: protocol.Position{Line: 3, Character: 10}},
		Code:    protocol.String(CodeUndefinedType),
		Message: protocol.String("field type doesn't exist"),
	}

	act, err := MakeAddMissingIncludeAction(t.Context(), view, fh, diag.Range, []protocol.Diagnostic{diag})
	require.NoError(t, err)
	require.NotNil(t, act)

	edits := act.Edit.Changes[uri.File(filePath)]
	got, err := mapper.NewMapper([]byte("include \"base.thrift\"\n\nstruct S {\n  1: User u,\n}\n")).ApplyEdits(edits)
	require.NoError(t, err)
	assert.Equal(t, "include \"base.thrift\"\ninclude \"shared.thrift\"\n\nstruct S {\n  1: User u,\n}\n", string(got))
}

func Test_MakeAddMissingIncludeAction_TypeNotFound(t *testing.T) {
	folder := t.TempDir()
	filePath := writeThrift(t, folder, "user.thrift", "struct S {\n  1: Ghost u,\n}\n")

	view := buildFolderSnapshotForTest(t, folder, []*cache.FileChange{
		{
			URI:     uri.File(filePath),
			Version: 0,
			Content: []byte("struct S {\n  1: Ghost u,\n}\n"),
			From:    cache.FileChangeTypeDidOpen,
		},
	})

	fh, err := view.ReadFile(t.Context(), uri.File(filePath))
	require.NoError(t, err)

	diag := protocol.Diagnostic{
		Range:   protocol.Range{Start: protocol.Position{Line: 1, Character: 6}, End: protocol.Position{Line: 1, Character: 11}},
		Code:    protocol.String(CodeUndefinedType),
		Message: protocol.String("field type doesn't exist"),
	}

	act, err := MakeAddMissingIncludeAction(t.Context(), view, fh, diag.Range, []protocol.Diagnostic{diag})
	require.NoError(t, err)
	assert.Nil(t, act)
}

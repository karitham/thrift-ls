package sema

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.lsp.dev/uri"

	"github.com/karitham/thrift-ls/store"
)

// addIncludeFixes returns the fixes the fixer offers for the undefined-type
// diagnostics the analysis produced for file.
func addIncludeFixes(t *testing.T, view *store.View, file uri.URI) []Fix {
	t.Helper()

	report := runOne(t, EachFile(&SemanticAnalysis{}), view, file)
	diags := report[file]

	pf, err := view.Parse(t.Context(), file)
	require.NoError(t, err)

	f := File{URI: file, PF: pf, run: &Run{view: view}}

	var got []Fix
	for _, d := range diags {
		if d.Code == CodeUndefinedType {
			got = append(got, AddIncludeFixer{}.Fix(t.Context(), f, d)...)
		}
	}

	return got
}

func Test_AddIncludeFixer(t *testing.T) {
	t.Run("adds the include defining the missing type", func(t *testing.T) {
		user := uri.File("/tmp/user.thrift")
		shared := uri.File("/tmp/shared.thrift")
		content := "struct S {\n  1: User u,\n}\n"

		view := store.BuildViewForTest([]*store.FileChange{
			{URI: shared, Content: []byte("struct User {\n  1: i32 id,\n}\n"), From: store.FileChangeTypeDidOpen},
			{URI: user, Content: []byte(content), From: store.FileChangeTypeDidOpen},
		})

		fixes := addIncludeFixes(t, view, user)
		require.Len(t, fixes, 1)
		assert.Equal(t, `Add include "shared.thrift"`, fixes[0].Title)
		assert.Equal(t, "include \"shared.thrift\"\n", fixes[0].Edits[0].NewText)
		assert.Equal(t, "include \"shared.thrift\"\nstruct S {\n  1: User u,\n}\n",
			applyEdits(t, content, fixes[0].Edits))
	})

	t.Run("inserts after existing includes", func(t *testing.T) {
		user := uri.File("/tmp/user.thrift")
		shared := uri.File("/tmp/shared.thrift")
		content := "include \"base.thrift\"\n\nstruct S {\n  1: User u,\n}\n"

		view := store.BuildViewForTest([]*store.FileChange{
			{URI: shared, Content: []byte("struct User {\n  1: i32 id,\n}\n"), From: store.FileChangeTypeDidOpen},
			{URI: user, Content: []byte(content), From: store.FileChangeTypeDidOpen},
		})

		fixes := addIncludeFixes(t, view, user)
		require.Len(t, fixes, 1)
		assert.Equal(t, "include \"base.thrift\"\ninclude \"shared.thrift\"\n\nstruct S {\n  1: User u,\n}\n",
			applyEdits(t, content, fixes[0].Edits))
	})

	t.Run("a type defined nowhere offers no fix", func(t *testing.T) {
		user := uri.File("/tmp/user.thrift")
		content := "struct S {\n  1: Ghost u,\n}\n"

		view := store.BuildViewForTest([]*store.FileChange{
			{URI: user, Content: []byte(content), From: store.FileChangeTypeDidOpen},
		})

		assert.Empty(t, addIncludeFixes(t, view, user))
	})

	t.Run("a type defined in the same file offers no fix", func(t *testing.T) {
		user := uri.File("/tmp/user.thrift")
		content := "struct User {}\nstruct S {\n  1: User u,\n}\n"

		view := store.BuildViewForTest([]*store.FileChange{
			{URI: user, Content: []byte(content), From: store.FileChangeTypeDidOpen},
		})

		assert.Empty(t, addIncludeFixes(t, view, user))
	})
}

// Test_UnusedIncludeCheck_InlineFix pins the inline quickfix: the unused
// include line is deleted whole, trailing newline included.
func Test_UnusedIncludeCheck_InlineFix(t *testing.T) {
	content := "include \"shared.thrift\"\nstruct S { 1: i32 a }\n"
	file := uri.File("/tmp/user.thrift")

	view := store.BuildViewForTest([]*store.FileChange{
		{URI: file, Content: []byte(content), From: store.FileChangeTypeDidOpen},
	})

	report := runOne(t, EachFile(&UnusedIncludeCheck{}), view, file)
	diags := report[file]
	require.Len(t, diags, 1)
	require.Len(t, diags[0].Fixes, 1)

	fix := diags[0].Fixes[0]
	assert.Equal(t, `Remove unused include "shared.thrift"`, fix.Title)
	assert.Equal(t, "struct S { 1: i32 a }\n", applyEdits(t, content, fix.Edits))
}

// TestFixAllEnumValues drives the whole fix loop over a file with implicit
// enum members: every member ends up with the value the compiler would
// auto-increment, and the fixed file passes the check on the next pass.
func TestFixAllEnumValues(t *testing.T) {
	t.Parallel()

	folder := t.TempDir()
	b := uri.File(folder + "/user.thrift")
	files := map[uri.URI][]byte{
		b: []byte("enum Color {\n  RED,\n  GREEN = 2,\n  BLUE,\n}\n"),
	}

	view := store.NewView(uri.File(folder), store.NewMemFS(files), nil)

	var written []byte

	res, err := DefaultPipeline(Config{}).FixAll(t.Context(), view, []uri.URI{b},
		func(_ context.Context, u uri.URI, content []byte) error {
			files[u] = content
			written = content

			return nil
		})
	require.NoError(t, err)

	require.Equal(t, 2, res.Applied)
	require.Empty(t, res.Skipped)

	// Every implicit member gains its auto-incremented value.
	require.Equal(t, []byte("enum Color {\n  RED = 0,\n  GREEN = 2,\n  BLUE = 3,\n}\n"), written)

	// The fixed file passes the check on the next pass.
	require.Empty(t, res.Remaining[b])
}

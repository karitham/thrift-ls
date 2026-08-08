package source

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.lsp.dev/protocol"

	"github.com/karitham/thrift-ls/formatter"
	"github.com/karitham/thrift-ls/lsp/cache"
)

// TestOnTypeFormat verifies that typing a closing brace formats the
// enclosing construct.
func TestOnTypeFormat(t *testing.T) {
	src := "struct S {1: i32 a,2: string b}"
	// The closing brace was just typed at the end of the document.
	pos := protocol.Position{Line: 0, Character: uint32(len(src) - 1)}

	ss := cache.BuildSnapshotForTest([]*cache.FileChange{
		{
			URI:     "file:///tmp/f.thrift",
			Version: 0,
			Content: []byte(src),
			From:    cache.FileChangeTypeDidOpen,
		},
	})
	fh, err := ss.ReadFile(t.Context(), "file:///tmp/f.thrift")
	require.NoError(t, err)

	edits, err := OnTypeFormat(t.Context(), ss, fh, formatter.DefaultOptions(), pos)
	require.NoError(t, err)
	require.Len(t, edits, 1)

	assert.Equal(t, "struct S { 1: i32 a, 2: string b }\n", edits[0].NewText)
}

// TestOnTypeFormatSkipsBrokenDocument verifies that on-type formatting of
// a document with parse errors formats nothing: the Parse checker reports
// the errors, and the request must not fail.
func TestOnTypeFormatSkipsBrokenDocument(t *testing.T) {
	src := "struct S { 1: "
	pos := protocol.Position{Line: 0, Character: uint32(len(src))}

	ss := cache.BuildSnapshotForTest([]*cache.FileChange{
		{
			URI:     "file:///tmp/f.thrift",
			Version: 0,
			Content: []byte(src),
			From:    cache.FileChangeTypeDidOpen,
		},
	})
	fh, err := ss.ReadFile(t.Context(), "file:///tmp/f.thrift")
	require.NoError(t, err)

	edits, err := OnTypeFormat(t.Context(), ss, fh, formatter.DefaultOptions(), pos)
	require.NoError(t, err)
	assert.Empty(t, edits)
}

// TestFormatSkipsBrokenDocument verifies that whole-document formatting of
// a file with parse errors returns no edits instead of failing.
func TestFormatSkipsBrokenDocument(t *testing.T) {
	src := "struct S { 1: "

	ss := cache.BuildSnapshotForTest([]*cache.FileChange{
		{
			URI:     "file:///tmp/f.thrift",
			Version: 0,
			Content: []byte(src),
			From:    cache.FileChangeTypeDidOpen,
		},
	})
	fh, err := ss.ReadFile(t.Context(), "file:///tmp/f.thrift")
	require.NoError(t, err)

	edit, err := FormatDocument(t.Context(), ss, fh, formatter.DefaultOptions())
	require.NoError(t, err)
	assert.Nil(t, edit)
}

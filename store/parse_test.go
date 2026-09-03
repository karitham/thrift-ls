package store

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.lsp.dev/uri"
)

func TestParse(t *testing.T) {
	t.Run("valid file parses to a document", func(t *testing.T) {
		fh := NewOverlay("file:///tmp/types.thrift", []byte(`
#include "base.thrift"
struct Xtruct3
{
  1:  string string_thing,
  4:  i32    changed,
  9:  i32    i32_thing,
  11: i64    i64_thing
}
			`), 0)

		got, err := Parse(fh)
		require.NoError(t, err)
		require.NotNil(t, got)
		require.NotNil(t, got.AST(), "valid content must yield a document")
		assert.Contains(t, got.Definitions(), "Xtruct3")
		assert.NotEmpty(t, got.Tokens(), "identifier tokens are collected")
	})

	t.Run("syntax errors ride the file, not the call", func(t *testing.T) {
		fh := NewOverlay("file:///tmp/types.thrift", []byte(`
#include "base.thrift"
struct Xtruct3
{
  1:  string string_thing,
  4:  i32    changed,
  9:  i32    i32_thing,
  11: i64    i64_thing,
  12: 
}
			`), 0)

		got, err := Parse(fh)
		require.NoError(t, err, "Parse only fails when content is unreadable")
		require.NotNil(t, got)
		assert.NotEmpty(t, got.Errors(), "the trailing comma must surface as a file error")
	})

	t.Run("unreadable content fails the call", func(t *testing.T) {
		u := uri.URI("file:///tmp/ghost.thrift")
		fh, err := NewDiskFS().ReadFile(t.Context(), u)
		require.NoError(t, err)

		got, err := Parse(fh)
		require.Error(t, err)
		assert.Nil(t, got)
	})
}

// TestParsedFileDefinitions pins the definition and enum-value indexes:
// every top-level definition is reachable by name, enum values by name.
func TestParsedFileDefinitions(t *testing.T) {
	ss := BuildViewForTest([]*FileChange{
		{URI: "file:///tmp/test.thrift", Version: 0, Content: []byte(`struct S {
	1: required string Name,
}

union U {
	1: string x,
}

exception X {
	1: string m,
}

enum Color {
	RED,
	GREEN,
}

service Fed {
	void go(),
}

const i32 LIMIT = 1,
typedef string PilotName`), From: FileChangeTypeDidOpen},
	})

	pf, err := ss.Parse(t.Context(), "file:///tmp/test.thrift")
	require.NoError(t, err)

	defs := pf.Definitions()
	require.Len(t, defs, 7)

	for _, name := range []string{"S", "U", "X", "Color", "Fed", "LIMIT", "PilotName"} {
		_, ok := defs[name]
		assert.True(t, ok, "definition %q missing", name)
	}

	values := pf.EnumValues()
	require.Len(t, values, 2)
	assert.NotNil(t, values["RED"])
	assert.NotNil(t, values["GREEN"])
}

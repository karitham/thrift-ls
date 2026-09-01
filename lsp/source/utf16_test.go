package source

// UTF-16 regression tests.
//
// LSP character columns count UTF-16 code units; the parser's columns
// count runes. The two diverge on astral-plane characters (e.g. emoji,
// which encode as surrogate pairs), so any emoji before a token on its
// line shifts the correct LSP column by one. These tests pin every range
// builder to UTF-16 columns.
//
// The fixtures follow the light music club at Sakuragaoka High: HTT
// (Houkago Tea Time) is the band, Gitah the guitar, fuwa fuwa time the
// song.

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.lsp.dev/protocol"
	"go.lsp.dev/uri"

	"github.com/karitham/thrift-ls/lsp/cache"
	"github.com/karitham/thrift-ls/sema"
)

// utf16Snapshot parses src as htt.thrift.
func utf16Snapshot(t *testing.T, src string) *cache.View {
	t.Helper()

	return cache.BuildViewForTest([]*cache.FileChange{
		{URI: "file:///tmp/htt.thrift", Version: 0, Content: []byte(src), From: cache.FileChangeTypeDidOpen},
	})
}

// In "/* 😀 */ struct HTT {}", the emoji is one rune but two UTF-16 units,
// so every token after it on the line sits one column further than the
// rune-based column. HTT starts at UTF-16 column 16.

func TestDefinitionUTF16(t *testing.T) {
	// htt.thrift declares HTT after an emoji comment; sakuragaoka.thrift
	// references it. The returned definition range must use UTF-16 columns.
	view := cache.BuildViewForTest([]*cache.FileChange{
		{URI: "file:///tmp/htt.thrift", Version: 0, Content: []byte("/* 😀 */ struct HTT {}"), From: cache.FileChangeTypeDidOpen},
		{URI: "file:///tmp/sakuragaoka.thrift", Version: 0, Content: []byte("include \"htt.thrift\"\nstruct LightMusicClub {\n  1: required HTT band\n}"), From: cache.FileChangeTypeDidOpen},
	})

	locs, err := Definition(t.Context(), view, "file:///tmp/sakuragaoka.thrift", protocol.Position{
		Line:      2,
		Character: 14, // 'H' of HTT, in UTF-16 units
	})
	require.NoError(t, err)
	require.Len(t, locs, 1)
	assert.Equal(t, uri.URI("file:///tmp/htt.thrift"), locs[0].URI)

	// 'H' of HTT starts at UTF-16 column 16: 15 runes before it, one of
	// them the emoji pair.
	assert.Equal(t, uint32(16), locs[0].Range.Start.Character)
}

func TestDocumentSymbolsUTF16(t *testing.T) {
	src := "/* 😀 */ struct HTT {\n  1: required string yui\n}"

	view := utf16Snapshot(t, src)

	syms := DocumentSymbols(t.Context(), view, "file:///tmp/htt.thrift")
	require.Len(t, syms, 1)

	assert.Equal(t, uint32(16), syms[0].SelectionRange.Start.Character)
}

func TestSemanticTokensUTF16(t *testing.T) {
	tokens := semanticTokens(t, "/* 😀 */ struct HTT {}")

	// The comment is 8 UTF-16 units long: 7 ASCII chars + the emoji pair.
	comment := tokens[0]
	require.Equal(t, tokComment, comment.typ)
	assert.Equal(t, uint32(8), comment.length)

	// The struct keyword starts after the comment at UTF-16 column 9.
	kw := tokens[1]
	require.Equal(t, tokKeyword, kw.typ)
	assert.Equal(t, uint32(9), kw.char)
}

func TestParseErrorDiagnosticUTF16(t *testing.T) {
	src := `/* 😀 */ const string song = "fuwa fuwa time`

	view := utf16Snapshot(t, src)

	report, err := sema.New(sema.Config{}, []sema.Analyzer{&sema.ParseCheck{}}).Run(t.Context(), view, []uri.URI{"file:///tmp/htt.thrift"})
	require.NoError(t, err)

	protoDiags, err := ToProtocolDiagnostics(t.Context(), view, "file:///tmp/htt.thrift", report["file:///tmp/htt.thrift"])
	require.NoError(t, err)
	require.NotEmpty(t, protoDiags)

	// The unterminated string error points at the opening quote, at UTF-16
	// column 29: 28 runes before it, one of them the emoji pair.
	assert.Equal(t, uint32(29), protoDiags[0].Range.Start.Character)
}

func TestLinksUTF16(t *testing.T) {
	src := `include /* 😀 */ "htt.thrift"`

	file := "file:///tmp/sakuragaoka.thrift"
	view := buildLinksSnapshot(t, uri.URI(file), src)

	links := Links(t.Context(), view, uri.URI(file))
	require.Len(t, links, 1)

	// The path literal starts at UTF-16 column 17: 16 runes before it, one
	// of them the emoji pair.
	assert.Equal(t, uint32(17), links[0].Range.Start.Character)
}

func TestFoldingUTF16(t *testing.T) {
	ranges := foldingRanges(t, "/* 😀 */ struct HTT {\n}\n")
	require.Len(t, ranges, 1)

	// The fold spans the braces; the opening brace starts at UTF-16 column
	// 27: 26 runes before it, one of them the emoji pair.
	require.NotNil(t, ranges[0].StartCharacter)
	assert.Equal(t, uint32(20), *ranges[0].StartCharacter)
}

// TestRenameUTF16 exercises the same path as Definition through rename:
// the workspace edit for the definition file must use UTF-16 columns.
func TestRenameUTF16(t *testing.T) {
	view := cache.BuildViewForTest([]*cache.FileChange{
		{URI: "file:///tmp/htt.thrift", Version: 0, Content: []byte("/* 😀 */ struct HTT {}"), From: cache.FileChangeTypeDidOpen},
		{URI: "file:///tmp/sakuragaoka.thrift", Version: 0, Content: []byte("include \"htt.thrift\"\nstruct LightMusicClub {\n  1: required HTT band\n}"), From: cache.FileChangeTypeDidOpen},
	})

	edit, err := Rename(t.Context(), view, "file:///tmp/sakuragaoka.thrift", protocol.Position{
		Line:      2,
		Character: 14,
	}, "HoukagoTeaTime")
	require.NoError(t, err)

	edits := edit.Changes["file:///tmp/htt.thrift"]
	require.NotEmpty(t, edits)

	// The renamed identifier starts at UTF-16 column 16.
	assert.Equal(t, uint32(16), edits[0].Range.Start.Character)
}

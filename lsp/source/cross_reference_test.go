package source

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.lsp.dev/protocol"
	"go.lsp.dev/uri"

	"github.com/karitham/thrift-ls/store"
)

// TestReferenceBareCrossFile covers find-references across an include when
// the reference uses the bare name: federation.gundam.thrift defines
// Gundam; main.thrift includes it and uses the bare Gundam. References
// found from the definition file must include the bare usage.
func TestReferenceBareCrossFile(t *testing.T) {
	mainFile := `include "federation.gundam.thrift"

struct StrikeRouge {
	1: required Gundam pack
}`

	gundamFile := `struct Gundam {
	1: required string Name
}`

	view := store.BuildViewForTest([]*store.FileChange{
		{URI: "file:///tmp/federation.gundam.thrift", Version: 0, Content: []byte(gundamFile), From: store.FileChangeTypeDidOpen},
		{URI: "file:///tmp/main.thrift", Version: 0, Content: []byte(mainFile), From: store.FileChangeTypeDidOpen},
	})

	// Cursor on "Gundam" (the definition name) in federation.gundam.thrift.
	locations, err := Reference(t.Context(), view, "file:///tmp/federation.gundam.thrift", protocol.Position{Line: 0, Character: 7})
	require.NoError(t, err)
	require.Len(t, locations, 1)

	assert.Equal(t, protocol.Location{
		URI: "file:///tmp/main.thrift",
		Range: protocol.Range{
			Start: protocol.Position{Line: 3, Character: 13},
			End:   protocol.Position{Line: 3, Character: 19},
		},
	}, locations[0], "bare reference resolves to its exact range")
}

// TestReferenceQualifiedCrossFile covers find-references when the
// reference uses the include-qualified name (includeName.Type).
func TestReferenceQualifiedCrossFile(t *testing.T) {
	mainFile := `include "federation.gundam.thrift"

struct StrikeRouge {
	1: required federation.gundam.Gundam pack
}`

	gundamFile := `struct Gundam {
	1: required string Name
}`

	view := store.BuildViewForTest([]*store.FileChange{
		{URI: "file:///tmp/federation.gundam.thrift", Version: 0, Content: []byte(gundamFile), From: store.FileChangeTypeDidOpen},
		{URI: "file:///tmp/main.thrift", Version: 0, Content: []byte(mainFile), From: store.FileChangeTypeDidOpen},
	})

	locations, err := Reference(t.Context(), view, "file:///tmp/federation.gundam.thrift", protocol.Position{Line: 0, Character: 7})
	require.NoError(t, err)
	require.Len(t, locations, 1)

	assert.Equal(t, uri.URI("file:///tmp/main.thrift"), locations[0].URI)
	assert.Equal(t, protocol.Position{Line: 3, Character: 13}, locations[0].Range.Start)
	assert.Equal(t, protocol.Position{Line: 3, Character: 37}, locations[0].Range.End,
		"qualified reference covers the full dotted name")
}

// TestDefinitionTransitiveInclude covers go-to-definition through a
// multi-hop include chain: main.thrift includes federation.gundam.thrift,
// which includes mobile_suit.zeon.thrift, which defines Zaku. A definition
// lookup in main.thrift must reach it.
func TestDefinitionTransitiveInclude(t *testing.T) {
	mainFile := `include "federation.gundam.thrift"

struct Char {
	1: optional Zaku ride
}`

	federationFile := `include "mobile_suit.zeon.thrift"

struct Gundam {
	1: required string Name
}`

	zeonFile := `struct Zaku {
	1: required string Model
}`

	view := store.BuildViewForTest([]*store.FileChange{
		{URI: "file:///tmp/mobile_suit.zeon.thrift", Version: 0, Content: []byte(zeonFile), From: store.FileChangeTypeDidOpen},
		{URI: "file:///tmp/federation.gundam.thrift", Version: 0, Content: []byte(federationFile), From: store.FileChangeTypeDidOpen},
		{URI: "file:///tmp/main.thrift", Version: 0, Content: []byte(mainFile), From: store.FileChangeTypeDidOpen},
	})

	// Cursor on "Zaku" in main.thrift (line 3, character 13: the type of
	// "1: optional Zaku ride").
	locations, err := Definition(t.Context(), view, "file:///tmp/main.thrift", protocol.Position{Line: 3, Character: 13})
	assert.NoError(t, err)

	assert.Len(t, locations, 1)
	assert.Equal(t, uri.URI("file:///tmp/mobile_suit.zeon.thrift"), locations[0].URI, "definition must resolve through the include chain")
}

package source

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"go.lsp.dev/protocol"
	"go.lsp.dev/uri"

	"github.com/karitham/thrift-ls/lsp/cache"
	"github.com/karitham/thrift-ls/lsp/types"
	"github.com/karitham/thrift-ls/syntax"
)

// lspPosOf returns the LSP position (0-based line, UTF-16 character)
// immediately after the first occurrence of marker in content.
func lspPosOf(t *testing.T, content, marker string) types.Position {
	t.Helper()

	idx := strings.Index(content, marker)
	assert.NotEqual(t, -1, idx, "marker %q not found", marker)

	before := content[:idx]
	line := strings.Count(before, "\n")

	lineStart := strings.LastIndex(before, "\n") + 1

	return types.Position{
		Line:      uint32(line),
		Character: uint32(utf16Len([]byte(before[lineStart:])) + utf16Len([]byte(marker))),
	}
}

func utf16Len(b []byte) int {
	n := 0

	for _, r := range string(b) {
		if r > 0xFFFF {
			n += 2
		} else {
			n++
		}
	}

	return n
}

// completionLabels runs the completion entry point at an LSP position and
// returns the item labels, the edit range, and the truncated flag.
func completionLabels(t *testing.T, ss *cache.Snapshot, file string, pos types.Position) ([]string, protocol.Range, bool) {
	t.Helper()

	fh, err := ss.ReadFile(t.Context(), uri.URI(file))
	assert.NoError(t, err)

	items, rng, truncated, err := DefaultTokenCompletion.Completion(t.Context(), ss, &CompletionRequest{
		Pos: pos,
		Fh:  fh,
	})
	assert.NoError(t, err)

	labels := make([]string, 0, len(items))
	for _, item := range items {
		labels = append(labels, item.Label)
	}

	return labels, rng, truncated
}

// completionItems runs the entry point and returns raw items.
func completionItems(t *testing.T, ss *cache.Snapshot, file string, pos types.Position) ([]*CompletionItem, protocol.Range, bool) {
	t.Helper()

	fh, err := ss.ReadFile(t.Context(), uri.URI(file))
	assert.NoError(t, err)

	items, rng, truncated, err := DefaultTokenCompletion.Completion(t.Context(), ss, &CompletionRequest{
		Pos: pos,
		Fh:  fh,
	})
	assert.NoError(t, err)

	return items, rng, truncated
}

// gundamSnapshot builds a snapshot with the gundam corpus: a main file with
// a struct, enum, const, and an included file defining another type. It
// returns the snapshot and the main file content.
func gundamSnapshot(t *testing.T, includePaths []string) (*cache.Snapshot, string) {
	t.Helper()

	mainContent := `include "federation.gundam.thrift"

struct Gundam {
	1: required string Name (color = "red")
}

enum ZeonForces {
	ZAKU_I = 1,
	GELGOOG
}

const i32 LIMIT = 10`

	incContent := `struct MobileSuit {
	1: required string ModelName
}

exception BayFull {
	1: string message
}`

	ss := buildSnapshot(t, includePaths,
		&cache.FileChange{URI: "file:///tmp/main.thrift", Version: 0, Content: []byte(mainContent), From: cache.FileChangeTypeDidOpen},
		&cache.FileChange{URI: "file:///tmp/federation.gundam.thrift", Version: 0, Content: []byte(incContent), From: cache.FileChangeTypeDidOpen},
	)

	return ss, mainContent
}

func TestCompletionSlots(t *testing.T) {
	ss, mainContent := gundamSnapshot(t, nil)

	tests := []struct {
		name    string
		marker  string
		want    []string
		notWant []string
	}{
		{
			name:   "type slot suggests types and base keywords",
			marker: "1: required ",
			want:   []string{"Gundam", "i32"},
		},
		{
			name:   "field name slot suggests the field name",
			marker: "1: required string Na",
			want:   []string{"Name"},
		},
		{
			name:   "value slot suggests consts and enum values",
			marker: "const i32 LIMIT = ",
			want:   []string{"LIMIT", "ZAKU_I", "ZeonForces.ZAKU_I"},
		},
		{
			name:   "annotation key slot suggests known keys",
			marker: "1: required string Name (c",
			want:   []string{"color"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			pos := lspPosOf(t, mainContent, tt.marker)

			labels, _, _ := completionLabels(t, ss, "file:///tmp/main.thrift", pos)

			for _, w := range tt.want {
				assert.Contains(t, labels, w, "labels: %v", labels)
			}

			for _, nw := range tt.notWant {
				assert.NotContains(t, labels, nw, "labels: %v", labels)
			}
		})
	}
}

// TestCompletionNoDuplicates: providers overlap (a type is both a type
// candidate and an identifier token), so the shared pipeline must dedupe.
func TestCompletionNoDuplicates(t *testing.T) {
	ss, mainContent := gundamSnapshot(t, nil)

	for _, marker := range []string{"1: required ", "const i32 LIMIT = "} {
		pos := lspPosOf(t, mainContent, marker)

		labels, _, _ := completionLabels(t, ss, "file:///tmp/main.thrift", pos)

		seen := make(map[string]struct{}, len(labels))
		for _, label := range labels {
			assert.NotContains(t, seen, label, "duplicate candidate %q in %v", label, labels)
			seen[label] = struct{}{}
		}
	}
}

// TestCompletionSlotProviders asserts the full (uncapped) candidate sets per
// provider: cross-file types, modifiers on field names, and the absence of
// value candidates on field-name positions (regression: the old code
// suggested values while typing a field name).
func TestCompletionSlotProviders(t *testing.T) {
	ss, _ := gundamSnapshot(t, nil)

	cc := Context{Doc: mustParse(t, ss, "file:///tmp/main.thrift")}

	ctx := t.Context()

	typeCands := typeProvider{}.Candidates(ctx, ss, "file:///tmp/main.thrift", cc)
	typeLabels := labelsOf(typeCands)
	assert.Contains(t, typeLabels, "Gundam")
	assert.Contains(t, typeLabels, "MobileSuit", "types from included files")
	assert.Contains(t, typeLabels, "BayFull", "types from included files")

	fieldCands := fieldNameProvider{}.Candidates(ctx, ss, "file:///tmp/main.thrift", cc)
	fieldLabels := labelsOf(fieldCands)
	assert.Contains(t, fieldLabels, "required")
	assert.Contains(t, fieldLabels, "optional")
	assert.NotContains(t, fieldLabels, "ZeonForces.ZAKU_I", "field name slot must not suggest qualified values")

	valueCands := valueProvider{}.Candidates(ctx, ss, "file:///tmp/main.thrift", cc)
	valueLabels := labelsOf(valueCands)
	assert.Contains(t, valueLabels, "ZeonForces.ZAKU_I", "value slot suggests qualified enum values")

	keyCands := annotationKeyProvider{}.Candidates(ctx, ss, "file:///tmp/main.thrift", cc)
	keyLabels := labelsOf(keyCands)
	assert.Contains(t, keyLabels, "color")
}

func labelsOf(cands []Candidate) []string {
	labels := make([]string, 0, len(cands))
	for _, c := range cands {
		labels = append(labels, c.showText)
	}

	return labels
}

func mustParse(t *testing.T, ss *cache.Snapshot, file string) *syntax.Document {
	t.Helper()

	pf, err := ss.Parse(t.Context(), uri.URI(file))
	assert.NoError(t, err)
	assert.NotNil(t, pf.AST())

	return pf.AST()
}

// TestCompletionQualifiedValue covers "ZeonForces.|": the qualified name is
// filtered by the typed prefix, inserted without the qualifier, and the edit
// range starts at the cursor (the dot stays).
func TestCompletionQualifiedValue(t *testing.T) {
	_, mainContent := gundamSnapshot(t, nil)

	content := strings.Replace(mainContent, "const i32 LIMIT = 10", "const i32 LIMIT = ZeonForces.", 1)
	assert.NotEqual(t, mainContent, content)

	ss := buildSnapshot(t, nil,
		&cache.FileChange{URI: "file:///tmp/main.thrift", Version: 0, Content: []byte(content), From: cache.FileChangeTypeDidOpen},
		&cache.FileChange{URI: "file:///tmp/federation.gundam.thrift", Version: 0, Content: []byte("struct MobileSuit {\n\t1: required string ModelName\n}"), From: cache.FileChangeTypeDidOpen},
	)

	dotPos := lspPosOf(t, content, "const i32 LIMIT = ZeonForces.")

	items, rng, _ := completionItems(t, ss, "file:///tmp/main.thrift", dotPos)

	var labels []string
	for _, item := range items {
		labels = append(labels, item.Label)
	}

	assert.Contains(t, labels, "ZeonForces.ZAKU_I")

	// The edit range starts at the cursor: the dot is not replaced.
	assert.Equal(t, dotPos.Character, rng.Start.Character)

	for _, item := range items {
		if item.Label == "ZeonForces.ZAKU_I" {
			assert.Equal(t, "ZAKU_I", item.InsertText)
		}
	}
}

func TestCompletionKeywordFallback(t *testing.T) {
	ss := buildSnapshot(t, nil,
		&cache.FileChange{URI: "file:///tmp/empty.thrift", Version: 0, Content: []byte(""), From: cache.FileChangeTypeDidOpen},
	)

	labels, _, truncated := completionLabels(t, ss, "file:///tmp/empty.thrift", types.Position{Line: 0, Character: 0})
	assert.Contains(t, labels, "include")
	assert.True(t, truncated, "keyword fallback exceeds the cap")
}

// TestCompletionCapReportsIncomplete: a small result set reports the list as
// complete (isIncomplete false), the keyword fallback reports truncation.
func TestCompletionCapReportsIncomplete(t *testing.T) {
	ss, mainContent := gundamSnapshot(t, nil)

	pos := lspPosOf(t, mainContent, "1: required ")
	_, _, truncated := completionLabels(t, ss, "file:///tmp/main.thrift", pos)
	assert.True(t, truncated, "type slot exceeds the cap (types + keywords)")

	pos = lspPosOf(t, mainContent, "1: required string Name (c")
	_, _, truncated = completionLabels(t, ss, "file:///tmp/main.thrift", pos)
	assert.False(t, truncated, "annotation key slot has one candidate")
}

// TestCompletionIncludePath covers include-path completion: quotes are
// preserved (the edit range excludes them) and configured include path roots
// are searched in addition to the current directory.
func TestCompletionIncludePath(t *testing.T) {
	dir := t.TempDir()

	assert.NoError(t, os.WriteFile(filepath.Join(dir, "federation.gundam.thrift"), []byte("struct Gundam {}"), 0o644))
	assert.NoError(t, os.MkdirAll(filepath.Join(dir, "zeon"), 0o755))
	assert.NoError(t, os.WriteFile(filepath.Join(dir, "zeon", "mobile_suit.thrift"), []byte("enum ZeonForces {}"), 0o644))

	mainContent := "include \"fed|"
	pos := lspPosOf(t, mainContent, "include \"fed")

	ss := buildSnapshot(t, []string{filepath.Join(dir, "zeon")},
		&cache.FileChange{URI: uri.File(filepath.Join(dir, "main.thrift")), Version: 0, Content: []byte(mainContent), From: cache.FileChangeTypeDidOpen},
	)

	labels, rng, _ := completionLabels(t, ss, uri.File(filepath.Join(dir, "main.thrift")).String(), pos)
	assert.Contains(t, labels, "federation.gundam.thrift")

	// The edit range starts after the opening quote: "include \"" is 9
	// UTF-16 units.
	assert.Equal(t, uint32(9), rng.Start.Character, "range must exclude the opening quote")

	// includePaths root is searched too: typing "zeon/m" lists subdir files.
	mainContent2 := "include \"zeon/m"
	pos2 := lspPosOf(t, mainContent2, "include \"zeon/m")
	ss2 := buildSnapshot(t, []string{filepath.Join(dir, "zeon")},
		&cache.FileChange{URI: uri.File(filepath.Join(dir, "main.thrift")), Version: 0, Content: []byte(mainContent2), From: cache.FileChangeTypeDidOpen},
	)

	labels2, _, _ := completionLabels(t, ss2, uri.File(filepath.Join(dir, "main.thrift")).String(), pos2)
	assert.Contains(t, labels2, "zeon/mobile_suit.thrift", "include path roots must be searched")
}

// TestCompletionNoPrefixUnderflow: a line without spaces must not produce a
// wrapped edit range (the old whole-file prefix fallback bug).
func TestCompletionNoPrefixUnderflow(t *testing.T) {
	ss := buildSnapshot(t, nil,
		&cache.FileChange{URI: "file:///tmp/underflow.thrift", Version: 0, Content: []byte("const X=1"), From: cache.FileChangeTypeDidOpen},
	)

	_, rng, _ := completionLabels(t, ss, "file:///tmp/underflow.thrift", types.Position{Line: 0, Character: 9})
	assert.LessOrEqual(t, rng.Start.Character, uint32(9), "edit range must not wrap")
}

// TestCompletionNonASCIIPrefix: a non-ASCII line prefix must not corrupt the
// edit range character (UTF-16 vs byte counting).
func TestCompletionNonASCIIPrefix(t *testing.T) {
	content := "// モビルスーツ\nconst X=1 😀"
	pos := lspPosOf(t, content, "const X=1 😀")

	ss := buildSnapshot(t, nil,
		&cache.FileChange{URI: "file:///tmp/emoji.thrift", Version: 0, Content: []byte(content), From: cache.FileChangeTypeDidOpen},
	)

	_, rng, _ := completionLabels(t, ss, "file:///tmp/emoji.thrift", pos)
	assert.Equal(t, pos.Character, rng.Start.Character, "empty prefix: range starts at the cursor")
}

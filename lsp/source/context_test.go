package source

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/karitham/thrift-ls/syntax"
)

// parseWithCursor parses src with a | cursor marker and returns the
// document and the byte offset of the cursor.
func parseWithCursor(t *testing.T, src string) (*syntax.Document, int) {
	t.Helper()

	idx := strings.Index(src, "|")
	assert.NotEqual(t, -1, idx, "missing cursor marker in %q", src)

	doc, _ := syntax.Parse([]byte(src[:idx] + src[idx+1:]))

	return doc, idx
}

func TestResolveContext(t *testing.T) {
	tests := []struct {
		name string
		src  string
		want ContextKind
	}{
		// Top level and keywords.
		{"empty file", `|`, CtxKeyword},
		{"whitespace only", "   |\n", CtxKeyword},
		{"service name slot", `service |Federation {`, CtxDefinitionName},
		{"service name slot at end", `service Federatio|n`, CtxDefinitionName},

		// Include paths.
		{"include path mid-string", `include "fed|era"`, CtxIncludePath},
		{"include path unterminated at EOF", `include "fed|`, CtxIncludePath},
		{"include path after closing quote", `include "federation.gundam.thrift"|`, CtxKeyword},
		{"cpp include path", `cpp_include "zeon.thrift|"`, CtxIncludePath},

		// Container member slots.
		{"struct body after brace", `struct Gundam {|`, CtxFieldName},
		{"struct body after separator", `struct Gundam { 1: string Name; |}`, CtxFieldName},
		{"union body", `union MobileSuit { 1: string Name, |}`, CtxFieldName},
		{"exception body", `exception BayFull {|`, CtxFieldName},
		{"enum body after brace", `enum ZeonForces {|`, CtxEnumValueName},
		{"enum body after separator", `enum ZeonForces { ZAKU_I, |}`, CtxEnumValueName},
		{"service body after brace", `service Federation {|`, CtxFunctionName},
		{"service body after separator", `service Federation { void f(); |}`, CtxFunctionName},

		// Function args and throws.
		{"function args after paren", `service Federation { void f(|`, CtxFieldName},
		{"function args after comma", `service Federation { void f(1: i32 id, |`, CtxFieldName},
		{"function args after a comment line", "service Federation { void f(1: i32 id, // c\n|", CtxFieldName},
		{"function args after a same-line comment", `service Federation { void f(1: i32 id /* c */, |`, CtxFieldName},
		{"cursor in a comment is not a slot", "service Federation { void f(1: i32 id, // |c", CtxNone},
		{"throws after paren", `service Federation { void f() throws (|`, CtxFieldName},
		{"throws after comma", `service Federation { void f() throws (1: string m, |`, CtxFieldName},
		{"function annotations after args", `service Federation { void f() (|`, CtxAnnotationKey},

		// Annotations.
		{"field annotation after name", `struct Gundam { 1: string Name (|`, CtxAnnotationKey},
		{"field annotation after comma", `struct Gundam { 1: string Name (color = "blue", |)`, CtxAnnotationKey},
		{"field annotation value string", `struct Gundam { 1: string Name (color = "|")`, CtxAnnotationValue},
		{"type annotation after base type", `struct Gundam { 1: string (|) Name`, CtxAnnotationKey},

		// Field structure.
		{"field value after equal", `struct Gundam { 1: string Name = |}`, CtxFieldValue},
		{"const value after equal", `const i32 LIMIT = |`, CtxFieldValue},
		{"enum value after equal", `enum ZeonForces { ZAKU_I = |}`, CtxFieldValue},
		{"field id before colon", `struct Gundam { |1: string Name }`, CtxFieldID},
		{"field type after colon", `struct Gundam { 1: |}`, CtxType},
		{"field type after required", `struct Gundam { 1: required |}`, CtxType},
		{"field name on identifier", `struct Gundam { 1: required string Na|me }`, CtxFieldName},
		{"field type on identifier", `struct Gundam { 1: required Str|ing Name }`, CtxType},
		{"map key type", `struct Gundam { 1: map<|i32, string> fields }`, CtxType},
		{"field after annotations", `struct Gundam { 1: string Name (color = "blue") |}`, CtxKeyword},

		// Values.
		{"const ident value", `const i32 LIMIT = Color.GRE|EN`, CtxFieldValue},
		{"const string value", `const string s = "|`, CtxNone},
		{"const string value closed", `const string s = "x"|`, CtxNone},
		{"const int value", `const i32 LIMIT = 1|`, CtxNone},
		{"argument default string", `service Federation { void f(1: string s = "|") }`, CtxNone},

		// Service extends.
		{"extends after keyword", `service Federation extends |`, CtxServiceExtends},
		{"extends on identifier", `service Federation extends Zeo|n {`, CtxServiceExtends},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			doc, offset := parseWithCursor(t, tt.src)

			pos := positionAt(t, doc, offset)
			got := ResolveContext(doc, pos)

			assert.Equal(t, tt.want, got.Kind)
		})
	}
}

// positionAt computes the document position for a byte offset.
func positionAt(t *testing.T, doc *syntax.Document, offset int) syntax.Position {
	t.Helper()

	// Reconstruct line starts from the source offsets recorded on tokens.
	for i := range doc.Tokens {
		if doc.Tokens[i].Offset <= offset {
			continue
		}
	}

	// The parser stores 1-based line/col per token; find the token at or
	// before the offset and derive the position from it.
	line, col := 1, 1

	for i := range doc.Tokens {
		tok := &doc.Tokens[i]
		if tok.Offset > offset {
			break
		}

		line = tok.Line
		col = tok.Col + (offset - tok.Offset)
	}

	return syntax.Position{Line: line, Col: col, Offset: offset}
}

// TestResolveContextNeverPanics exercises every byte offset of a document
// covering include, struct, enum, service, annotations, and values: the
// resolver must never panic on truncated or mid-token positions.
func TestResolveContextNeverPanics(t *testing.T) {
	src := `include "federation.gundam.thrift"

exception BayFull {
	1: string message (code = "BAY_FULL")
}

enum ZeonForces {
	ZAKU_I = 1,
	GELGOOG
}

service Federation extends ZeonForces {
	void intercept(i32 count) throws (1: BayFull bay)
}`

	doc, errs := syntax.Parse([]byte(src))
	assert.Empty(t, errs)

	for offset := 0; offset <= len(src); offset++ {
		pos := positionAt(t, doc, offset)
		assert.NotPanics(t, func() {
			_ = ResolveContext(doc, pos)
		}, "offset %d", offset)
	}
}

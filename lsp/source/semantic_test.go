package source

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.lsp.dev/protocol"

	"github.com/karitham/thrift-ls/lsp/cache"
)

// decodedToken is one semantic token in absolute coordinates.
type decodedToken struct {
	line, char, length uint32
	typ                int
}

// decode converts the delta-encoded token data back into absolute
// coordinates.
func decode(data []uint32) []decodedToken {
	var out []decodedToken

	line, char := uint32(0), uint32(0)

	for i := 0; i+4 < len(data); i += 5 {
		line += data[i]
		if data[i] == 0 {
			char += data[i+1]
		} else {
			char = data[i+1]
		}

		out = append(out, decodedToken{
			line:   line,
			char:   char,
			length: data[i+2],
			typ:    int(data[i+3]),
		})
	}

	return out
}

func semanticTokens(t *testing.T, src string) []decodedToken {
	t.Helper()

	ss := cache.BuildSnapshotForTest([]*cache.FileChange{
		{URI: "file:///tmp/main.thrift", Version: 0, Content: []byte(src), From: cache.FileChangeTypeDidOpen},
	})

	data, err := Tokens(t.Context(), ss, "file:///tmp/main.thrift")
	require.NoError(t, err)

	return decode(data)
}

func TestSemanticTokens(t *testing.T) {
	tests := []struct {
		name string
		src  string
		want []decodedToken
	}{
		{
			name: "keywords, comments, strings, numbers",
			src:  "// doc\nconst i32 LIMIT = 10\ninclude \"base.thrift\"",
			want: []decodedToken{
				{0, 0, 6, tokComment},
				{1, 0, 5, tokKeyword}, // const
				{1, 6, 3, tokType},    // i32
				{1, 10, 5, tokVariable},
				{1, 18, 2, tokNumber},
				{2, 0, 7, tokKeyword}, // include
				{2, 8, 13, tokString},
			},
		},
		{
			name: "definition names and members",
			src: `struct MobileSuit {
	1: required string Name
}

service Federation {
	void Deploy(1: string suitName),
}

enum ZeonForces {
	ZAKU_I = 1,
}`,
			want: []decodedToken{
				{0, 0, 6, tokKeyword},    // struct
				{0, 7, 10, tokStruct},    // MobileSuit
				{1, 1, 1, tokNumber},     // 1
				{1, 4, 8, tokKeyword},    // required
				{1, 13, 6, tokType},      // string
				{1, 20, 4, tokProperty},  // Name
				{4, 0, 7, tokKeyword},    // service
				{4, 8, 10, tokInterface}, // Federation
				{5, 1, 4, tokType},       // void
				{5, 6, 6, tokFunction},   // Deploy
				{5, 13, 1, tokNumber},    // 1
				{5, 16, 6, tokType},      // string
				{5, 23, 8, tokProperty},  // suitName
				{8, 0, 4, tokKeyword},    // enum
				{8, 5, 10, tokEnum},      // ZeonForces
				{9, 1, 6, tokEnumMember}, // ZAKU_I
				{9, 10, 1, tokNumber},    // 1
			},
		},
		{
			name: "type references and nested containers",
			src: `struct StrikeRouge {
	1: required map<string, Gundam> packs
}`,
			want: []decodedToken{
				{0, 0, 6, tokKeyword},   // struct
				{0, 7, 11, tokStruct},   // StrikeRouge
				{1, 1, 1, tokNumber},    // 1
				{1, 4, 8, tokKeyword},   // required
				{1, 13, 3, tokType},     // map
				{1, 17, 6, tokType},     // string
				{1, 25, 6, tokType},     // Gundam
				{1, 33, 5, tokProperty}, // packs
			},
		},
		{
			name: "a keyword used as a field name stays a property",
			src:  "struct S {\n\t1: string string\n}",
			want: []decodedToken{
				{0, 0, 6, tokKeyword},   // struct
				{0, 7, 1, tokStruct},    // S
				{1, 1, 1, tokNumber},    // 1
				{1, 4, 6, tokType},      // string (type position)
				{1, 11, 6, tokProperty}, // string (field name)
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, semanticTokens(t, tt.src))
		})
	}
}

// TestSemanticTokensEncoding pins the delta encoding: tokens on the same
// line carry relative characters, tokens on new lines carry absolute ones.
func TestSemanticTokensEncoding(t *testing.T) {
	ss := cache.BuildSnapshotForTest([]*cache.FileChange{
		{URI: "file:///tmp/main.thrift", Version: 0, Content: []byte("const i32 A = 1\nconst i32 B = 2"), From: cache.FileChangeTypeDidOpen},
	})

	data, err := Tokens(t.Context(), ss, "file:///tmp/main.thrift")
	require.NoError(t, err)

	// Token 0: line 0, char 0. Token 1 (i32): same line, relative char 6.
	// Token 4 (const on line 1): delta line 1, absolute char 0.
	require.GreaterOrEqual(t, len(data), 25)
	assert.Equal(t, uint32(0), data[0])
	assert.Equal(t, uint32(0), data[1])
	assert.Equal(t, uint32(0), data[5]) // same line as the previous token
	assert.Equal(t, uint32(6), data[6]) // relative char
	assert.Equal(t, uint32(1), data[20])
	assert.Equal(t, uint32(0), data[21]) // absolute char on the new line
}

// TestSemanticTokensLegend pins the advertised legend.
func TestSemanticTokensLegend(t *testing.T) {
	assert.Equal(t, []string{
		"keyword", "string", "number", "comment",
		"type", "struct", "union", "exception", "enum", "interface",
		"property", "function", "enumMember", "variable",
	}, Legend())
}

var _ = protocol.SemanticTokens{}

// TestSemanticTokensUnionException pins distinct token types for union and
// exception definitions.
func TestSemanticTokensUnionException(t *testing.T) {
	src := `union MobileArmor {
	1: string loadout,
}

exception BayFull {
	1: string message,
}`

	got := semanticTokens(t, src)

	var union, exception decodedToken

	found := 0

	for _, tok := range got {
		switch tok.typ {
		case tokUnion:
			union = tok
			found++
		case tokException:
			exception = tok
			found++
		}
	}

	require.Equal(t, 2, found)

	lines := strings.Split(src, "\n")
	assert.Equal(t, "MobileArmor", lines[union.line][union.char:union.char+union.length])
	assert.Equal(t, "BayFull", lines[exception.line][exception.char:exception.char+exception.length])
}

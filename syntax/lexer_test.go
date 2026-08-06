package syntax

import (
	"reflect"
	"strings"
	"testing"
)

type tokSpec struct {
	kind TokenKind
	text string
}

func tokenSpecs(toks []Token) []tokSpec {
	specs := make([]tokSpec, 0, len(toks))
	for _, tok := range toks {
		specs = append(specs, tokSpec{tok.Kind, tok.Text})
	}
	return specs
}

func TestLexTokens(t *testing.T) {
	eof := tokSpec{TokenEOF, ""}
	tests := []struct {
		name string
		src  string
		want []tokSpec
	}{
		{"empty", "", []tokSpec{eof}},
		{"whitespace only", "  \n\t\r\n", []tokSpec{eof}},
		{"identifiers", "foo _bar baz1", []tokSpec{
			{TokenIdentifier, "foo"},
			{TokenIdentifier, "_bar"},
			{TokenIdentifier, "baz1"},
			eof,
		}},
		{"dotted identifier", "foo.bar.baz", []tokSpec{
			{TokenIdentifier, "foo.bar.baz"},
			eof,
		}},
		{"dotted keyword is identifier", "include.foo", []tokSpec{
			{TokenIdentifier, "include.foo"},
			eof,
		}},
		{"ints", "0 42 -1 +7", []tokSpec{
			{TokenIntConstant, "0"},
			{TokenIntConstant, "42"},
			{TokenIntConstant, "-1"},
			{TokenIntConstant, "+7"},
			eof,
		}},
		{"hex", "0x1F -0x10 +0xabcdef 0X2A", []tokSpec{
			{TokenIntConstant, "0x1F"},
			{TokenIntConstant, "-0x10"},
			{TokenIntConstant, "+0xabcdef"},
			{TokenIntConstant, "0X2A"},
			eof,
		}},
		{"bare 0x splits into 0 and x", "0x", []tokSpec{
			{TokenIntConstant, "0"},
			{TokenIdentifier, "x"},
			eof,
		}},
		{"doubles", "1.5 -1.5 .5 1e10 1.5e-3 +2.0 -0.25E+2", []tokSpec{
			{TokenDoubleConstant, "1.5"},
			{TokenDoubleConstant, "-1.5"},
			{TokenDoubleConstant, ".5"},
			{TokenDoubleConstant, "1e10"},
			{TokenDoubleConstant, "1.5e-3"},
			{TokenDoubleConstant, "+2.0"},
			{TokenDoubleConstant, "-0.25E+2"},
			eof,
		}},
		{"int wins ties against double", "-1 42", []tokSpec{
			{TokenIntConstant, "-1"},
			{TokenIntConstant, "42"},
			eof,
		}},
		{"e10 is an identifier", "e10", []tokSpec{
			{TokenIdentifier, "e10"},
			eof,
		}},
		{"1e splits into int and identifier", "1e", []tokSpec{
			{TokenIntConstant, "1"},
			{TokenIdentifier, "e"},
			eof,
		}},
		{"strings", `"foo" 'bar' "a\"b" 'it\'s'`, []tokSpec{
			{TokenStringLiteral, `"foo"`},
			{TokenStringLiteral, `'bar'`},
			{TokenStringLiteral, `"a\"b"`},
			{TokenStringLiteral, `'it\'s'`},
			eof,
		}},
		{"escape sequences", `"\r\n\t\"\'\\"`, []tokSpec{
			{TokenStringLiteral, `"\r\n\t\"\'\\"`},
			eof,
		}},
		{"empty strings", `"" ''`, []tokSpec{
			{TokenStringLiteral, `""`},
			{TokenStringLiteral, `''`},
			eof,
		}},
		{"symbols", "{}()[]<>,;:=*&", []tokSpec{
			{TokenLBrace, "{"},
			{TokenRBrace, "}"},
			{TokenLParen, "("},
			{TokenRParen, ")"},
			{TokenLBracket, "["},
			{TokenRBracket, "]"},
			{TokenLt, "<"},
			{TokenGt, ">"},
			{TokenComma, ","},
			{TokenSemicolon, ";"},
			{TokenColon, ":"},
			{TokenEqual, "="},
			{TokenStar, "*"},
			{TokenAmp, "&"},
			eof,
		}},
		{"all keywords", "include cpp_include cpp_type namespace struct union exception service enum const typedef oneway async throws extends required optional void bool byte i8 i16 i32 i64 double string binary slist uuid map list set true false", []tokSpec{
			{TokenInclude, "include"},
			{TokenCPPInclude, "cpp_include"},
			{TokenCPPType, "cpp_type"},
			{TokenNamespace, "namespace"},
			{TokenStruct, "struct"},
			{TokenUnion, "union"},
			{TokenException, "exception"},
			{TokenService, "service"},
			{TokenEnum, "enum"},
			{TokenConst, "const"},
			{TokenTypedef, "typedef"},
			{TokenOneway, "oneway"},
			{TokenAsync, "async"},
			{TokenThrows, "throws"},
			{TokenExtends, "extends"},
			{TokenRequired, "required"},
			{TokenOptional, "optional"},
			{TokenVoid, "void"},
			{TokenBool, "bool"},
			{TokenByte, "byte"},
			{TokenI8, "i8"},
			{TokenI16, "i16"},
			{TokenI32, "i32"},
			{TokenI64, "i64"},
			{TokenDouble, "double"},
			{TokenString, "string"},
			{TokenBinary, "binary"},
			{TokenSlist, "slist"},
			{TokenUUID, "uuid"},
			{TokenMap, "map"},
			{TokenList, "list"},
			{TokenSet, "set"},
			{TokenTrue, "true"},
			{TokenFalse, "false"},
			eof,
		}},
		{"struct skeleton", "struct S {\n 1: i32 id\n}", []tokSpec{
			{TokenStruct, "struct"},
			{TokenIdentifier, "S"},
			{TokenLBrace, "{"},
			{TokenIntConstant, "1"},
			{TokenColon, ":"},
			{TokenI32, "i32"},
			{TokenIdentifier, "id"},
			{TokenRBrace, "}"},
			eof,
		}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			toks, errs := Lex([]byte(tt.src))
			if len(errs) > 0 {
				t.Fatalf("unexpected errors: %v", errs)
			}
			if got := tokenSpecs(toks); !reflect.DeepEqual(got, tt.want) {
				t.Errorf("tokens mismatch\n got: %v\nwant: %v", got, tt.want)
			}
		})
	}
}

type posSpec struct {
	line, col, offset int
}

func TestLexPositions(t *testing.T) {
	tests := []struct {
		name string
		src  string
		want []posSpec // one per token, aligned with tokenSpecs order
	}{
		{
			"single line",
			"struct S {}",
			[]posSpec{{1, 1, 0}, {1, 8, 7}, {1, 10, 9}, {1, 11, 10}, {1, 12, 11}},
		},
		{
			"multiline",
			"struct S {\n  1: i32 id\n}",
			[]posSpec{{1, 1, 0}, {1, 8, 7}, {1, 10, 9}, {2, 3, 13}, {2, 4, 14}, {2, 6, 16}, {2, 10, 20}, {3, 1, 23}, {3, 2, 24}},
		},
		{
			"columns count runes",
			"// é\nstruct S {}",
			[]posSpec{{2, 1, 6}, {2, 8, 13}, {2, 10, 15}, {2, 11, 16}, {2, 12, 17}},
		},
		{
			"crlf line endings",
			"struct S {\r\n}",
			[]posSpec{{1, 1, 0}, {1, 8, 7}, {1, 10, 9}, {2, 1, 12}, {2, 2, 13}},
		},
		{
			"string with multibyte content",
			`const string s = "héllo"`,
			[]posSpec{{1, 1, 0}, {1, 7, 6}, {1, 14, 13}, {1, 16, 15}, {1, 18, 17}, {1, 25, 25}},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			toks, errs := Lex([]byte(tt.src))
			if len(errs) > 0 {
				t.Fatalf("unexpected errors: %v", errs)
			}
			if len(toks) != len(tt.want) {
				t.Fatalf("got %d tokens (%v), want %d", len(toks), tokenSpecs(toks), len(tt.want))
			}
			for i, want := range tt.want {
				got := toks[i]
				if got.Line != want.line || got.Col != want.col || got.Offset != want.offset {
					t.Errorf("token %d (%s %q) at %d:%d (offset %d), want %d:%d (offset %d)",
						i, got.Kind, got.Text, got.Line, got.Col, got.Offset,
						want.line, want.col, want.offset)
				}
			}
		})
	}
}

type triviaCheck struct {
	idx              int
	leading          []string // comment texts, in order
	trailing         []string
	blankLinesBefore int
}

func TestLexTrivia(t *testing.T) {
	tests := []struct {
		name   string
		src    string
		checks []triviaCheck
	}{
		{
			"same-line comment is trailing",
			"i32 x // c\ni32 y",
			[]triviaCheck{
				{idx: 1, trailing: []string{"// c"}},
			},
		},
		{
			"own-line comment is leading",
			"// c\ni32 x",
			[]triviaCheck{
				{idx: 0, leading: []string{"// c"}},
			},
		},
		{
			"comment between tokens on own line is leading of next",
			"i32 x\n// c\ni32 y",
			[]triviaCheck{
				{idx: 2, leading: []string{"// c"}},
			},
		},
		{
			"block comment on token line is trailing",
			"i32 x /* c */ i32 y",
			[]triviaCheck{
				{idx: 1, trailing: []string{"/* c */"}},
			},
		},
		{
			"hash comment is a line comment",
			"i32 x # c\ni32 y",
			[]triviaCheck{
				{idx: 1, trailing: []string{"# c"}},
			},
		},
		{
			"multiple trailing comments keep order",
			"i32 x /* a */ // b\ni32 y",
			[]triviaCheck{
				{idx: 1, trailing: []string{"/* a */", "// b"}},
			},
		},
		{
			"annotation is leading trivia of the next declaration",
			"@naming.PreviouslyKnownAs{'namespace_': 'x'}\nservice Foo {}",
			[]triviaCheck{
				{idx: 0, leading: []string{"@naming.PreviouslyKnownAs{'namespace_': 'x'}"}},
			},
		},
		{
			"annotation inside a struct body attaches to the closing brace",
			"struct S {\n  1: string x\n  @weird\n}",
			[]triviaCheck{
				{idx: 7, leading: []string{"@weird"}},
			},
		},
		{
			"doc comment kind",
			"/** doc */\nstruct S {}",
			[]triviaCheck{
				{idx: 0, leading: []string{"/** doc */"}},
			},
		},
		{
			"empty doc comment",
			"/**/\nstruct S {}",
			[]triviaCheck{
				{idx: 0, leading: []string{"/**/"}},
			},
		},
		{
			"comments at end of file attach to eof",
			"i32 x\n// tail",
			[]triviaCheck{
				{idx: 2, leading: []string{"// tail"}},
			},
		},
		{
			"trailing comment at end of file attaches to token",
			"i32 x // tail",
			[]triviaCheck{
				{idx: 1, trailing: []string{"// tail"}},
			},
		},
		{
			"blank lines counted",
			"i32 x\n\n\ni32 y",
			[]triviaCheck{
				{idx: 2, blankLinesBefore: 2},
			},
		},
		{
			"blank line before comment counts",
			"i32 x\n\n// c\ni32 y",
			[]triviaCheck{
				{idx: 2, leading: []string{"// c"}, blankLinesBefore: 1},
			},
		},
		{
			"blank line after trailing comment counts",
			"i32 x // c\n\ni32 y",
			[]triviaCheck{
				{idx: 2, blankLinesBefore: 1},
			},
		},
		{
			"no blank lines",
			"i32 x\ni32 y",
			[]triviaCheck{
				{idx: 2, blankLinesBefore: 0},
			},
		},
		{
			"multiline block comment spanning tokens",
			"i32 x /*\n c\n*/ i32 y",
			[]triviaCheck{
				{idx: 1, trailing: []string{"/*\n c\n*/"}},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			toks, errs := Lex([]byte(tt.src))
			if len(errs) > 0 {
				t.Fatalf("unexpected errors: %v", errs)
			}
			for _, check := range tt.checks {
				if check.idx >= len(toks) {
					t.Fatalf("token index %d out of range (%d tokens)", check.idx, len(toks))
				}
				tok := toks[check.idx]
				if got := triviaTexts(tok.Leading); !reflect.DeepEqual(got, check.leading) {
					t.Errorf("token %d leading: got %v, want %v", check.idx, got, check.leading)
				}
				if got := triviaTexts(tok.Trailing); !reflect.DeepEqual(got, check.trailing) {
					t.Errorf("token %d trailing: got %v, want %v", check.idx, got, check.trailing)
				}
				if tok.BlankLinesBefore != check.blankLinesBefore {
					t.Errorf("token %d blankLinesBefore: got %d, want %d", check.idx, tok.BlankLinesBefore, check.blankLinesBefore)
				}
			}
		})
	}
}

func triviaTexts(trivia []Trivia) []string {
	if len(trivia) == 0 {
		return nil
	}
	texts := make([]string, 0, len(trivia))
	for _, t := range trivia {
		texts = append(texts, t.Text)
	}
	return texts
}

func TestLexTriviaKinds(t *testing.T) {
	tests := []struct {
		name string
		src  string
		want []TriviaKind
	}{
		{"line", "// a\n# b\n", []TriviaKind{TriviaLineComment, TriviaLineComment}},
		{"block and doc", "/** d */\n/* b */\n", []TriviaKind{TriviaDocComment, TriviaBlockComment}},
		{"silly comment is doc", "/***/\n", []TriviaKind{TriviaDocComment}},
		{"annotation", "@naming.PreviouslyKnownAs{'x': 'y'}\n", []TriviaKind{TriviaAnnotation}},
		{"annotation with comment", "@deprecation.Deprecated{}\n// note\n", []TriviaKind{TriviaAnnotation, TriviaLineComment}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			toks, errs := Lex([]byte(tt.src))
			if len(errs) > 0 {
				t.Fatalf("unexpected errors: %v", errs)
			}
			eof := toks[len(toks)-1]
			if eof.Kind != TokenEOF {
				t.Fatalf("last token is %v, want eof", eof.Kind)
			}
			var got []TriviaKind
			for _, trivia := range eof.Leading {
				got = append(got, trivia.Kind)
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("trivia kinds: got %v, want %v", got, tt.want)
			}
		})
	}
}

func TestLexErrors(t *testing.T) {
	tests := []struct {
		name      string
		src       string
		wantErrs  []string // substrings, one per expected error
		wantKinds []TokenKind
	}{
		{
			name:      "invalid escape",
			src:       `"a\qb"`,
			wantErrs:  []string{"invalid escape sequence"},
			wantKinds: []TokenKind{TokenStringLiteral, TokenEOF},
		},
		{
			name:      "unterminated string",
			src:       `"abc`,
			wantErrs:  []string{"unterminated string literal"},
			wantKinds: []TokenKind{TokenStringLiteral, TokenEOF},
		},
		{
			name:      "trailing backslash before eof",
			src:       `"abc\`,
			wantErrs:  []string{"unterminated string literal"},
			wantKinds: []TokenKind{TokenStringLiteral, TokenEOF},
		},
		{
			name:      "newline in string",
			src:       "\"a\nb\"",
			wantErrs:  []string{"newline in string literal", "unterminated string literal"},
			wantKinds: []TokenKind{TokenStringLiteral, TokenIdentifier, TokenStringLiteral, TokenEOF},
		},
		{
			name:      "lone dot",
			src:       "foo.",
			wantErrs:  []string{"unexpected character"},
			wantKinds: []TokenKind{TokenIdentifier, TokenEOF},
		},
		{
			name:      "dot before identifier",
			src:       "include .foo",
			wantErrs:  []string{"unexpected character"},
			wantKinds: []TokenKind{TokenInclude, TokenIdentifier, TokenEOF},
		},
		{
			name:      "trailing dot after int",
			src:       "1.",
			wantErrs:  []string{"unexpected character"},
			wantKinds: []TokenKind{TokenIntConstant, TokenEOF},
		},
		{
			name:      "lone plus",
			src:       "1 +",
			wantErrs:  []string{"unexpected character"},
			wantKinds: []TokenKind{TokenIntConstant, TokenEOF},
		},
		{
			name:      "unexpected character with recovery",
			src:       "foo $ bar",
			wantErrs:  []string{"unexpected character"},
			wantKinds: []TokenKind{TokenIdentifier, TokenIdentifier, TokenEOF},
		},
		{
			name:      "unterminated comment",
			src:       "/* x",
			wantErrs:  []string{"unterminated comment"},
			wantKinds: []TokenKind{TokenEOF},
		},
		{
			name:      "unterminated doc comment",
			src:       "/** x",
			wantErrs:  []string{"unterminated comment"},
			wantKinds: []TokenKind{TokenEOF},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			toks, errs := Lex([]byte(tt.src))
			if len(errs) != len(tt.wantErrs) {
				t.Fatalf("got %d errors (%v), want %d", len(errs), errs, len(tt.wantErrs))
			}
			for i, want := range tt.wantErrs {
				if got := errs[i].Error(); !strings.Contains(got, want) {
					t.Errorf("error %d: got %q, want substring %q", i, got, want)
				}
			}
			got := make([]TokenKind, 0, len(toks))
			for _, tok := range toks {
				got = append(got, tok.Kind)
			}
			if !reflect.DeepEqual(got, tt.wantKinds) {
				t.Errorf("token kinds: got %v, want %v", got, tt.wantKinds)
			}
		})
	}
}

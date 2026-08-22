// Package semantic computes LSP semantic tokens for a thrift document:
// keywords, types, definition names, comments, strings, and numbers.
// Pure over the view: parsing and file I/O happen in the caller.
package source

import (
	"context"

	"go.lsp.dev/uri"

	"github.com/karitham/thrift-ls/lsp/cache"
	"github.com/karitham/thrift-ls/syntax"
)

// legend is the ordered list of token types; indexes into it are the
// encoded token types. The server advertises it in the registration
// options, so it must stay in sync with the constants below. The types
// follow thrift's own naming; "union" and "exception" are server-defined
// additions to the standard semantic token types.
var legend = []string{
	"keyword", "string", "number", "comment",
	"type", "struct", "union", "exception", "enum", "interface",
	"property", "function", "enumMember", "variable",
}

const (
	tokKeyword = iota
	tokString
	tokNumber
	tokComment
	tokType
	tokStruct
	tokUnion
	tokException
	tokEnum
	tokInterface
	tokProperty
	tokFunction
	tokEnumMember
	tokVariable
)

// Legend returns the semantic token types the server emits.
func Legend() []string {
	return legend
}

// Tokens returns the delta-encoded semantic tokens of a file, in source
// order.
func Tokens(ctx context.Context, view *cache.View, file uri.URI) ([]uint32, error) {
	pf, err := view.Parse(ctx, file)
	if err != nil || pf.AST() == nil {
		return nil, err
	}

	doc := pf.AST()
	names, types := tokenFacts(doc)

	data := make([]uint32, 0, len(doc.Tokens)*5)
	prevLine, prevChar := 0, 0

	for i, tok := range doc.Tokens {
		typ, ok := classifyToken(i, tok, names, types)
		if !ok {
			continue
		}

		// The token span in UTF-16 code units: lengths and columns are
		// byte- and rune-based in the lexer, so non-ASCII content (e.g.
		// astral chars in comments or string literals) shifts them.
		start, err := pf.Mapper().OffsetToLSPPosition(tok.Offset)
		if err != nil {
			continue
		}

		end, err := pf.Mapper().OffsetToLSPPosition(tok.Offset + len(tok.Text))
		if err != nil {
			continue
		}

		line := int(start.Line)
		char := int(start.Character)
		length := int(end.Character - start.Character)

		deltaChar := char
		if line == prevLine {
			deltaChar = char - prevChar
		}

		data = append(data,
			uint32(line-prevLine),
			uint32(deltaChar),
			uint32(length),
			uint32(typ),
			0, // no token modifiers
		)

		prevLine, prevChar = line, char
	}

	return data, nil
}

// classifyToken maps a token to its semantic type. Definition names win
// over type keywords so a field named "string" stays a property; type
// references win over keywords so "string" in a type position is a type.
func classifyToken(i int, tok syntax.Token, names map[int]int, types map[int]bool) (int, bool) {
	if syntax.IsComment(tok.Kind) {
		return tokComment, true
	}

	if t, ok := names[i]; ok {
		return t, true
	}

	if types[i] {
		return tokType, true
	}

	switch tok.Kind {
	case syntax.TokenStringLiteral:
		return tokString, true
	case syntax.TokenIntConstant, syntax.TokenDoubleConstant:
		return tokNumber, true
	}

	if syntax.IsTypeKeyword(tok.Kind) {
		return tokType, true
	}

	if syntax.IsKeyword(tok.Kind) {
		return tokKeyword, true
	}

	return 0, false
}

// tokenFacts collects the semantic classification facts of a document in
// one tree walk: names maps every definition-name token index to its
// semantic type, types marks the first token of every type reference.
// Field names are properties wherever they legally appear (struct fields,
// arguments, throws members), and container element types are marked like
// their parents, so no position needs separate handling.
func tokenFacts(doc *syntax.Document) (names map[int]int, types map[int]bool) {
	names = map[int]int{}
	types = map[int]bool{}

	syntax.Walk(doc, func(n syntax.Node) bool {
		switch v := n.(type) {
		case *syntax.Struct:
			names[v.Name.TokStart()] = structToken(v.Kind)
		case *syntax.Enum:
			names[v.Name.TokStart()] = tokEnum
		case *syntax.Service:
			names[v.Name.TokStart()] = tokInterface
		case *syntax.Const:
			names[v.Name.TokStart()] = tokVariable
		case *syntax.Typedef:
			names[v.Name.TokStart()] = tokType
		case *syntax.Function:
			names[v.Name.TokStart()] = tokFunction
		case *syntax.EnumValue:
			names[v.Name.TokStart()] = tokEnumMember
		case *syntax.Field:
			names[v.Name.TokStart()] = tokProperty
		case *syntax.FieldType:
			types[v.TokStart()] = true
		}

		return true
	})

	return names, types
}

// structToken maps a struct-kind declaration to its semantic token type.
func structToken(k syntax.TokenKind) int {
	switch k {
	case syntax.UnionDecl:
		return tokUnion
	case syntax.ExceptionDecl:
		return tokException
	default:
		return tokStruct
	}
}

// isKeyword reports whether the kind is a reserved word.

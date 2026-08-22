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
	names := definitionNames(doc)
	types := typeReferences(doc)

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

// definitionNames maps every definition name token to its semantic type:
// structs, enums, services, consts, typedefs, and their members.
func definitionNames(doc *syntax.Document) map[int]int {
	names := map[int]int{}

	for _, n := range doc.Nodes {
		switch v := n.(type) {
		case *syntax.Struct:
			switch v.Kind {
			case syntax.UnionDecl:
				names[v.Name.TokStart()] = tokUnion
			case syntax.ExceptionDecl:
				names[v.Name.TokStart()] = tokException
			default:
				names[v.Name.TokStart()] = tokStruct
			}

			for _, f := range v.Fields {
				names[f.Name.TokStart()] = tokProperty
			}
		case *syntax.Enum:
			names[v.Name.TokStart()] = tokEnum
			for _, value := range v.Values {
				names[value.Name.TokStart()] = tokEnumMember
			}
		case *syntax.Service:
			names[v.Name.TokStart()] = tokInterface
			for _, fn := range v.Functions {
				names[fn.Name.TokStart()] = tokFunction
				for _, arg := range fn.Args {
					names[arg.Name.TokStart()] = tokProperty
				}

				if fn.Throws != nil {
					for _, f := range fn.Throws.Fields {
						names[f.Name.TokStart()] = tokProperty
					}
				}
			}
		case *syntax.Const:
			names[v.Name.TokStart()] = tokVariable
		case *syntax.Typedef:
			names[v.Name.TokStart()] = tokType
		}
	}

	return names
}

// typeReferences maps every type reference's first token to the type
// semantic type: field types, function return types, argument and throws
// types, const and typedef types, and the nested container types.
func typeReferences(doc *syntax.Document) map[int]bool {
	types := map[int]bool{}

	var add func(t *syntax.FieldType)

	add = func(t *syntax.FieldType) {
		if t == nil {
			return
		}

		types[t.TokStart()] = true
		add(t.KeyType)
		add(t.ValueType)
	}

	for _, n := range doc.Nodes {
		switch v := n.(type) {
		case *syntax.Struct:
			for _, f := range v.Fields {
				add(f.Type)
			}
		case *syntax.Service:
			for _, fn := range v.Functions {
				add(fn.Type)

				for _, arg := range fn.Args {
					add(arg.Type)
				}

				if fn.Throws != nil {
					for _, f := range fn.Throws.Fields {
						add(f.Type)
					}
				}
			}
		case *syntax.Const:
			add(v.Type)
		case *syntax.Typedef:
			add(v.Type)
		}
	}

	return types
}

// isKeyword reports whether the kind is a reserved word.

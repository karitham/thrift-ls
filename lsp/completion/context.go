package completion

import (
	"strings"

	"github.com/karitham/thrift-ls/syntax"
)

// ContextKind classifies the grammar slot the cursor sits in. Providers are
// selected by slot, so a cursor on a field name never suggests value
// candidates and a cursor in an include literal never suggests keywords.
type ContextKind uint8

const (
	CtxNone            ContextKind = iota
	CtxIncludePath                 // inside an include/cpp_include string literal
	CtxType                        // any type reference slot: field/return/arg/throws/typedef/const/map<k,v>
	CtxFieldName                   // struct/union/exception field, argument, or throws member name
	CtxFieldID                     // field id slot (before ':')
	CtxFieldValue                  // after '=' in a field or const
	CtxEnumValueName               // enum member name slot
	CtxDefinitionName              // top-level definition name (struct/enum/service/typedef/const)
	CtxFunctionName                // service function name slot
	CtxServiceExtends              // after 'extends'
	CtxAnnotationKey               // inside ( ... ) — annotation name position
	CtxAnnotationValue             // inside an annotation value string literal
	CtxKeyword                     // no structural slot: keywords + identifiers
)

// Context is the resolved grammar slot at the cursor.
type Context struct {
	Kind ContextKind

	Path   []syntax.Node // SearchNodePathByPosition result; annotations stay opaque
	Offset int           // byte offset of the cursor in the document

	// Prefix is the text to filter candidates against (the typed path
	// inside quotes for include/annotation slots), and EditStart the byte
	// offset where the completion edit range starts.
	Prefix    string
	EditStart int

	Doc *syntax.Document
}

// ResolveContext classifies the grammar slot at pos. Token-level checks come
// first (cheap and precise: strings, braces, parens, separators), then the
// node path, then a CtxKeyword fallback.
func ResolveContext(doc *syntax.Document, pos syntax.Position) Context {
	c := Context{
		Kind:   CtxKeyword,
		Offset: pos.Offset,
		Path:   doc.SearchNodePathByPosition(pos),
		Doc:    doc,
	}

	atIdx, at := tokenAt(doc, pos.Offset)

	// A cursor in a comment is not a completion slot.
	if at != nil && syntax.IsComment(at.Kind) {
		c.Kind = CtxNone

		return c
	}

	// The token before the cursor: the token containing the cursor when the
	// cursor sits at its end, the previous token when mid-token. Comments
	// are skipped — the grammar slot is determined by the real tokens.
	prevIdx := prevReal(doc.Tokens, atIdx)
	if at != nil && pos.Offset < at.Offset+len(at.Text) {
		prevIdx = prevReal(doc.Tokens, atIdx-1)
	}

	c.Prefix, c.EditStart = prefixRange(doc, pos, atIdx, at)

	// 1. String literal slots: include paths and annotation values. Other
	// strings (const defaults, values) are not completion slots.
	if at != nil && at.Kind == syntax.TokenStringLiteral && insideString(at, pos.Offset) {
		text := strings.Trim(stringPrefix(at, pos.Offset), "'\"")

		switch {
		case hasInclude(c.Path):
			c.Kind = CtxIncludePath
			c.Prefix = text
			c.EditStart = at.Offset + 1
		case deepestConstValue(c.Path):
			// A default value string (e.g. an argument default): not a slot.
			c.Kind = CtxNone
		default:
			if opener, ok := parenOpener(doc, atIdx); ok && !isThrowsGroup(doc, opener) {
				c.Kind = CtxAnnotationValue
				c.Prefix = text
				c.EditStart = at.Offset + 1
			} else {
				c.Kind = CtxNone
			}
		}

		return c
	}

	// 2. Field id slot: cursor on an int immediately followed by ':' —
	// before the struct member rule, so "{ |1:" is CtxFieldID, not a
	// member name position.
	if at != nil && at.Kind == syntax.TokenIntConstant {
		if n := nextReal(doc.Tokens, atIdx+1); n < len(doc.Tokens) && doc.Tokens[n].Kind == syntax.TokenColon {
			c.Kind = CtxFieldID

			return c
		}
	}

	// 3. Token adjacency before the cursor.
	if prevIdx >= 0 {
		switch prev := doc.Tokens[prevIdx]; prev.Kind {
		case syntax.TokenLParen:
			c.Kind = afterParenKind(doc, prevIdx)

			return c
		case syntax.TokenComma, syntax.TokenSemicolon:
			if opener, ok := parenOpener(doc, prevIdx); ok {
				c.Kind = insideParenKind(doc, opener)
			} else if kw, ok := braceBodyKind(doc, prevIdx); ok {
				c.Kind = memberKind(kw)
			}

			return c
		case syntax.TokenLBrace:
			if kw, ok := braceBodyKind(doc, prevIdx); ok {
				c.Kind = memberKind(kw)
			}

			return c
		case syntax.TokenExtends:
			c.Kind = CtxServiceExtends

			return c
		case syntax.TokenEqual:
			c.Kind = CtxFieldValue

			return c
		case syntax.TokenColon:
			// Between the id and the type: a type position.
			c.Kind = CtxType

			return c
		case syntax.TokenRequired, syntax.TokenOptional:
			c.Kind = CtxType

			return c
		}
	}

	// 4. Cursor on a token.
	if at != nil {
		switch at.Kind {
		case syntax.TokenExtends:
			c.Kind = CtxServiceExtends

			return c
		case syntax.TokenRequired, syntax.TokenOptional:
			c.Kind = CtxKeyword

			return c
		case syntax.TokenIdentifier:
			// "ZeonForces.|" — an identifier ending in a dot is a
			// qualified value position; the lexer may split the dot off
			// the token, so the cursor lands right at its end.
			if strings.HasSuffix(at.Text, ".") && pos.Offset == at.Offset+len(at.Text) {
				c.Kind = CtxFieldValue

				return c
			}
		}
	}

	// 5. Node path: the deepest node decides.
	switch n := deepestNode(c.Path).(type) {
	case *syntax.FieldType:
		c.Kind = CtxType
	case *syntax.ConstValue:
		if n.Kind == syntax.ValueIdent {
			c.Kind = CtxFieldValue
		} else {
			c.Kind = CtxNone
		}
	case *syntax.Identifier:
		c.Kind = identifierKind(c.Path, n)
	case *syntax.Field:
		switch {
		case n.Name != nil && pos.Offset < tokenOffset(doc, n.Name):
			c.Kind = CtxType
		case n.Value == nil:
			c.Kind = CtxFieldName
		default:
			c.Kind = CtxFieldValue
		}
	}

	return c
}

// identifierKind classifies an identifier by its role, carried by its parent.
func identifierKind(path []syntax.Node, n *syntax.Identifier) ContextKind {
	if len(path) < 2 {
		return CtxKeyword
	}

	switch parent := path[len(path)-2].(type) {
	case *syntax.FieldType:
		return CtxType
	case *syntax.Field:
		if parent.Value == nil {
			return CtxFieldName
		}

		return CtxFieldValue
	case *syntax.EnumValue:
		return CtxEnumValueName
	case *syntax.Const:
		return CtxDefinitionName
	case *syntax.Struct, *syntax.Enum:
		return CtxDefinitionName
	case *syntax.Service:
		if parent.Name == n {
			return CtxDefinitionName
		}

		return CtxServiceExtends
	case *syntax.Function:
		return CtxFunctionName
	case *syntax.Typedef:
		return CtxDefinitionName
	}

	return CtxKeyword
}

// afterParenKind classifies the cursor right after '(' (prev is the opener).
func afterParenKind(doc *syntax.Document, opener int) ContextKind {
	prevIdx := prevReal(doc.Tokens, opener-1)
	if prevIdx < 0 {
		return CtxAnnotationKey
	}

	switch prev := doc.Tokens[prevIdx]; prev.Kind {
	case syntax.TokenRParen:
		// Function annotations after a closed args list.
		return CtxAnnotationKey
	case syntax.TokenThrows:
		return CtxFieldName
	case syntax.TokenIdentifier:
		// A function name opens the args list; a member name opens its
		// annotations. The enclosing brace body disambiguates.
		if kw, ok := braceBodyKind(doc, prevIdx); ok && kw == syntax.TokenService {
			return CtxFieldName
		}

		return CtxAnnotationKey
	default:
		// Type annotations (i32 ( ... ), map<...> ( ... )).
		return CtxAnnotationKey
	}
}

// insideParenKind classifies the cursor after ','/';' inside the group
// opened at opener.
func insideParenKind(doc *syntax.Document, opener int) ContextKind {
	prevIdx := prevReal(doc.Tokens, opener-1)
	if prevIdx < 0 {
		return CtxAnnotationKey
	}

	switch prev := doc.Tokens[prevIdx]; prev.Kind {
	case syntax.TokenRParen:
		return CtxAnnotationKey
	case syntax.TokenThrows:
		return CtxFieldName
	case syntax.TokenIdentifier:
		if kw, ok := braceBodyKind(doc, prevIdx); ok && kw == syntax.TokenService {
			return CtxFieldName
		}

		return CtxAnnotationKey
	default:
		return CtxAnnotationKey
	}
}

// isThrowsGroup reports whether the group opened at opener is a throws
// clause (which contains fields, not annotations).
func isThrowsGroup(doc *syntax.Document, opener int) bool {
	prevIdx := prevReal(doc.Tokens, opener-1)

	return prevIdx >= 0 && doc.Tokens[prevIdx].Kind == syntax.TokenThrows
}

// memberKind maps a container keyword to its member slot.
func memberKind(kw syntax.TokenKind) ContextKind {
	switch kw {
	case syntax.TokenEnum:
		return CtxEnumValueName
	case syntax.TokenService:
		return CtxFunctionName
	default:
		return CtxFieldName
	}
}

// tokenAt returns the index and token containing offset (inclusive end), or
// (index of the token before offset, nil) when the cursor sits between
// tokens. -1 when offset precedes the first token. The zero-length EOF
// token never matches: a cursor at the end of the document resolves to the
// last real token.
func tokenAt(doc *syntax.Document, offset int) (int, *syntax.Token) {
	toks := doc.Tokens

	lo, hi := 0, len(toks)
	for lo < hi {
		mid := (lo + hi) / 2
		if toks[mid].Offset <= offset {
			lo = mid + 1
		} else {
			hi = mid
		}
	}

	i := lo - 1
	for i >= 0 && toks[i].Kind == syntax.TokenEOF {
		i--
	}

	if i < 0 {
		return -1, nil
	}

	t := &toks[i]
	if offset >= t.Offset && offset <= t.Offset+len(t.Text) {
		return i, t
	}

	return i, nil
}

// insideString reports whether offset is inside the string token: after the
// opening quote, or at the end of an unterminated literal.
func insideString(t *syntax.Token, offset int) bool {
	if offset <= t.Offset || offset > t.Offset+len(t.Text) {
		return false
	}

	if offset < t.Offset+len(t.Text) {
		return true
	}

	// At the token end: still inside when the literal is unterminated.
	return len(t.Text) < 2 || t.Text[len(t.Text)-1] != t.Text[0]
}

// stringPrefix returns the string token text up to offset, including the
// opening quote.
func stringPrefix(t *syntax.Token, offset int) string {
	end := min(offset-t.Offset, len(t.Text))

	return t.Text[:end]
}

// prefixRange returns the identifier-ish text typed before the cursor (same
// line, contiguous tokens) and the byte offset where it starts.
func prefixRange(doc *syntax.Document, pos syntax.Position, atIdx int, at *syntax.Token) (string, int) {
	var parts []string

	start := pos.Offset

	// The cursor may sit inside a token (mid-word typing): take the partial
	// text, then continue over byte-adjacent tokens only.
	nextEnd := pos.Offset
	if at != nil && pos.Offset > at.Offset && at.Kind != syntax.TokenStringLiteral {
		partial := at.Text[:pos.Offset-at.Offset]
		if identish(partial) {
			parts = append(parts, partial)
			start = at.Offset
			nextEnd = at.Offset
		}
	}

	for i := atIdx - 1; i >= 0; i-- {
		t := &doc.Tokens[i]
		if t.Line != pos.Line || !identish(t.Text) {
			break
		}

		// Tokens must be byte-adjacent: whitespace ends the prefix.
		if t.Offset+len(t.Text) != nextEnd {
			break
		}

		start = t.Offset
		nextEnd = t.Offset
		parts = append([]string{t.Text}, parts...)
	}

	return strings.Join(parts, ""), start
}

// identish reports whether s consists of identifier-ish characters only.
func identish(s string) bool {
	if s == "" {
		return false
	}

	for _, r := range s {
		switch {
		case r == '_' || r == '.':
		case r >= 'a' && r <= 'z':
		case r >= 'A' && r <= 'Z':
		case r >= '0' && r <= '9':
		default:
			return false
		}
	}

	return true
}

// parenOpener returns the index of the '(' opening the nearest enclosing
// paren group at or before from.
func parenOpener(doc *syntax.Document, from int) (int, bool) {
	if from < 0 {
		return 0, false
	}

	depth := 0

	for i := from; i >= 0; i-- {
		switch doc.Tokens[i].Kind {
		case syntax.TokenRParen:
			depth++
		case syntax.TokenLParen:
			if depth == 0 {
				return i, true
			}

			depth--
		}
	}

	return 0, false
}

// braceBodyKind returns the container keyword (struct/union/exception/enum/
// service) of the nearest enclosing brace body at or before from.
func braceBodyKind(doc *syntax.Document, from int) (syntax.TokenKind, bool) {
	if from < 0 {
		return 0, false
	}

	depth := 0

	for i := from; i >= 0; i-- {
		switch doc.Tokens[i].Kind {
		case syntax.TokenRBrace:
			depth++
		case syntax.TokenLBrace:
			if depth == 0 {
				return containerKeywordBefore(doc, i)
			}

			depth--
		}
	}

	return 0, false
}

// containerKeywordBefore returns the keyword of the container whose opening
// brace is at index brace, skipping annotations between the name and brace.
func containerKeywordBefore(doc *syntax.Document, brace int) (syntax.TokenKind, bool) {
	j := brace - 1

	// Skip a closing paren group (annotations on the container).
	for j >= 0 && doc.Tokens[j].Kind == syntax.TokenRParen {
		depth := 0

		for j >= 0 {
			switch doc.Tokens[j].Kind {
			case syntax.TokenRParen:
				depth++
			case syntax.TokenLParen:
				depth--
				if depth == 0 {
					j--

					goto pastParens
				}
			}

			j--
		}

	pastParens:
	}

	j = prevReal(doc.Tokens, j)
	if j < 1 || doc.Tokens[j].Kind != syntax.TokenIdentifier {
		return 0, false
	}

	k := prevReal(doc.Tokens, j-1)
	if k < 0 {
		return 0, false
	}

	switch kw := doc.Tokens[k].Kind; kw {
	case syntax.TokenStruct, syntax.TokenUnion, syntax.TokenException,
		syntax.TokenEnum, syntax.TokenService:
		return kw, true
	}

	return 0, false
}

// deepestNode returns the innermost node of the path, or nil.
func deepestNode(path []syntax.Node) syntax.Node {
	if len(path) == 0 {
		return nil
	}

	return path[len(path)-1]
}

// prevReal returns the index of the previous non-comment token strictly
// before idx, or -1. Comments are stream tokens but never participate in
// the grammar, so every adjacency lookup skips them.
func prevReal(toks []syntax.Token, idx int) int {
	for idx >= 0 && syntax.IsComment(toks[idx].Kind) {
		idx--
	}

	return idx
}

// nextReal returns the index of the next non-comment token at or after idx.
func nextReal(toks []syntax.Token, idx int) int {
	for idx < len(toks) && syntax.IsComment(toks[idx].Kind) {
		idx++
	}

	return idx
}

// tokenOffset returns the byte offset of the first token of n.
func tokenOffset(doc *syntax.Document, n syntax.Node) int {
	return doc.TokenPosition(n.TokStart()).Offset
}

// deepestConstValue reports whether the deepest node is a ConstValue.
func deepestConstValue(path []syntax.Node) bool {
	_, ok := deepestNode(path).(*syntax.ConstValue)

	return ok
}

// hasInclude reports whether the path contains an include statement.
func hasInclude(path []syntax.Node) bool {
	for _, n := range path {
		switch n.(type) {
		case *syntax.Include, *syntax.CPPInclude:
			return true
		}
	}

	return false
}

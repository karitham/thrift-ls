package syntax

import (
	"fmt"
	"slices"
	"sort"
)

// Parse lexes and parses src into a Document. It always returns a document
// (possibly partial) and every lexical and parse error that was found.
// Documents with errors must not be formatted.
func Parse(src []byte) (*Document, []Error) {
	toks, lexErrs := Lex(src)
	doc, parseErrs := ParseTokens(toks)
	errs := append(lexErrs, parseErrs...)
	sort.SliceStable(errs, func(i, j int) bool { return errs[i].Offset < errs[j].Offset })

	return doc, errs
}

// ParseTokens parses an already-lexed token stream. The LSP can reuse this to
// reparse a file from its cached tokens without re-lexing.
func ParseTokens(toks []Token) (*Document, []Error) {
	p := &parser{toks: toks}

	return p.parseDocument(), p.errs
}

type parser struct {
	toks []Token
	pos  int
	errs []Error
}

// --- token helpers ---------------------------------------------------------

// nextReal returns the index of the next non-comment token at or after i.
func (p *parser) nextReal(i int) int { return NextReal(p.toks, i) }

func (p *parser) cur() Token { return p.toks[p.nextReal(p.pos)] }

func (p *parser) at(k TokenKind) bool { return p.cur().Kind == k }

func (p *parser) advance() *Token {
	i := p.nextReal(p.pos)
	t := &p.toks[i]
	p.pos = i + 1

	return t
}

// peekAfter returns the kind of the real token after the token at index i.
func (p *parser) peekAfter(i int) TokenKind {
	return p.toks[p.nextReal(i+1)].Kind
}

// accept consumes and returns the current token when its kind matches.
func (p *parser) accept(k TokenKind) *Token {
	if p.at(k) {
		return p.advance()
	}

	return nil
}

// expect consumes the current token when its kind matches and reports an
// error otherwise.
func (p *parser) expect(k TokenKind, what string) bool {
	if p.at(k) {
		p.advance()

		return true
	}

	p.errorfCur("expected %s, got %q", what, p.cur().Text)

	return false
}

// synchronizeTo skips tokens until one of the given kinds or EOF.
func (p *parser) synchronizeTo(kinds ...TokenKind) {
	for !p.at(TokenEOF) {
		if slices.Contains(kinds, p.cur().Kind) {
			return
		}

		p.advance()
	}
}

// recoverEntry skips past a malformed entry in a list-like construct:
// synchronize to a separator or the terminator, then consume one stray
// separator so the enclosing loop can continue with the next entry.
func (p *parser) recoverEntry(term TokenKind) {
	p.synchronizeTo(TokenComma, TokenSemicolon, term)

	if !p.at(term) && !p.at(TokenEOF) {
		p.advance()
	}
}

func (p *parser) errorfCur(format string, args ...any) {
	t := p.cur()
	p.errs = append(p.errs, Error{
		Message:  fmt.Sprintf(format, args...),
		Offset:   t.Offset,
		Line:     t.Line,
		Col:      t.Col,
		Severity: SeverityError,
	})
}

func (p *parser) warnf(t *Token, format string, args ...any) {
	p.errs = append(p.errs, Error{
		Message:  fmt.Sprintf(format, args...),
		Offset:   t.Offset,
		Line:     t.Line,
		Col:      t.Col,
		Severity: SeverityWarning,
	})
}

// --- document level --------------------------------------------------------

func (p *parser) parseDocument() *Document {
	doc := &Document{Tokens: p.toks}
	for {
		switch p.cur().Kind {
		case TokenEOF:
			return doc
		case TokenInclude:
			doc.appendNode(p.parseInclude())
		case TokenCPPInclude:
			doc.appendNode(p.parseCPPInclude())
		case TokenNamespace:
			doc.appendNode(p.parseNamespace())
		case TokenConst:
			doc.appendNode(p.parseConst())
		case TokenTypedef:
			doc.appendNode(p.parseTypedef())
		case TokenEnum:
			doc.appendNode(p.parseEnum())
		case TokenStruct, TokenUnion, TokenException:
			doc.appendNode(p.parseStruct())
		case TokenService:
			doc.appendNode(p.parseService())
		default:
			p.errorfCur("unexpected token %q at top level", p.cur().Text)
			p.synchronizeTo(TokenInclude, TokenCPPInclude, TokenNamespace,
				TokenConst, TokenTypedef, TokenEnum, TokenStruct,
				TokenUnion, TokenException, TokenService)
		}
	}
}

// appendNode appends n to the document's node list. The top-level parse
// functions return Node (not a concrete pointer), so a failed parse is a
// nil interface and this guard suffices.
func (d *Document) appendNode(n Node) {
	if n != nil {
		d.Nodes = append(d.Nodes, n)
	}
}

// --- headers ---------------------------------------------------------------

func (p *parser) parseInclude() Node {
	n := &Include{nodeBase: nodeBase{first: p.nextReal(p.pos)}}
	p.advance() // include

	if !p.at(TokenStringLiteral) {
		p.errorfCur("expected include path string, got %q", p.cur().Text)

		if !p.at(TokenEOF) {
			p.advance() // consume the offending token so the top level can continue
		}

		return nil
	}

	n.Path = p.advance()
	n.last = p.pos - 1

	return n
}

func (p *parser) parseCPPInclude() Node {
	n := &CPPInclude{nodeBase: nodeBase{first: p.nextReal(p.pos)}}
	p.advance() // cpp_include

	if !p.at(TokenStringLiteral) {
		p.errorfCur("expected cpp_include path string, got %q", p.cur().Text)

		if !p.at(TokenEOF) {
			p.advance() // consume the offending token so the top level can continue
		}

		return nil
	}

	n.Path = p.advance()
	n.last = p.pos - 1

	return n
}

func (p *parser) parseNamespace() Node {
	n := &Namespace{nodeBase: nodeBase{first: p.nextReal(p.pos)}}
	p.advance() // namespace

	if !p.at(TokenIdentifier) && !p.at(TokenStar) {
		p.errorfCur("expected namespace scope, got %q", p.cur().Text)
		p.synchronizeTo(TokenEOF)

		return nil
	}

	n.Scope = p.advance()

	n.Name = p.expectIdentifier("namespace name")
	if n.Name == nil {
		p.synchronizeTo(TokenEOF)

		return nil
	}

	n.Annotations = p.parseAnnotationsIfPresent()
	n.last = p.pos - 1

	return n
}

// --- definitions -----------------------------------------------------------

func (p *parser) parseConst() Node {
	n := &Const{nodeBase: nodeBase{first: p.nextReal(p.pos)}}
	p.advance() // const

	n.Type = p.parseFieldType()
	if n.Type == nil {
		p.synchronizeTo(TokenEOF)

		return nil
	}

	n.Name = p.expectIdentifier("constant name")
	if n.Name == nil {
		p.synchronizeTo(TokenEOF)

		return nil
	}

	if !p.expect(TokenEqual, "'='") {
		p.synchronizeTo(TokenEOF)

		return nil
	}

	n.Value = p.parseConstValue()
	if n.Value == nil {
		p.synchronizeTo(TokenEOF)

		return nil
	}

	n.Sep = p.acceptSeparator()

	n.last = p.pos - 1

	return n
}

func (p *parser) parseTypedef() Node {
	n := &Typedef{nodeBase: nodeBase{first: p.nextReal(p.pos)}}
	p.advance() // typedef

	n.Type = p.parseFieldType()
	if n.Type == nil {
		p.synchronizeTo(TokenEOF)

		return nil
	}

	n.Name = p.expectIdentifier("typedef name")
	if n.Name == nil {
		p.synchronizeTo(TokenEOF)

		return nil
	}

	n.Annotations = p.parseAnnotationsIfPresent()
	n.Sep = p.acceptSeparator()

	n.last = p.pos - 1

	return n
}

func (p *parser) parseEnum() Node {
	n := &Enum{nodeBase: nodeBase{first: p.nextReal(p.pos)}}
	p.advance() // enum

	n.Name = p.expectIdentifier("enum name")
	if n.Name == nil {
		p.synchronizeTo(TokenEOF)

		return nil
	}

	if p.accept(TokenLBrace) != nil {
		for !p.at(TokenRBrace) && !p.at(TokenEOF) {
			v := p.parseEnumValue()
			if v == nil {
				p.synchronizeTo(TokenRBrace)

				continue
			}

			n.Values = append(n.Values, v)
		}

		p.expect(TokenRBrace, "'}' to close enum")
	} else {
		p.errorfCur("expected '{' after enum name, got %q", p.cur().Text)
	}

	n.Annotations = p.parseAnnotationsIfPresent()
	n.last = p.pos - 1

	return n
}

func (p *parser) parseEnumValue() *EnumValue {
	if !p.at(TokenIdentifier) {
		p.errorfCur("expected enum value name, got %q", p.cur().Text)

		return nil
	}

	v := &EnumValue{nodeBase: nodeBase{first: p.nextReal(p.pos)}}

	v.Name = p.identifier()
	if p.at(TokenEqual) {
		p.advance()

		if !p.at(TokenIntConstant) {
			p.errorfCur("expected integer enum value, got %q", p.cur().Text)
		} else {
			v.Value = p.advance()
		}
	}

	v.Annotations = p.parseAnnotationsIfPresent()
	v.Sep = p.acceptSeparator()

	v.last = p.pos - 1

	return v
}

func (p *parser) parseStruct() Node {
	n := &Struct{nodeBase: nodeBase{first: p.nextReal(p.pos)}, Kind: StructKind(p.cur().Kind)}
	p.advance() // struct | union | exception

	n.Name = p.expectIdentifier("struct name")
	if n.Name == nil {
		p.synchronizeTo(TokenEOF)

		return nil
	}

	if p.accept(TokenLBrace) != nil {
		n.Fields = p.parseFieldList(TokenRBrace)
		p.expect(TokenRBrace, "'}' to close struct")
	} else {
		p.errorfCur("expected '{' after struct name, got %q", p.cur().Text)
	}

	n.Annotations = p.parseAnnotationsIfPresent()
	n.last = p.pos - 1

	return n
}

func (p *parser) parseService() Node {
	n := &Service{nodeBase: nodeBase{first: p.nextReal(p.pos)}}
	p.advance() // service

	n.Name = p.expectIdentifier("service name")
	if n.Name == nil {
		p.synchronizeTo(TokenEOF)

		return nil
	}

	if p.at(TokenExtends) {
		p.advance()

		n.Extends = p.expectIdentifier("base service name")
		if n.Extends == nil {
			p.synchronizeTo(TokenLBrace, TokenEOF)

			if !p.at(TokenLBrace) {
				return nil
			}
		}
	}

	if p.accept(TokenLBrace) != nil {
		for !p.at(TokenRBrace) && !p.at(TokenEOF) {
			f := p.parseFunction()
			if f == nil {
				p.synchronizeTo(TokenRBrace)

				continue
			}

			n.Functions = append(n.Functions, f)
		}

		p.expect(TokenRBrace, "'}' to close service")
	} else {
		p.errorfCur("expected '{' after service name, got %q", p.cur().Text)
	}

	n.Annotations = p.parseAnnotationsIfPresent()
	n.last = p.pos - 1

	return n
}

func (p *parser) parseFunction() *Function {
	f := &Function{nodeBase: nodeBase{first: p.nextReal(p.pos)}}

	switch p.cur().Kind {
	case TokenOneway:
		f.Oneway = p.advance()
	case TokenAsync:
		f.Oneway = p.advance()
		p.warnf(f.Oneway, "async is deprecated, use oneway")
	}

	if p.at(TokenVoid) {
		f.Void = p.advance()
	} else {
		f.Type = p.parseFieldType()
		if f.Type == nil {
			p.synchronizeTo(TokenLParen, TokenEOF)

			return nil
		}
	}

	f.Name = p.expectIdentifier("function name")
	if f.Name == nil {
		p.synchronizeTo(TokenLParen, TokenEOF)

		return nil
	}

	if !p.expect(TokenLParen, "'('") {
		p.synchronizeTo(TokenEOF)

		return nil
	}

	f.Args = p.parseFieldList(TokenRParen)
	p.expect(TokenRParen, "')' to close arguments")

	if p.at(TokenThrows) {
		p.advance()

		if !p.expect(TokenLParen, "'(' after throws") {
			p.synchronizeTo(TokenEOF)

			return nil
		}

		f.Throws = &Throws{nodeBase: nodeBase{first: p.pos - 1}}

		f.Throws.Fields = p.parseFieldList(TokenRParen)
		p.expect(TokenRParen, "')' to close throws")

		f.Throws.last = p.pos - 1
	}

	f.Annotations = p.parseAnnotationsIfPresent()
	f.Sep = p.acceptSeparator()

	f.last = p.pos - 1

	return f
}

// --- fields ----------------------------------------------------------------

// parseFieldList parses fields until the terminator token. It is shared by
// struct bodies, function arguments, and throws clauses.
func (p *parser) parseFieldList(term TokenKind) []*Field {
	var fields []*Field

	for !p.at(term) && !p.at(TokenEOF) {
		f, ok := p.parseField()
		if !ok {
			p.recoverEntry(term)

			continue
		}

		fields = append(fields, f)
	}

	return fields
}

func (p *parser) parseField() (*Field, bool) {
	f := &Field{nodeBase: nodeBase{first: p.nextReal(p.pos)}}

	if p.at(TokenIntConstant) && p.peekAfter(p.nextReal(p.pos)) == TokenColon {
		f.FieldID = p.advance()
		if !p.expect(TokenColon, "':' after field id") {
			p.synchronizeTo(TokenComma, TokenSemicolon, TokenRBrace, TokenRParen)

			return f, false
		}
	}

	switch p.cur().Kind {
	case TokenRequired, TokenOptional:
		f.Req = p.advance()
	}

	f.Type = p.parseFieldType()
	if f.Type == nil {
		p.synchronizeTo(TokenComma, TokenSemicolon, TokenRBrace, TokenRParen)

		return f, false
	}

	if p.accept(TokenAmp) != nil {
		f.Reference = true
	}

	// Field names may be any keyword, not just identifiers: type names such
	// as uuid are both valid types and common field names, and the thrift
	// compiler accepts them there. Rejecting keywords as field names breaks
	// valid IDLs.
	if p.at(TokenIdentifier) || IsKeyword(p.cur().Kind) {
		f.Name = p.identifier()
	} else {
		p.errorfCur("expected field name, got %q", p.cur().Text)
		p.synchronizeTo(TokenComma, TokenSemicolon, TokenRBrace, TokenRParen)

		return f, false
	}

	if p.at(TokenEqual) {
		p.advance()

		f.Value = p.parseConstValue()
		if f.Value == nil {
			p.synchronizeTo(TokenComma, TokenSemicolon, TokenRBrace, TokenRParen)

			return f, false
		}
	}

	f.Annotations = p.parseAnnotationsIfPresent()
	f.Sep = p.acceptSeparator()

	f.last = p.pos - 1

	return f, true
}

// --- types -----------------------------------------------------------------

func (p *parser) parseFieldType() *FieldType {
	t := &FieldType{nodeBase: nodeBase{first: p.nextReal(p.pos)}}

	switch p.cur().Kind {
	case TokenMap, TokenList, TokenSet:
		switch p.cur().Kind {
		case TokenMap:
			t.Kind = TypeMap
		case TokenList:
			t.Kind = TypeList
		case TokenSet:
			t.Kind = TypeSet
		}

		p.advance()

		if p.at(TokenCPPType) {
			p.advance()

			if !p.at(TokenStringLiteral) {
				p.errorfCur("expected string literal after cpp_type, got %q", p.cur().Text)
			} else {
				t.CPPType = p.advance()
			}
		}

		if !p.expect(TokenLt, "'<' to open container type") {
			p.synchronizeTo(TokenComma, TokenSemicolon, TokenRBrace, TokenRParen)

			return nil
		}

		if t.Kind == TypeMap {
			t.KeyType = p.parseFieldType()
			if t.KeyType == nil {
				p.synchronizeTo(TokenComma, TokenGt)
			}

			if p.accept(TokenComma) != nil {
				t.ValueType = p.parseFieldType()
				if t.ValueType == nil {
					p.synchronizeTo(TokenGt)
				}
			} else {
				p.errorfCur("expected ',' and a value type for map, got %q", p.cur().Text)

				if !p.at(TokenGt) {
					p.synchronizeTo(TokenGt)
				}
			}
		} else {
			t.ValueType = p.parseFieldType()
			if t.ValueType == nil {
				p.synchronizeTo(TokenGt)
			}
		}

		if !p.expect(TokenGt, "'>' to close container type") {
			p.synchronizeTo(TokenComma, TokenSemicolon, TokenRBrace, TokenRParen)

			return nil
		}

	case TokenIdentifier:
		t.Kind = TypeIdent
		t.Ident = p.identifier()

	default:
		if !isBaseType(p.cur().Kind) {
			p.errorfCur("expected type, got %q", p.cur().Text)

			return nil
		}

		t.Kind = TypeBase
		t.Base = p.cur().Kind
		p.deprecationWarnings(p.cur())
		p.advance()
	}

	// Base and container types take annotations; identifier types do not.
	if t.Kind != TypeIdent {
		t.Annotations = p.parseAnnotationsIfPresent()
	}

	t.last = p.pos - 1

	return t
}

func (p *parser) deprecationWarnings(t Token) {
	switch t.Kind {
	case TokenByte:
		p.warnf(&t, "byte is deprecated, use i8")
	case TokenSlist:
		p.warnf(&t, "slist is no longer supported, use string or binary")
	}
}

func isBaseType(k TokenKind) bool {
	switch k {
	case TokenBool, TokenByte, TokenI8, TokenI16, TokenI32, TokenI64,
		TokenDouble, TokenString, TokenBinary, TokenSlist, TokenUUID:
		return true
	}

	return false
}

// --- constant values -------------------------------------------------------

func (p *parser) parseConstValue() *ConstValue {
	v := &ConstValue{nodeBase: nodeBase{first: p.nextReal(p.pos)}}

	switch p.cur().Kind {
	case TokenIntConstant, TokenTrue, TokenFalse:
		v.Kind = ValueInt
		v.Text = p.advance().Text

	case TokenDoubleConstant:
		v.Kind = ValueDouble
		v.Text = p.advance().Text

	case TokenStringLiteral:
		v.Kind = ValueString
		v.Text = p.advance().Text

	case TokenIdentifier:
		v.Kind = ValueIdent
		v.Text = p.advance().Text

	case TokenLBracket:
		v.Kind = ValueList

		p.advance()

		for !p.at(TokenRBracket) && !p.at(TokenEOF) {
			item := p.parseConstValue()
			if item == nil {
				p.recoverEntry(TokenRBracket)

				continue
			}

			v.List = append(v.List, item)

			p.acceptSeparator()
		}

		p.expect(TokenRBracket, "']' to close list constant")

	case TokenLBrace:
		v.Kind = ValueMap

		p.advance()

		for !p.at(TokenRBrace) && !p.at(TokenEOF) {
			key := p.parseConstValue()
			if key == nil {
				p.recoverEntry(TokenRBrace)

				continue
			}

			if !p.expect(TokenColon, "':' between map key and value") {
				p.recoverEntry(TokenRBrace)

				continue
			}

			value := p.parseConstValue()
			if value == nil {
				p.recoverEntry(TokenRBrace)

				continue
			}

			v.Map = append(v.Map, ConstMapEntry{Key: key, Value: value})

			p.acceptSeparator()
		}

		p.expect(TokenRBrace, "'}' to close map constant")

	default:
		p.errorfCur("expected constant value, got %q", p.cur().Text)

		return nil
	}

	v.last = p.pos - 1

	return v
}

// --- annotations -----------------------------------------------------------

func (p *parser) parseAnnotationsIfPresent() *Annotations {
	if !p.at(TokenLParen) {
		return nil
	}

	return p.parseAnnotations()
}

// parseAnnotations parses a parenthesized annotation group. Per the compiler
// grammar, each annotation is a name optionally followed by '=' and a string
// literal (a bare name means an implicit value of "1"), and each may end
// with an optional ',' or ';'.
func (p *parser) parseAnnotations() *Annotations {
	a := &Annotations{nodeBase: nodeBase{first: p.nextReal(p.pos)}}
	p.advance() // (

	for !p.at(TokenRParen) && !p.at(TokenEOF) {
		if !p.at(TokenIdentifier) {
			p.errorfCur("expected annotation name, got %q", p.cur().Text)
			p.recoverEntry(TokenRParen)

			continue
		}

		item := &Annotation{nodeBase: nodeBase{first: p.nextReal(p.pos)}}

		item.Name = p.identifier()
		if p.at(TokenEqual) {
			p.advance()

			if !p.at(TokenStringLiteral) {
				p.errorfCur("expected string literal annotation value, got %q", p.cur().Text)
				p.synchronizeTo(TokenComma, TokenSemicolon, TokenRParen)
			} else {
				item.Value = p.advance()
			}
		}

		item.Sep = p.acceptSeparator()

		item.last = p.pos - 1
		a.Items = append(a.Items, item)
	}

	p.expect(TokenRParen, "')' to close annotations")

	a.last = p.pos - 1

	return a
}

// --- shared helpers --------------------------------------------------------

func (p *parser) acceptSeparator() TokenKind {
	switch p.cur().Kind {
	case TokenComma, TokenSemicolon:
		k := p.cur().Kind
		p.advance()

		return k
	}

	return 0
}

func (p *parser) identifier() *Identifier {
	i := p.nextReal(p.pos)
	t := p.advance()

	return &Identifier{nodeBase: nodeBase{first: i, last: i}, Text: t.Text}
}

// expectIdentifier parses a plain identifier name and reports an error when
// the current token is not one.
func (p *parser) expectIdentifier(what string) *Identifier {
	if !p.at(TokenIdentifier) {
		p.errorfCur("expected %s, got %q", what, p.cur().Text)

		return nil
	}

	return p.identifier()
}

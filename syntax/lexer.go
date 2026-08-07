// Package syntax provides a lossless lexer, AST, and recursive-descent
// parser for the Apache Thrift IDL.
//
// Grammar reference: the Apache Thrift compiler
// (compiler/cpp/src/thrift/thriftl.ll and thrifty.yy).
//
// Losslessness: comments are preserved as trivia attached to tokens in
// source order. Whitespace itself is not preserved; instead each token
// records how many blank lines preceded it, which is the only layout
// information a formatter is allowed to act on.
package syntax

import (
	"fmt"
	"unicode/utf8"
)

// TokenKind identifies the lexical class of a Token.
type TokenKind uint8

const (
	TokenInvalid TokenKind = iota
	TokenEOF

	TokenIdentifier
	TokenIntConstant
	TokenDoubleConstant
	TokenStringLiteral

	// Keywords.
	TokenInclude
	TokenCPPInclude
	TokenCPPType
	TokenNamespace
	TokenStruct
	TokenUnion
	TokenException
	TokenService
	TokenEnum
	TokenConst
	TokenTypedef
	TokenOneway
	TokenAsync // deprecated alias for oneway
	TokenThrows
	TokenExtends
	TokenRequired
	TokenOptional
	TokenVoid
	TokenBool
	TokenByte // deprecated, accepted with a warning
	TokenI8
	TokenI16
	TokenI32
	TokenI64
	TokenDouble
	TokenString
	TokenBinary
	TokenSlist // no longer supported by the compiler, accepted for old files
	TokenUUID
	TokenMap
	TokenList
	TokenSet
	TokenTrue
	TokenFalse

	// Punctuation.
	TokenLBrace
	TokenRBrace
	TokenLParen
	TokenRParen
	TokenLBracket
	TokenRBracket
	TokenLt
	TokenGt
	TokenComma
	TokenSemicolon
	TokenColon
	TokenEqual
	TokenStar
	TokenAmp
)

var keywordKinds = map[string]TokenKind{
	"include":     TokenInclude,
	"cpp_include": TokenCPPInclude,
	"cpp_type":    TokenCPPType,
	"namespace":   TokenNamespace,
	"struct":      TokenStruct,
	"union":       TokenUnion,
	"exception":   TokenException,
	"service":     TokenService,
	"enum":        TokenEnum,
	"const":       TokenConst,
	"typedef":     TokenTypedef,
	"oneway":      TokenOneway,
	"async":       TokenAsync,
	"throws":      TokenThrows,
	"extends":     TokenExtends,
	"required":    TokenRequired,
	"optional":    TokenOptional,
	"void":        TokenVoid,
	"bool":        TokenBool,
	"byte":        TokenByte,
	"i8":          TokenI8,
	"i16":         TokenI16,
	"i32":         TokenI32,
	"i64":         TokenI64,
	"double":      TokenDouble,
	"string":      TokenString,
	"binary":      TokenBinary,
	"slist":       TokenSlist,
	"uuid":        TokenUUID,
	"map":         TokenMap,
	"list":        TokenList,
	"set":         TokenSet,
	"true":        TokenTrue,
	"false":       TokenFalse,
}

var keywordNames = func() map[TokenKind]string {
	m := make(map[TokenKind]string, len(keywordKinds))
	for text, kind := range keywordKinds {
		m[kind] = text
	}

	return m
}()

// isKeyword reports whether the token kind is one of the reserved words.
func isKeyword(k TokenKind) bool {
	_, ok := keywordNames[k]

	return ok
}

var tokenKindNames = map[TokenKind]string{
	TokenInvalid: "invalid", TokenEOF: "eof",
	TokenIdentifier: "identifier", TokenIntConstant: "int",
	TokenDoubleConstant: "double", TokenStringLiteral: "string",
	TokenLBrace: "{", TokenRBrace: "}", TokenLParen: "(", TokenRParen: ")",
	TokenLBracket: "[", TokenRBracket: "]", TokenLt: "<", TokenGt: ">",
	TokenComma: ",", TokenSemicolon: ";", TokenColon: ":", TokenEqual: "=",
	TokenStar: "*", TokenAmp: "&",
}

func (k TokenKind) String() string {
	if name, ok := keywordNames[k]; ok {
		return name
	}

	if name, ok := tokenKindNames[k]; ok {
		return name
	}

	return fmt.Sprintf("TokenKind(%d)", uint8(k))
}

// TriviaKind identifies the kind of a comment trivia.
type TriviaKind uint8

const (
	TriviaLineComment  TriviaKind = iota // // or #
	TriviaBlockComment                   // /* */
	TriviaDocComment                     // /** */
	TriviaAnnotation                     // @name{...} to end of line
)

func (k TriviaKind) String() string {
	switch k {
	case TriviaLineComment:
		return "line comment"
	case TriviaBlockComment:
		return "block comment"
	case TriviaDocComment:
		return "doc comment"
	case TriviaAnnotation:
		return "annotation"
	}

	return fmt.Sprintf("TriviaKind(%d)", uint8(k))
}

// Trivia is a comment preserved in source order. It is attached to a Token
// either as leading (before the token) or trailing (after the previous token
// on the same line).
type Trivia struct {
	Kind   TriviaKind
	Text   string // exact source text, including comment delimiters
	Offset int    // byte offset of the first character
	Line   int    // 1-based line of the first character
	Col    int    // 1-based rune column of the first character

	// BlankLinesBefore is the number of empty lines between the previous
	// token (or trivia) and this one, within the enclosing gap.
	BlankLinesBefore int
}

// Token is a single lexical token with its attached comment trivia.
type Token struct {
	Kind TokenKind
	Text string // exact source text

	Offset int // byte offset of the first character
	Line   int // 1-based line of the first character
	Col    int // 1-based rune column of the first character

	// Leading holds comments that appear between the previous token and
	// this one, on their own lines.
	Leading []Trivia
	// Trailing holds comments that appear after this token on the same
	// line.
	Trailing []Trivia
	// BlankLinesBefore is the number of empty lines between the previous
	// token and this one (0 means no blank line).
	BlankLinesBefore int
}

// Severity classifies a syntax error or warning.
type Severity uint8

const (
	SeverityError Severity = iota
	SeverityWarning
)

// Error is a lexical or parse error with a source position.
type Error struct {
	Message  string
	Offset   int
	Line     int
	Col      int
	Severity Severity
}

func (e Error) Error() string {
	return fmt.Sprintf("%d:%d: %s", e.Line, e.Col, e.Message)
}

// Lex tokenizes src into a flat token stream. The final token is always
// TokenEOF. Lexical errors are collected and returned alongside the tokens;
// lexing continues past errors so the parser can still recover.
func Lex(src []byte) ([]Token, []Error) {
	l := &lexer{src: string(src)}

	return l.run()
}

type lexer struct {
	src  string
	off  int // byte offset of the next unread character
	line int // 1-based line of the next unread character
	col  int // 1-based rune column of the next unread character
	errs []Error
}

type srcPos struct {
	offset, line, col int
}

func (l *lexer) run() ([]Token, []Error) {
	l.line, l.col = 1, 1

	var tokens []Token

	for {
		prevLine := -1
		if n := len(tokens); n > 0 {
			prevLine = tokens[n-1].Line
		}

		leading, trailing, blankLines := l.scanTrivia(prevLine)

		tok := l.scanToken()
		tok.Leading = leading
		tok.BlankLinesBefore = blankLines
		tokens = append(tokens, tok)

		if len(tokens) > 1 {
			tokens[len(tokens)-2].Trailing = trailing
		}

		if tok.Kind == TokenEOF {
			return tokens, l.errs
		}
	}
}

// scanTrivia consumes whitespace and comments between the previous token and
// the next one. Comments starting on the previous token's line become its
// trailing trivia; everything else becomes leading trivia of the next token.
// The returned blankLines count empty lines in the gap.
func (l *lexer) scanTrivia(prevLine int) (leading, trailing []Trivia, blankLines int) {
	for l.off < len(l.src) {
		switch c := l.src[l.off]; {
		case isWhitespace(c):
			blankLines += l.scanWhitespace()
		case c == '/' && l.peekByte(1) == '/':
			leading, trailing = l.appendComment(leading, trailing, prevLine, blankLines, l.scanLineComment())
		case c == '/' && l.peekByte(1) == '*':
			leading, trailing = l.appendComment(leading, trailing, prevLine, blankLines, l.scanBlockComment())
		case c == '#':
			leading, trailing = l.appendComment(leading, trailing, prevLine, blankLines, l.scanLineComment())
		case c == '@':
			// Java-style annotations (@name{...}) are preserved as trivia,
			// like comments, so they round-trip without being part of the
			// grammar.
			leading, trailing = l.appendComment(leading, trailing, prevLine, blankLines, l.scanLineAnnotation())
		default:
			return leading, trailing, blankLines
		}
	}

	return leading, trailing, blankLines
}

func (l *lexer) appendComment(leading, trailing []Trivia, prevLine, blankLines int, t Trivia) ([]Trivia, []Trivia) {
	t.BlankLinesBefore = blankLines
	if t.Line == prevLine {
		return leading, append(trailing, t)
	}

	return append(leading, t), trailing
}

// scanWhitespace consumes a whitespace run and returns the number of empty
// lines it contains (a blank line is a line containing only whitespace).
func (l *lexer) scanWhitespace() int {
	newlines := 0

	for l.off < len(l.src) {
		switch l.src[l.off] {
		case '\n':
			newlines++

			l.advanceByte()
		case '\r':
			newlines++

			l.advanceByte()

			if l.off < len(l.src) && l.src[l.off] == '\n' {
				l.advanceByte()
			}
		case ' ', '\t':
			l.advanceByte()
		default:
			if newlines > 0 {
				return newlines - 1
			}

			return 0
		}
	}

	if newlines > 0 {
		return newlines - 1
	}

	return 0
}

func (l *lexer) scanLineComment() Trivia {
	start := l.pos()
	for l.off < len(l.src) && l.src[l.off] != '\n' && l.src[l.off] != '\r' {
		l.advanceRune()
	}

	return l.finishTrivia(TriviaLineComment, start)
}

// scanLineAnnotation scans an @annotation line: from '@' to the end of the
// line, verbatim. Like line comments, the newline itself is left for the
// whitespace scanner.
func (l *lexer) scanLineAnnotation() Trivia {
	start := l.pos()
	for l.off < len(l.src) && l.src[l.off] != '\n' && l.src[l.off] != '\r' {
		l.advanceRune()
	}

	return l.finishTrivia(TriviaAnnotation, start)
}

// scanBlockComment scans a /* */ or /** */ comment. /** ... */ yields a doc
// comment trivia; everything else a block comment trivia. An unterminated
// comment consumes the rest of the input and reports an error.
func (l *lexer) scanBlockComment() Trivia {
	start := l.pos()
	doc := l.peekByte(2) == '*'
	l.advanceByte() // /
	l.advanceByte() // *

	for {
		if l.off >= len(l.src) {
			l.errorfAt(start, "unterminated comment")

			break
		}

		if l.src[l.off] == '*' && l.peekByte(1) == '/' {
			l.advanceByte()
			l.advanceByte()

			kind := TriviaBlockComment
			if doc {
				kind = TriviaDocComment
			}

			return l.finishTrivia(kind, start)
		}
		// An empty doc comment /**/ ends right after the opening /**.
		if doc && l.off == start.offset+3 && l.src[l.off] == '/' {
			l.advanceByte()

			return l.finishTrivia(TriviaDocComment, start)
		}

		l.advanceRune()
	}

	return l.finishTrivia(TriviaBlockComment, start)
}

func (l *lexer) pos() srcPos {
	return srcPos{l.off, l.line, l.col}
}

func (l *lexer) finishTrivia(kind TriviaKind, start srcPos) Trivia {
	return Trivia{Kind: kind, Text: l.src[start.offset:l.off], Offset: start.offset, Line: start.line, Col: start.col}
}

// scanToken scans the next real token, skipping over invalid characters with
// errors. It always returns a token; TokenEOF at end of input.
func (l *lexer) scanToken() Token {
	for {
		if l.off >= len(l.src) {
			return Token{Kind: TokenEOF, Offset: l.off, Line: l.line, Col: l.col}
		}

		c := l.src[l.off]

		switch {
		case isIdentStart(c):
			return l.scanIdentifier()
		case isDigit(c) || c == '+' || c == '-' || (c == '.' && isDigit(l.peekByte(1))):
			if tok, ok := l.scanNumber(); ok {
				return tok
			}

			l.errorf("unexpected character %q", c)
			l.advanceRune()
		case c == '\'' || c == '"':
			return l.scanString()
		case c == '&':
			return l.symbolToken(TokenAmp)
		case c == '*':
			return l.symbolToken(TokenStar)
		case isWhitespace(c):
			l.advanceByte()
		case c == '/' && l.peekByte(1) == '/':
			l.scanLineComment() // discarded: appears after an invalid character
		case c == '/' && l.peekByte(1) == '*':
			l.scanBlockComment() // discarded: appears after an invalid character
		case c == '#':
			l.scanLineComment() // discarded: appears after an invalid character
		default:
			if kind, ok := symbolKinds[c]; ok {
				return l.symbolToken(kind)
			}

			l.errorf("unexpected character %q", c)
			l.advanceRune()
		}
	}
}

var symbolKinds = map[byte]TokenKind{
	'{': TokenLBrace, '}': TokenRBrace,
	'(': TokenLParen, ')': TokenRParen,
	'[': TokenLBracket, ']': TokenRBracket,
	'<': TokenLt, '>': TokenGt,
	',': TokenComma, ';': TokenSemicolon,
	':': TokenColon, '=': TokenEqual,
}

func (l *lexer) symbolToken(kind TokenKind) Token {
	start := l.pos()
	l.advanceByte()

	return Token{Kind: kind, Text: l.src[start.offset:l.off], Offset: start.offset, Line: start.line, Col: start.col}
}

// scanIdentifier scans an identifier or keyword. Keywords are only matched
// when the identifier has no dot: "include.foo" is a single identifier,
// matching the compiler's longest-match lexing.
func (l *lexer) scanIdentifier() Token {
	start := l.pos()
	dotted := false

	l.advanceByte() // first character is [a-zA-Z_]

	for l.off < len(l.src) {
		c := l.src[l.off]
		if isIdentPart(c) {
			l.advanceByte()

			continue
		}

		if c == '.' && isIdentStart(l.peekByte(1)) {
			dotted = true

			l.advanceByte() // dot
			l.advanceByte() // first identifier character after dot

			continue
		}

		break
	}

	text := l.src[start.offset:l.off]
	kind := TokenIdentifier

	if !dotted {
		if k, ok := keywordKinds[text]; ok {
			kind = k
		}
	}

	return Token{Kind: kind, Text: text, Offset: start.offset, Line: start.line, Col: start.col}
}

// scanNumber scans int, hex, or double constants, mirroring the compiler's
// lexer: identifiers are scanned first ("e10" is an identifier), the longest
// of hex/int/double matches wins, and int wins ties against double ("-1" is
// an int, "-1.5" is a double, ".5" is a double).
func (l *lexer) scanNumber() (Token, bool) {
	start := l.pos()
	rest := l.src[start.offset:]

	hexLen := matchHex(rest)
	intLen := matchInt(rest)
	dubLen := matchDouble(rest)

	length := 0
	kind := TokenIntConstant

	switch {
	case hexLen > 0 && hexLen >= intLen && hexLen >= dubLen:
		length = hexLen
	case intLen > 0 && intLen >= dubLen:
		length = intLen
	case dubLen > 0:
		length = dubLen
		kind = TokenDoubleConstant
	default:
		return Token{}, false
	}

	for i := 0; i < length; i++ {
		l.advanceByte()
	}

	return Token{Kind: kind, Text: rest[:length], Offset: start.offset, Line: start.line, Col: start.col}, true
}

// matchHex returns the length of a hex constant at the start of s, or 0.
// A hex constant is [+-]?"0x" followed by at least one hex digit.
func matchHex(s string) int {
	i := 0
	if i < len(s) && (s[i] == '+' || s[i] == '-') {
		i++
	}

	if i+2 > len(s) || s[i] != '0' || (s[i+1] != 'x' && s[i+1] != 'X') {
		return 0
	}

	i += 2
	digits := 0

	for i < len(s) && isHexDigit(s[i]) {
		i++
		digits++
	}

	if digits == 0 {
		return 0
	}

	return i
}

// matchInt returns the length of an int constant at the start of s, or 0.
// An int constant is [+-]? followed by at least one digit.
func matchInt(s string) int {
	i := 0
	if i < len(s) && (s[i] == '+' || s[i] == '-') {
		i++
	}

	start := i
	for i < len(s) && isDigit(s[i]) {
		i++
	}

	if i == start {
		return 0
	}

	return i
}

// matchDouble returns the length of a double constant at the start of s, or 0.
// A double constant is [+-]?[0-9]*(\.[0-9]+)?([eE][+-]?[0-9]+)? with at least
// one digit somewhere: ".5", "+.5", and "1e10" are valid, "1." and "+." are not.
func matchDouble(s string) int {
	i := 0
	if i < len(s) && (s[i] == '+' || s[i] == '-') {
		i++
	}

	sawDigit := false

	for i < len(s) && isDigit(s[i]) {
		i++
		sawDigit = true
	}

	if i < len(s) && s[i] == '.' {
		digits := 0
		for j := i + 1; j < len(s) && isDigit(s[j]); j++ {
			digits++
		}

		if digits == 0 {
			return 0
		}

		sawDigit = true
		i += 1 + digits
	}

	if i < len(s) && (s[i] == 'e' || s[i] == 'E') {
		j := i + 1
		if j < len(s) && (s[j] == '+' || s[j] == '-') {
			j++
		}

		digits := 0

		for j < len(s) && isDigit(s[j]) {
			j++
			digits++
		}

		if digits == 0 {
			return 0
		}

		i = j
	}

	if !sawDigit {
		return 0
	}

	return i
}

// scanString scans a quoted string literal, which may use single or double
// quotes and the escapes \r \n \t \" \' \\, matching the compiler's lexer.
// It returns the raw literal (including quotes) as the token text. A string
// ending at a newline or EOF is reported as an error and returned as-is so
// lexing can continue.
func (l *lexer) scanString() Token {
	start := l.pos()
	quote := l.src[l.off]
	l.advanceByte()

	for {
		if l.off >= len(l.src) {
			l.errorfAt(start, "unterminated string literal")

			break
		}

		c := l.src[l.off]
		switch c {
		case quote:
			l.advanceByte()

			return Token{Kind: TokenStringLiteral, Text: l.src[start.offset:l.off], Offset: start.offset, Line: start.line, Col: start.col}
		case '\n', '\r':
			l.errorfAt(start, "newline in string literal")

			return Token{Kind: TokenStringLiteral, Text: l.src[start.offset:l.off], Offset: start.offset, Line: start.line, Col: start.col}
		case '\\':
			if l.off+1 >= len(l.src) {
				l.advanceByte() // consume the backslash with the string
				l.errorfAt(start, "unterminated string literal")

				return Token{Kind: TokenStringLiteral, Text: l.src[start.offset:l.off], Offset: start.offset, Line: start.line, Col: start.col}
			}

			esc := l.peekByte(1)
			switch esc {
			case 'r', 'n', 't', '"', '\'', '\\':
				l.advanceByte()
				l.advanceByte()
			default:
				l.errorf("invalid escape sequence %q", "\\"+string(rune(esc)))
				l.advanceByte()
				// The escaped byte may be part of a multibyte rune; decode it
				// as a rune so column counting stays consistent.
				l.advanceRune()
			}
		default:
			l.advanceRune()
		}
	}

	return Token{Kind: TokenStringLiteral, Text: l.src[start.offset:l.off], Offset: start.offset, Line: start.line, Col: start.col}
}

func (l *lexer) peekByte(ahead int) byte {
	if l.off+ahead >= len(l.src) {
		return 0
	}

	return l.src[l.off+ahead]
}

// advanceByte consumes one byte, tracking line and column. It is only called
// for ASCII characters.
func (l *lexer) advanceByte() {
	switch l.src[l.off] {
	case '\n':
		l.line++
		l.col = 1
	case '\r':
		// \r\n counts as a single newline; the column resets on \n.
		if l.off+1 >= len(l.src) || l.src[l.off+1] != '\n' {
			l.line++
			l.col = 1
		}
	default:
		l.col++
	}

	l.off++
}

// advanceRune consumes one UTF-8 rune. Columns are counted in runes, and
// newlines reset the line like advanceByte does.
func (l *lexer) advanceRune() {
	r, size := utf8.DecodeRuneInString(l.src[l.off:])
	switch r {
	case '\n':
		l.line++
		l.col = 1
	case '\r':
		// \r\n counts as a single newline; the column resets on \n.
		if l.off+size >= len(l.src) || l.src[l.off+size] != '\n' {
			l.line++
			l.col = 1
		}
	default:
		l.col++
	}

	l.off += size
}

// errorf records a lexical error at the current position.
func (l *lexer) errorf(format string, args ...any) {
	l.errs = append(l.errs, Error{
		Message: fmt.Sprintf(format, args...),
		Offset:  l.off,
		Line:    l.line,
		Col:     l.col,
	})
}

func (l *lexer) errorfAt(p srcPos, format string, args ...any) {
	l.errs = append(l.errs, Error{
		Message: fmt.Sprintf(format, args...),
		Offset:  p.offset,
		Line:    p.line,
		Col:     p.col,
	})
}

func isWhitespace(c byte) bool {
	return c == ' ' || c == '\t' || c == '\n' || c == '\r'
}

func isDigit(c byte) bool {
	return c >= '0' && c <= '9'
}

func isHexDigit(c byte) bool {
	return isDigit(c) || (c >= 'a' && c <= 'f') || (c >= 'A' && c <= 'F')
}

func isIdentStart(c byte) bool {
	return (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || c == '_'
}

func isIdentPart(c byte) bool {
	return isIdentStart(c) || isDigit(c)
}

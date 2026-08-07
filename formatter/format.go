// Package formatter turns a parsed thrift document into a doc IR document
// and renders it. It is the pure core of the formatting pipeline: parsing
// and file I/O happen in the caller (CLI, LSP), and the formatter never
// touches the filesystem.
//
// Layout decisions are width-driven: every construct is a group that stays
// on one line when it fits and breaks otherwise, with nested groups
// deciding independently based on the remaining width at their position.
package formatter

import (
	"errors"
	"fmt"
	"strings"

	"github.com/karitham/thrift-ls/doc"
	"github.com/karitham/thrift-ls/syntax"
)

// AlignMode controls column alignment of struct-like bodies and enum values.
type AlignMode uint8

const (
	// AlignField aligns the id, requiredness, and type columns of fields.
	AlignField AlignMode = iota
	// AlignAssign aligns the '=' sign of fields and enum values that have
	// default values.
	AlignAssign
	// AlignDisable disables alignment.
	AlignDisable
)

// SeparatorMode controls trailing separators after fields, enum values, and
// function signatures.
type SeparatorMode uint8

const (
	// SeparatorPreserve keeps the original separators as written (',', ';',
	// or none).
	SeparatorPreserve SeparatorMode = iota
	// SeparatorComma adds a trailing comma everywhere.
	SeparatorComma
	// SeparatorSemicolon adds a trailing semicolon everywhere.
	SeparatorSemicolon
	// SeparatorNone removes all trailing separators.
	SeparatorNone
)

// Construct identifies a construct with per-construct options.
type Construct uint8

const (
	ConstructStruct Construct = iota
	ConstructUnion
	ConstructException
	ConstructEnum
	ConstructArguments
	ConstructThrows
)

// PerConstruct holds one option value per construct.
type PerConstruct[T any] struct {
	Structs    T
	Unions     T
	Exceptions T
	Enums      T
	Arguments  T
	Throws     T
}

// Get returns the value for the construct.
func (p PerConstruct[T]) Get(c Construct) T {
	switch c {
	case ConstructUnion:
		return p.Unions
	case ConstructException:
		return p.Exceptions
	case ConstructEnum:
		return p.Enums
	case ConstructArguments:
		return p.Arguments
	case ConstructThrows:
		return p.Throws
	}

	return p.Structs
}

// Set assigns the value for the construct.
func (p *PerConstruct[T]) Set(c Construct, v T) {
	switch c {
	case ConstructUnion:
		p.Unions = v
	case ConstructException:
		p.Exceptions = v
	case ConstructEnum:
		p.Enums = v
	case ConstructArguments:
		p.Arguments = v
	case ConstructThrows:
		p.Throws = v
	default:
		p.Structs = v
	}
}

// AllConstructs lists every construct, in config order.
var AllConstructs = []Construct{
	ConstructStruct, ConstructUnion, ConstructException,
	ConstructEnum, ConstructArguments, ConstructThrows,
}

// String returns the config key of the construct.
func (c Construct) String() string {
	switch c {
	case ConstructUnion:
		return "unions"
	case ConstructException:
		return "exceptions"
	case ConstructEnum:
		return "enums"
	case ConstructArguments:
		return "arguments"
	case ConstructThrows:
		return "throws"
	}

	return "structs"
}

// Options controls formatting behavior. Zero values mean defaults.
type Options struct {
	// PrintWidth is the target line width. Must be positive.
	PrintWidth int
	// Indent is the string emitted for one indentation level.
	Indent string
	// TabWidth is the display width of one indentation level. Must be
	// positive.
	TabWidth int
	// Align controls column alignment (default AlignField).
	Align AlignMode
	// Separator controls trailing separators per construct (default
	// SeparatorPreserve).
	Separator PerConstruct[SeparatorMode]
	// Break forces the multiline layout per construct, even when the body
	// fits on one line.
	Break PerConstruct[bool]
	// NoTrailingNewline suppresses the final newline that is otherwise
	// appended to the formatted output.
	NoTrailingNewline bool
}

// DefaultOptions returns the default formatting options.
func DefaultOptions() Options {
	return Options{
		PrintWidth: 80,
		Indent:     "    ",
		TabWidth:   4,
		Align:      AlignField,
		Separator: PerConstruct[SeparatorMode]{
			Structs:    SeparatorPreserve,
			Unions:     SeparatorPreserve,
			Exceptions: SeparatorPreserve,
			Enums:      SeparatorPreserve,
			Arguments:  SeparatorPreserve,
			Throws:     SeparatorPreserve,
		},
	}
}

// normalize fills zero values with defaults.
func (o Options) normalize() Options {
	d := DefaultOptions()
	if o.PrintWidth <= 0 {
		o.PrintWidth = d.PrintWidth
	}

	if o.Indent == "" {
		o.Indent = d.Indent
	}

	if o.TabWidth <= 0 {
		o.TabWidth = d.TabWidth
	}

	return o
}

// Format renders a parsed document. The document must have been parsed
// without errors; callers check the parse errors before formatting. Format
// is deterministic and pure: it reads nothing but the document and options.
func Format(d *syntax.Document, o Options) (string, error) {
	if d == nil {
		return "", errors.New("formatter: nil document")
	}

	o = o.normalize()

	return PrintIR(BuildIR(d, o), o)
}

// BuildIR builds the document IR for the given options. The IR can be
// inspected with doc.Dump before printing; the printer mutates groups in
// place, so dump after PrintIR to see the layout decisions.
func BuildIR(d *syntax.Document, o Options) doc.Doc {
	f := &formatter{
		doc:  d,
		toks: d.Tokens,
		opts: o,
	}

	return f.document()
}

// PrintIR prints the document IR.
func PrintIR(ir doc.Doc, o Options) (string, error) {
	return doc.Print(ir, doc.Options{
		PrintWidth: o.PrintWidth,
		Indent:     o.Indent,
		TabWidth:   o.TabWidth,
		NewLine:    "\n",
	})
}

// FormatNode renders a single node with its comments, for hover previews
// and similar snippets.
func FormatNode(d *syntax.Document, n syntax.Node, o Options) (string, error) {
	if d == nil || n == nil {
		return "", errors.New("formatter: nil document or node")
	}

	o = o.normalize()

	f := &formatter{
		doc:  d,
		toks: d.Tokens,
		opts: o,
	}
	ir := f.node(n)

	return doc.Print(ir, doc.Options{
		PrintWidth: o.PrintWidth,
		Indent:     o.Indent,
		TabWidth:   o.TabWidth,
		NewLine:    "\n",
	})
}

type formatter struct {
	doc    *syntax.Document
	toks   []syntax.Token
	opts   Options
	nextID int
}

// id returns a fresh non-zero group id for IfBreak references.
func (f *formatter) id() int {
	f.nextID++

	return f.nextID
}

// token returns the i-th token.
func (f *formatter) token(i int) syntax.Token {
	return f.toks[i]
}

// emitOpts controls token emission.
type emitOpts struct {
	leading       bool  // emit the first token's leading trivia
	trailing      bool  // emit the last token's trailing trivia
	breakTrailing bool  // line-comment trailing forces groups to break
	skipText      []int // token indexes whose text and gap are suppressed
	breakSkip     bool  // hard line before a skipped token whose text
	// the caller emits (separators)
	pads   []padEntry // spaces inserted after a token, before its gap
	prefix string     // spaces emitted before the first token
}

// padEntry is one alignment pad at a token index.
type padEntry struct {
	idx  int
	text string
}

// containsInt reports whether xs contains v.
func containsInt(xs []int, v int) bool {
	for _, x := range xs {
		if x == v {
			return true
		}
	}

	return false
}

// padAt returns the combined pads for the token index, or "". Multiple
// entries at the same index (id pad + requiredness column) concatenate.
func padAt(pads []padEntry, idx int) string {
	var out string

	for _, p := range pads {
		if p.idx == idx {
			out += p.text
		}
	}

	return out
}

// emitTokens renders the tokens in [start, end] with their trivia, joined
// with canonical spacing. The first token's leading and last token's
// trailing trivia belong to the caller's comment helpers unless the
// corresponding flag is set. skipText suppresses separator tokens that the
// structural layout emits itself; pads widen alignment columns.
func (f *formatter) emitTokens(start, end int, o emitOpts) doc.Doc {
	parts := make([]doc.Doc, 0, 8)
	if o.prefix != "" {
		parts = append(parts, doc.Text(o.prefix))
	}

	for i := start; i <= end; i++ {
		tok := f.token(i)

		skipped := containsInt(o.skipText, i)
		if i > start {
			if skipped {
				// Leading trivia always forces a hard line; a trailing
				// line comment only when the caller emits the skipped
				// token's text after it (separators), which would
				// otherwise be swallowed.
				if len(tok.Leading) > 0 || o.breakSkip && f.lineAfter(i-1) {
					parts = append(parts, doc.HardLine)
				}
			} else {
				parts = append(parts, f.tokenGap(f.token(i-1), tok))
			}
		}

		if i > start || o.leading {
			for j, c := range tok.Leading {
				parts = append(parts, doc.Text(trimComment(c.Text)))
				// The last comment's line end comes from the caller's
				// structure for suppressed tokens, unless the caller
				// emits the token's text after it.
				if j < len(tok.Leading)-1 || !skipped || o.breakSkip {
					parts = append(parts, doc.HardLine)
				}
			}
		}

		if !skipped {
			text := tok.Text
			if tok.Kind == syntax.TokenAsync {
				text = "oneway"
			}

			parts = append(parts, doc.Text(text))
		}

		if !skipped && o.pads != nil {
			if pad := padAt(o.pads, i); pad != "" {
				parts = append(parts, doc.Text(pad))
			}
		}

		if i < end || o.trailing {
			for _, c := range tok.Trailing {
				parts = append(parts, doc.Text(" "+trimComment(c.Text)))
				if o.breakTrailing && (c.Kind == syntax.TriviaLineComment || c.Kind == syntax.TriviaAnnotation) {
					// A line comment must end its line: force the
					// enclosing groups to break so nothing follows it.
					parts = append(parts, doc.BreakParent)
				}
			}
		}
	}

	return doc.Concat(parts)
}

// tokenGap returns the doc between two adjacent tokens: a line break when
// the source separated them with a line comment (which would swallow the
// next token on the same line), a canonical space otherwise.
func (f *formatter) tokenGap(prev, cur syntax.Token) doc.Doc {
	for _, c := range prev.Trailing {
		if c.Kind == syntax.TriviaLineComment || c.Kind == syntax.TriviaAnnotation {
			return doc.HardLine
		}
	}

	if len(cur.Leading) > 0 {
		return doc.HardLine
	}

	return doc.Text(rawTokenGap(prev, cur))
}

// lineAfter reports whether the token ends its line with a line comment or
// annotation, which forces the next doc onto a new line.
func (f *formatter) lineAfter(i int) bool {
	for _, c := range f.token(i).Trailing {
		if c.Kind == syntax.TriviaLineComment || c.Kind == syntax.TriviaAnnotation {
			return true
		}
	}

	return false
}

// foldBreak is the foldable gap after an opening token or separating
// comma: a line in the broken layout, a space (or nothing) flat. A
// trailing line comment forces a hard line.
func (f *formatter) foldBreak(i int, flat string) doc.Doc {
	if f.lineAfter(i) {
		return doc.HardLine
	}

	return doc.IfBreak(doc.Line, doc.Text(flat))
}

// commaSep renders a separating comma with its trivia: a hard line when
// the previous item ends its line with a comment (which would swallow the
// comma), then the comma and the foldable gap after it.
func (f *formatter) commaSep(comma int) []doc.Doc {
	parts := []doc.Doc{}
	if f.lineAfter(comma-1) || len(f.token(comma).Leading) > 0 {
		parts = append(parts, doc.HardLine)
	}

	parts = append(parts, f.emitTokens(comma, comma, emitOpts{leading: true, trailing: true}))
	parts = append(parts, f.foldBreak(comma, " "))

	return parts
}

// rawTokenGap returns the canonical text between two adjacent tokens:
// opening and closing punctuation attaches to its neighbor, separators get
// a space after them, everything else is space-separated so tokens cannot
// merge ("const list" must not become "constlist").
func rawTokenGap(prev, cur syntax.Token) string {
	switch cur.Kind {
	case syntax.TokenRBrace, syntax.TokenRParen, syntax.TokenRBracket,
		syntax.TokenGt, syntax.TokenComma, syntax.TokenSemicolon, syntax.TokenColon:
		return ""
	}

	switch prev.Kind {
	case syntax.TokenLBrace, syntax.TokenLParen, syntax.TokenLBracket,
		syntax.TokenLt, syntax.TokenAmp:
		return ""
	}

	switch {
	case cur.Kind == syntax.TokenLt && (prev.Kind == syntax.TokenMap || prev.Kind == syntax.TokenList || prev.Kind == syntax.TokenSet):
		return "" // map<, list<, set<
	case prev.Kind == syntax.TokenComma, prev.Kind == syntax.TokenSemicolon,
		prev.Kind == syntax.TokenColon, prev.Kind == syntax.TokenEqual:
		return " "
	}

	return " "
}

// blankBefore returns the number of blank lines before the node's first
// token.
func (f *formatter) blankBefore(n syntax.Node) int {
	return f.token(n.TokStart()).BlankLinesBefore
}

// leadingComments returns the comments attached before the node's first
// token, each ending with a hard line, with the blank lines from the source
// gap distributed exactly as written: before the run, between comments, and
// between the last comment and the node itself. blankLines deliberately
// emits nothing when leading comments exist.
func (f *formatter) leadingComments(n syntax.Node) []doc.Doc {
	tok := f.token(n.TokStart())
	if len(tok.Leading) == 0 {
		return nil
	}

	parts := make([]doc.Doc, 0, 8)

	prevBlank := 0
	for _, c := range tok.Leading {
		parts = append(parts, f.blankLineDocs(c.BlankLinesBefore-prevBlank, doc.HardLine)...)
		prevBlank = c.BlankLinesBefore
		parts = append(parts, doc.Text(trimComment(c.Text)), doc.HardLine)
	}

	parts = append(parts, f.blankLineDocs(tok.BlankLinesBefore-prevBlank, doc.HardLine)...)

	return parts
}

// trailingComments returns the comments attached after the node's last token
// on the same line. Block comments render as line-suffix docs; line comments
// and annotations render inline with a break parent, since a line suffix
// after them would merge into the comment's text. When the content before
// the separator already ends with a line comment, these comments cannot
// share the line and get their own lines instead.
func (f *formatter) trailingComments(n syntax.Node, sepEmitted bool) []doc.Doc {
	parts := make([]doc.Doc, 0, 8)
	// Comments attached to a separator share the separator's own line,
	// unless the separator is not emitted (the mode drops it): then a line
	// comment before it would swallow them, and they need their own lines.
	last := f.token(n.TokEnd())

	ownLine := !sepEmitted && (last.Kind == syntax.TokenComma || last.Kind == syntax.TokenSemicolon) &&
		(f.lineAfter(n.TokEnd()-1) || leadingLineComment(last))
	for _, c := range last.Trailing {
		line := c.Kind == syntax.TriviaLineComment || c.Kind == syntax.TriviaAnnotation
		if ownLine {
			parts = append(parts, doc.HardLine, doc.Text(trimComment(c.Text)), doc.BreakParent)

			continue
		}

		if line {
			parts = append(parts, doc.Text(" "+trimComment(c.Text)), doc.BreakParent)
		} else {
			parts = append(parts, doc.LineSuffix(doc.Text(" "+trimComment(c.Text))), doc.BreakParent)
		}
	}

	return parts
}

// leadingLineComment reports whether the token's leading trivia contains a
// line comment or annotation, which ends the previous line.
func leadingLineComment(tok syntax.Token) bool {
	for _, c := range tok.Leading {
		if c.Kind == syntax.TriviaLineComment || c.Kind == syntax.TriviaAnnotation {
			return true
		}
	}

	return false
}

// node assembles a top-level node: its leading comments, its formatted
// body, and its trailing comments.
func (f *formatter) node(n syntax.Node) doc.Doc {
	parts := append(f.leadingComments(n), f.nodeBody(n))
	parts = append(parts, f.trailingComments(n, true)...)

	return doc.Concat(parts)
}

// nodeBody formats a node without its comments.
func (f *formatter) nodeBody(n syntax.Node) doc.Doc {
	switch v := n.(type) {
	case *syntax.Include:
		return f.include(v)
	case *syntax.CPPInclude:
		return f.cppInclude(v)
	case *syntax.Namespace:
		return f.namespace(v)
	case *syntax.Const:
		return f.constant(v)
	case *syntax.Typedef:
		return f.typedef(v)
	case *syntax.Enum:
		return f.enum(v)
	case *syntax.Struct:
		return f.structLike(v)
	case *syntax.Service:
		return f.service(v)
	default:
		panic(fmt.Sprintf("formatter: unknown node type %T", n))
	}
}

// document assembles the whole file: top-level nodes separated by hard
// lines, blank lines preserved, and trailing comments.
func (f *formatter) document() doc.Doc {
	parts := make([]doc.Doc, 0, 8)

	for i, n := range f.doc.Nodes {
		if i > 0 {
			parts = append(parts, doc.HardLine)
			parts = append(parts, f.blankLines(n, doc.HardLine)...)
		} else if lead := f.token(n.TokStart()).Leading; len(lead) > 0 && lead[0].BlankLinesBefore > 0 {
			// At file start the leading comments carry their blanks without
			// a separator line, so N blanks would round-trip as N-1. The
			// extra line keeps the count canonical: N blanks are N+1
			// newlines.
			parts = append(parts, doc.HardLine)
		}

		parts = append(parts, f.node(n))
	}

	// Comments at the end of the file attach to the EOF token. Like the
	// first node's leading comments, a comment run at file start (no nodes)
	// needs a separator line before its blanks to round-trip the count.
	eof := f.toks[len(f.toks)-1]
	if len(eof.Leading) > 0 {
		if len(f.doc.Nodes) > 0 || eof.Leading[0].BlankLinesBefore > 0 {
			parts = append(parts, doc.HardLine)
		}

		prevBlank := 0
		for i, c := range eof.Leading {
			parts = append(parts, f.blankLineDocs(c.BlankLinesBefore-prevBlank, doc.HardLine)...)
			prevBlank = c.BlankLinesBefore

			parts = append(parts, doc.Text(trimComment(c.Text)))
			if i < len(eof.Leading)-1 {
				parts = append(parts, doc.HardLine)
			}
		}
	}

	if !f.opts.NoTrailingNewline {
		parts = append(parts, doc.HardLine)
	}

	return doc.Concat(parts)
}

// --- headers ---------------------------------------------------------------

func (f *formatter) include(v *syntax.Include) doc.Doc {
	return f.emitTokens(v.TokStart(), v.TokEnd(), emitOpts{})
}

func (f *formatter) cppInclude(v *syntax.CPPInclude) doc.Doc {
	return f.emitTokens(v.TokStart(), v.TokEnd(), emitOpts{})
}

func (f *formatter) namespace(v *syntax.Namespace) doc.Doc {
	end := v.TokEnd()
	if v.Annotations != nil {
		end = v.Annotations.TokStart() - 1
	}

	o := emitOpts{}
	if v.Annotations != nil {
		o.trailing = true
	}

	parts := []doc.Doc{f.emitTokens(v.TokStart(), end, o)}
	if v.Annotations != nil {
		parts = append(parts, f.breakBeforeAnnotations(end))
	}

	parts = append(parts, f.annotationsDoc(v.Annotations, v.Annotations != nil && v.Annotations.TokEnd() == v.TokEnd()))
	parts = append(parts, f.afterAnnotations(v.Annotations, v.TokEnd()))

	return doc.Concat(parts)
}

func (f *formatter) typedef(v *syntax.Typedef) doc.Doc {
	end := v.TokEnd()
	if v.Annotations != nil {
		end = v.Annotations.TokStart() - 1
	}

	o := emitOpts{}
	if v.Annotations != nil {
		o.trailing = true
	}

	parts := []doc.Doc{f.emitTokens(v.TokStart(), end, o)}
	if v.Annotations != nil {
		parts = append(parts, f.breakBeforeAnnotations(end))
	}

	parts = append(parts, f.annotationsDoc(v.Annotations, v.Annotations != nil && v.Annotations.TokEnd() == v.TokEnd()))
	parts = append(parts, f.afterAnnotations(v.Annotations, v.TokEnd()))

	return doc.Concat(parts)
}

func (f *formatter) constant(v *syntax.Const) doc.Doc {
	value := v.Value
	if value == nil {
		return f.emitTokens(v.TokStart(), v.TokEnd(), emitOpts{})
	}

	eq := value.TokStart() - 1
	gap := f.tokenGap(f.token(eq), f.token(value.TokStart()))

	parts := []doc.Doc{
		f.emitTokens(v.TokStart(), eq, emitOpts{trailing: true}),
		gap,
		f.constValue(value, value.TokEnd() == v.TokEnd()),
	}
	if value.TokEnd() < v.TokEnd() {
		// Stray tokens after the value (lenient sources): preserve them
		// and their trivia, with a line break after the value's close.
		stray := f.emitTokens(value.TokEnd()+1, v.TokEnd(), emitOpts{leading: true})
		if f.lineAfter(value.TokEnd()) || len(f.token(value.TokEnd()+1).Leading) > 0 {
			stray = doc.Concat{doc.HardLine, stray}
		}

		parts = append(parts, stray)
	}

	return doc.Concat(parts)
}

// --- annotations -----------------------------------------------------------

// breakBeforeAnnotations returns a hard line when the token at idx ends
// its line with a comment, or the annotations' first token carries leading
// trivia, so neither gets swallowed by the other.
func (f *formatter) breakBeforeAnnotations(idx int) doc.Doc {
	if f.lineAfter(idx) || len(f.token(idx+1).Leading) > 0 {
		return doc.HardLine
	}

	return doc.Concat{}
}

// afterAnnotations renders any tokens between the annotations and the
// node's end — stray separators lenient sources may leave — preserving
// their leading trivia and forcing a line break after the annotations'
// close when it ends its line with a comment.
func (f *formatter) afterAnnotations(a *syntax.Annotations, end int) doc.Doc {
	if a == nil || a.TokEnd() >= end {
		return doc.Concat{}
	}

	parts := []doc.Doc{}
	if f.lineAfter(a.TokEnd()) || len(f.token(a.TokEnd()+1).Leading) > 0 {
		parts = append(parts, doc.HardLine)
	}

	parts = append(parts, f.emitTokens(a.TokEnd()+1, end, emitOpts{leading: true}))

	return doc.Concat(parts)
}

// annotationsDoc returns an annotation group, or an empty doc when absent.
// The group folds when it does not fit; the items and their separating
// commas render as token runs, so trivia inside the parens is preserved.
func (f *formatter) annotationsDoc(a *syntax.Annotations, isLast bool) doc.Doc {
	if a == nil {
		return doc.Concat{}
	}

	open, close := a.TokStart(), a.TokEnd()
	if len(a.Items) == 0 {
		closeDoc := f.emitTokens(close, close, emitOpts{leading: true, trailing: !isLast})
		if f.lineAfter(open) || len(f.token(close).Leading) > 0 {
			closeDoc = doc.Concat{doc.HardLine, closeDoc}
		}

		return doc.Concat{
			doc.Text(" "),
			f.emitTokens(open, open, emitOpts{leading: true, trailing: true}),
			closeDoc,
		}
	}

	all := emitOpts{leading: true, trailing: true}
	middle := make([]doc.Doc, 0, len(a.Items)*2)
	last := open

	for i, item := range a.Items {
		if i > 0 {
			prev := a.Items[i-1]
			if prev.Sep != 0 {
				middle = append(middle, f.commaSep(prev.TokEnd())...)
			} else {
				// Lenient sources may omit separators; keep the items
				// apart so their tokens cannot merge.
				middle = append(middle, f.foldBreak(prev.TokEnd(), " "))
			}
		}

		end := item.TokEnd()
		if item.Sep != 0 {
			end--
		}

		middle = append(middle, f.emitTokens(item.TokStart(), end, all))
		last = end
	}
	// A trailing comma after the last item (which may carry comments) is
	// not between two items, so it is emitted here.
	if lastItem := a.Items[len(a.Items)-1]; lastItem.Sep != 0 {
		middle = append(middle, f.commaSep(lastItem.TokEnd())...)
		last = lastItem.TokEnd()
	}

	group := doc.Group(doc.Concat{
		f.emitTokens(open, open, all),
		doc.Indent(doc.Concat{f.foldBreak(open, ""), doc.Concat(middle)}),
		f.foldBreak(last, ""),
		f.emitTokens(close, close, emitOpts{leading: true, trailing: !isLast}),
	})

	return doc.Concat{doc.Text(" "), group}
}

// trimComment returns the comment text without trailing whitespace, which
// the printer would trim at line ends anyway; emitting it untrimmed would
// skew the width measurement of enclosing groups.
func trimComment(text string) string {
	return strings.TrimRight(text, " \t")
}

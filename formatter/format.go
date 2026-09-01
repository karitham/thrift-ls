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
	"slices"
	"strings"
	"sync"

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
// arenaPool pools the node arenas for Format and FormatNode. The doc IR
// dies with the print, so an arena is free for reuse the moment the call
// returns: regions are reused across calls and the garbage the GC would
// scan shrinks to the region overflows. The pool hands out one arena per
// goroutine, so concurrent formats do not serialize.
var arenaPool = sync.Pool{New: func() any { return new(doc.Arena) }}

// Format renders the whole document. The arena is pooled: the returned
// string is the only thing that outlives the call.
func Format(d *syntax.Document, o Options) (string, error) {
	if d == nil {
		return "", errors.New("formatter: nil document")
	}

	o = o.normalize()

	a := arenaPool.Get().(*doc.Arena)
	defer arenaPool.Put(a)

	a.Reset()

	f := &formatter{
		Arena: a,
		doc:   d,
		toks:  d.Tokens,
		opts:  o,
	}

	return a.Print(f.document(), printOptions(o))
}

// printOptions maps formatter options to printer options.
func printOptions(o Options) doc.Options {
	return doc.Options{
		PrintWidth: o.PrintWidth,
		Indent:     o.Indent,
		TabWidth:   o.TabWidth,
		NewLine:    "\n",
	}
}

// BuildIR builds the document IR for the given options, from a fresh
// arena: the IR outlives the call and can be inspected with doc.Dump
// before printing; the printer mutates groups in place, so dump after
// PrintIR to see the layout decisions.
func BuildIR(d *syntax.Document, o Options) doc.Doc {
	f := &formatter{
		Arena: &doc.Arena{},
		doc:   d,
		toks:  d.Tokens,
		opts:  o,
	}

	return f.document()
}

// PrintIR prints the document IR.
func PrintIR(ir doc.Doc, o Options) (string, error) {
	return doc.Print(ir, printOptions(o))
}

// FormatNode renders a single node with its comments, for hover previews
// and similar snippets.
func FormatNode(d *syntax.Document, n syntax.Node, o Options) (string, error) {
	if d == nil || n == nil {
		return "", errors.New("formatter: nil document or node")
	}

	o = o.normalize()

	a := arenaPool.Get().(*doc.Arena)
	defer arenaPool.Put(a)

	a.Reset()

	f := &formatter{
		Arena: a,
		doc:   d,
		toks:  d.Tokens,
		opts:  o,
	}

	return a.Print(f.node(n), printOptions(o))
}

type formatter struct {
	*doc.Arena
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
	leading  bool // emit the own-line comments before the first token
	trailing bool // emit the same-line comments after the last token

	skipText []int      // token indexes whose text is suppressed
	text     string     // text emitted in place of a skipped token's text
	pads     []padEntry // spaces inserted after a token, before its gap
	prefix   string     // spaces emitted before the first token
}

// padEntry is one alignment pad at a token index.
type padEntry struct {
	idx  int
	text string
}

// padAt returns the combined pads for the token index, or "". Multiple
// entries at the same index (id pad + requiredness column) concatenate.
func padAt(pads []padEntry, idx int) string {
	var out strings.Builder

	for _, p := range pads {
		if p.idx == idx {
			out.WriteString(p.text)
		}
	}

	return out.String()
}

// prevReal returns the index of the previous real (non-comment) token
// strictly before idx, or -1.
func (f *formatter) prevReal(idx int) int {
	return syntax.PrevReal(f.toks, idx)
}

// nextReal returns the index of the next real (non-comment) token at or
// after idx.
func (f *formatter) nextReal(idx int) int {
	return syntax.NextReal(f.toks, idx)
}

// emitTokens renders the tokens in [start, end] with the comments
// interleaved between them, joined with canonical spacing. Comments render
// by one uniform rule: a comment on the same source line as the previous
// real token renders inline after it (a line comment owns its line end and
// emits a hard line), a comment on its own line renders on its own line
// and always ends with a hard line. The first token's own-line comments
// and the last token's same-line comments belong to the caller unless the
// corresponding flag is set. skipText suppresses separator tokens that the
// structural layout emits itself (text replaces the suppressed text when
// set); pads widen alignment columns.
func (f *formatter) emitTokens(start, end int, o emitOpts) doc.Doc {
	parts := f.Parts(12)
	if o.prefix != "" {
		parts = append(parts, f.Text(o.prefix))
	}

	if o.leading {
		parts = append(parts, f.ownLineComments(start)...)
	}

	prev := start
	first := true

	for i := start; i <= end; i++ {
		if isComment(f.token(i).Kind) {
			continue
		}

		skipped := slices.Contains(o.skipText, i)

		if !first {
			// Comments between the previous real token and this one
			// render in place; the canonical gap follows only when the
			// run left the line open. A skipped token whose text the
			// caller emits itself (braces, field separators) gets no
			// canonical gap — the caller's own spacing provides it.
			comments, lineEnded := f.commentsRun(prev, i)

			parts = append(parts, comments...)
			if !lineEnded && (!skipped || o.text != "") {
				parts = append(parts, f.tokenGap(prev, i))
			}
		}

		if !skipped {
			text := f.token(i).Text
			if f.token(i).Kind == syntax.TokenAsync {
				text = "oneway"
			}

			parts = append(parts, f.Text(text))
		} else if o.text != "" {
			parts = append(parts, f.Text(o.text))
		}

		if !skipped && o.pads != nil {
			if pad := padAt(o.pads, i); pad != "" {
				parts = append(parts, f.Text(pad))
			}
		}

		prev = i
		first = false
	}

	if !o.trailing {
		return f.Concat(parts...)
	}

	if !slices.Contains(o.skipText, prev) || o.text != "" {
		parts = append(parts, f.sameLineComments(prev)...)

		return f.Concat(parts...)
	}

	// Same-line comments after the last real token. When the token's text
	// is suppressed (the separator mode drops it), its same-line comments
	// render inline only when they also share the previous content's line;
	// otherwise they start their own line, so the output round-trips — the
	// next emission would skip them as same-line with the suppressed token.
	prevTok := f.prevReal(prev - 1)
	if prevTok >= 0 && f.token(prevTok).Line == f.token(prev).Line {
		parts = append(parts, f.sameLineComments(prev)...)
	} else {
		parts = append(parts, f.suppressedSepComments(prev)...)
	}

	return f.Concat(parts...)
}

// tokenGap returns the canonical gap between two adjacent real tokens:
// nothing when the previous token's same-line comments already ended the
// line, the canonical spacing otherwise.
func (f *formatter) tokenGap(prev, cur int) doc.Doc {
	if f.sameLineEndsLine(prev) {
		return f.Concat()
	}

	return f.Text(rawTokenGap(f.token(prev), f.token(cur)))
}

// foldBreak is the foldable gap after an opening token or separating
// comma: a line in the broken layout, a space (or nothing) flat.
func (f *formatter) foldBreak(i int, flat string) doc.Doc {
	return f.IfBreak(doc.Line, f.Text(flat))
}

// commaSep renders a separating comma with its trivia, then the foldable
// gap after it.
func (f *formatter) commaSep(comma int) []doc.Doc {
	p := f.Parts(2)
	p = append(p, f.emitTokens(comma, comma, emitOpts{leading: true, trailing: true}))
	p = append(p, f.foldBreak(comma, " "))

	return p
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
		syntax.TokenLt, syntax.TokenAmp, syntax.TokenAt:
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

// node assembles a top-level node: its own-line comments, its formatted
// body, and its same-line comments.
func (f *formatter) node(n syntax.Node) doc.Doc {
	parts := append(f.ownLineComments(n.TokStart()), f.nodeBody(n))
	parts = append(parts, f.sameLineComments(n.TokEnd())...)

	return f.Concat(parts...)
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

// document assembles the whole file: top-level nodes separated by
// collapsible lines, blank lines preserved, and trailing comments.
func (f *formatter) document() doc.Doc {
	parts := f.Parts(12)

	for i, n := range f.doc.Nodes {
		if i > 0 {
			// The separator line collapses when the previous node ended
			// with a line comment (which owns its line end).
			parts = append(parts, doc.Line)
			parts = append(parts, f.blankLines(n, doc.HardLine)...)
		}

		parts = append(parts, f.node(n))
	}

	// Comments at the end of the file precede the EOF token.
	parts = append(parts, f.ownLineComments(len(f.toks)-1)...)

	if !f.opts.NoTrailingNewline {
		// The final newline collapses when the file ends with a line
		// comment (which owns its line end).
		parts = append(parts, doc.Line)
	}

	return f.Concat(parts...)
}

// --- headers ---------------------------------------------------------------

func (f *formatter) include(v *syntax.Include) doc.Doc {
	return f.emitTokens(v.TokStart(), v.TokEnd(), emitOpts{})
}

func (f *formatter) cppInclude(v *syntax.CPPInclude) doc.Doc {
	return f.emitTokens(v.TokStart(), v.TokEnd(), emitOpts{})
}

func (f *formatter) namespace(v *syntax.Namespace) doc.Doc {
	return f.headerWithAnnotations(v.Structured, v.TokStart(), v.TokEnd(), v.Annotations)
}

func (f *formatter) typedef(v *syntax.Typedef) doc.Doc {
	return f.headerWithAnnotations(v.Structured, v.TokStart(), v.TokEnd(), v.Annotations)
}

// headerWithAnnotations emits the node's structured annotations (one per
// line), the header tokens up to its annotation group (which keeps its own
// foldable group), and any stray tokens after them.
func (f *formatter) headerWithAnnotations(sas []*syntax.StructuredAnnotation, start, end int, ann *syntax.Annotations) doc.Doc {
	pre, start := f.structuredAnnosLead(sas, start)

	headerEnd := end
	if ann != nil {
		headerEnd = ann.TokStart() - 1
	}

	o := emitOpts{}
	if ann != nil {
		o.trailing = true
	}

	parts := f.Parts(3)
	parts = append(parts, pre)
	parts = append(parts, f.emitTokens(start, headerEnd, o))
	parts = append(parts, f.annotationsDoc(ann, ann != nil && ann.TokEnd() == end))
	parts = append(parts, f.afterAnnotations(ann, end))

	return f.Concat(parts...)
}

func (f *formatter) constant(v *syntax.Const) doc.Doc {
	pre, start := f.structuredAnnosLead(v.Structured, v.TokStart())

	value := v.Value
	if value == nil {
		return f.Concat(pre, f.emitTokens(start, v.TokEnd(), emitOpts{}))
	}

	eq := f.prevReal(value.TokStart() - 1)

	parts := f.Parts(2)
	parts = append(parts, pre)
	parts = append(parts, f.emitTokens(start, eq, emitOpts{trailing: true}))
	parts = append(parts, f.tokenGap(eq, value.TokStart()))
	// Own-line comments before the value render at the value boundary,
	// outside the value's own group.
	parts = append(parts, f.ownLineComments(value.TokStart())...)

	parts = append(parts, f.constValue(value, value.TokEnd() == v.TokEnd(), containerConstruct(v.Type)))
	if value.TokEnd() < v.TokEnd() {
		// Same-line comments after the value render at the value
		// boundary, outside the value's own group, before the stray
		// tokens (the const's trailing separator).
		parts = append(parts, f.sameLineComments(value.TokEnd())...)
		parts = append(parts, f.emitTokens(f.nextReal(value.TokEnd()+1), v.TokEnd(), emitOpts{leading: true}))
	}

	return f.Concat(parts...)
}

// --- annotations -----------------------------------------------------------

// afterAnnotations renders any tokens between the annotations and the
// node's end — stray separators lenient sources may leave — preserving
// their comments.
func (f *formatter) afterAnnotations(a *syntax.Annotations, end int) doc.Doc {
	if a == nil || a.TokEnd() >= end {
		return f.Concat()
	}

	return f.emitTokens(f.nextReal(a.TokEnd()+1), end, emitOpts{leading: true})
}

// annotationsDoc returns an annotation group, or an empty doc when absent.
// The group folds when it does not fit; the items and their separating
// commas render as token runs, so comments inside the parens are preserved.
func (f *formatter) annotationsDoc(a *syntax.Annotations, isLast bool) doc.Doc {
	if a == nil {
		return f.Concat()
	}

	open, close := a.TokStart(), a.TokEnd()
	if len(a.Items) == 0 {
		out := f.Concat(
			f.Text(" "),
			f.emitTokens(open, open, emitOpts{leading: true, trailing: true}),
			f.emitTokens(close, close, emitOpts{leading: true}),
		)
		if !isLast {
			// Same-line comments after the close render at the group
			// boundary, outside it.
			out = f.Concat(out, f.Concat(f.sameLineComments(close)...))
		}

		return out
	}

	all := emitOpts{leading: true, trailing: true}
	middle := f.Parts(len(a.Items) * 2)
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

	openBreak := f.Parts(2)
	openBreak = append(openBreak, f.foldBreak(open, ""))
	openBreak = append(openBreak, f.Concat(middle...))

	parts := f.Parts(4)
	parts = append(parts, f.emitTokens(open, open, all))
	parts = append(parts, f.Indent(f.Concat(openBreak...)))
	parts = append(parts, f.foldBreak(last, ""))
	parts = append(parts, f.emitTokens(close, close, emitOpts{leading: true}))

	group := f.Group(f.Concat(parts...))

	out := f.Parts(2)
	out = append(out, f.Text(" "))
	out = append(out, group)

	if !isLast {
		// Same-line comments after the close render at the group boundary,
		// outside the group, so the group folds independently.
		withSuffix := f.Parts(2)
		withSuffix = append(withSuffix, f.Concat(out...))
		withSuffix = append(withSuffix, f.Concat(f.sameLineComments(close)...))

		return f.Concat(withSuffix...)
	}

	return f.Concat(out...)
}

// trimComment returns the comment text without trailing whitespace, which
// the printer would trim at line ends anyway; emitting it untrimmed would
// skew the width measurement of enclosing groups.
func trimComment(text string) string {
	return strings.TrimRight(text, " \t")
}

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
	ConstructList
	ConstructMap
)

// PerConstruct holds one option value per construct. The JSON tags make the
// per-construct option maps config-compatible ("structs", "arguments", ...),
// so the options layer and the CLI share this single source of truth.
type PerConstruct[T any] struct {
	Structs    T `json:"structs"`
	Unions     T `json:"unions"`
	Exceptions T `json:"exceptions"`
	Enums      T `json:"enums"`
	Arguments  T `json:"arguments"`
	Throws     T `json:"throws"`
	Lists      T `json:"lists"`
	Maps       T `json:"maps"`
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
	case ConstructList:
		return p.Lists
	case ConstructMap:
		return p.Maps
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
	case ConstructList:
		p.Lists = v
	case ConstructMap:
		p.Maps = v
	default:
		p.Structs = v
	}
}

// AllConstructs lists every construct, in config order.
var AllConstructs = []Construct{
	ConstructStruct, ConstructUnion, ConstructException,
	ConstructEnum, ConstructArguments, ConstructThrows,
	ConstructList, ConstructMap,
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
	case ConstructList:
		return "lists"
	case ConstructMap:
		return "maps"
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

// isComment reports whether the token kind is a comment trivia token.
func isComment(k syntax.TokenKind) bool {
	return syntax.IsComment(k)
}

// lineComment reports whether the token kind is a line comment or
// annotation, which consumes the rest of its source line: whatever follows
// always starts a fresh line.
func lineComment(k syntax.TokenKind) bool {
	return k == syntax.TokenLineComment || k == syntax.TokenAnnotation
}

// prevReal returns the index of the previous real (non-comment) token
// strictly before idx, or -1.
func (f *formatter) prevReal(idx int) int {
	for idx >= 0 && isComment(f.token(idx).Kind) {
		idx--
	}

	return idx
}

// nextReal returns the index of the next real (non-comment) token at or
// after idx.
func (f *formatter) nextReal(idx int) int {
	for idx < len(f.toks) && isComment(f.token(idx).Kind) {
		idx++
	}

	return idx
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
	parts := make([]doc.Doc, 0, 8)
	if o.prefix != "" {
		parts = append(parts, doc.Text(o.prefix))
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

		skipped := containsInt(o.skipText, i)

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

			parts = append(parts, doc.Text(text))
		} else if o.text != "" {
			parts = append(parts, doc.Text(o.text))
		}

		if !skipped && o.pads != nil {
			if pad := padAt(o.pads, i); pad != "" {
				parts = append(parts, doc.Text(pad))
			}
		}

		prev = i
		first = false
	}

	if o.trailing {
		// Same-line comments after the last real token. When the token's
		// text is suppressed (the separator mode drops it), its same-line
		// comments render inline only when they also share the previous
		// content's line; otherwise they start their own line, so the
		// output round-trips — the next emission would skip them as
		// same-line with the suppressed token.
		if containsInt(o.skipText, prev) && o.text == "" {
			prevTok := f.prevReal(prev - 1)
			if prevTok >= 0 && f.token(prevTok).Line == f.token(prev).Line {
				parts = append(parts, f.sameLineComments(prev)...)
			} else {
				parts = append(parts, f.suppressedSepComments(prev)...)
			}
		} else {
			parts = append(parts, f.sameLineComments(prev)...)
		}
	}

	return doc.Concat(parts)
}

// suppressedSepComments renders the same-line comments of a suppressed
// separator (the mode drops its text) that cannot share the previous
// content's line: each starts its own line, so the output round-trips.
func (f *formatter) suppressedSepComments(idx int) []doc.Doc {
	parts := make([]doc.Doc, 0, 4)

	for c := idx + 1; c < len(f.toks); c++ {
		ct := f.token(c)
		if !isComment(ct.Kind) || ct.Line != f.token(idx).Line {
			break
		}

		parts = append(parts, doc.SoftLine)
		parts = append(parts, f.blankLineDocs(ct.BlankLinesBefore, doc.HardLine)...)
		parts = append(parts, doc.Text(trimComment(ct.Text)), doc.HardLine)
	}

	return parts
}

// commentsRun renders the comments strictly between the real tokens at
// prev and cur (all stream entries in between are comments): a comment on
// the previous token's line renders inline after it, a comment on its own
// line renders on its own line and owns its line end. It reports whether
// the run ended the line, in which case the caller must not emit the
// canonical gap.
func (f *formatter) commentsRun(prev, cur int) ([]doc.Doc, bool) {
	parts := make([]doc.Doc, 0, 4)
	lineEnded := false

	for c := prev + 1; c < cur; c++ {
		ct := f.token(c)
		if ct.Line == f.token(prev).Line {
			// Same-line: inline after the previous token. A line comment
			// owns its line end.
			parts = append(parts, doc.Text(" "), doc.Text(trimComment(ct.Text)))
			if lineComment(ct.Kind) {
				parts = append(parts, doc.HardLine)
				lineEnded = true
			} else {
				lineEnded = false
			}

			continue
		}

		// Own-line: the comment starts its own line. The soft line
		// collapses when the output already ended the line (the comment
		// before it owns its line end) and provides the break otherwise.
		parts = append(parts, doc.SoftLine)
		parts = append(parts, f.blankLineDocs(ct.BlankLinesBefore, doc.HardLine)...)
		parts = append(parts, doc.Text(trimComment(ct.Text)), doc.HardLine)
		lineEnded = true
	}

	return parts, lineEnded
}

// ownLineComments renders the own-line comments in the gap before the real
// token at idx: the comments between the previous real token and idx that
// start their own source line, each preceded by its blank lines and
// followed by a hard line, plus the blank lines between the last comment
// and idx itself. Same-line comments in the gap belong to the previous
// token's trailing and are emitted by the caller's trailing phase or the
// next commentsRun.
func (f *formatter) ownLineComments(idx int) []doc.Doc {
	prev := f.prevReal(idx - 1)

	parts := make([]doc.Doc, 0, 4)
	first := true

	for c := prev + 1; c < idx; c++ {
		ct := f.token(c)
		if prev >= 0 && ct.Line == f.token(prev).Line {
			continue
		}

		if prev >= 0 {
			// The soft line collapses when the output already ended the
			// line and provides the break otherwise.
			parts = append(parts, doc.SoftLine)
		} else if first && ct.BlankLinesBefore > 0 {
			// At file start there is no separator line before the first
			// comment: N blank lines round-trip as N+1 newlines.
			parts = append(parts, doc.HardLine)
		}

		first = false

		parts = append(parts, f.blankLineDocs(ct.BlankLinesBefore, doc.HardLine)...)
		parts = append(parts, doc.Text(trimComment(ct.Text)), doc.HardLine)
	}

	if len(parts) > 0 && f.token(idx).BlankLinesBefore > 0 {
		parts = append(parts, f.blankLineDocs(f.token(idx).BlankLinesBefore, doc.HardLine)...)
	}

	return parts
}

// sameLineComments renders the same-line comments after the real token at
// idx: comments sharing its source line, each preceded by a space and —
// for line comments — ending with a hard line.
func (f *formatter) sameLineComments(idx int) []doc.Doc {
	parts := make([]doc.Doc, 0, 4)

	for c := idx + 1; c < len(f.toks); c++ {
		ct := f.token(c)
		if !isComment(ct.Kind) || ct.Line != f.token(idx).Line {
			break
		}

		parts = append(parts, doc.Text(" "), doc.Text(trimComment(ct.Text)))
		if lineComment(ct.Kind) {
			parts = append(parts, doc.HardLine)
		}
	}

	return parts
}

// hasOwnLineComments reports whether the gap before the real token at idx
// contains an own-line comment.
func (f *formatter) hasOwnLineComments(idx int) bool {
	prev := f.prevReal(idx - 1)
	for c := prev + 1; c < idx; c++ {
		if prev < 0 || f.token(c).Line != f.token(prev).Line {
			return true
		}
	}

	return false
}

// hasSameLineComments reports whether the real token at idx has same-line
// comments after it.
func (f *formatter) hasSameLineComments(idx int) bool {
	c := idx + 1
	if c >= len(f.toks) {
		return false
	}

	ct := f.token(c)
	if !isComment(ct.Kind) || ct.Line != f.token(idx).Line {
		return false
	}

	return true
}

// tokenGap returns the canonical gap between two adjacent real tokens:
// nothing when the previous token's same-line comments already ended the
// line, the canonical spacing otherwise.
func (f *formatter) tokenGap(prev, cur int) doc.Doc {
	if f.sameLineEndsLine(prev) {
		return doc.Concat{}
	}

	return doc.Text(rawTokenGap(f.token(prev), f.token(cur)))
}

// sameLineEndsLine reports whether the token's same-line comments end with
// a line comment, which owns its line end.
func (f *formatter) sameLineEndsLine(idx int) bool {
	for c := idx + 1; c < len(f.toks); c++ {
		ct := f.token(c)
		if !isComment(ct.Kind) || ct.Line != f.token(idx).Line {
			break
		}

		if lineComment(ct.Kind) {
			return true
		}
	}

	return false
}

// foldBreak is the foldable gap after an opening token or separating
// comma: a line in the broken layout, a space (or nothing) flat.
func (f *formatter) foldBreak(i int, flat string) doc.Doc {
	return doc.IfBreak(doc.Line, doc.Text(flat))
}

// commaSep renders a separating comma with its trivia, then the foldable
// gap after it.
func (f *formatter) commaSep(comma int) []doc.Doc {
	return []doc.Doc{
		f.emitTokens(comma, comma, emitOpts{leading: true, trailing: true}),
		f.foldBreak(comma, " "),
	}
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

// node assembles a top-level node: its own-line comments, its formatted
// body, and its same-line comments.
func (f *formatter) node(n syntax.Node) doc.Doc {
	parts := append(f.ownLineComments(n.TokStart()), f.nodeBody(n))
	parts = append(parts, f.sameLineComments(n.TokEnd())...)

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

// document assembles the whole file: top-level nodes separated by
// collapsible lines, blank lines preserved, and trailing comments.
func (f *formatter) document() doc.Doc {
	parts := make([]doc.Doc, 0, 8)

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
	parts = append(parts, f.annotationsDoc(v.Annotations, v.Annotations != nil && v.Annotations.TokEnd() == v.TokEnd()))
	parts = append(parts, f.afterAnnotations(v.Annotations, v.TokEnd()))

	return doc.Concat(parts)
}

func (f *formatter) constant(v *syntax.Const) doc.Doc {
	value := v.Value
	if value == nil {
		return f.emitTokens(v.TokStart(), v.TokEnd(), emitOpts{})
	}

	eq := f.prevReal(value.TokStart() - 1)

	parts := []doc.Doc{
		f.emitTokens(v.TokStart(), eq, emitOpts{trailing: true}),
		f.tokenGap(eq, value.TokStart()),
	}
	// Own-line comments before the value render at the value boundary,
	// outside the value's own group.
	parts = append(parts, f.ownLineComments(value.TokStart())...)
	parts = append(parts, f.constValue(value, value.TokEnd() == v.TokEnd()))
	if value.TokEnd() < v.TokEnd() {
		// Same-line comments after the value render at the value
		// boundary, outside the value's own group, before the stray
		// tokens (the const's trailing separator).
		parts = append(parts, f.sameLineComments(value.TokEnd())...)
		parts = append(parts, f.emitTokens(f.nextReal(value.TokEnd()+1), v.TokEnd(), emitOpts{leading: true}))
	}

	return doc.Concat(parts)
}

// --- annotations -----------------------------------------------------------

// afterAnnotations renders any tokens between the annotations and the
// node's end — stray separators lenient sources may leave — preserving
// their comments.
func (f *formatter) afterAnnotations(a *syntax.Annotations, end int) doc.Doc {
	if a == nil || a.TokEnd() >= end {
		return doc.Concat{}
	}

	return f.emitTokens(f.nextReal(a.TokEnd()+1), end, emitOpts{leading: true})
}

// annotationsDoc returns an annotation group, or an empty doc when absent.
// The group folds when it does not fit; the items and their separating
// commas render as token runs, so comments inside the parens are preserved.
func (f *formatter) annotationsDoc(a *syntax.Annotations, isLast bool) doc.Doc {
	if a == nil {
		return doc.Concat{}
	}

	open, close := a.TokStart(), a.TokEnd()
	if len(a.Items) == 0 {
		out := doc.Concat{
			doc.Text(" "),
			f.emitTokens(open, open, emitOpts{leading: true, trailing: true}),
			f.emitTokens(close, close, emitOpts{leading: true}),
		}
		if !isLast {
			// Same-line comments after the close render at the group
			// boundary, outside it.
			out = doc.Concat{out, doc.Concat(f.sameLineComments(close))}
		}

		return out
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
		f.emitTokens(close, close, emitOpts{leading: true}),
	})

	out := doc.Concat{doc.Text(" "), group}
	if !isLast {
		// Same-line comments after the close render at the group boundary,
		// outside the group, so the group folds independently.
		out = doc.Concat{out, doc.Concat(f.sameLineComments(close))}
	}

	return out
}

// trimComment returns the comment text without trailing whitespace, which
// the printer would trim at line ends anyway; emitting it untrimmed would
// skew the width measurement of enclosing groups.
func trimComment(text string) string {
	return strings.TrimRight(text, " \t")
}

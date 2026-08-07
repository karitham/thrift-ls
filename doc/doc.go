// Package doc implements the Wadler/Prettier document IR and a width-aware
// printer. A document is a declarative description of formatted output:
// text, lines that break or stay flat depending on available width, groups
// that decide independently whether they fit, and conditional pieces. The
// single printer turns any document into a string given a print width.
//
// This is a faithful port of Prettier's document algebra
// (src/document/builders and src/document/printer).
//
// A document passed to Print is mutated by break propagation. Do not print
// the same document concurrently, and do not reuse a document after printing
// it.
package doc

// Doc is a formatted document. The interface is sealed: only the concrete
// types in this package can implement it, so the printer can exhaustively
// switch over it.
type Doc interface {
	isDoc()
}

// Text is literal output.
type Text string

func (Text) isDoc() {}

// textNode is the heap or arena allocated form of Text: a pointer, so
// boxing it into Doc does not allocate.
type textNode struct {
	s string
}

func (textNode) isDoc() {}

// NewText returns a doc for literal output. The value type Text also
// exists for direct construction; prefer NewText or Arena.Text so the
// node is a pointer.
func NewText(s string) Doc { return &textNode{s: s} }

// Concat is a sequence of documents printed in order.
type Concat []Doc

func (Concat) isDoc() {}

// concatNode is the arena allocated form of Concat.
type concatNode struct {
	parts []Doc
}

func (concatNode) isDoc() {}

// Join returns a Concat of parts joined by sep.
func Join(sep Doc, parts []Doc) Doc {
	if len(parts) == 0 {
		return Concat(nil)
	}

	out := make(Concat, 0, len(parts)*2-1)
	for i, part := range parts {
		if i > 0 {
			out = append(out, sep)
		}

		out = append(out, part)
	}

	return out
}

// Arena is a bump allocator for doc nodes. A document built through an
// arena allocates its nodes from a few growing regions instead of one
// heap allocation per node, at the cost of the arena retaining the
// regions until it is discarded. The arena must outlive the printing of
// the documents it built, must not be used by concurrent builders, and
// must not be copied after first use (it holds a printer with scratch
// buffers). The zero value is ready to use.
type Arena struct {
	texts    []textNode
	concats  []concatNode
	groups   []group
	ifs      []ifBreak
	indents  []indent
	aligns   []align
	suffixes []lineSuffix
	parts    []Doc
	partsLen int
	printer  printer // reused by Print; not safe to copy once used
}

// Reset reuses the arena's regions for a fresh document: the previous
// document built from this arena must be dead (no references to its
// nodes), as its storage is overwritten. The zero value is already
// reset.
func (a *Arena) Reset() {
	a.partsLen = 0
	a.texts = a.texts[:0]
	a.concats = a.concats[:0]
	a.groups = a.groups[:0]
	a.ifs = a.ifs[:0]
	a.indents = a.indents[:0]
	a.aligns = a.aligns[:0]
	a.suffixes = a.suffixes[:0]
}

// Text returns a doc for literal output, allocated from the arena.
func (a *Arena) Text(s string) Doc {
	a.texts = append(a.texts, textNode{s: s})
	return &a.texts[len(a.texts)-1]
}

// Concat returns a doc for parts, allocated from the arena. parts may
// be a Parts slice; it is kept as the node's children, so it must not
// be reused by the caller.
func (a *Arena) Concat(parts ...Doc) Doc {
	a.concats = append(a.concats, concatNode{parts: parts})
	return &a.concats[len(a.concats)-1]
}

// Join returns a doc for parts joined by sep, allocated from the arena.
func (a *Arena) Join(sep Doc, parts []Doc) Doc {
	if len(parts) == 0 {
		return a.Concat(nil)
	}

	out := a.Parts(len(parts)*2 - 1)
	for i, part := range parts {
		if i > 0 {
			out = append(out, sep)
		}

		out = append(out, part)
	}

	return a.Concat(out...)
}

// Parts returns a scratch slice of docs with the given capacity,
// allocated from the arena's parts region. The region is never reused
// for the arena's lifetime, so the slice may become a node's children.
func (a *Arena) Parts(capacity int) []Doc {
	if a.partsLen+capacity > len(a.parts) {
		// Grow the single region geometrically. The old region stays
		// reachable from slices handed out earlier, so it must not be
		// reused; the copy is amortized.
		n := len(a.parts) * 2
		if n < a.partsLen+capacity {
			n = a.partsLen + capacity
		}

		grown := make([]Doc, n)
		copy(grown, a.parts[:a.partsLen])
		a.parts = grown
	}

	s := a.parts[a.partsLen : a.partsLen+capacity : a.partsLen+capacity]
	a.partsLen += capacity

	return s[:0]
}

// Group wraps d in a group allocated from the arena.
func (a *Arena) Group(d Doc) Doc {
	a.groups = append(a.groups, group{doc: d})
	return &a.groups[len(a.groups)-1]
}

// GroupBreak wraps d in a group that always breaks, from the arena.
func (a *Arena) GroupBreak(d Doc) Doc {
	a.groups = append(a.groups, group{doc: d, brk: true})
	return &a.groups[len(a.groups)-1]
}

// GroupID wraps d in a group with an ID, from the arena.
func (a *Arena) GroupID(id int, d Doc) Doc {
	a.groups = append(a.groups, group{doc: d, id: id})
	return &a.groups[len(a.groups)-1]
}

// ConditionalGroup tries each state in order, from the arena.
func (a *Arena) ConditionalGroup(id int, states ...Doc) Doc {
	a.groups = append(a.groups, group{doc: states[0], id: id, expanded: states})
	return &a.groups[len(a.groups)-1]
}

// IfBreak builds an IfBreak for the innermost enclosing group, from the
// arena.
func (a *Arena) IfBreak(broken, flat Doc) Doc {
	a.ifs = append(a.ifs, ifBreak{breakDoc: broken, flatDoc: flat})
	return &a.ifs[len(a.ifs)-1]
}

// IfBreakFor builds an IfBreak following the group with the given ID,
// from the arena.
func (a *Arena) IfBreakFor(broken, flat Doc, groupID int) Doc {
	a.ifs = append(a.ifs, ifBreak{breakDoc: broken, flatDoc: flat, groupID: groupID})
	return &a.ifs[len(a.ifs)-1]
}

// Indent increases the indentation of its contents by one level, from
// the arena.
func (a *Arena) Indent(d Doc) Doc {
	a.indents = append(a.indents, indent{doc: d})
	return &a.indents[len(a.indents)-1]
}

// Align indents its contents by n columns, from the arena.
func (a *Arena) Align(n int, d Doc) Doc {
	a.aligns = append(a.aligns, align{n: n, doc: d})
	return &a.aligns[len(a.aligns)-1]
}

// LineSuffix prints its contents at the end of the current line, from
// the arena.
func (a *Arena) LineSuffix(d Doc) Doc {
	a.suffixes = append(a.suffixes, lineSuffix{doc: d})
	return &a.suffixes[len(a.suffixes)-1]
}

// LineDoc is a line separator. In flat mode a plain line prints a space, a
// soft line prints nothing, and a hard line prints a newline regardless of
// mode. In break mode every line prints a newline followed by the current
// indentation. Comment marks a hard line that ends a line comment's line:
// the printer remembers that the line was comment-ended, so a following
// AfterComment line can collapse instead of leaving a blank. AfterComment
// marks a soft structural line that renders a newline unless the output
// already ended with a Comment line (a blank line before it still renders).
type LineDoc struct {
	Soft         bool
	Hard         bool
	Literal      bool // like Hard, but the newline is followed by no indentation
	Comment      bool // hard line ending a line comment's line
	AfterComment bool // soft structural line collapsing after a Comment line
}

func (LineDoc) isDoc() {}

// Lines, matching Prettier's builders:
//
//	Line          - a space in flat mode, a newline in break mode
//	SoftLine      - nothing in flat mode, a newline in break mode
//	HardLine      - always a newline; breaks enclosing groups
//	LiteralLine   - always a newline with no indentation; breaks enclosing groups
//	CommentLine   - a hard line owning a line comment's line end; breaks
//	                enclosing groups; the printer remembers the line was
//	                comment-ended
//	AfterCommentLine - a soft structural line that renders a newline unless
//	                the output already ended with a CommentLine
var (
	Line               Doc = LineDoc{}
	SoftLine           Doc = LineDoc{Soft: true}
	HardLine           Doc = Concat{LineDoc{Hard: true}, BreakParent}
	LiteralLine        Doc = Concat{LineDoc{Hard: true, Literal: true}, BreakParent}
	CommentLine        Doc = Concat{LineDoc{Hard: true, Comment: true}, BreakParent}
	AfterCommentLine   Doc = LineDoc{Soft: true, AfterComment: true}
	HardLineNoBreak    Doc = LineDoc{Hard: true}
	LiteralLineNoBreak Doc = LineDoc{Hard: true, Literal: true}
)

// group is a piece of a document that fits on one line if possible and
// breaks otherwise. The printer decides per group based on the remaining
// width at its position, so nested groups break independently.
type group struct {
	doc      Doc
	id       int   // non-zero ID lets IfBreak refer to this group
	brk      bool  // forced break; mutated by break propagation
	expanded []Doc // conditional group states, least expanded first
}

func (*group) isDoc() {}

// Group wraps d in a group that breaks only when it does not fit.
func Group(d Doc) Doc { return &group{doc: d} }

// GroupBreak wraps d in a group that always breaks.
func GroupBreak(d Doc) Doc { return &group{doc: d, brk: true} }

// GroupID wraps d in a group with an ID so IfBreak can query its mode.
func GroupID(id int, d Doc) Doc { return &group{doc: d, id: id} }

// ConditionalGroup tries each state in order (least expanded first) and
// prints the first that fits; the last state breaks if none fit.
func ConditionalGroup(id int, states ...Doc) Doc {
	return &group{doc: states[0], id: id, expanded: states}
}

// ifBreak prints BreakDoc when the group it belongs to (or the group with
// GroupID) is broken, and FlatDoc when it is flat.
type ifBreak struct {
	breakDoc Doc
	flatDoc  Doc
	groupID  int // 0 means the innermost enclosing group
}

func (*ifBreak) isDoc() {}

// IfBreak builds an IfBreak for the innermost enclosing group.
func IfBreak(broken, flat Doc) Doc { return &ifBreak{breakDoc: broken, flatDoc: flat} }

// IfBreakFor builds an IfBreak that follows the group with the given ID.
func IfBreakFor(broken, flat Doc, groupID int) Doc {
	return &ifBreak{breakDoc: broken, flatDoc: flat, groupID: groupID}
}

type indent struct {
	doc Doc
}

func (*indent) isDoc() {}

// Indent increases the indentation of its contents by one level.
func Indent(d Doc) Doc { return &indent{doc: d} }

type align struct {
	n   int
	doc Doc
}

func (*align) isDoc() {}

// Align indents its contents by n columns relative to the current
// indentation. With tabs enabled, n is rounded up to one tab.
func Align(n int, d Doc) Doc { return &align{n: n, doc: d} }

type lineSuffix struct {
	doc Doc
}

func (*lineSuffix) isDoc() {}

// LineSuffix prints its contents at the end of the current line, after the
// next line break (used for end-of-line comments).
func LineSuffix(d Doc) Doc { return &lineSuffix{doc: d} }

type lineSuffixBoundary struct{}

func (lineSuffixBoundary) isDoc() {}

// LineSuffixBoundary ends the current line-suffix group: suffixes before the
// boundary print at the next line break, suffixes after it wait for the one
// after that.
var LineSuffixBoundary Doc = lineSuffixBoundary{}

type trim struct{}

func (trim) isDoc() {}

// TrimDoc removes trailing whitespace from the output produced so far.
var TrimDoc Doc = trim{}

type breakParent struct{}

func (breakParent) isDoc() {}

// BreakParent forces the nearest enclosing group to break.
var BreakParent Doc = breakParent{}

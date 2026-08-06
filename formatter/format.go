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
	// FieldSeparator controls trailing separators after
	// struct/union/exception fields and enum values (default
	// SeparatorPreserve).
	FieldSeparator SeparatorMode
	// FunctionSeparator controls trailing separators after service
	// arguments and throws entries (default SeparatorPreserve).
	FunctionSeparator SeparatorMode
	// BreakStructs forces struct, union, and exception bodies to the
	// multiline layout, even when they fit on one line.
	BreakStructs bool
	// BreakEnums forces enum bodies to the multiline layout, even when
	// they fit on one line.
	BreakEnums bool
	// NoTrailingNewline suppresses the final newline that is otherwise
	// appended to the formatted output.
	NoTrailingNewline bool
}

// DefaultOptions returns the default formatting options.
func DefaultOptions() Options {
	return Options{
		PrintWidth:     80,
		Indent:         "    ",
		TabWidth:       4,
		Align:          AlignField,
		FieldSeparator: SeparatorPreserve,
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

	f := &formatter{
		doc:  d,
		toks: d.Tokens,
		opts: o,
	}
	ir := f.document()
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

func (f *formatter) token(i int) syntax.Token {
	return f.toks[i]
}

// blankBefore returns the number of blank lines before the node's first
// token.
func (f *formatter) blankBefore(n syntax.Node) int {
	return f.token(n.TokStart()).BlankLinesBefore
}

// leadingComments returns the comments attached before the node's first
// token, as docs that each end with a hard line.
func (f *formatter) leadingComments(n syntax.Node) []doc.Doc {
	var parts []doc.Doc
	for _, c := range f.token(n.TokStart()).Leading {
		parts = append(parts, doc.Text(c.Text), doc.HardLine)
	}
	return parts
}

// trailingComments returns the comments attached after the node's last token
// on the same line, as line-suffix docs. The break parent forces the
// enclosing group to break so the comment stays on the node's own line.
func (f *formatter) trailingComments(n syntax.Node) []doc.Doc {
	var parts []doc.Doc
	for _, c := range f.token(n.TokEnd()).Trailing {
		parts = append(parts, doc.LineSuffix(doc.Text(" "+c.Text)), doc.BreakParent)
	}
	return parts
}

// node assembles a top-level node: its leading comments, its formatted
// body, and its trailing comments.
func (f *formatter) node(n syntax.Node) doc.Doc {
	parts := append(f.leadingComments(n), f.nodeBody(n))
	parts = append(parts, f.trailingComments(n)...)
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
	var parts []doc.Doc
	for i, n := range f.doc.Nodes {
		if i > 0 {
			parts = append(parts, doc.HardLine)
			if f.blankBefore(n) >= 1 {
				parts = append(parts, doc.HardLine)
			}
		}
		parts = append(parts, f.node(n))
	}

	// Comments at the end of the file attach to the EOF token.
	eof := f.toks[len(f.toks)-1]
	if len(eof.Leading) > 0 {
		if len(f.doc.Nodes) > 0 {
			parts = append(parts, doc.HardLine)
			if eof.BlankLinesBefore >= 1 {
				parts = append(parts, doc.HardLine)
			}
		}
		for i, c := range eof.Leading {
			parts = append(parts, doc.Text(c.Text))
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
	return doc.Concat{doc.Text("include "), doc.Text(v.Path.Text)}
}

func (f *formatter) cppInclude(v *syntax.CPPInclude) doc.Doc {
	return doc.Concat{doc.Text("cpp_include "), doc.Text(v.Path.Text)}
}

func (f *formatter) namespace(v *syntax.Namespace) doc.Doc {
	parts := []doc.Doc{
		doc.Text("namespace "),
		doc.Text(v.Scope.Text),
		doc.Text(" "),
		doc.Text(v.Name.Text),
	}
	parts = append(parts, f.annotationsDoc(v.Annotations))
	return doc.Concat(parts)
}

func (f *formatter) typedef(v *syntax.Typedef) doc.Doc {
	parts := []doc.Doc{
		doc.Text("typedef "),
		f.fieldType(v.Type),
		doc.Text(" "),
		doc.Text(v.Name.Text),
	}
	parts = append(parts, f.annotationsDoc(v.Annotations))
	return doc.Concat(parts)
}

func (f *formatter) constant(v *syntax.Const) doc.Doc {
	return doc.Concat{
		doc.Text("const "),
		f.fieldType(v.Type),
		doc.Text(" "),
		doc.Text(v.Name.Text),
		doc.Text(" = "),
		f.constValue(v.Value),
	}
}

// --- annotations -----------------------------------------------------------

// annotationsDoc returns an annotation group, or an empty doc when absent.
func (f *formatter) annotationsDoc(a *syntax.Annotations) doc.Doc {
	if a == nil {
		return doc.Concat{}
	}
	items := make([]doc.Doc, 0, len(a.Items))
	for _, item := range a.Items {
		var parts []doc.Doc
		parts = append(parts, doc.Text(item.Name.Text))
		if item.Value != nil {
			parts = append(parts, doc.Text(" = "), doc.Text(item.Value.Text))
		}
		items = append(items, doc.Concat(parts))
	}
	group := doc.Group(doc.Concat{
		doc.Text("("),
		doc.Indent(doc.Concat{doc.SoftLine, doc.Join(doc.Concat{doc.Text(","), doc.Line}, items)}),
		doc.SoftLine,
		doc.Text(")"),
	})
	return doc.Concat{doc.Text(" "), group}
}

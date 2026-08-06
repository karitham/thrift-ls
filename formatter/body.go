package formatter

import (
	"github.com/karitham/thrift-ls/doc"
	"github.com/karitham/thrift-ls/syntax"
)

// structLike formats struct, union, and exception declarations.
func (f *formatter) structLike(v *syntax.Struct) doc.Doc {
	parts := []doc.Doc{
		doc.Text(v.Kind.String()),
		doc.Text(" "),
		doc.Text(v.Name.Text),
		f.bracedBody(v.Fields),
	}
	parts = append(parts, f.annotationsDoc(v.Annotations))
	return doc.Concat(parts)
}

// enum formats an enum declaration.
func (f *formatter) enum(v *syntax.Enum) doc.Doc {
	parts := []doc.Doc{
		doc.Text("enum "),
		doc.Text(v.Name.Text),
		f.bracedEnumBody(v.Values),
	}
	parts = append(parts, f.annotationsDoc(v.Annotations))
	return doc.Concat(parts)
}

// bracedBody formats "{ fields }": flat as "S { 1: i32 a }" when it fits,
// otherwise one field per line. Field alignment and trailing separators
// switch on the body group breaking.
func (f *formatter) bracedBody(fields []*syntax.Field) doc.Doc {
	if len(fields) == 0 {
		return doc.Text(" {}")
	}
	bodyID := f.id()
	inner := append([]doc.Doc{doc.Line, f.fieldList(fields, bodyID)}, f.closingTrivia(fields[len(fields)-1])...)
	open := append([]doc.Doc{doc.Text(" {")}, f.openTrivia(fields[0])...)
	content := doc.Concat{
		doc.Concat(open),
		doc.Indent(doc.Concat(inner)),
		doc.IfBreak(doc.SoftLine, doc.Text(" ")),
		doc.Text("}"),
	}
	if f.opts.BreakStructs {
		// BreakParent inside the group forces it to the broken layout.
		content = doc.Concat{doc.BreakParent, content}
	}
	return doc.GroupID(bodyID, content)
}

// bracedEnumBody is bracedBody for enum values.
func (f *formatter) bracedEnumBody(values []*syntax.EnumValue) doc.Doc {
	if len(values) == 0 {
		return doc.Text(" {}")
	}
	bodyID := f.id()
	inner := append([]doc.Doc{doc.Line, f.enumValueList(values, bodyID)}, f.closingTrivia(values[len(values)-1])...)
	open := append([]doc.Doc{doc.Text(" {")}, f.openTrivia(values[0])...)
	content := doc.Concat{
		doc.Concat(open),
		doc.Indent(doc.Concat(inner)),
		doc.IfBreak(doc.SoftLine, doc.Text(" ")),
		doc.Text("}"),
	}
	if f.opts.BreakEnums {
		content = doc.Concat{doc.BreakParent, content}
	}
	return doc.GroupID(bodyID, content)
}

// closingTrivia returns the comments attached to the token that closes a
// body (the one right after the last item), as docs each ending with a hard
// line. The leading hard line forces the body to break so the comments stay
// inside; the caller's closing line provides the newline after the last one.
func (f *formatter) closingTrivia(last syntax.Node) []doc.Doc {
	close := f.token(last.TokEnd() + 1)
	var parts []doc.Doc
	if len(close.Leading) > 0 {
		parts = append(parts, doc.HardLine)
		for i, c := range close.Leading {
			parts = append(parts, doc.Text(c.Text))
			if i < len(close.Leading)-1 {
				parts = append(parts, doc.HardLine)
			}
		}
	}
	return parts
}

// openTrivia returns the trailing comments of the token that opens a body
// (the one right before the first item), as line-suffix docs, plus a break
// parent so the body goes multiline. Empty when there are none.
func (f *formatter) openTrivia(first syntax.Node) []doc.Doc {
	open := f.token(first.TokStart() - 1)
	var parts []doc.Doc
	if len(open.Trailing) > 0 {
		for _, c := range open.Trailing {
			parts = append(parts, doc.LineSuffix(doc.Text(" "+c.Text)))
		}
		parts = append(parts, doc.BreakParent)
	}
	return parts
}

// service formats a service declaration. The body is always multiline:
// functions are too complex to flatten.
func (f *formatter) service(v *syntax.Service) doc.Doc {
	var parts []doc.Doc
	for i, fn := range v.Functions {
		if i > 0 {
			parts = append(parts, doc.HardLineNoBreak)
			parts = append(parts, f.blankLines(fn, doc.HardLineNoBreak)...)
		}
		parts = append(parts, f.function(fn))
	}

	body := doc.Concat{
		doc.Text(" {"),
		doc.Indent(doc.Concat{doc.HardLineNoBreak, doc.Concat(parts)}),
		doc.HardLineNoBreak,
		doc.Text("}"),
	}

	out := []doc.Doc{
		doc.Text("service "),
		doc.Text(v.Name.Text),
	}
	if v.Extends != nil {
		out = append(out, doc.Text(" extends "), doc.Text(v.Extends.Text))
	}
	out = append(out, body)
	out = append(out, f.annotationsDoc(v.Annotations))
	return doc.Concat(out)
}

// function formats a service method with the signature escalation:
//
//  1. the whole signature on one line, when it fits;
//  2. arguments on one line, the throws clause broken;
//  3. arguments and throws clause both broken.
//
// Each state is tried in order by the conditional group, so the escalation
// is decided by the remaining width at the function's position.
func (f *formatter) function(v *syntax.Function) doc.Doc {
	parts := append(f.leadingComments(v), f.functionBody(v))
	parts = append(parts, f.trailingComments(v)...)
	return doc.Concat(parts)
}

func (f *formatter) functionBody(v *syntax.Function) doc.Doc {
	// Comments or blank lines inside the argument or throws lists force the
	// multiline layout: the flat states would drop them.
	if f.fieldsForcedBroken(v.Args) || v.Throws != nil && f.fieldsForcedBroken(v.Throws.Fields) {
		return f.functionBrokenArgs(v)
	}

	if v.Throws == nil {
		return doc.ConditionalGroup(0,
			f.functionFlat(v, false),
			f.functionBrokenArgs(v),
		)
	}
	return doc.ConditionalGroup(0,
		f.functionFlat(v, false),
		f.functionFlat(v, true),
		f.functionBrokenArgs(v),
	)
}

// functionHeader renders "[oneway] <type|void> <name>".
func (f *formatter) functionHeader(v *syntax.Function) string {
	out := ""
	if v.Oneway != nil {
		out += "oneway "
	}
	if v.Void != nil {
		out += "void "
	} else {
		out += typeText(v.Type) + " "
	}
	return out + v.Name.Text
}

// functionFlat renders the signature with args flat and throws either flat
// or broken. The states are printed flat (or measured flat), so line docs
// render as spaces; throwsBroken inserts hard lines to break the clause.
func (f *formatter) functionFlat(v *syntax.Function, throwsBroken bool) doc.Doc {
	args := f.flatFieldsJoin(v.Args)
	parts := []doc.Doc{
		doc.Text(f.functionHeader(v)),
		doc.Text("("),
		args,
		doc.Text(")"),
	}
	if v.Throws != nil {
		if throwsBroken {
			parts = append(parts, doc.Text(" throws "), f.brokenParens("(", ")", v.Throws.Fields))
		} else {
			parts = append(parts,
				doc.Text(" throws ("),
				f.flatFieldsJoin(v.Throws.Fields),
				doc.Text(")"),
			)
		}
	}
	parts = append(parts, f.annotationsDoc(v.Annotations))
	return doc.Concat(parts)
}

// flatFieldsJoin joins fields with their separators on one line: each
// field's own separator when preserving, or a single forced separator per
// the FunctionSeparator mode.
func (f *formatter) flatFieldsJoin(fields []*syntax.Field) doc.Doc {
	parts := make([]doc.Doc, 0, len(fields))
	for i, field := range fields {
		if i > 0 {
			parts = append(parts, doc.Concat{
				doc.Text(sepText(fields[i-1].Sep, f.opts.FunctionSeparator)),
				doc.Line,
			})
		}
		parts = append(parts, f.fieldContent(field, nil, false))
	}
	return doc.Concat(parts)
}

// functionBrokenArgs renders the signature with arguments and throws both
// broken, one per line.
func (f *formatter) functionBrokenArgs(v *syntax.Function) doc.Doc {
	parts := []doc.Doc{
		doc.Text(f.functionHeader(v)),
		f.brokenParens("(", ")", v.Args),
	}
	if v.Throws != nil {
		parts = append(parts, doc.Text(" throws "), f.brokenParens("(", ")", v.Throws.Fields))
	}
	parts = append(parts, f.annotationsDoc(v.Annotations))
	return doc.Concat(parts)
}

// brokenFields renders fields one per line, each with its trailing
// separator per the FieldSeparator option. Comments and blank lines inside
// the list are preserved.
func (f *formatter) brokenFields(fields []*syntax.Field) doc.Doc {
	var parts []doc.Doc
	for i, field := range fields {
		if i > 0 {
			parts = append(parts, doc.HardLineNoBreak)
			parts = append(parts, f.blankLines(field, doc.HardLineNoBreak)...)
		}
		content := doc.Concat{
			f.fieldContent(field, f.alignmentFor(fields, i), true),
			trailingSep(field.Sep, f.opts.FunctionSeparator),
		}
		fieldDoc := append(f.leadingComments(field), content)
		fieldDoc = append(fieldDoc, f.trailingComments(field)...)
		parts = append(parts, doc.Concat(fieldDoc))
	}
	return doc.Concat(parts)
}

// brokenParens renders "open, fields, close" one field per line, or just
// "openclose" when there are no fields.
func (f *formatter) brokenParens(open, close string, fields []*syntax.Field) doc.Doc {
	if len(fields) == 0 {
		return doc.Text(open + close)
	}
	inner := append([]doc.Doc{doc.HardLineNoBreak, f.brokenFields(fields)}, f.closingTrivia(fields[len(fields)-1])...)
	parts := append([]doc.Doc{doc.Text(open)}, f.openTrivia(fields[0])...)
	parts = append(parts, doc.Indent(doc.Concat(inner)), doc.HardLineNoBreak, doc.Text(close))
	return doc.Concat(parts)
}

// fieldsForcedBroken reports whether any field has comments or blank lines
// that require the multiline function layout.
func (f *formatter) fieldsForcedBroken(fields []*syntax.Field) bool {
	if len(fields) == 0 {
		return false
	}
	// Comments on the opening or closing paren would be lost in the flat
	// layout.
	if len(f.token(fields[0].TokStart()-1).Trailing) > 0 {
		return true
	}
	close := f.token(fields[len(fields)-1].TokEnd() + 1)
	if len(close.Leading) > 0 {
		return true
	}
	for _, field := range fields {
		if f.blankBefore(field) >= 1 {
			return true
		}
		if len(f.token(field.TokStart()).Leading) > 0 || len(f.token(field.TokEnd()).Trailing) > 0 {
			return true
		}
	}
	return false
}

// blankLines returns count hard-line docs for the blank lines before a
// node's first token.
func (f *formatter) blankLines(n syntax.Node, line doc.Doc) []doc.Doc {
	return f.blankLineDocs(f.blankBefore(n), line)
}

// blankLineDocs returns count copies of line, for blank lines that must be
// preserved exactly.
func (f *formatter) blankLineDocs(count int, line doc.Doc) []doc.Doc {
	parts := make([]doc.Doc, 0, count)
	for i := 0; i < count; i++ {
		parts = append(parts, line)
	}
	return parts
}

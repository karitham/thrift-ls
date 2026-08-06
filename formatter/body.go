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
	return doc.GroupID(bodyID, doc.Concat{
		doc.Text(" {"),
		doc.Indent(doc.Concat{doc.Line, f.fieldList(fields, bodyID)}),
		doc.IfBreak(doc.SoftLine, doc.Text(" ")),
		doc.Text("}"),
	})
}

// bracedEnumBody is bracedBody for enum values.
func (f *formatter) bracedEnumBody(values []*syntax.EnumValue) doc.Doc {
	if len(values) == 0 {
		return doc.Text(" {}")
	}
	bodyID := f.id()
	return doc.GroupID(bodyID, doc.Concat{
		doc.Text(" {"),
		doc.Indent(doc.Concat{doc.Line, f.enumValueList(values, bodyID)}),
		doc.IfBreak(doc.SoftLine, doc.Text(" ")),
		doc.Text("}"),
	})
}

// service formats a service declaration. The body is always multiline:
// functions are too complex to flatten.
func (f *formatter) service(v *syntax.Service) doc.Doc {
	var parts []doc.Doc
	for i, fn := range v.Functions {
		if i > 0 {
			parts = append(parts, doc.HardLineNoBreak)
			if f.blankBefore(fn) >= 1 {
				parts = append(parts, doc.HardLineNoBreak)
			}
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
	parts := []doc.Doc{
		doc.Text(f.functionHeader(v)),
		doc.Text("("),
		doc.Join(doc.Text(", "), f.flatFields(v.Args)),
		doc.Text(")"),
	}
	if v.Throws != nil {
		if throwsBroken {
			parts = append(parts, doc.Text(" throws "), f.brokenParens("(", ")", v.Throws.Fields))
		} else {
			parts = append(parts,
				doc.Text(" throws ("),
				doc.Join(doc.Text(", "), f.flatFields(v.Throws.Fields)),
				doc.Text(")"),
			)
		}
	}
	parts = append(parts, f.annotationsDoc(v.Annotations))
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

// flatFields renders fields joined by ", " on one line, without comments.
func (f *formatter) flatFields(fields []*syntax.Field) []doc.Doc {
	out := make([]doc.Doc, 0, len(fields))
	for _, field := range fields {
		out = append(out, f.fieldContent(field, nil, false))
	}
	return out
}

// brokenFields renders fields one per line, each with its trailing
// separator per the FieldLineComma option. Comments and blank lines inside
// the list are preserved.
func (f *formatter) brokenFields(fields []*syntax.Field) doc.Doc {
	var parts []doc.Doc
	for i, field := range fields {
		if i > 0 {
			parts = append(parts, doc.HardLineNoBreak)
			if f.blankBefore(field) >= 1 {
				parts = append(parts, doc.HardLineNoBreak)
			}
		}
		content := doc.Concat{f.fieldContent(field, nil, false), f.trailingSep(field.Sep)}
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
	return doc.Concat{
		doc.Text(open),
		doc.Indent(doc.Concat{doc.HardLineNoBreak, f.brokenFields(fields)}),
		doc.HardLineNoBreak,
		doc.Text(close),
	}
}

// fieldsForcedBroken reports whether any field has comments or blank lines
// that require the multiline function layout.
func (f *formatter) fieldsForcedBroken(fields []*syntax.Field) bool {
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

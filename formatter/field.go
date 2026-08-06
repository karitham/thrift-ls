package formatter

import (
	"strings"

	"github.com/karitham/thrift-ls/doc"
	"github.com/karitham/thrift-ls/syntax"
)

// fieldList formats struct-like body fields. Fields are joined with their
// separator when the body stays on one line and with newlines otherwise; in
// break mode each field's own trailing separator is emitted, driven by the
// FieldLineComma option. Blank lines between fields are preserved and force
// the body to break. Column alignment applies per blank-line group,
// matching the previous formatter.
func (f *formatter) fieldList(fields []*syntax.Field, bodyID int) doc.Doc {
	var parts []doc.Doc
	for i, field := range fields {
		if i > 0 {
			parts = append(parts, fieldSep(fields[i-1].Sep, f.opts.FieldLineComma))
			if f.blankBefore(field) >= 1 {
				parts = append(parts, doc.HardLine)
			}
		}
		parts = append(parts, f.field(field, f.alignmentFor(fields, i), bodyID))
	}
	return doc.Concat(parts)
}

// fieldSep is the separator between two list items: a newline when the
// enclosing group breaks, otherwise the separator text — per-field when
// preserving (each item keeps its own trailing separator), or a single
// forced separator per the comma mode.
func fieldSep(prevSep syntax.TokenKind, mode CommaMode) doc.Doc {
	return doc.IfBreak(doc.Line, doc.Concat{doc.Text(sepText(prevSep, mode)), doc.Line})
}

// sepText is the separator text between two flat list items: each item's
// own trailing separator when preserving, or a forced separator per the
// comma mode.
func sepText(prevSep syntax.TokenKind, mode CommaMode) string {
	switch mode {
	case CommaAdd:
		return ","
	case CommaSemicolon:
		return ";"
	case CommaRemove:
		return ""
	}
	switch prevSep {
	case syntax.TokenComma:
		return ","
	case syntax.TokenSemicolon:
		return ";"
	}
	return ""
}

// enumValueList formats enum bodies with the same layout as fieldList.
func (f *formatter) enumValueList(values []*syntax.EnumValue, bodyID int) doc.Doc {
	var parts []doc.Doc
	for i, value := range values {
		if i > 0 {
			parts = append(parts, fieldSep(values[i-1].Sep, f.opts.FieldLineComma))
			if f.blankBefore(value) >= 1 {
				parts = append(parts, doc.HardLine)
			}
		}
		parts = append(parts, f.enumValue(value, f.alignmentForEnum(values, i), bodyID))
	}
	return doc.Concat(parts)
}

// alignmentFor returns the column alignment for field i, or nil when
// alignment is disabled. Alignment is scoped to the blank-line group the
// field belongs to.
func (f *formatter) alignmentFor(fields []*syntax.Field, i int) *columnAlign {
	if f.opts.Align == AlignDisable {
		return nil
	}
	start := i
	for start > 0 && f.blankBefore(fields[start]) < 1 {
		start--
	}
	end := i
	for end+1 < len(fields) && f.blankBefore(fields[end+1]) < 1 {
		end++
	}
	return computeFieldAlign(fields[start : end+1])
}

func (f *formatter) alignmentForEnum(values []*syntax.EnumValue, i int) *columnAlign {
	if f.opts.Align == AlignDisable {
		return nil
	}
	start := i
	for start > 0 && f.blankBefore(values[start]) < 1 {
		start--
	}
	end := i
	for end+1 < len(values) && f.blankBefore(values[end+1]) < 1 {
		end++
	}
	return computeEnumAlign(values[start : end+1])
}

// columnAlign holds the computed column widths for one aligned group.
type columnAlign struct {
	hasReq     bool
	idWidth    int // width of the "N:" prefix column
	reqWidth   int // width of the required/optional column
	typeWidth  int // width of the type column
	nameWidth  int // for AlignAssign: max name width among fields with values
	enumAssign bool
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}

// computeFieldAlign computes column widths for a group of fields.
func computeFieldAlign(fields []*syntax.Field) *columnAlign {
	a := &columnAlign{}
	for _, field := range fields {
		if field.FieldID != nil {
			a.idWidth = maxInt(a.idWidth, len(field.FieldID.Text)+1) // "N:"
		}
		if field.Req != 0 {
			a.hasReq = true
			a.reqWidth = maxInt(a.reqWidth, len(field.Req.String()))
		}
		a.typeWidth = maxInt(a.typeWidth, len(typeText(field.Type)))
		if field.Value != nil {
			a.nameWidth = maxInt(a.nameWidth, len(field.Name.Text))
		}
	}
	return a
}

// computeEnumAlign computes the name width for aligning '=' signs.
func computeEnumAlign(values []*syntax.EnumValue) *columnAlign {
	a := &columnAlign{enumAssign: true}
	for _, value := range values {
		if value.Value != nil {
			a.nameWidth = maxInt(a.nameWidth, len(value.Name.Text))
		}
	}
	return a
}

// field assembles a struct-like body field: leading comments, content, and
// trailing comments. When the body breaks (referenced by bodyID), the
// content switches to its column-aligned form with the trailing separator.
func (f *formatter) field(v *syntax.Field, align *columnAlign, bodyID int) doc.Doc {
	content := f.fieldContent(v, align, false)
	if bodyID != 0 {
		content = doc.IfBreakFor(
			doc.Concat{f.fieldContent(v, align, true), trailingSep(v.Sep, f.opts.FieldLineComma)},
			content,
			bodyID,
		)
	}
	parts := append(f.leadingComments(v), content)
	parts = append(parts, f.trailingComments(v)...)
	return doc.Concat(parts)
}

// fieldContent renders the field line: id, requiredness, type, reference,
// name, default value, and annotations. padded selects the column-aligned
// form used when the enclosing body breaks; it has no effect when align is
// nil.
func (f *formatter) fieldContent(v *syntax.Field, align *columnAlign, padded bool) doc.Doc {
	padded = padded && align != nil
	var parts []doc.Doc

	if v.FieldID != nil {
		id := v.FieldID.Text + ":"
		if padded {
			id = padRight(id, align.idWidth)
		}
		parts = append(parts, doc.Text(id), doc.Text(" "))
	}

	if v.Req != 0 || padded && align.hasReq {
		req := ""
		if v.Req != 0 {
			req = v.Req.String()
		}
		if padded {
			req = padRight(req, align.reqWidth)
		}
		parts = append(parts, doc.Text(req), doc.Text(" "))
	}

	typ := doc.Text(typeText(v.Type))
	if padded && f.opts.Align == AlignField {
		typ = doc.Text(padRight(typeText(v.Type), align.typeWidth))
	}
	parts = append(parts, typ, f.annotationsDoc(v.Type.Annotations), doc.Text(" "))

	if v.Reference {
		parts = append(parts, doc.Text("&"))
	}

	name := doc.Text(v.Name.Text)
	if padded && v.Value != nil && align.nameWidth > 0 {
		// Fields with values have their name padded so the "=" signs
		// align, in both align modes.
		name = doc.Text(padRight(v.Name.Text, align.nameWidth))
	}
	parts = append(parts, name)

	if v.Value != nil {
		parts = append(parts, doc.Text(" = "), f.constValue(v.Value))
	}

	parts = append(parts, f.annotationsDoc(v.Annotations))
	return doc.Concat(parts)
}

// enumValue assembles an enum value with comments, aligning '=' signs when
// the body breaks.
func (f *formatter) enumValue(v *syntax.EnumValue, align *columnAlign, bodyID int) doc.Doc {
	content := f.enumValueContent(v, align, false)
	if bodyID != 0 {
		content = doc.IfBreakFor(
			doc.Concat{f.enumValueContent(v, align, true), trailingSep(v.Sep, f.opts.FieldLineComma)},
			content,
			bodyID,
		)
	}
	parts := append(f.leadingComments(v), content)
	parts = append(parts, f.trailingComments(v)...)
	return doc.Concat(parts)
}

func (f *formatter) enumValueContent(v *syntax.EnumValue, align *columnAlign, padded bool) doc.Doc {
	padded = padded && align != nil

	name := doc.Text(v.Name.Text)
	if padded && align.enumAssign {
		name = doc.Text(padRight(v.Name.Text, align.nameWidth))
	}
	parts := []doc.Doc{name}

	if v.Value != nil {
		parts = append(parts, doc.Text(" = "), doc.Text(v.Value.Text))
	}

	parts = append(parts, f.annotationsDoc(v.Annotations))
	return doc.Concat(parts)
}

// trailingSep returns the trailing separator for the given original
// separator, per the comma mode: always a comma or semicolon when forcing,
// nothing when removing, the original separator when preserving.
func trailingSep(sep syntax.TokenKind, mode CommaMode) doc.Doc {
	switch mode {
	case CommaAdd:
		return doc.Text(",")
	case CommaSemicolon:
		return doc.Text(";")
	case CommaRemove:
		return doc.Concat{}
	default:
		switch sep {
		case syntax.TokenComma:
			return doc.Text(",")
		case syntax.TokenSemicolon:
			return doc.Text(";")
		}
		return doc.Concat{}
	}
}

// padRight pads s with trailing spaces to width w.
func padRight(s string, w int) string {
	if n := w - len(s); n > 0 {
		return s + strings.Repeat(" ", n)
	}
	return s
}

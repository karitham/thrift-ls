package formatter

import (
	"strings"

	"github.com/karitham/thrift-ls/doc"
	"github.com/karitham/thrift-ls/syntax"
)

// fieldList formats struct-like body fields. Fields are joined with their
// separator when the body stays on one line and with newlines otherwise; in
// break mode each field's own trailing separator is emitted, driven by the
// sepMode option. Blank lines between fields are preserved and force
// the body to break. Column alignment applies per blank-line group,
// matching the previous formatter.
func (f *formatter) fieldList(fields []*syntax.Field, bodyID int, sepMode SeparatorMode) doc.Doc {
	var parts []doc.Doc

	for i, field := range fields {
		if i > 0 {
			parts = append(parts, fieldSep(fields[i-1].Sep, sepMode))
			parts = append(parts, f.blankLines(field, doc.HardLine)...)
		} else {
			parts = append(parts, f.blankLines(field, doc.HardLine)...)
		}

		parts = append(parts, f.field(field, f.alignmentFor(fields, i, sepMode), bodyID, sepMode))
	}

	return doc.Concat(parts)
}

// fieldSep is the separator between two list items: a newline when the
// enclosing group breaks, otherwise the separator text — per-field when
// preserving (each item keeps its own trailing separator), or a single
// forced separator per the comma mode.
func fieldSep(prevSep syntax.TokenKind, mode SeparatorMode) doc.Doc {
	return doc.IfBreak(doc.Line, doc.Concat{doc.Text(sepText(prevSep, mode)), doc.Line})
}

// sepText is the separator text between two flat list items: each item's
// own trailing separator when preserving, or a forced separator per the
// comma mode.
func sepText(prevSep syntax.TokenKind, mode SeparatorMode) string {
	switch mode {
	case SeparatorComma:
		return ","
	case SeparatorSemicolon:
		return ";"
	case SeparatorNone:
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
			parts = append(parts, fieldSep(values[i-1].Sep, f.opts.Separator.Get(ConstructEnum)))
			parts = append(parts, f.blankLines(value, doc.HardLine)...)
		} else {
			parts = append(parts, f.blankLines(value, doc.HardLine)...)
		}

		parts = append(parts, f.enumValue(value, f.alignmentForEnum(values, i, f.opts.Separator.Get(ConstructEnum)), bodyID))
	}

	return doc.Concat(parts)
}

// groupedWith reports whether the node joins the alignment group of the
// item before it: no blank line and no comment sits between them. A
// comment in the gap is a visual break, like whitespace. A comment
// trailing the previous item's separator only breaks the group when the
// separator is suppressed: the comment then moves to its own line and
// re-attaches, so the group must not depend on it.
func (f *formatter) groupedWith(prev, cur syntax.Node, sepMode SeparatorMode) bool {
	// A comment trailing the previous item's separator only breaks the
	// group when the separator is suppressed and the comment moves to its
	// own line (re-attaching): the group must not depend on it. Comments
	// that stay on the separator's line do not break the group.
	prevTok := f.token(prev.TokEnd())
	sepBreak := isListSep(prevTok.Kind) && !sepEmits(prevTok.Kind, sepMode) &&
		(leadingLineComment(prevTok) || len(prevTok.Trailing) > 0 && f.lineAfter(prev.TokEnd()-1))

	return f.blankBefore(cur) < 1 &&
		len(f.token(cur.TokStart()).Leading) == 0 &&
		!sepBreak
}

// alignmentFor returns the column alignment for field i, or nil when
// alignment is disabled. Alignment is scoped to the blank-line and comment
// group the field belongs to; sepMode is the separator mode the enclosing
// list uses, for the width gate.
func (f *formatter) alignmentFor(fields []*syntax.Field, i int, sepMode SeparatorMode) *columnAlign {
	if f.opts.Align == AlignDisable {
		return nil
	}

	start := i
	for start > 0 && f.groupedWith(fields[start-1], fields[start], sepMode) {
		start--
	}

	end := i
	for end+1 < len(fields) && f.groupedWith(fields[end], fields[end+1], sepMode) {
		end++
	}

	group := fields[start : end+1]

	a := computeFieldAlign(group)
	if !f.alignmentFits(group, a, sepMode) && !f.sourceAligned(fields, start, end) {
		return nil
	}

	return a
}

// alignmentFits reports whether column alignment keeps the structural
// content of every line within printWidth: the padded columns plus, per
// field, its name and the trailing separator the given mode emits.
// Trailing comments are excluded — a comment may overflow its line without
// affecting alignment. Default values and annotations are ignored; a line
// that long overflows regardless of alignment.
func (f *formatter) alignmentFits(fields []*syntax.Field, a *columnAlign, sepMode SeparatorMode) bool {
	limit := f.opts.PrintWidth - f.opts.TabWidth

	columns := a.idWidth + 1
	if a.hasReq {
		columns += a.reqWidth + 1
	}

	if f.opts.Align == AlignField {
		columns += a.typeWidth + 1
	}

	longest := 0

	for _, field := range fields {
		w := len(field.Name.Text) + sepLen(field.Sep, sepMode)
		longest = maxInt(longest, w)
	}

	return columns+longest <= limit
}

// sepLen is the length of the trailing separator the mode emits after a
// field with the given source separator.
func sepLen(sep syntax.TokenKind, mode SeparatorMode) int {
	switch mode {
	case SeparatorComma, SeparatorSemicolon:
		return 1
	case SeparatorNone:
		return 0
	}

	if sep != 0 {
		return 1
	}

	return 0
}

// sourceAligned reports whether the group's field names start at the same
// column in the source, with at least one field padded beyond a single
// space after its type — a deliberately aligned layout, preserved even
// when it overflows printWidth. Accidental alignment (natural columns
// coinciding) does not count.
func (f *formatter) sourceAligned(fields []*syntax.Field, start, end int) bool {
	col := 0
	padded := false

	for i := start; i <= end; i++ {
		field := fields[i]

		c := f.token(field.Name.TokStart()).Col
		if col == 0 {
			col = c
		} else if c != col {
			return false
		}

		if f.namePadded(field) {
			padded = true
		}
	}

	return padded
}

// namePadded reports whether more than one space separates the field name
// from its type in the source; a reference field (&) occupies one extra
// column.
func (f *formatter) namePadded(field *syntax.Field) bool {
	prev := field.Name.TokStart() - 1
	if prev < 0 {
		return false
	}

	extra := 0
	if f.token(prev).Kind == syntax.TokenAmp {
		extra = 1 // the & reference

		prev--
		if prev < 0 {
			return false
		}
	}

	name := f.token(field.Name.TokStart())
	tok := f.token(prev)
	// A single canonical space puts the name two columns past the type's
	// end (the space at end+1); a reference adds one for the '&'.
	return name.Col-(tok.Col+len(tok.Text)-1) > 2+extra
}

func (f *formatter) alignmentForEnum(values []*syntax.EnumValue, i int, sepMode SeparatorMode) *columnAlign {
	if f.opts.Align == AlignDisable {
		return nil
	}

	start := i
	for start > 0 && f.groupedWith(values[start-1], values[start], sepMode) {
		start--
	}

	end := i
	for end+1 < len(values) && f.groupedWith(values[end], values[end+1], sepMode) {
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
func (f *formatter) field(v *syntax.Field, align *columnAlign, bodyID int, sepMode SeparatorMode) doc.Doc {
	content := f.fieldContent(v, align, false, sepMode)
	if bodyID != 0 {
		content = doc.IfBreakFor(
			doc.Concat{f.fieldContent(v, align, true, sepMode), trailingSep(v.Sep, sepMode)},
			content,
			bodyID,
		)
	}

	parts := append(f.leadingComments(v), content)
	parts = append(parts, f.trailingComments(v, sepEmits(v.Sep, sepMode))...)

	return doc.Concat(parts)
}

// emitWithAnnotations renders a token run split at the node's annotations,
// so they keep their foldable group instead of being inlined.
func (f *formatter) emitWithAnnotations(start, end int, ann *syntax.Annotations, o emitOpts) doc.Doc {
	if ann == nil {
		return f.emitTokens(start, end, o)
	}
	// The segment before the annotations owns its last token's trailing
	// trivia (the annotations' close owns the node's trailing instead).
	first := o
	first.trailing = true

	parts := []doc.Doc{f.emitTokens(start, ann.TokStart()-1, first)}
	if f.lineAfter(ann.TokStart()-1) || len(f.token(ann.TokStart()).Leading) > 0 {
		parts = append(parts, doc.HardLine)
	}

	parts = append(parts, f.annotationsDoc(ann, ann.TokEnd() == end))
	if ann.TokEnd() < end {
		parts = append(parts, f.emitTokens(ann.TokEnd()+1, end, o))
	}

	return doc.Concat(parts)
}

// fieldContent renders the field as a token run. padded selects the
// column-aligned form used when the enclosing body breaks; it has no
// effect when align is nil. The separator token's text is suppressed (the
// caller emits it), but its trivia is preserved.
func (f *formatter) fieldContent(v *syntax.Field, align *columnAlign, padded bool, sepMode SeparatorMode) doc.Doc {
	padded = padded && align != nil

	o := emitOpts{breakTrailing: true}
	if v.Sep != 0 {
		o.skipText = map[int]bool{v.TokEnd(): true}
		o.breakSkip = sepEmits(v.Sep, sepMode)
	}

	if padded {
		o.pads, o.prefix = f.fieldPads(v, align)
	}

	return f.emitWithAnnotations(v.TokStart(), v.TokEnd(), v.Annotations, o)
}

// fieldPads returns the alignment padding after each column token, or nil
// when the field carries comments that make the padded widths unknowable.
// The prefix is the leading padding of a field without an id whose
// requiredness column is empty.
func (f *formatter) fieldPads(v *syntax.Field, a *columnAlign) (map[int]string, string) {
	start, end := v.TokStart(), v.TokEnd()
	if v.Sep != 0 {
		end--
	}

	for i := start + 1; i <= end; i++ {
		if len(f.token(i).Leading) > 0 {
			return nil, ""
		}
	}

	for i := start; i < end; i++ {
		if len(f.token(i).Trailing) > 0 {
			return nil, ""
		}
	}

	pads := map[int]string{}
	if v.FieldID != nil {
		pads[v.TokStart()+1] = padRight("", a.idWidth-len(v.FieldID.Text)-1)
	}

	if v.Req != 0 {
		pads[v.Type.TokStart()-1] = padRight("", a.reqWidth-len(v.Req.String()))
	} else if a.hasReq {
		// The empty requiredness column: extend the id pad by one column
		// plus the missing req width, or lead the field with it when there
		// is no id token to pad after.
		if v.FieldID != nil {
			pads[v.TokStart()+1] += padRight("", a.reqWidth+1)
		} else {
			return nil, padRight("", a.reqWidth+1)
		}
	}

	if f.opts.Align == AlignField {
		end := v.Type.TokEnd()
		if v.Type.Annotations != nil {
			end = v.Type.Annotations.TokStart() - 1
		}

		pads[end] = padRight("", a.typeWidth-len(typeText(v.Type)))
	}

	if v.Value != nil && a.nameWidth > 0 {
		pads[v.Name.TokStart()] = padRight("", a.nameWidth-len(v.Name.Text))
	}

	return pads, ""
}

// enumValue assembles an enum value with comments, aligning '=' signs when
// the body breaks.
func (f *formatter) enumValue(v *syntax.EnumValue, align *columnAlign, bodyID int) doc.Doc {
	content := f.enumValueContent(v, align, false, f.opts.Separator.Get(ConstructEnum))
	if bodyID != 0 {
		content = doc.IfBreakFor(
			doc.Concat{f.enumValueContent(v, align, true, f.opts.Separator.Get(ConstructEnum)), trailingSep(v.Sep, f.opts.Separator.Get(ConstructEnum))},
			content,
			bodyID,
		)
	}

	parts := append(f.leadingComments(v), content)
	parts = append(parts, f.trailingComments(v, sepEmits(v.Sep, f.opts.Separator.Get(ConstructEnum)))...)

	return doc.Concat(parts)
}

func (f *formatter) enumValueContent(v *syntax.EnumValue, align *columnAlign, padded bool, sepMode SeparatorMode) doc.Doc {
	padded = padded && align != nil

	o := emitOpts{breakTrailing: true}
	if v.Sep != 0 {
		o.skipText = map[int]bool{v.TokEnd(): true}
		o.breakSkip = sepEmits(v.Sep, sepMode)
	}

	if padded && align.enumAssign && align.nameWidth > 0 {
		o.pads = map[int]string{v.Name.TokStart(): padRight("", align.nameWidth-len(v.Name.Text))}
	}

	return f.emitWithAnnotations(v.TokStart(), v.TokEnd(), v.Annotations, o)
}

// sepEmits reports whether the mode emits a non-empty trailing separator
// after a field with the given source separator.
func sepEmits(sep syntax.TokenKind, mode SeparatorMode) bool {
	switch mode {
	case SeparatorComma, SeparatorSemicolon:
		return true
	case SeparatorNone:
		return false
	}

	return sep != 0
}

// trailingSep returns the trailing separator for the given original
// separator, per the comma mode: always a comma or semicolon when forcing,
// nothing when removing, the original separator when preserving.
func trailingSep(sep syntax.TokenKind, mode SeparatorMode) doc.Doc {
	switch mode {
	case SeparatorComma:
		return doc.Text(",")
	case SeparatorSemicolon:
		return doc.Text(";")
	case SeparatorNone:
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

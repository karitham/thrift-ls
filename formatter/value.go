package formatter

import (
	"github.com/karitham/thrift-ls/doc"
	"github.com/karitham/thrift-ls/syntax"
)

// isListSep reports whether the token separates list items: a comma or a
// semicolon (lenient sources may use either).
func isListSep(kind syntax.TokenKind) bool {
	return kind == syntax.TokenComma || kind == syntax.TokenSemicolon
}

// containerConstruct returns the per-construct key of a declared container
// type. Set values are written with the list literal syntax, so the
// declared type is the only way to select the sets construct.
func containerConstruct(t *syntax.FieldType) Construct {
	if t == nil {
		return ConstructList
	}

	switch t.Kind {
	case syntax.TypeSet:
		return ConstructSet
	case syntax.TypeMap:
		return ConstructMap
	default:
		return ConstructList
	}
}

// constValue formats a constant value. Scalars render as a token run;
// lists and maps are groups that stay on one line when they fit and break
// with one entry per line otherwise. c is the construct whose separator
// and break options apply to list literals — the declared container type
// at the top level, the list construct for nested values. Every segment is
// a token run, so comments inside the value are preserved. isLast reports
// whether the value ends the enclosing declaration, in which case its
// trailing trivia belongs to the declaration's trailing comments.
func (f *formatter) constValue(v *syntax.ConstValue, isLast bool, c Construct) doc.Doc {
	if v == nil {
		return f.Concat()
	}

	switch v.Kind {
	case syntax.ValueList:
		return f.constList(v, isLast, c)
	case syntax.ValueMap:
		return f.constMap(v, isLast)
	default:
		// Comments before and after the value belong to the enclosing
		// structure (constant's or constItems' boundaries).
		return f.emitTokens(v.TokStart(), v.TokEnd(), emitOpts{})
	}
}

// constList formats "[ items ]" as a foldable group honoring the c
// construct's separator and break options.
func (f *formatter) constList(v *syntax.ConstValue, isLast bool, c Construct) doc.Doc {
	items := make([]constItem, len(v.List))
	for i, item := range v.List {
		items[i] = constItem{start: item.TokStart(), end: item.TokEnd(), doc: f.constValue(item, false, ConstructList)}
	}

	return f.constItems(items, v.TokStart(), v.TokEnd(), c, isLast)
}

// constMap formats "{ key: value, ... }" as a foldable group honoring the
// maps separator and break options.
func (f *formatter) constMap(v *syntax.ConstValue, isLast bool) doc.Doc {
	items := make([]constItem, len(v.Map))
	for i, entry := range v.Map {
		items[i] = constItem{
			start: entry.Key.TokStart(),
			end:   entry.Value.TokEnd(),
			doc:   f.emitTokens(entry.Key.TokStart(), entry.Value.TokEnd(), emitOpts{}),
		}
	}

	return f.constItems(items, v.TokStart(), v.TokEnd(), ConstructMap, isLast)
}

// constItem is one list/map entry: its formatted doc and the token span of
// its last token (the separator, if any, follows it).
type constItem struct {
	doc   doc.Doc
	start int
	end   int
}

// constItems is the shared list/map body: a foldable group with one entry
// per line when broken, honoring the construct's separator and break
// options. The separator between entries and the trailing separator follow
// the mode; the closing bracket gets exactly one break, so a trailing
// separator never leaves a blank line before it.
func (f *formatter) constItems(items []constItem, open, close int, c Construct, isLast bool) doc.Doc {
	sepMode := f.opts.Separator.Get(c)
	openOpts := emitOpts{trailing: true}

	if len(items) == 0 {
		return f.Concat(
			f.emitTokens(open, open, openOpts),
			f.emitTokens(close, close, emitOpts{leading: true}),
		)
	}

	middle := f.Parts(len(items) * 2)

	for i, item := range items {
		if i > 0 {
			prevEnd := items[i-1].end

			sepIdx := f.nextReal(prevEnd + 1)
			if isListSep(f.token(sepIdx).Kind) {
				middle = append(middle, f.itemSep(sepIdx, sepMode)...)
			} else {
				// Lenient sources may omit separators; keep the items
				// apart so their tokens cannot merge.
				middle = append(middle, f.foldBreak(prevEnd, " "))
			}
		}

		// Comments before and after the item render at the item boundary,
		// outside the item's own group, so a nested container stays flat.
		middle = append(middle, f.ownLineComments(item.start)...)
		middle = append(middle, item.doc)
		middle = append(middle, f.sameLineComments(item.end)...)
	}

	// The trailing separator: the mode's text (or nothing), with the
	// source separator token's comments preserved. Its gap is flat: the
	// close break below is the only line before the closing bracket.
	trailing := f.trailingItemSep(items[len(items)-1].end, sepMode)

	closeOpts := emitOpts{leading: true}

	// The single break before the close. A line comment owns its line end
	// (HardLine); the printer collapses a following structural line, so
	// the SoftLine never leaves a blank line.
	closeBreak := f.IfBreak(doc.SoftLine, f.Text(""))

	inner := f.Concat(
		f.emitTokens(open, open, openOpts),
		f.Indent(f.Concat(f.foldBreak(open, ""), f.Concat(middle...))),
		trailing,
		closeBreak,
		f.emitTokens(close, close, closeOpts),
	)
	if f.opts.Break.Get(c) || sepForcesBreakList(f.sepsOf(items), sepMode) {
		// BreakParent inside the group forces it to the broken layout.
		p := f.Parts(2)
		p = append(p, doc.BreakParent)
		p = append(p, inner)
		inner = f.Concat(p...)
	}

	return f.Group(inner)
}

// sepForcesBreakList reports whether a preserved separator mix forces the
// broken layout: items separated and unseparated inconsistently look broken
// on a flat line. Unlike fields, list separators sit between items only, so
// a single separator never forces a break.
func sepForcesBreakList(seps []syntax.TokenKind, mode SeparatorMode) bool {
	if mode != SeparatorPreserve || len(seps) < 2 {
		return false
	}

	want := seps[0] != 0
	for _, sep := range seps[1:] {
		if (sep != 0) != want {
			return true
		}
	}

	return false
}

// itemSep renders the separator between two list/map items: the source
// separator token (with comments) when the mode preserves its text, the
// forced text with the source token's comments otherwise, and the foldable
// gap after it that lines up the next item. A line comment owns its line
// end (HardLine), so the separator lands on the next line by construction.
func (f *formatter) itemSep(sep int, mode SeparatorMode) []doc.Doc {
	text := f.token(sep).Text

	switch mode {
	case SeparatorComma:
		text = ","
	case SeparatorSemicolon:
		text = ";"
	case SeparatorNone:
		text = ""
	}

	if text == f.token(sep).Text {
		p := f.Parts(2)
		p = append(p, f.emitTokens(sep, sep, emitOpts{leading: true, trailing: true}))
		p = append(p, f.foldBreak(sep, " "))

		return p
	}

	// Forced separator differing from the source: the forced text replaces
	// the suppressed text inside the run, so the source token's comments
	// stay ordered around it — own-line comments before it, same-line
	// comments after.
	p := f.Parts(2)
	p = append(p, f.emitTokens(sep, sep, emitOpts{leading: true, trailing: true, skipText: []int{sep}, text: text}))
	p = append(p, f.foldBreak(sep, " "))

	return p
}

// trailingItemSep is the trailing separator of a list/map: the source
// separator token (with comments) when present and the mode preserves its
// text, the forced text otherwise, nothing under SeparatorNone. Its gap is
// a flat space — the close break provides the line before the bracket. A
// line comment owns its line end, so the separator lands on the next line
// by construction.
func (f *formatter) trailingItemSep(last int, mode SeparatorMode) doc.Doc {
	sep := f.nextReal(last + 1)
	hasSep := isListSep(f.token(sep).Kind)

	text := ""
	if hasSep {
		text = f.token(sep).Text
	}

	switch mode {
	case SeparatorComma:
		text = ","
	case SeparatorSemicolon:
		text = ";"
	case SeparatorNone:
		text = ""
	}

	if !hasSep && text == "" {
		return f.Concat()
	}

	if hasSep && text == f.token(sep).Text {
		// Preserve the source separator with its comments; the flat gap
		// before the closing bracket. A trailing line comment owns its
		// line end instead.
		sepDoc := f.emitTokens(sep, sep, emitOpts{leading: true, trailing: true})
		if !f.sameLineEndsLine(sep) {
			p := f.Parts(2)
			p = append(p, sepDoc)
			p = append(p, f.Text(" "))
			sepDoc = f.Concat(p...)
		}

		return sepDoc
	}

	if !hasSep {
		// No source separator: the forced text and the flat gap before
		// the closing bracket.
		return f.Concat(f.Text(text), f.Text(" "))
	}

	// Forced text (or dropping the source separator under SeparatorNone):
	// the forced text replaces the suppressed text inside the run, so the
	// source token's comments stay ordered around it. The flat gap belongs
	// to the forced text; a dropped separator leaves no gap, so the output
	// is stable across a reparse.
	sepDoc := f.emitTokens(sep, sep, emitOpts{leading: true, trailing: true, skipText: []int{sep}, text: text})
	if text != "" && !f.sameLineEndsLine(sep) {
		p := f.Parts(2)
		p = append(p, sepDoc)
		p = append(p, f.Text(" "))
		sepDoc = f.Concat(p...)
	}

	return sepDoc
}

// sepsOf returns the separator kinds between the items, in order (0 when
// an item boundary has no separator token).
func (f *formatter) sepsOf(items []constItem) []syntax.TokenKind {
	seps := make([]syntax.TokenKind, 0, len(items)-1)

	for i := 1; i < len(items); i++ {
		prevEnd := items[i-1].end
		if isListSep(f.token(prevEnd + 1).Kind) {
			seps = append(seps, f.token(prevEnd+1).Kind)
		} else {
			seps = append(seps, 0)
		}
	}

	return seps
}

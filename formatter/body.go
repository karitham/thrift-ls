package formatter

import (
	"github.com/karitham/thrift-ls/doc"
	"github.com/karitham/thrift-ls/syntax"
)

// structLike formats struct, union, and exception declarations. The header
// renders as a token run up to and including the open brace; the brace
// text itself is emitted by bracedBody.
func (f *formatter) structLike(v *syntax.Struct) doc.Doc {
	open := f.scanKind(v.TokStart(), v.TokEnd(), syntax.TokenLBrace)
	close := f.scanKind(open+1, v.TokEnd(), syntax.TokenRBrace)

	parts := []doc.Doc{
		f.emitTokens(v.TokStart(), open, emitOpts{skipText: []int{open}, breakSkip: true}),
		f.bracedBody(v.Fields, open, close, close != v.TokEnd(), f.constructOf(v.Kind)),
	}
	if v.Annotations != nil {
		parts = append(parts, f.breakBeforeAnnotations(close))
	}

	parts = append(parts, f.annotationsDoc(v.Annotations, v.Annotations != nil && v.Annotations.TokEnd() == v.TokEnd()))
	parts = append(parts, f.afterAnnotations(v.Annotations, v.TokEnd()))

	return doc.Concat(parts)
}

// enum formats an enum declaration.
func (f *formatter) enum(v *syntax.Enum) doc.Doc {
	open := f.scanKind(v.TokStart(), v.TokEnd(), syntax.TokenLBrace)
	close := f.scanKind(open+1, v.TokEnd(), syntax.TokenRBrace)

	parts := []doc.Doc{
		f.emitTokens(v.TokStart(), open, emitOpts{skipText: []int{open}, breakSkip: true}),
		f.bracedEnumBody(v.Values, open, close, close != v.TokEnd()),
	}
	if v.Annotations != nil {
		parts = append(parts, f.breakBeforeAnnotations(close))
	}

	parts = append(parts, f.annotationsDoc(v.Annotations, v.Annotations != nil && v.Annotations.TokEnd() == v.TokEnd()))
	parts = append(parts, f.afterAnnotations(v.Annotations, v.TokEnd()))

	return doc.Concat(parts)
}

// scanKind returns the first token of the given kind in [start, end], or
// end when not found.
func (f *formatter) scanKind(start, end int, kind syntax.TokenKind) int {
	for i := start; i <= end; i++ {
		if f.token(i).Kind == kind {
			return i
		}
	}

	return end
}

// bracedBody formats "{ fields }": flat as "S { 1: i32 a }" when it fits,
// otherwise one field per line. Field alignment and trailing separators
// switch on the body group breaking. open and close are the brace token
// indices; closeTrailing reports whether the close brace is not the last
// token of the declaration (annotations follow), so its trailing trivia
// belongs here rather than to the declaration's trailing comments.
// constructOf returns the construct for the struct-like kind.
func (f *formatter) constructOf(kind syntax.StructKind) Construct {
	switch kind {
	case syntax.TokenUnion:
		return ConstructUnion
	case syntax.TokenException:
		return ConstructException
	}

	return ConstructStruct
}

func (f *formatter) bracedBody(fields []*syntax.Field, open, close int, closeTrailing bool, c Construct) doc.Doc {
	bodyID := f.id()
	sepMode := f.opts.Separator.Get(c)
	inner := append([]doc.Doc{doc.Line, f.fieldList(fields, bodyID, sepMode)}, f.closingTriviaAt(close)...)
	closeBreak := doc.IfBreak(doc.SoftLine, doc.Text(" "))

	if len(fields) == 0 {
		inner = append([]doc.Doc{}, f.closingTriviaAt(close)...)
		closeBreak = doc.IfBreak(doc.SoftLine, doc.Concat{})
	}

	openDoc := append([]doc.Doc{doc.Text(" {")}, f.openTriviaAt(open)...)

	content := doc.Concat{
		doc.Concat(openDoc),
		doc.Indent(doc.Concat(inner)),
		closeBreak,
		f.emitTokens(close, close, emitOpts{trailing: closeTrailing}),
	}
	if len(fields) > 0 && (f.opts.Break.Get(c) || sepForcesBreak(sepsOfFields(fields), sepMode)) {
		// BreakParent inside the group forces it to the broken layout.
		content = doc.Concat{doc.BreakParent, content}
	}

	return doc.GroupID(bodyID, content)
}

// bracedEnumBody is bracedBody for enum values.
func (f *formatter) bracedEnumBody(values []*syntax.EnumValue, open, close int, closeTrailing bool) doc.Doc {
	bodyID := f.id()
	inner := append([]doc.Doc{doc.Line, f.enumValueList(values, bodyID)}, f.closingTriviaAt(close)...)
	closeBreak := doc.IfBreak(doc.SoftLine, doc.Text(" "))

	if len(values) == 0 {
		inner = append([]doc.Doc{}, f.closingTriviaAt(close)...)
		closeBreak = doc.IfBreak(doc.SoftLine, doc.Concat{})
	}

	openDoc := append([]doc.Doc{doc.Text(" {")}, f.openTriviaAt(open)...)

	content := doc.Concat{
		doc.Concat(openDoc),
		doc.Indent(doc.Concat(inner)),
		closeBreak,
		f.emitTokens(close, close, emitOpts{trailing: closeTrailing}),
	}
	if len(values) > 0 && (f.opts.Break.Get(ConstructEnum) || sepForcesBreak(sepsOfValues(values), f.opts.Separator.Get(ConstructEnum))) {
		content = doc.Concat{doc.BreakParent, content}
	}

	return doc.GroupID(bodyID, content)
}

// closingTriviaAt returns the comments attached before the closing token,
// as docs each ending with a hard line. The leading hard line forces the
// body to break so the comments stay inside; the caller's closing line
// provides the newline after the last one.
func (f *formatter) closingTriviaAt(close int) []doc.Doc {
	tok := f.token(close)

	parts := make([]doc.Doc, 0, 8)
	if len(tok.Leading) > 0 {
		parts = append(parts, doc.HardLine)

		prevBlank := 0
		for i, c := range tok.Leading {
			parts = append(parts, f.blankLineDocs(c.BlankLinesBefore-prevBlank, doc.HardLine)...)
			prevBlank = c.BlankLinesBefore

			parts = append(parts, doc.Text(trimComment(c.Text)))
			if i < len(tok.Leading)-1 {
				parts = append(parts, doc.HardLine)
			}
		}
	}

	return parts
}

// openTriviaAt returns the trailing comments of the opening token as
// line-suffix docs, plus a break parent so the body goes multiline. Empty
// when there are none.
func (f *formatter) openTriviaAt(open int) []doc.Doc {
	tok := f.token(open)

	parts := make([]doc.Doc, 0, 8)

	if len(tok.Trailing) > 0 {
		for _, c := range tok.Trailing {
			parts = append(parts, doc.LineSuffix(doc.Text(" "+trimComment(c.Text))))
		}

		parts = append(parts, doc.BreakParent)
	}

	return parts
}

// service formats a service declaration. The body is always multiline:
// functions are too complex to flatten.
func (f *formatter) service(v *syntax.Service) doc.Doc {
	open := f.scanKind(v.TokStart(), v.TokEnd(), syntax.TokenLBrace)
	close := f.scanKind(open+1, v.TokEnd(), syntax.TokenRBrace)

	parts := make([]doc.Doc, 0, 8)

	for i, fn := range v.Functions {
		if i > 0 {
			parts = append(parts, doc.HardLineNoBreak)
			parts = append(parts, f.blankLines(fn, doc.HardLineNoBreak)...)
		} else {
			parts = append(parts, f.blankLines(fn, doc.HardLineNoBreak)...)
		}

		parts = append(parts, f.function(fn))
	}

	inner := doc.Concat{doc.Concat(parts), doc.Concat(f.closingTriviaAt(close))}
	if len(v.Functions) > 0 {
		// The first function starts its own line; the closing trivia
		// provides the break before it for empty bodies, so the blank
		// count does not double.
		inner = doc.Concat{doc.HardLineNoBreak, doc.Concat(parts), doc.Concat(f.closingTriviaAt(close))}
	} else if len(f.token(close).Leading) == 0 && f.token(close).BlankLinesBefore > 0 {
		// Empty body with blank lines before the close and no comments:
		// closingTriviaAt emits nothing, so preserve the blanks here.
		inner = doc.Concat{doc.Concat(f.blankLineDocs(f.token(close).BlankLinesBefore, doc.HardLineNoBreak)), inner}
	}

	openDoc := append([]doc.Doc{doc.Text(" {")}, f.openTriviaAt(open)...)
	body := doc.Concat{
		doc.Concat(openDoc),
		doc.Indent(inner),
		doc.HardLineNoBreak,
		f.emitTokens(close, close, emitOpts{trailing: close != v.TokEnd()}),
	}

	out := []doc.Doc{
		f.emitTokens(v.TokStart(), open, emitOpts{skipText: []int{open}, breakSkip: true}),
		body,
	}
	if v.Annotations != nil {
		out = append(out, f.breakBeforeAnnotations(close))
	}

	out = append(out, f.annotationsDoc(v.Annotations, v.Annotations != nil && v.Annotations.TokEnd() == v.TokEnd()))
	out = append(out, f.afterAnnotations(v.Annotations, v.TokEnd()))

	return doc.Concat(out)
}

// function formats a service method. The signature escalates via nested
// groups: the whole signature folds when it fits, otherwise the throws
// clause unfolds while the arguments stay flat, and the arguments unfold
// last. Each clause is forced independently — comments, blank lines, or a
// trailing delimiter in throws never break the arguments, because the
// throws clause is a sibling group, not an ancestor.
func (f *formatter) function(v *syntax.Function) doc.Doc {
	parts := append(f.leadingComments(v), f.functionBody(v))
	parts = append(parts, f.trailingComments(v, true)...)

	return doc.Concat(parts)
}

func (f *formatter) functionBody(v *syntax.Function) doc.Doc {
	// The header (up to the args open paren) renders as a token run, so
	// comments between the header tokens are preserved by construction.
	// The open paren's text is emitted by the args group; its trailing
	// trivia belongs to openTrivia.
	open := f.scanKind(v.TokStart(), v.TokEnd(), syntax.TokenLParen)
	header := f.emitTokens(v.TokStart(), open, emitOpts{skipText: []int{open}, breakSkip: true})

	// Comments or blank lines in the arguments force the multiline layout:
	// the flat argument group would drop them.
	argsMode := f.opts.Separator.Get(ConstructArguments)
	if f.fieldsForcedBroken(v.Args) || sepForcesBreak(sepsOfFields(v.Args), argsMode) || f.opts.Break.Get(ConstructArguments) {
		return f.functionBrokenArgs(v, header)
	}

	// The argument group folds to "(a, b)" when it fits and unfolds to one
	// field per line otherwise, like the throws clause.
	args := f.parenGroup(v.Args, open, parenClose(v.Args, open), false, argsMode)

	if v.Throws == nil {
		return doc.Group(doc.Concat{
			header,
			args,
			f.functionTail(v, open),
		})
	}

	return doc.Group(doc.Concat{
		header,
		args,
		f.throwsGroup(v),
		f.functionTail(v, open),
	})
}

// parenGroup renders "(fields)" as its own group, folding independently:
// flat when it fits, one field per line otherwise. open and close are the
// paren token indices, whose trivia is preserved. forced requires the
// broken layout (comments, blank lines, or a trailing delimiter inside).
func (f *formatter) parenGroup(fields []*syntax.Field, open, close int, forced bool, sepMode SeparatorMode) doc.Doc {
	broken := f.brokenParens(fields, open, close, sepMode)
	if forced {
		broken = doc.Concat{broken, doc.BreakParent}
	}

	return doc.Group(doc.IfBreak(
		broken,
		doc.Concat{doc.Text("("), f.flatFieldsJoin(fields, sepMode), doc.Text(")")},
	))
}

// throwsGroup renders the throws clause with the same folding as the
// arguments, so it stays flat when it fits even if the arguments broke.
func (f *formatter) throwsGroup(v *syntax.Function) doc.Doc {
	forced := f.fieldsForcedBroken(v.Throws.Fields) || sepForcesBreak(sepsOfFields(v.Throws.Fields), f.opts.Separator.Get(ConstructThrows)) || f.opts.Break.Get(ConstructThrows)

	return doc.Concat{doc.Text(" throws "), f.parenGroup(v.Throws.Fields, v.Throws.TokStart(), v.Throws.TokEnd(), forced, f.opts.Separator.Get(ConstructThrows))}
}

// parenClose returns the close paren index matching the open paren at
// open, given the field list it encloses.
func parenClose(fields []*syntax.Field, open int) int {
	if len(fields) == 0 {
		return open + 1
	}

	return fields[len(fields)-1].TokEnd() + 1
}

// sepForcesBreak reports whether the source's separators force the broken
// layout: a trailing delimiter on the last item under a mode that emits
// it, or a mixed separator pattern under preserve — a flat line whose
// separators are inconsistently present looks broken.
func sepForcesBreak(seps []syntax.TokenKind, mode SeparatorMode) bool {
	if len(seps) > 0 && mode != SeparatorNone && seps[len(seps)-1] != 0 {
		return true
	}

	if mode != SeparatorPreserve || len(seps) < 2 {
		return false
	}

	want := seps[0] != 0
	for _, sep := range seps[1 : len(seps)-1] {
		if (sep != 0) != want {
			return true
		}
	}

	return false
}

func sepsOfFields(fields []*syntax.Field) []syntax.TokenKind {
	seps := make([]syntax.TokenKind, len(fields))
	for i, f := range fields {
		seps[i] = f.Sep
	}

	return seps
}

func sepsOfValues(values []*syntax.EnumValue) []syntax.TokenKind {
	seps := make([]syntax.TokenKind, len(values))
	for i, v := range values {
		seps[i] = v.Sep
	}

	return seps
}

// flatFieldsJoin joins fields with their separators on one line: each
// field's own separator when preserving, or a single forced separator per
// the sepMode.
func (f *formatter) flatFieldsJoin(fields []*syntax.Field, sepMode SeparatorMode) doc.Doc {
	parts := make([]doc.Doc, 0, len(fields))
	for i, field := range fields {
		if i > 0 {
			parts = append(parts, doc.Concat{
				doc.Text(sepText(fields[i-1].Sep, sepMode)),
				doc.Line,
			})
		}

		parts = append(parts, f.fieldContent(field, nil, false, sepMode))
	}

	return doc.Concat(parts)
}

// functionBrokenArgs renders the signature with arguments and throws both
// broken, one per line.
func (f *formatter) functionBrokenArgs(v *syntax.Function, header doc.Doc) doc.Doc {
	open := f.scanKind(v.TokStart(), v.TokEnd(), syntax.TokenLParen)

	parts := []doc.Doc{
		header,
		f.parenGroup(v.Args, open, parenClose(v.Args, open), true, f.opts.Separator.Get(ConstructArguments)),
	}
	if v.Throws != nil {
		parts = append(parts, f.throwsGroup(v))
	}

	parts = append(parts, f.functionTail(v, open))

	return doc.Concat(parts)
}

// functionTail renders the tokens of the function after its args and
// throws clauses: the trailing trivia of the close parens, the
// annotations, and any stray tokens lenient sources leave — everything the
// structural layout does not emit itself. open is the args open paren.
func (f *formatter) functionTail(v *syntax.Function, open int) doc.Doc {
	parts := make([]doc.Doc, 0, 8)

	argsClose := parenClose(v.Args, open)
	if argsClose < v.TokEnd() {
		parts = append(parts, f.tailAfter(argsClose)...)
	}

	idx := argsClose + 1

	if v.Throws != nil {
		throwsClose := v.Throws.TokEnd()
		if throwsClose < v.TokEnd() {
			parts = append(parts, f.tailAfter(throwsClose)...)
		}

		idx = throwsClose + 1
	}

	if v.Annotations != nil {
		if idx < v.Annotations.TokStart() {
			parts = append(parts, f.emitTokens(idx, v.Annotations.TokStart()-1, emitOpts{leading: true}))
		}

		parts = append(parts, f.annotationsDoc(v.Annotations, v.Annotations.TokEnd() == v.TokEnd()))
		idx = v.Annotations.TokEnd() + 1
	}

	if idx <= v.TokEnd() {
		// A line comment on the previous token (e.g. the annotations'
		// close paren) would swallow the stray tokens.
		if f.lineAfter(idx-1) || len(f.token(idx).Leading) > 0 {
			parts = append(parts, doc.HardLine)
		}

		parts = append(parts, f.emitTokens(idx, v.TokEnd(), emitOpts{leading: true}))
	}

	return doc.Concat(parts)
}

// tailAfter renders the trailing trivia of a close paren followed by more
// tokens: the comments inline, with a hard line after line comments so
// nothing is swallowed.
func (f *formatter) tailAfter(idx int) []doc.Doc {
	parts := []doc.Doc{}
	for _, c := range f.token(idx).Trailing {
		parts = append(parts, doc.Text(" "+trimComment(c.Text)))
	}

	if f.lineAfter(idx) || len(f.token(idx+1).Leading) > 0 {
		parts = append(parts, doc.HardLine)
	}

	return parts
}

// brokenFields renders fields one per line, each with its trailing
// separator per the sepMode option. Comments and blank lines inside
// the list are preserved.
func (f *formatter) brokenFields(fields []*syntax.Field, sepMode SeparatorMode) doc.Doc {
	parts := make([]doc.Doc, 0, 8)

	for i, field := range fields {
		if i > 0 {
			parts = append(parts, doc.HardLineNoBreak)
			parts = append(parts, f.blankLines(field, doc.HardLineNoBreak)...)
		} else {
			parts = append(parts, f.blankLines(field, doc.HardLineNoBreak)...)
		}

		content := doc.Concat{
			f.fieldContent(field, f.alignmentFor(fields, i, sepMode), true, sepMode),
			trailingSep(field.Sep, sepMode),
		}
		fieldDoc := append(f.leadingComments(field), content)
		fieldDoc = append(fieldDoc, f.trailingComments(field, sepEmits(field.Sep, sepMode))...)
		parts = append(parts, doc.Concat(fieldDoc))
	}

	return doc.Concat(parts)
}

// brokenParens renders "open, fields, close" one field per line, or just
// "openclose" when there are no fields. open and close are the paren token
// indices; their trivia is preserved even with no fields.
func (f *formatter) brokenParens(fields []*syntax.Field, open, close int, sepMode SeparatorMode) doc.Doc {
	if len(fields) == 0 {
		closeDoc := f.emitTokens(close, close, emitOpts{leading: true})
		if f.lineAfter(open) || len(f.token(close).Leading) > 0 {
			closeDoc = doc.Concat{doc.HardLine, closeDoc}
		}

		return doc.Concat{
			doc.Text("("),
			doc.Concat(f.openTriviaAt(open)),
			closeDoc,
		}
	}

	inner := append([]doc.Doc{doc.HardLineNoBreak, f.brokenFields(fields, sepMode)}, f.closingTriviaAt(close)...)
	parts := append([]doc.Doc{doc.Text("(")}, f.openTriviaAt(open)...)
	parts = append(parts, doc.Indent(doc.Concat(inner)), doc.HardLineNoBreak, doc.Text(")"))

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
// node's first token. When the node carries leading comments the blank
// lines belong to that run and leadingComments emits them; returning nil
// here keeps them from being printed twice.
func (f *formatter) blankLines(n syntax.Node, line doc.Doc) []doc.Doc {
	if len(f.token(n.TokStart()).Leading) > 0 {
		return nil
	}

	return f.blankLineDocs(f.blankBefore(n), line)
}

// blankLineDocs returns count copies of line, for blank lines that must be
// preserved exactly.
func (f *formatter) blankLineDocs(count int, line doc.Doc) []doc.Doc {
	parts := make([]doc.Doc, 0, count)
	for range count {
		parts = append(parts, line)
	}

	return parts
}

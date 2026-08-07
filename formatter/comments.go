// Comment rendering model.
//
// Comments are first-class tokens in the stream. A comment renders by one
// of two rules, decided by line arithmetic — never by attachment:
//
//   - a comment on the same source line as the previous real token renders
//     inline after it (sameLineComment): a space, the comment text, and —
//     for line comments and annotations — a hard line owning its line end;
//   - a comment on its own line renders on its own line (ownLineComment):
//     a soft line (collapsing when the output already ended the line), the
//     blank lines before it, the comment text, and a hard line.
//
// The emitter (emitTokens) and the structural sites call the helpers in
// this file; nothing else inspects comment positions. The same-line run
// predicates (hasSameLineComments, sameLineEndsLine) and the alignment
// group checks (commentBreaksGroup, nameOnlyComment) mirror the rendering
// rules, so a comment's layout effect is always the one it would have in
// the output.
package formatter

import (
	"github.com/karitham/thrift-ls/doc"
	"github.com/karitham/thrift-ls/syntax"
)

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

// ownLineComment renders the comment token at c, which starts its own
// source line: a soft line (collapsing when the output already ended the
// line), the blank lines before it, the comment text, and a hard line
// owning its line end. At file start (prev < 0) the first comment with no
// blank lines begins the output, so no lead line is emitted; with blank
// lines the soft line provides the separator the file start lacks.
func (f *formatter) ownLineComment(c, prev int, first bool) []doc.Doc {
	ct := f.token(c)

	parts := f.Parts(4)
	if prev >= 0 || !first || ct.BlankLinesBefore > 0 {
		parts = append(parts, doc.SoftLine)
	}

	parts = append(parts, f.blankLineDocs(ct.BlankLinesBefore, doc.HardLine)...)
	parts = append(parts, f.Text(trimComment(ct.Text)), doc.CommentLine)

	return parts
}

// sameLineComment renders the comment token ct, which shares the previous
// real token's line: a space, the comment text, and — for line comments —
// a hard line owning its line end. It reports whether the comment ended
// the line.
func (f *formatter) sameLineComment(ct syntax.Token) ([]doc.Doc, bool) {
	parts := f.Parts(2)
	parts = append(parts, f.Text(" "), f.Text(trimComment(ct.Text)))
	if lineComment(ct.Kind) {
		return append(parts, doc.CommentLine), true
	}

	return parts, false
}

// sameLineRun calls fn for each comment after the real token at idx
// sharing its source line, in order.
func (f *formatter) sameLineRun(idx int, fn func(c int)) {
	for c := idx + 1; c < len(f.toks); c++ {
		ct := f.token(c)
		if !isComment(ct.Kind) || ct.Line != f.token(idx).Line {
			return
		}

		fn(c)
	}
}

// ownLineComments renders the own-line comments in the gap before the real
// token at idx: the comments between the previous real token and idx that
// start their own source line, each with its blank lines, plus the blank
// lines between the last comment and idx itself. Same-line comments in the
// gap belong to the previous token's trailing and are emitted by the
// caller's trailing phase or the next commentsRun.
func (f *formatter) ownLineComments(idx int) []doc.Doc {
	prev := f.prevReal(idx - 1)

	parts := f.Parts(4)
	first := true

	for c := prev + 1; c < idx; c++ {
		ct := f.token(c)
		if prev >= 0 && ct.Line == f.token(prev).Line {
			continue
		}

		parts = append(parts, f.ownLineComment(c, prev, first)...)
		first = false
	}

	if len(parts) > 0 && f.token(idx).BlankLinesBefore > 0 {
		parts = append(parts, f.blankLineDocs(f.token(idx).BlankLinesBefore, doc.HardLine)...)
	}

	return parts
}

// sameLineComments renders the same-line comments after the real token at
// idx.
func (f *formatter) sameLineComments(idx int) []doc.Doc {
	parts := f.Parts(4)

	f.sameLineRun(idx, func(c int) {
		docs, _ := f.sameLineComment(f.token(c))
		parts = append(parts, docs...)
	})

	return parts
}

// commentsRun renders the comments strictly between the real tokens at
// prev and cur (all stream entries in between are comments): a comment on
// the previous token's line renders inline after it, a comment on its own
// line renders on its own line and owns its line end. It reports whether
// the run ended the line, in which case the caller must not emit the
// canonical gap.
func (f *formatter) commentsRun(prev, cur int) ([]doc.Doc, bool) {
	parts := f.Parts(4)
	lineEnded := false

	for c := prev + 1; c < cur; c++ {
		ct := f.token(c)
		if ct.Line == f.token(prev).Line {
			// Same-line: inline after the previous token.
			docs, ended := f.sameLineComment(ct)
			parts = append(parts, docs...)
			lineEnded = ended

			continue
		}

		// Own-line: the comment starts its own line.
		parts = append(parts, f.ownLineComment(c, prev, false)...)
		lineEnded = true
	}

	return parts, lineEnded
}

// suppressedSepComments renders the same-line comments of a suppressed
// separator (the mode drops its text) that cannot share the previous
// content's line: each starts its own line, so the output round-trips.
func (f *formatter) suppressedSepComments(idx int) []doc.Doc {
	parts := f.Parts(4)

	f.sameLineRun(idx, func(c int) {
		parts = append(parts, f.ownLineComment(c, idx, false)...)
	})

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
	found := false
	f.sameLineRun(idx, func(int) { found = true })

	return found
}

// sameLineEndsLine reports whether the token's same-line comments contain
// a line comment, which owns its line end.
func (f *formatter) sameLineEndsLine(idx int) bool {
	ended := false
	f.sameLineRun(idx, func(c int) {
		if lineComment(f.token(c).Kind) {
			ended = true
		}
	})

	return ended
}

// commentBreaksGroup reports whether a comment between the real tokens at
// prevEnd and curStart renders on its own line — a visual break between
// the items. A comment renders inline when it shares the previous
// content's line, or when it follows the previous item's separator on the
// separator's line and the separator text is emitted; everything else
// starts its own line.
func (f *formatter) commentBreaksGroup(prevEnd, curStart int, sep syntax.TokenKind, sepMode SeparatorMode) bool {
	line := f.token(prevEnd).Line

	for c := prevEnd + 1; c < curStart; c++ {
		ct := f.token(c)
		if !isComment(ct.Kind) {
			continue
		}

		if ct.Line == line {
			continue // inline with the content
		}

		if sep != 0 && sepEmits(sep, sepMode) && ct.Line == f.token(f.nextReal(prevEnd+1)).Line {
			continue // inline after the emitted separator
		}

		return true
	}

	return false
}

// nameOnlyComment reports whether a comment follows the name of a
// name-only value (no content tokens) on the same source line, directly
// or after the separator: with the separator text dropped it renders
// against the name pad.
func (f *formatter) nameOnlyComment(name int) bool {
	for c := name + 1; c < len(f.toks); c++ {
		ct := f.token(c)
		if isComment(ct.Kind) {
			return ct.Line == f.token(name).Line
		}

		if ct.Line != f.token(name).Line {
			return false
		}
	}

	return false
}

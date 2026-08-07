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

// constValue formats a constant value. Scalars render as a token run;
// lists and maps are groups that stay on one line when they fit and break
// with one entry per line otherwise. Every segment is a token run, so
// comments inside the value are preserved. isLast reports whether the
// value ends the enclosing declaration, in which case its trailing trivia
// belongs to the declaration's trailing comments.
func (f *formatter) constValue(v *syntax.ConstValue, isLast bool) doc.Doc {
	if v == nil {
		return doc.Concat{}
	}

	switch v.Kind {
	case syntax.ValueList:
		return f.constList(v, isLast)
	case syntax.ValueMap:
		return f.constMap(v, isLast)
	default:
		o := emitOpts{leading: true}
		if !isLast {
			o.trailing = true
		}

		return f.emitTokens(v.TokStart(), v.TokEnd(), o)
	}
}

// constList formats "[ items ]" as a foldable group.
func (f *formatter) constList(v *syntax.ConstValue, isLast bool) doc.Doc {
	open, close := v.TokStart(), v.TokEnd()

	all := emitOpts{leading: true, trailing: true}
	if len(v.List) == 0 {
		closeDoc := f.emitTokens(close, close, emitOpts{leading: true, trailing: !isLast})
		if f.lineAfter(open) || len(f.token(close).Leading) > 0 {
			closeDoc = doc.Concat{doc.HardLine, closeDoc}
		}

		return doc.Concat{
			f.emitTokens(open, open, all),
			closeDoc,
		}
	}

	middle := make([]doc.Doc, 0, len(v.List)*2)
	last := open

	for i, item := range v.List {
		if i > 0 {
			prevEnd := v.List[i-1].TokEnd()
			if isListSep(f.token(prevEnd + 1).Kind) {
				middle = append(middle, f.commaSep(prevEnd+1)...)
			} else {
				// Lenient sources may omit separators; keep the items
				// apart so their tokens cannot merge.
				middle = append(middle, f.foldBreak(prevEnd, " "))
			}
		}

		middle = append(middle, f.constValue(item, false))
		last = item.TokEnd()
	}
	// A trailing comma after the last item (which may carry comments) is
	// not between two items, so it is emitted here.
	if isListSep(f.token(last + 1).Kind) {
		middle = append(middle, f.commaSep(last+1)...)
		last++
	}

	closeOpts := emitOpts{leading: true}
	if !isLast {
		closeOpts.trailing = true
	}

	return doc.Group(doc.Concat{
		f.emitTokens(open, open, all),
		doc.Indent(doc.Concat{f.foldBreak(open, ""), doc.Concat(middle)}),
		f.foldBreak(last, ""),
		f.emitTokens(close, close, closeOpts),
	})
}

// constMap formats "{ key: value, ... }" as a foldable group.
func (f *formatter) constMap(v *syntax.ConstValue, isLast bool) doc.Doc {
	open, close := v.TokStart(), v.TokEnd()

	all := emitOpts{leading: true, trailing: true}
	if len(v.Map) == 0 {
		closeDoc := f.emitTokens(close, close, emitOpts{leading: true, trailing: !isLast})
		if f.lineAfter(open) || len(f.token(close).Leading) > 0 {
			closeDoc = doc.Concat{doc.HardLine, closeDoc}
		}

		return doc.Concat{
			f.emitTokens(open, open, all),
			closeDoc,
		}
	}

	middle := make([]doc.Doc, 0, len(v.Map)*2)
	last := open

	for i, entry := range v.Map {
		if i > 0 {
			prevEnd := v.Map[i-1].Value.TokEnd()
			if isListSep(f.token(prevEnd + 1).Kind) {
				middle = append(middle, f.commaSep(prevEnd+1)...)
			} else {
				middle = append(middle, f.foldBreak(prevEnd, " "))
			}
		}

		middle = append(middle, f.emitTokens(entry.Key.TokStart(), entry.Value.TokEnd(), all))
		last = entry.Value.TokEnd()
	}

	if isListSep(f.token(last + 1).Kind) {
		middle = append(middle, f.commaSep(last+1)...)
		last++
	}

	closeOpts := emitOpts{leading: true}
	if !isLast {
		closeOpts.trailing = true
	}

	return doc.Group(doc.Concat{
		f.emitTokens(open, open, all),
		doc.Indent(doc.Concat{f.foldBreak(open, ""), doc.Concat(middle)}),
		f.foldBreak(last, ""),
		f.emitTokens(close, close, closeOpts),
	})
}

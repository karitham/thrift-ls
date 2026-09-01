package formatter

import (
	"github.com/karitham/thrift-ls/doc"
	"github.com/karitham/thrift-ls/syntax"
)

// Structured annotation rendering. A structured annotation leads the
// construct it decorates and renders on its own line (or lines, when its
// value breaks): the annotation owns the line end, so the decorated
// construct always starts fresh. Comments around the annotation belong to
// the enclosing node's boundaries; the gap between the annotations and the
// content is closed by callers via ownLineComments(contentStart).

// structuredAnnos renders leading structured annotations, one per line,
// and returns the index of the first content token after them. Comments
// before the first annotation belong to the caller; comments before later
// annotations render here. Callers close the gap to the content with
// ownLineComments(start) when annotations were present.
func (f *formatter) structuredAnnos(annos []*syntax.StructuredAnnotation, start int) (doc.Doc, int) {
	if len(annos) == 0 {
		return f.Concat(), start
	}

	parts := f.Parts(len(annos) * 2)
	for i, sa := range annos {
		if i > 0 {
			parts = append(parts, f.ownLineComments(sa.TokStart())...)
		}

		parts = append(parts, f.structuredAnno(sa))
		// A trailing line comment owns its line end (CommentLine); adding
		// the hard line on top of it would leave a blank line.
		if !f.sameLineEndsLine(sa.TokEnd()) {
			parts = append(parts, doc.HardLine)
		}
	}

	return f.Concat(parts...), f.nextReal(annos[len(annos)-1].TokEnd() + 1)
}

// structuredAnnosLead is the structuredAnnos prefix plus the comments
// between the last annotation and the content: the full lead-in of a
// construct carrying structured annotations. start is the construct's
// first token, returned unchanged when there are no annotations.
func (f *formatter) structuredAnnosLead(annos []*syntax.StructuredAnnotation, start int) (doc.Doc, int) {
	pre, start := f.structuredAnnos(annos, start)
	if len(annos) == 0 {
		return pre, start
	}

	return f.Concat(pre, f.Concat(f.ownLineComments(start)...)), start
}

// structuredAnno renders one structured annotation as a group: "@Name" and
// its value. Map and list values fold through the const-value machinery;
// a parenthesized scalar renders as the token run "( scalar )". Same-line
// comments after the value render at the group boundary, outside it, so
// the group folds independently.
func (f *formatter) structuredAnno(sa *syntax.StructuredAnnotation) doc.Doc {
	name := f.emitTokens(sa.TokStart(), sa.Name.TokEnd(), emitOpts{trailing: true})

	var value doc.Doc

	switch {
	case sa.Value == nil:
		// A failed parse: render whatever was consumed as a token run.
		value = f.emitTokens(f.nextReal(sa.Name.TokEnd()+1), sa.TokEnd(), emitOpts{leading: true})
	case sa.Value.Kind == syntax.ValueMap || sa.Value.Kind == syntax.ValueList:
		lead := f.ownLineComments(sa.Value.TokStart())
		gap := doc.Doc(f.Text(" "))
		if len(lead) > 0 || f.sameLineEndsLine(sa.Name.TokEnd()) {
			gap = f.Concat()
		}

		parts := append(lead, gap, f.constValue(sa.Value, false, ConstructList))
		value = f.Concat(parts...)
	default:
		// A parenthesized scalar: the parens are real tokens around the
		// value; render them together so comments inside survive.
		open := f.prevReal(sa.Value.TokStart() - 1)
		close := f.nextReal(sa.Value.TokEnd() + 1)
		value = f.emitTokens(open, close, emitOpts{leading: true})
	}

	return f.Concat(f.Group(f.Concat(name, value)), f.Concat(f.sameLineComments(sa.TokEnd())...))
}

package formatter

import (
	"github.com/karitham/thrift-ls/doc"
	"github.com/karitham/thrift-ls/syntax"
)

// constValue formats a constant value. Scalars are emitted verbatim from
// their source text; lists and maps are groups that stay on one line when
// they fit and break with one entry per line otherwise.
func (f *formatter) constValue(v *syntax.ConstValue) doc.Doc {
	if v == nil {
		return doc.Concat{}
	}
	switch v.Kind {
	case syntax.ValueList:
		items := make([]doc.Doc, 0, len(v.List))
		for _, item := range v.List {
			items = append(items, f.constValue(item))
		}
		return f.bracketList("[", "]", items)

	case syntax.ValueMap:
		entries := make([]doc.Doc, 0, len(v.Map))
		for _, entry := range v.Map {
			entries = append(entries, doc.Concat{
				f.constValue(entry.Key),
				doc.Text(": "),
				f.constValue(entry.Value),
			})
		}
		return f.bracketList("{", "}", entries)

	default:
		return doc.Text(v.Text)
	}
}

// bracketList formats [open, items, close] as a group: flat when it fits,
// otherwise one item per line, indented. Empty lists stay on one line.
func (f *formatter) bracketList(open, close string, items []doc.Doc) doc.Doc {
	if len(items) == 0 {
		return doc.Text(open + close)
	}
	return doc.Group(doc.Concat{
		doc.Text(open),
		doc.Indent(doc.Concat{
			doc.SoftLine,
			doc.Join(doc.Concat{doc.Text(","), doc.Line}, items),
		}),
		doc.SoftLine,
		doc.Text(close),
	})
}

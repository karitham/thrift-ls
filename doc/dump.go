package doc

import (
	"fmt"
	"strings"
)

// Dump renders the document IR as an indented tree, for debugging: every
// node with its type, text, group id and break state. Printing a doc
// mutates group break states in place, so dumping after Print shows the
// layout decisions.
func Dump(d Doc) string {
	var b strings.Builder
	dumpDoc(&b, d, "")

	return b.String()
}

func dumpDoc(b *strings.Builder, d Doc, ind string) {
	switch v := d.(type) {
	case nil:
		fmt.Fprintf(b, "%s<nil>\n", ind)
	case Concat:
		fmt.Fprintf(b, "%sConcat\n", ind)

		for _, c := range v {
			dumpDoc(b, c, ind+"  ")
		}
	case *concatNode:
		fmt.Fprintf(b, "%sConcat\n", ind)

		for _, c := range v.parts {
			dumpDoc(b, c, ind+"  ")
		}
	case *group:
		extra := ""
		if v.id != 0 {
			extra = fmt.Sprintf(" id=%d", v.id)
		}

		if v.brk {
			extra += " brk"
		}

		if v.expanded != nil {
			fmt.Fprintf(b, "%sConditionalGroup%s (%d states)\n", ind, extra, len(v.expanded))

			for _, s := range v.expanded {
				dumpDoc(b, s, ind+"  ")
			}

			return
		}

		fmt.Fprintf(b, "%sGroup%s\n", ind, extra)
		dumpDoc(b, v.doc, ind+"  ")
	case *ifBreak:
		fmt.Fprintf(b, "%sIfBreak (group %d)\n", ind, v.groupID)
		dumpDoc(b, v.breakDoc, ind+"  [broken] ")
		dumpDoc(b, v.flatDoc, ind+"  [flat] ")
	case *indent:
		fmt.Fprintf(b, "%sIndent\n", ind)
		dumpDoc(b, v.doc, ind+"  ")
	case *align:
		fmt.Fprintf(b, "%sAlign %d\n", ind, v.n)
		dumpDoc(b, v.doc, ind+"  ")
	case *lineSuffix:
		fmt.Fprintf(b, "%sLineSuffix\n", ind)
		dumpDoc(b, v.doc, ind+"  ")
	case lineSuffixBoundary:
		fmt.Fprintf(b, "%sLineSuffixBoundary\n", ind)
	case breakParent:
		fmt.Fprintf(b, "%sBreakParent\n", ind)
	case trim:
		fmt.Fprintf(b, "%sTrim\n", ind)
	case Text:
		if v == "" {
			fmt.Fprintf(b, "%sText \"\"\n", ind)
		} else {
			fmt.Fprintf(b, "%sText %q\n", ind, string(v))
		}
	case *textNode:
		if v.s == "" {
			fmt.Fprintf(b, "%sText \"\"\n", ind)
		} else {
			fmt.Fprintf(b, "%sText %q\n", ind, v.s)
		}
	case LineDoc:
		kind := "Line"
		if v.Hard {
			kind = "HardLine"
		}

		if v.Soft {
			kind += " (soft)"
		}

		if v.Literal {
			kind += " (literal)"
		}

		fmt.Fprintf(b, "%s%s\n", ind, kind)
	default:
		fmt.Fprintf(b, "%s%T\n", ind, d)
	}
}

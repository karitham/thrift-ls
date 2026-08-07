package syntax

import (
	"fmt"
	"strings"
)

// Dump renders a parsed document as a debug tree: every token with its
// kind, position, blank-line count, and attached trivia, followed by the
// node spans. Deterministic and stable for a given input, so dumps can be
// diffed across versions.
func Dump(d *Document) string {
	var b strings.Builder
	for i, tok := range d.Tokens {
		fmt.Fprintf(&b, "tok %3d %-16s line=%-3d col=%-3d blb=%d %q\n",
			i, tok.Kind, tok.Line, tok.Col, tok.BlankLinesBefore, tok.Text)

		for _, tr := range tok.Leading {
			fmt.Fprintf(&b, "      leading  %-18s %q\n", tr.Kind, tr.Text)
		}

		for _, tr := range tok.Trailing {
			fmt.Fprintf(&b, "      trailing %-18s %q\n", tr.Kind, tr.Text)
		}
	}

	for i, n := range d.Nodes {
		fmt.Fprintf(&b, "node %3d %-20s [%d..%d]\n", i, nodeName(n), n.TokStart(), n.TokEnd())
	}

	return b.String()
}

func nodeName(n Node) string {
	switch v := n.(type) {
	case *Include:
		return "Include"
	case *CPPInclude:
		return "CPPInclude"
	case *Namespace:
		return "Namespace"
	case *Const:
		return "Const"
	case *Typedef:
		return "Typedef"
	case *Enum:
		return "Enum"
	case *Struct:
		return v.Kind.String()
	case *Service:
		return "Service"
	default:
		return fmt.Sprintf("%T", n)
	}
}

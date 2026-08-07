package codejump

import (
	"go.lsp.dev/protocol"
)

// referenceHit is a matched reference with the text of the matched
// identifier, so rename can preserve include qualifiers.
type referenceHit struct {
	loc  protocol.Location
	text string
}

func hits(hits []referenceHit) []protocol.Location {
	out := make([]protocol.Location, 0, len(hits))
	for _, h := range hits {
		out = append(out, h.loc)
	}

	return out
}

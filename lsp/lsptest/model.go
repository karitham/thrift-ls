package lsptest

// model.go holds the harness's own assertion types plus the few helpers
// tests need to aim cursors at code. Tests see these plain types only;
// translation from protocol types happens at the harness boundary.
// Anything with real semantics, such as UTF-16 position math or edit
// application, is reused from mapper rather than reimplemented here.

import (
	"fmt"
	"strings"

	"go.lsp.dev/protocol"

	"github.com/karitham/thrift-ls/mapper"
)

// Position is a zero-based line/character position; character counts UTF-16
// code units, per LSP.
type Position struct {
	Line      int
	Character int
}

// Range is a half-open span of a document.
type Range struct {
	Start Position
	End   Position
}

// Diagnostic mirrors the subset of publishDiagnostics entries tests assert
// on. Severity uses LSP numbering: 1 error, 2 warning.
type Diagnostic struct {
	Range    Range
	Severity int
	Source   string
	Message  string
}

// Location points at a definition: a file path plus a range within it.
type Location struct {
	Path  string
	Range Range
}

// CompletionItem is one completion suggestion.
type CompletionItem struct {
	Label  string
	Detail string
}

// ErrNotFound reports a request against state the session does not have,
// such as formatting a document that was never opened.
var ErrNotFound = fmt.Errorf("lsptest: not found")

// IndexPosition returns the position of the nth (zero-based) occurrence of
// needle in text, how tests point a cursor at code without hard-coding
// coordinates. The byte offset to LSP position mapping goes through
// mapper.OffsetToLSPPosition, the same converter production requests use,
// so astral characters count as two columns here too.
func IndexPosition(text, needle string, occurrence int) (Position, error) {
	at := 0

	for n := 0; ; n++ {
		i := strings.Index(text[at:], needle)
		if i < 0 {
			return Position{}, fmt.Errorf("lsptest: %q occurrence %d not found", needle, occurrence)
		}

		at += i

		if n == occurrence {
			pos, err := mapper.NewMapper([]byte(text)).OffsetToLSPPosition(at)
			if err != nil {
				return Position{}, err
			}

			return toModelPos(pos), nil
		}

		at += len(needle)
	}
}

func toProtoPos(p Position) protocol.Position {
	return protocol.Position{Line: uint32(p.Line), Character: uint32(p.Character)}
}

func toModelPos(p protocol.Position) Position {
	return Position{Line: int(p.Line), Character: int(p.Character)}
}

func toModelRange(r protocol.Range) Range {
	return Range{Start: toModelPos(r.Start), End: toModelPos(r.End)}
}

// locationsFrom converts protocol locations into model ones, skipping
// non-file URIs.
func locationsFrom(ps []protocol.Location) []Location {
	out := make([]Location, 0, len(ps))

	for _, p := range ps {
		if !p.URI.IsFile() {
			continue
		}

		out = append(out, Location{Path: p.URI.Path(), Range: toModelRange(p.Range)})
	}

	return out
}

// messageText flattens the optional tooltip union into plain text.
func messageText(m protocol.InlayHintTooltip) string {
	switch v := m.(type) {
	case protocol.String:
		return string(v)
	case *protocol.MarkupContent:
		return v.Value
	default:
		return ""
	}
}

// hoverText extracts readable text from a hover result, reporting false when
// there is nothing to show.
func hoverText(h *protocol.Hover) (string, bool) {
	if h == nil {
		return "", false
	}

	switch v := h.Contents.(type) {
	case protocol.String:
		return string(v), len(v) > 0
	case *protocol.MarkupContent:
		return v.Value, v.Value != ""
	case *protocol.MarkedStringWithLanguage:
		return v.Value, v.Value != ""
	case protocol.MarkedStringSlice:
		var b strings.Builder

		for _, m := range v {
			switch mv := m.(type) {
			case protocol.String:
				b.WriteString(string(mv))
			case *protocol.MarkedStringWithLanguage:
				b.WriteString(mv.Value)
			}
			b.WriteByte('\n')
		}

		s := b.String()

		return s, len(s) > 0
	default:
		return "", false
	}
}

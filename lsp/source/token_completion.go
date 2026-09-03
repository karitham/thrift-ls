package source

import (
	"context"
	"errors"
	"sort"
	"strings"

	"go.lsp.dev/protocol"

	"github.com/karitham/thrift-ls/store"
)

var DefaultTokenCompletion Interface = &TokenCompletion{}

// TokenCompletion is the slot-based completion entry point. It resolves the
// grammar slot at the cursor, asks the providers for that slot, then
// filters, sorts, and caps the candidates.
type TokenCompletion struct{}

var keywords = map[string]protocol.InsertTextFormat{
	"bool":                  protocol.InsertTextFormatPlainText,
	"byte":                  protocol.InsertTextFormatPlainText,
	"i16":                   protocol.InsertTextFormatPlainText,
	"i32":                   protocol.InsertTextFormatPlainText,
	"i64":                   protocol.InsertTextFormatPlainText,
	"double":                protocol.InsertTextFormatPlainText,
	"binary":                protocol.InsertTextFormatPlainText,
	"uuid":                  protocol.InsertTextFormatPlainText,
	"string":                protocol.InsertTextFormatPlainText,
	"required":              protocol.InsertTextFormatPlainText,
	"optional":              protocol.InsertTextFormatPlainText,
	"include":               protocol.InsertTextFormatPlainText,
	"cpp_include":           protocol.InsertTextFormatPlainText,
	"list<$1>":              protocol.InsertTextFormatSnippet,
	"set<$1>":               protocol.InsertTextFormatSnippet,
	"map<$1, $2>":           protocol.InsertTextFormatSnippet,
	"struct $1 {\n$2\n}":    protocol.InsertTextFormatSnippet,
	"const $1 $2 = $3":      protocol.InsertTextFormatSnippet,
	"service $1 {\n$2\n}":   protocol.InsertTextFormatSnippet,
	"union $1 {\n$2\n}":     protocol.InsertTextFormatSnippet,
	"exception $1 {\n$2\n}": protocol.InsertTextFormatSnippet,
	"throws ($1)":           protocol.InsertTextFormatSnippet,
	"typedef $1 $2":         protocol.InsertTextFormatSnippet,
}

// maxCandidates caps the completion list; the server reports the list as
// incomplete when the cap truncates.
const maxCandidates = 10

// Candidate is a single completion entry before LSP conversion.
type Candidate struct {
	showText   string
	insertText string
	format     protocol.InsertTextFormat
}

// Completion resolves the grammar slot at the cursor and returns the
// candidates for that slot, the edit range, and whether the list was
// truncated by the cap.
func (c *TokenCompletion) Completion(ctx context.Context, view *store.View, cmp *CompletionRequest) ([]*CompletionItem, protocol.Range, bool, error) {
	parsedFile, err := view.Parse(ctx, cmp.Fh.URI())
	if err != nil {
		return nil, protocol.Range{}, false, err
	}

	if parsedFile.AST() == nil {
		return nil, protocol.Range{}, false, errors.New("parser ast failed")
	}

	pos, err := parsedFile.Mapper().LSPPosToParserPosition(cmp.Pos)
	if err != nil {
		return nil, protocol.Range{}, false, err
	}

	cc := ResolveContext(parsedFile.AST(), pos)

	// A trailing dot (qualified position, e.g. "ZeonForces.|" for a value
	// or "songs.|" for a type) means the user is about to type the member:
	// filter on everything after the dots, insert after them, and strip
	// the qualifier from inserted names so the result is "ZeonForces.ZAKU_I"
	// or "songs.Album", not a doubled qualifier. The lexer drops a trailing
	// dot, so detect it from the raw content, not the token stream. In a
	// type slot the dot keeps its slot — the type provider scopes to the
	// include — while any other slot becomes a qualified value position.
	qualified := strings.HasSuffix(cc.Prefix, ".")

	if !qualified && cc.Offset > 0 {
		if content, err := cmp.Fh.Content(); err == nil && cc.Offset <= len(content) && content[cc.Offset-1] == '.' {
			qualified = true

			if cc.Kind == CtxType {
				// Keep the dotted prefix so the type provider resolves
				// the include name and suggests its types.
				cc.Prefix += "."
			} else {
				cc.Kind = CtxFieldValue
				cc.Prefix = ""
			}
		}
	}

	filterPrefix := cc.Prefix

	editStart := cc.EditStart
	if qualified {
		filterPrefix = strings.TrimRight(cc.Prefix, ".")
		editStart = cc.Offset
	}

	var candidates []Candidate
	for _, p := range providersFor(cc.Kind) {
		candidates = append(candidates, p.Candidates(ctx, view, cmp.Fh.URI(), cc)...)
	}

	// Shared pipeline: prefix filter, dedupe, sort, cap.
	filtered := candidates[:0]

	seen := make(map[string]struct{}, len(candidates))
	for _, cand := range candidates {
		// Echo suppression: a candidate identical to the typed text adds
		// nothing (the client already shows it).
		if filterPrefix != "" && cand.showText == filterPrefix {
			continue
		}

		if !strings.HasPrefix(cand.showText, filterPrefix) {
			continue
		}

		// Providers may yield the same name (e.g. a type defined in the
		// file is both a type candidate and an identifier token).
		if _, ok := seen[cand.showText]; ok {
			continue
		}

		seen[cand.showText] = struct{}{}

		filtered = append(filtered, cand)
	}

	if qualified {
		for i := range filtered {
			if j := strings.LastIndex(filtered[i].showText, "."); j >= 0 {
				filtered[i].insertText = filtered[i].showText[j+1:]
			}
		}
	}

	sort.Slice(filtered, func(i, j int) bool {
		a, b := filtered[i].showText, filtered[j].showText
		aStarts := strings.HasPrefix(a, filterPrefix)

		bStarts := strings.HasPrefix(b, filterPrefix)
		if aStarts != bStarts {
			return aStarts
		}

		if len(a) != len(b) {
			return len(a) < len(b)
		}

		return a < b
	})

	truncated := false

	if len(filtered) > maxCandidates {
		filtered = filtered[:maxCandidates]
		truncated = true
	}

	cursor := cmp.Pos

	rng := protocol.Range{End: cursor}
	if start, err := parsedFile.Mapper().OffsetToLSPPosition(editStart); err == nil {
		rng.Start = protocol.Position{Line: start.Line, Character: start.Character}
	} else {
		rng.Start = cursor
	}

	res := make([]*CompletionItem, 0, len(filtered))
	for i := range filtered {
		res = append(res, BuildCompletionItem(filtered[i]))
	}

	return res, rng, truncated, nil
}

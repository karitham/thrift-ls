package completion

import (
	"context"
	"fmt"
	"log/slog"
	"path/filepath"
	"sort"
	"strings"
	"unicode"

	"go.lsp.dev/protocol"
	"go.lsp.dev/uri"

	"github.com/karitham/thrift-ls/lsp/cache"
	"github.com/karitham/thrift-ls/syntax"
)

var DefaultTokenCompletion Interface = &TokenCompletion{}

// TokenCompletion is token based completion. It generates completion list
// based on identifiers in the AST.
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

type Candidate struct {
	showText   string
	insertText string
	format     protocol.InsertTextFormat
}

func (c *TokenCompletion) Completion(ctx context.Context, ss *cache.Snapshot, cmp *CompletionRequest) ([]*CompletionItem, protocol.Range, error) {
	rng := protocol.Range{
		Start: protocol.Position{
			Line:      cmp.Pos.Line,
			Character: cmp.Pos.Character,
		},
		End: protocol.Position{
			Line:      cmp.Pos.Line,
			Character: cmp.Pos.Character,
		},
	}

	parsedFile, err := ss.Parse(ctx, cmp.Fh.URI())
	if err != nil {
		return nil, rng, err
	}

	if parsedFile.AST() == nil {
		return nil, rng, fmt.Errorf("parser ast failed")
	}

	pos, err := parsedFile.Mapper().LSPPosToParserPosition(cmp.Pos)
	if err != nil {
		return nil, rng, err
	}

	tokens := ss.TokensForFile(cmp.Fh.URI())

	slog.Debug("all tokens", "tokens", tokens)

	candidates := make([]Candidate, 0)

	slog.Debug("parser pos", "pos", pos)

	// Include completion: the cursor is inside an include path literal.
	includePos := pos
	includePos.Col--

	includePath := parsedFile.AST().SearchNodePathByPosition(includePos)
	if items, includeRng, err := c.includeCompletion(ss, cmp.Fh.URI(), parsedFile.AST(), includePath); err == nil {
		candidates = append(candidates, items...)
		if len(items) > 0 {
			rng = includeRng

			slog.Debug("include completion candidates", "candidates", candidates)
		}
	}

	if len(candidates) == 0 {
		content, err := cmp.Fh.Content()
		if err != nil {
			return nil, rng, err
		}

		var prefix []byte
		// get prefix by pos
		for i := pos.Offset - 1; i >= 0; i-- {
			if unicode.IsSpace(rune(content[i])) || content[i] == '.' || content[i] == '\'' || content[i] == '"' {
				prefix = content[i+1 : pos.Offset]
				rng.Start.Character = rng.Start.Character - uint32(len(prefix))

				break
			}
		}

		if len(prefix) == 0 {
			// prefix is empty, set prefix to content
			prefix = content
			rng.Start.Character = rng.Start.Character - uint32(len(prefix))
		}

		searchCandidate := func(token string, format protocol.InsertTextFormat) {
			if len(token) > len(prefix) && strings.HasPrefix(token, string(prefix)) {
				candidates = append(candidates, Candidate{
					showText:   token,
					insertText: token,
					format:     format,
				})
			}
		}

		// Semantic completion: context-aware candidates for type and
		// constant value positions; fall back to keywords and all
		// identifiers otherwise.
		semantic := semanticCandidates(ctx, ss, cmp.Fh.URI(), parsedFile, pos)
		if len(semantic) > 0 {
			for _, cand := range semantic {
				searchCandidate(cand.showText, cand.format)
			}
		} else {
			for i := range keywords {
				searchCandidate(i, keywords[i])
			}

			for i := range tokens {
				searchCandidate(i, protocol.InsertTextFormatPlainText)
			}
		}

		// Sort candidates: prefix matches first (by length, shorter first), then alphabetically
		sort.Slice(candidates, func(i, j int) bool {
			a, b := candidates[i].showText, candidates[j].showText
			aStarts := strings.HasPrefix(a, string(prefix))

			bStarts := strings.HasPrefix(b, string(prefix))
			if aStarts != bStarts {
				return aStarts
			}

			if len(a) != len(b) {
				return len(a) < len(b)
			}

			return a < b
		})

		if len(candidates) > 10 {
			candidates = candidates[:10]
		}

		slog.Debug("token prefix", "prefix", string(prefix), "candidates", candidates)
	}

	res := make([]*CompletionItem, 0, len(candidates))
	for i := range candidates {
		res = append(res, BuildCompletionItem(candidates[i]))
	}

	return res, rng, nil
}

// includeCompletion completes include path literals by listing the
// directory of the current file.
func (c *TokenCompletion) includeCompletion(ss *cache.Snapshot, file uri.URI, doc *syntax.Document, path []syntax.Node) (res []Candidate, rng protocol.Range, err error) {
	if len(path) == 0 {
		return res, rng, err
	}

	include, ok := path[len(path)-1].(*syntax.Include)
	if !ok || include.Path == nil {
		return res, rng, err
	}

	pathPrefix := include.Path.Text
	start, end := doc.TokenRange(include.Path)
	rng = protocol.Range{
		Start: protocol.Position{
			Line:      uint32(start.Line - 1),
			Character: uint32(start.Col - 1),
		},
		End: protocol.Position{
			Line:      uint32(end.Line - 1),
			Character: uint32(end.Col - 1),
		},
	}

	currentDir := filepath.Dir(file.Path())

	slog.Debug("searching prefix in path", "prefix", pathPrefix, "dir", currentDir)

	res, err = ListDirAndFiles(currentDir, pathPrefix)

	slog.Debug("include completion", "res", res, "err", err)

	return res, rng, err
}

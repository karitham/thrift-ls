package completion

import (
	"context"
	"sort"

	"go.lsp.dev/protocol"
	"go.lsp.dev/uri"

	"github.com/karitham/thrift-ls/lsp/cache"
	"github.com/karitham/thrift-ls/syntax"
)

// typeCandidates collects the names of all type definitions (structs,
// unions, exceptions, enums, typedefs, services) from the file and its
// transitively included files, plus the base type keywords.
func typeCandidates(ctx context.Context, ss *cache.Snapshot, file uri.URI, doc *syntax.Document) []Candidate {
	names := make(map[string]struct{})
	collectTypeNames := func(ast *syntax.Document) {
		for _, st := range ast.Structs() {
			names[st.Name.Text] = struct{}{}
		}

		for _, st := range ast.Unions() {
			names[st.Name.Text] = struct{}{}
		}

		for _, st := range ast.Exceptions() {
			names[st.Name.Text] = struct{}{}
		}

		for _, enum := range ast.Enums() {
			names[enum.Name.Text] = struct{}{}
		}

		for _, td := range ast.Typedefs() {
			names[td.Name.Text] = struct{}{}
		}

		for _, svc := range ast.Services() {
			names[svc.Name.Text] = struct{}{}
		}
	}

	collectTypeNames(doc)

	for _, inc := range includedFiles(ss, file) {
		if pf, err := ss.Parse(ctx, inc); err == nil && pf.AST() != nil {
			collectTypeNames(pf.AST())
		}
	}

	var res []Candidate
	for name := range names {
		res = append(res, Candidate{
			showText:   name,
			insertText: name,
			format:     protocol.InsertTextFormatPlainText,
		})
	}

	sort.Slice(res, func(i, j int) bool { return res[i].showText < res[j].showText })

	return res
}

// valueCandidates collects const names and enum names and values from the
// file and its transitively included files, both bare and enum-qualified.
func valueCandidates(ctx context.Context, ss *cache.Snapshot, file uri.URI, doc *syntax.Document) []Candidate {
	names := make(map[string]struct{})
	collectValueNames := func(ast *syntax.Document) {
		for _, cst := range ast.Consts() {
			names[cst.Name.Text] = struct{}{}
		}

		for _, enum := range ast.Enums() {
			names[enum.Name.Text] = struct{}{}
			for _, value := range enum.Values {
				names[value.Name.Text] = struct{}{}
				names[enum.Name.Text+"."+value.Name.Text] = struct{}{}
			}
		}
	}

	collectValueNames(doc)

	for _, inc := range includedFiles(ss, file) {
		if pf, err := ss.Parse(ctx, inc); err == nil && pf.AST() != nil {
			collectValueNames(pf.AST())
		}
	}

	var res []Candidate
	for name := range names {
		res = append(res, Candidate{
			showText:   name,
			insertText: name,
			format:     protocol.InsertTextFormatPlainText,
		})
	}

	sort.Slice(res, func(i, j int) bool { return res[i].showText < res[j].showText })

	return res
}

// includedFiles returns the files transitively included by file, per the
// include graph.
func includedFiles(ss *cache.Snapshot, file uri.URI) []uri.URI {
	var out []uri.URI

	visited := make(map[uri.URI]bool)

	var visit func(f uri.URI)

	visit = func(f uri.URI) {
		if visited[f] {
			return
		}

		visited[f] = true

		node := ss.Graph().Get(f)
		if node == nil {
			return
		}

		for _, inc := range node.OutDegree() {
			out = append(out, inc)
			visit(inc)
		}
	}
	visit(file)

	return out
}

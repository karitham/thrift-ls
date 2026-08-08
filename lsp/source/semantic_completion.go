package source

import (
	"context"
	"sort"
	"strings"

	"go.lsp.dev/protocol"
	"go.lsp.dev/uri"

	"github.com/karitham/thrift-ls/lsp/cache"
	"github.com/karitham/thrift-ls/syntax"
)

// typeCandidates collects the names of all type definitions (structs,
// unions, exceptions, enums, typedefs, services) from the file and its
// transitively included files, plus the base type keywords.
func typeCandidates(ctx context.Context, ss *cache.Snapshot, file uri.URI, c Context) []Candidate {
	// A dotted prefix scopes the completion to the include: suggest the
	// include's type names, qualified with the include name.
	if i := strings.LastIndexByte(c.Prefix, '.'); i >= 0 {
		includeName := c.Prefix[:i]

		incURI := ss.Resolver().GetIncludeURI(file, c.Doc, includeName)
		if incURI == "" {
			return nil
		}

		pf, err := ss.Parse(ctx, incURI)
		if err != nil || pf.AST() == nil {
			return nil
		}

		names := make(map[string]struct{})
		collectTypeNames(pf.AST(), names)

		res := make([]Candidate, 0, len(names))
		for name := range names {
			res = append(res, Candidate{
				showText:   includeName + "." + name,
				insertText: includeName + "." + name,
				format:     protocol.InsertTextFormatPlainText,
			})
		}

		sort.Slice(res, func(i, j int) bool { return res[i].showText < res[j].showText })

		return res
	}

	names := make(map[string]struct{})
	collectTypeNames(c.Doc, names)

	var res []Candidate
	for name := range names {
		res = append(res, Candidate{
			showText:   name,
			insertText: name,
			format:     protocol.InsertTextFormatPlainText,
		})
	}

	// Types from included files are suggested with their include
	// qualifier: a bare reference to an imported type does not resolve.
	for _, inc := range includedFiles(ss, file) {
		pf, err := ss.Parse(ctx, inc)
		if err != nil || pf.AST() == nil {
			continue
		}

		incNames := make(map[string]struct{})
		collectTypeNames(pf.AST(), incNames)

		qual := includeNameOf(inc)
		for name := range incNames {
			show := qual + "." + name
			res = append(res, Candidate{
				showText:   show,
				insertText: show,
				format:     protocol.InsertTextFormatPlainText,
			})
		}
	}

	for _, kw := range typeKeywords {
		res = append(res, Candidate{
			showText:   kw.text,
			insertText: kw.text,
			format:     kw.format,
		})
	}

	sort.Slice(res, func(i, j int) bool { return res[i].showText < res[j].showText })

	return res
}

// collectTypeNames adds the type definition names of a document to names:
// structs, unions, exceptions, enums, and typedefs. Services are not
// types and never complete in a type position.
func collectTypeNames(ast *syntax.Document, names map[string]struct{}) {
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
}

// typeKeywords are the base and container type keywords offered in a type
// position.
var typeKeywords = []struct {
	text   string
	format protocol.InsertTextFormat
}{
	{"bool", protocol.InsertTextFormatPlainText},
	{"byte", protocol.InsertTextFormatPlainText},
	{"i8", protocol.InsertTextFormatPlainText},
	{"i16", protocol.InsertTextFormatPlainText},
	{"i32", protocol.InsertTextFormatPlainText},
	{"i64", protocol.InsertTextFormatPlainText},
	{"double", protocol.InsertTextFormatPlainText},
	{"string", protocol.InsertTextFormatPlainText},
	{"binary", protocol.InsertTextFormatPlainText},
	{"slist", protocol.InsertTextFormatPlainText},
	{"uuid", protocol.InsertTextFormatPlainText},
	{"list<$1>", protocol.InsertTextFormatSnippet},
	{"set<$1>", protocol.InsertTextFormatSnippet},
	{"map<$1, $2>", protocol.InsertTextFormatSnippet},
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

		for _, inc := range ss.Includes(f) {
			out = append(out, inc)
			visit(inc)
		}
	}
	visit(file)

	return out
}

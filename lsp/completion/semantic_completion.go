package completion

import (
	"context"
	"sort"

	"go.lsp.dev/protocol"
	"go.lsp.dev/uri"

	"github.com/karitham/thrift-ls/lsp/cache"
	"github.com/karitham/thrift-ls/syntax"
)

// semanticCandidates returns context-aware completion candidates for the
// cursor position, or nil when the context is not a known completion
// position. Type positions complete with type names; constant value
// positions complete with const and enum value names.
func semanticCandidates(ctx context.Context, ss *cache.Snapshot, file uri.URI, parsedFile *cache.ParsedFile, pos syntax.Position) []Candidate {
	path := parsedFile.AST().SearchNodePathByPosition(pos)
	if len(path) == 0 {
		return nil
	}

	target := path[len(path)-1]

	switch n := target.(type) {
	case *syntax.FieldType:
		// Cursor on a type reference.
		return typeCandidates(ctx, ss, file, parsedFile.AST())

	case *syntax.Identifier:
		// The role of an identifier is carried by its parent: inside a
		// FieldType it is a type reference; as a field name the cursor is
		// in the value position.
		if len(path) < 2 {
			return nil
		}

		switch parent := path[len(path)-2].(type) {
		case *syntax.FieldType:
			return typeCandidates(ctx, ss, file, parsedFile.AST())
		case *syntax.Field:
			if parent.Value == nil {
				return valueCandidates(ctx, ss, file, parsedFile.AST())
			}
		}

	case *syntax.ConstValue:
		// Cursor on a constant value.
		return valueCandidates(ctx, ss, file, parsedFile.AST())

	case *syntax.Field:
		// Cursor before the field name is a type position; after the name
		// is a value position.
		if n.Name != nil && pos.Offset < tokenOffset(parsedFile.AST(), n.Name) {
			return typeCandidates(ctx, ss, file, parsedFile.AST())
		}

		if n.Value == nil {
			return valueCandidates(ctx, ss, file, parsedFile.AST())
		}

	case *syntax.Const:
		// Cursor on the const value.
		if n.Value != nil && pos.Offset > tokenOffset(parsedFile.AST(), n.Name) {
			return valueCandidates(ctx, ss, file, parsedFile.AST())
		}

	case *syntax.Typedef:
		// Cursor on the typedef type.
		if pos.Offset < tokenOffset(parsedFile.AST(), n.Name) {
			return typeCandidates(ctx, ss, file, parsedFile.AST())
		}
	}

	return nil
}

func tokenOffset(doc *syntax.Document, n syntax.Node) int {
	return doc.TokenPosition(n.TokStart()).Offset
}

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

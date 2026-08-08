package source

import (
	"context"
	"fmt"
	"io/fs"
	"log/slog"
	"path/filepath"
	"sort"
	"strings"

	"go.lsp.dev/protocol"
	"go.lsp.dev/uri"

	"github.com/karitham/thrift-ls/lsp/cache"
	"github.com/karitham/thrift-ls/syntax"
)

// Index answers cross-file semantic queries over one snapshot: definition
// resolution and reference search. It composes per-file
// cache.FileIndexes over the include graph.
//
// An Index is cheap — construct one per request with NewIndex.
type Index struct {
	ss *cache.Snapshot
}

// NewIndex returns an Index for the snapshot.
func NewIndex(ss *cache.Snapshot) *Index {
	return &Index{ss: ss}
}

// parseDefinitionFile parses the definition file, tolerating parse errors
// in the target file (the definitions may still be found in the partial
// AST). It returns the parsed file so callers can use its indexes.
func parseDefinitionFile(ctx context.Context, ss *cache.Snapshot, file uri.URI) (*cache.ParsedFile, error) {
	pf, err := ss.Parse(ctx, file)
	if err != nil {
		return nil, err
	}

	if len(pf.Errors()) > 0 {
		slog.Error("parse error", "errs", pf.Errors())
	}

	if pf.AST() == nil {
		return nil, errNoAST
	}

	return pf, nil
}

// Resolved is a resolved definition: the target file, the parsed
// document, the definition identifier (jump target), and its kind.
type Resolved struct {
	File   uri.URI
	Parsed *cache.ParsedFile

	// Name is the definition's identifier node, whose range is the jump
	// target.
	Name *syntax.Identifier

	// Node is the definition itself: *syntax.Struct, *syntax.Enum, etc.
	// For an enum value, Node is the *syntax.Identifier (same as Name).
	Node syntax.Node

	Kind DefinitionKind
}

// ResolveType resolves a type reference to its definition, or returns nil
// when unresolved (base types, unresolvable name). parseDefinitionFile
// errors are propagated.
func (x *Index) ResolveType(ctx context.Context, from *cache.ParsedFile, ft *syntax.FieldType) (*Resolved, error) {
	name := typeReferenceName(ft)
	if name == "" || IsBasicType(name) {
		return nil, nil
	}

	_, identifier := parseIdent(from.URI(), from.AST().Includes(), name)
	for _, astFile := range definitionFiles(ctx, x.ss, from.URI(), from.AST(), name) {
		dst, err := parseDefinitionFile(ctx, x.ss, astFile)
		if err != nil {
			return nil, err
		}

		switch v := dst.Definitions()[identifier].(type) {
		case *syntax.Struct:
			return defStruct(dst, v), nil
		case *syntax.Enum:
			return defFrom(dst, v.Name, v, DefinitionEnum), nil
		case *syntax.Typedef:
			return defFrom(dst, v.Name, v, DefinitionTypedef), nil
		}
	}

	return nil, nil
}

// ResolveValue resolves a const-value identifier to its definition
// (an enum value or a const), or returns nil when unresolved.
func (x *Index) ResolveValue(ctx context.Context, from *cache.ParsedFile, v *syntax.ConstValue) (*Resolved, error) {
	if v == nil || v.Kind != syntax.ValueIdent {
		return nil, nil
	}

	if v.Text == "true" || v.Text == "false" {
		return nil, nil
	}

	_, identifier := parseIdent(from.URI(), from.AST().Includes(), v.Text)
	identifier = bareName(identifier)

	for _, astFile := range definitionFiles(ctx, x.ss, from.URI(), from.AST(), v.Text) {
		dst, err := parseDefinitionFile(ctx, x.ss, astFile)
		if err != nil {
			return nil, err
		}

		if id := dst.EnumValues()[identifier]; id != nil {
			return defFrom(dst, id, id, DefinitionEnumValue), nil
		}

		if cst, ok := dst.Definitions()[identifier].(*syntax.Const); ok {
			return defFrom(dst, cst.Name, cst, DefinitionConst), nil
		}
	}

	return nil, nil
}

// ResolveService resolves a service name or extends reference, or returns
// nil when unresolved.
func (x *Index) ResolveService(ctx context.Context, from *cache.ParsedFile, ident *syntax.Identifier) (*Resolved, error) {
	if ident == nil {
		return nil, nil
	}

	_, identifier := parseIdent(from.URI(), from.AST().Includes(), ident.Text)
	for _, astFile := range definitionFiles(ctx, x.ss, from.URI(), from.AST(), ident.Text) {
		dst, err := parseDefinitionFile(ctx, x.ss, astFile)
		if err != nil {
			return nil, err
		}

		if svc, ok := dst.Definitions()[identifier].(*syntax.Service); ok {
			return defFrom(dst, svc.Name, svc, DefinitionService), nil
		}
	}

	return nil, nil
}

// Hit is one reference occurrence of a name, with the qualifying text
// preserved so a rename can rewrite includes correctly.
type Hit struct {
	File  uri.URI
	Range protocol.Range
	Text  string // as written: "User", "shared.User", "shared.thrift.User"
}

// References returns every occurrence of name in file and in every file
// that transitively includes it, restricted to the given reference kinds.
// The definition site is not included (no self-referencing hit).
func (x *Index) References(ctx context.Context, file uri.URI, name string, kinds ...cache.RefKind) ([]Hit, error) {
	files := x.searchFiles(file)

	var out []Hit
	seen := map[uri.URI]bool{}

	for _, f := range files {
		if seen[f] {
			continue
		}

		seen[f] = true

		pf, err := x.ss.Parse(ctx, f)
		if err != nil || pf.AST() == nil {
			continue
		}

		out = append(out, x.matches(pf, name, kinds)...)
	}

	return out, nil
}

// QualifiedValues returns value-position references whose qualifier is
// enumName: "Song.FUWA_FUWA_TIME" or "songs.Song.FUWA_FUWA_TIME", each
// hit covering only the enum segment so a rename rewrites the qualifier
// while keeping the member name.
func (x *Index) QualifiedValues(ctx context.Context, file uri.URI, enumName string) ([]Hit, error) {
	files := x.searchFiles(file)

	var out []Hit
	seen := map[uri.URI]bool{}

	for _, f := range files {
		if seen[f] {
			continue
		}

		seen[f] = true

		pf, err := x.ss.Parse(ctx, f)
		if err != nil || pf.AST() == nil {
			continue
		}

		for _, r := range pf.Index().References() {
			if r.Kind != cache.RefConstValue {
				continue
			}

			seg, off, ok := enumSegment(r.Name, enumName)
			if !ok {
				continue
			}

			start, _ := pf.AST().Range(r.Node)

			segStart := toLSPPosition(pf, syntax.Position{
				Line: start.Line, Col: start.Col, Offset: start.Offset + off,
			})
			segEnd := toLSPPosition(pf, syntax.Position{
				Line: start.Line, Col: start.Col, Offset: start.Offset + off + len(seg),
			})

			out = append(out, Hit{
				File:  f,
				Range: protocol.Range{Start: segStart, End: segEnd},
				Text:  seg,
			})
		}
	}

	return out, nil
}

// ReferencingFiles returns every file that directly includes file,
// in graph order.
func (x *Index) ReferencingFiles(file uri.URI) []uri.URI {
	return x.ss.Includers(file)
}

// FindInWorkspace returns the definition of name in any known file of the
// workspace, falling back to a directory walk when the workspace has not
// been indexed yet (e.g. a quick-fix on the first didOpen).
func (x *Index) FindInWorkspace(ctx context.Context, name string) (*Resolved, error) {
	view := x.ss.View()
	if view == nil {
		return nil, nil
	}

	for _, f := range view.KnownFiles() {
		pf, err := x.ss.Parse(ctx, f)
		if err != nil || pf.AST() == nil {
			continue
		}

		if n, ok := pf.Definitions()[name]; ok {
			return defFromNode(pf, n), nil
		}
	}

	// Fallback to the old directory walk when KnownFiles is empty.
	root := view.Folder().Path()
	if root == "" {
		return nil, nil
	}

	var files []string

	err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}

		if !d.IsDir() && strings.HasSuffix(d.Name(), ".thrift") {
			files = append(files, p)
		}

		return nil
	})
	if err != nil {
		return nil, nil
	}

	sort.Strings(files)
	for _, p := range files {
		pf, err := x.ss.Parse(ctx, uri.File(p))
		if err != nil || pf.AST() == nil {
			continue
		}

		if n, ok := pf.Definitions()[name]; ok {
			return defFromNode(pf, n), nil
		}
	}

	return nil, nil
}

// refKindsFor returns the reference slots a definition kind can appear in.
//
// An exception is only thrown (signatures), never used as a field type.
// Enum values and consts live in value positions. Services are
// extends-only. Every other type can appear in both field and signature
// slots.
func refKindsFor(k DefinitionKind) []cache.RefKind {
	switch k {
	case DefinitionException:
		return []cache.RefKind{cache.RefSignatureType}
	case DefinitionEnumValue, DefinitionConst:
		return []cache.RefKind{cache.RefConstValue}
	case DefinitionService:
		return []cache.RefKind{cache.RefServiceExtends}
	case DefinitionStruct, DefinitionUnion, DefinitionEnum, DefinitionTypedef:
		return []cache.RefKind{cache.RefFieldType, cache.RefSignatureType}
	}

	return nil
}

// --- helpers ---

// searchFiles returns the file itself followed by its direct includers,
// deduplicated. This matches the existing reference-search file ordering.
func (x *Index) searchFiles(file uri.URI) []uri.URI {
	files := []uri.URI{file}

	for _, dep := range x.ReferencingFiles(file) {
		if dep != file {
			files = append(files, dep)
		}
	}

	return files
}

// matches returns hits for references in pf whose kind is in kinds and
// whose bare name equals bareName(name).
func (x *Index) matches(pf *cache.ParsedFile, name string, kinds []cache.RefKind) []Hit {
	kindSet := make(map[cache.RefKind]bool, len(kinds))
	for _, k := range kinds {
		kindSet[k] = true
	}

	haveKindSet := len(kinds) > 0

	var out []Hit

	for _, r := range pf.Index().References() {
		if haveKindSet && !kindSet[r.Kind] {
			continue
		}

		if bareName(r.Name) != bareName(name) {
			continue
		}

		out = append(out, Hit{
			File:  pf.URI(),
			Range: nodeRange(pf, r.Node),
			Text:  r.Name,
		})
	}

	return out
}

// enumSegment splits a value identifier on dots and returns the segment
// that equals enumName, its byte offset, and ok=true. If the identifier is
// not qualified with enumName, ok=false.
func enumSegment(text, enumName string) (seg string, off int, ok bool) {
	items := strings.Split(text, ".")

	for i, item := range items {
		if item == enumName {
			off := 0
			for _, p := range items[:i] {
				off += len(p) + 1
			}

			return item, off, true
		}
	}

	return "", 0, false
}

// defStruct maps a struct/union/exception definition.
func defStruct(pf *cache.ParsedFile, v *syntax.Struct) *Resolved {
	return &Resolved{
		File:   pf.URI(),
		Parsed: pf,
		Name:   v.Name,
		Node:   v,
		Kind:   structKind(v.Kind),
	}
}

func structKind(k syntax.TokenKind) DefinitionKind {
	switch k {
	case syntax.UnionDecl:
		return DefinitionUnion
	case syntax.ExceptionDecl:
		return DefinitionException
	}

	return DefinitionStruct
}

func defFrom(pf *cache.ParsedFile, name *syntax.Identifier, node syntax.Node, kind DefinitionKind) *Resolved {
	return &Resolved{File: pf.URI(), Parsed: pf, Name: name, Node: node, Kind: kind}
}

// defFromNode builds a Resolved from any top-level definition node. Use
// when the concrete type and Kind are not known statically (e.g.
// FindInWorkspace).
func defFromNode(pf *cache.ParsedFile, n syntax.Node) *Resolved {
	switch v := n.(type) {
	case *syntax.Struct:
		return defStruct(pf, v)
	case *syntax.Enum:
		return defFrom(pf, v.Name, v, DefinitionEnum)
	case *syntax.Typedef:
		return defFrom(pf, v.Name, v, DefinitionTypedef)
	case *syntax.Const:
		return defFrom(pf, v.Name, v, DefinitionConst)
	case *syntax.Service:
		return defFrom(pf, v.Name, v, DefinitionService)
	case *syntax.Identifier:
		// Enum value names are Identifiers in the defs map? No — enum
		// values live in EnumValues(), not Definitions(). This case is
		// unreachable from FindInWorkspace (which queries Definitions),
		// but kept for completeness.
		return defFrom(pf, v, v, DefinitionEnumValue)
	}

	panic(fmt.Sprintf("unexpected definition node type %T", n))
}

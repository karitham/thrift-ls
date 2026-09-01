package source

import (
	"context"
	"fmt"
	"log/slog"
	"slices"
	"strings"

	"go.lsp.dev/protocol"
	"go.lsp.dev/uri"

	"github.com/karitham/thrift-ls/lsp/cache"
	"github.com/karitham/thrift-ls/syntax"
)

// Index answers cross-file semantic queries over one view: definition
// resolution and reference search. It composes per-file
// cache.FileIndexes over the include graph.
//
// An Index is cheap — construct one per request with NewIndex. Resolutions
// are memoized per (file, name), so a request resolving the same name in
// the same file repeatedly (references, diagnostics) resolves it once.
type Index struct {
	view *cache.View

	resolved map[resolveKey]*Resolved
}

// NewIndex returns an Index over the view's store.
func NewIndex(view *cache.View) *Index {
	return &Index{view: view}
}

// resolveKey identifies one resolution: the referencing file, the name as
// written, and the resolver (type, value, or service).
type resolveKey struct {
	file uri.URI
	name string
	kind resolveKind
}

type resolveKind uint8

const (
	resolveType resolveKind = iota + 1
	resolveValue
	resolveService
)

// memoized returns the memoized resolution for (file, name, kind), if any.
func (x *Index) memoized(file uri.URI, name string, kind resolveKind) (*Resolved, bool) {
	def, ok := x.resolved[resolveKey{file: file, name: name, kind: kind}]

	return def, ok
}

// memoize records the resolution of (file, name, kind). A name resolves
// identically everywhere in the same file, so nil (unresolved) results are
// memoized too.
func (x *Index) memoize(file uri.URI, name string, kind resolveKind, def *Resolved) {
	if x.resolved == nil {
		x.resolved = make(map[resolveKey]*Resolved)
	}

	x.resolved[resolveKey{file: file, name: name, kind: kind}] = def
}

// parseDefinitionFile parses the definition file, tolerating parse errors
// in the target file (the definitions may still be found in the partial
// AST). It returns nil when the file cannot be read at all — an unresolved
// include is a normal, reported condition, not a failure of the feature
// answering a request.
func parseDefinitionFile(ctx context.Context, view *cache.View, file uri.URI) *cache.ParsedFile {
	pf, err := view.Parse(ctx, file)
	if err != nil {
		slog.Debug("definition file unreadable", "file", file, "err", err)

		return nil
	}

	if len(pf.Errors()) > 0 {
		slog.Debug("parse error", "errs", pf.Errors())
	}

	return pf
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
// when unresolved (base types, unresolvable name). Files backing the
// resolution may fail to read; those count as unresolved.
func (x *Index) ResolveType(ctx context.Context, from *cache.ParsedFile, ft *syntax.FieldType) (*Resolved, error) {
	name := typeReferenceName(ft)
	if name == "" || IsBasicType(name) {
		return nil, nil
	}

	if def, ok := x.memoized(from.URI(), name, resolveType); ok {
		return def, nil
	}

	def, err := x.resolveType(ctx, from, name)
	if err != nil {
		return nil, err
	}

	x.memoize(from.URI(), name, resolveType, def)

	return def, nil
}

// resolveType resolves a non-basic type name in from, without memoization.
func (x *Index) resolveType(ctx context.Context, from *cache.ParsedFile, name string) (*Resolved, error) {
	_, identifier := parseIdent(from.URI(), from.AST().Includes(), name)

	for _, astFile := range definitionFiles(ctx, x.view, from.URI(), from.AST(), name) {
		dst := parseDefinitionFile(ctx, x.view, astFile)
		if dst == nil || dst.AST() == nil {
			continue
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

	if def, ok := x.memoized(from.URI(), v.Text, resolveValue); ok {
		return def, nil
	}

	def, err := x.resolveValue(ctx, from, v.Text)
	if err != nil {
		return nil, err
	}

	x.memoize(from.URI(), v.Text, resolveValue, def)

	return def, nil
}

// resolveValue resolves a value-identifier text in from, without
// memoization.
func (x *Index) resolveValue(ctx context.Context, from *cache.ParsedFile, text string) (*Resolved, error) {
	_, identifier := parseIdent(from.URI(), from.AST().Includes(), text)
	identifier = bareName(identifier)

	for _, astFile := range definitionFiles(ctx, x.view, from.URI(), from.AST(), text) {
		dst := parseDefinitionFile(ctx, x.view, astFile)
		if dst == nil || dst.AST() == nil {
			continue
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

	if def, ok := x.memoized(from.URI(), ident.Text, resolveService); ok {
		return def, nil
	}

	def, err := x.resolveService(ctx, from, ident.Text)
	if err != nil {
		return nil, err
	}

	x.memoize(from.URI(), ident.Text, resolveService, def)

	return def, nil
}

// resolveService resolves a service name text in from, without
// memoization.
func (x *Index) resolveService(ctx context.Context, from *cache.ParsedFile, name string) (*Resolved, error) {
	_, identifier := parseIdent(from.URI(), from.AST().Includes(), name)
	for _, astFile := range definitionFiles(ctx, x.view, from.URI(), from.AST(), name) {
		dst := parseDefinitionFile(ctx, x.view, astFile)
		if dst == nil || dst.AST() == nil {
			continue
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

	// Kind is the grammar slot the reference sits in, so callers can tell
	// type hits from value hits (e.g. for highlight kinds).
	Kind cache.RefKind
}

// References returns every occurrence of name in file and in every file
// that transitively includes it, restricted to the given reference kinds.
// The definition site is not included (no self-referencing hit). Hits are
// matched by bare name only; use ReferencesTo for resolution-matched
// results.
func (x *Index) References(ctx context.Context, file uri.URI, name string, kinds ...cache.RefKind) ([]Hit, error) {
	files := x.searchFiles(file)

	var out []Hit
	seen := map[uri.URI]bool{}

	for _, f := range files {
		if seen[f] {
			continue
		}

		seen[f] = true

		pf, err := x.view.Parse(ctx, f)
		if err != nil || pf.AST() == nil {
			continue
		}

		out = append(out, x.matches(pf, name, kinds)...)
	}

	return out, nil
}

// ReferencesTo returns every reference to def: in def.File and every file
// that transitively includes it. Hits are name- and resolution-matched, so
// same-named definitions elsewhere are not reported.
//
// For an enum def, value references qualified with the enum name
// ("Color.RED", "shared.Color.RED") are matched too, provided the
// qualifier resolves to this very enum; the hit covers only the enum
// segment, so a rename rewrites the qualifier while keeping the member
// name.
func (x *Index) ReferencesTo(ctx context.Context, def *Resolved, kinds ...cache.RefKind) ([]Hit, error) {
	var out []Hit

	for _, f := range x.searchFiles(def.File) {
		pf, err := x.view.Parse(ctx, f)
		if err != nil || pf.AST() == nil {
			continue
		}

		for _, r := range pf.Index().References() {
			if r.Kind == cache.RefConstValue && def.Kind == DefinitionEnum && referenceKind(cache.RefConstValue, kinds) {
				// Value position qualified with the enum: only the enum
				// segment is rewritten.
				seg, off, ok := enumSegment(r.Name, def.Name.Text)
				if !ok {
					continue
				}

				// The qualifier ("Color", "shared.Color") must resolve to
				// this very enum, not to a same-named one elsewhere.
				qualifier := r.Name[:off+len(seg)]
				enum, err := x.ResolveType(ctx, pf, typeReference(qualifier))
				if err != nil {
					return nil, err
				}

				if !sameDefinition(enum, def) {
					continue
				}

				out = append(out, enumSegmentHit(pf, r.Node, off, seg))

				continue
			}

			if !referenceKind(r.Kind, kinds) {
				continue
			}

			if bareName(r.Name) != bareName(def.Name.Text) {
				continue
			}

			resolved, err := x.resolveReference(ctx, pf, r)
			if err != nil {
				return nil, err
			}

			if !sameDefinition(resolved, def) {
				continue
			}

			out = append(out, Hit{
				File:  pf.URI(),
				Range: nodeRange(pf, r.Node),
				Text:  r.Name,
				Kind:  r.Kind,
			})
		}
	}

	return out, nil
}

// ReferencingFiles returns every file that directly includes file,
// in graph order.
func (x *Index) ReferencingFiles(file uri.URI) []uri.URI {
	return x.view.Includers(file)
}

// FindInWorkspace returns the definition of name in any known file of the
// workspace, falling back to a directory walk when the workspace has not
// been indexed yet (e.g. a quick-fix on the first didOpen).
func (x *Index) FindInWorkspace(ctx context.Context, name string) (*Resolved, error) {
	include, identifier := splitQualifiedName(name)

	for _, f := range x.view.KnownFiles() {
		if include != "" && includeNameOf(f) != include {
			continue
		}

		pf, err := x.view.Parse(ctx, f)
		if err != nil || pf.AST() == nil {
			continue
		}

		if n, ok := pf.Definitions()[identifier]; ok {
			return defFromNode(pf, n), nil
		}
	}

	// Fallback to the directory walk when KnownFiles is empty: the walk
	// goes through the view's file source (disk, or the in-memory tree in
	// tests).
	root := x.view.Folder()
	if root == "" {
		return nil, nil
	}

	var files []uri.URI

	err := x.view.WalkFiles(ctx, root, func(u uri.URI) error {
		if strings.HasSuffix(u.Path(), ".thrift") {
			files = append(files, u)
		}

		return nil
	})
	if err != nil {
		return nil, nil
	}

	slices.Sort(files)

	for _, f := range files {
		if include != "" && includeNameOf(f) != include {
			continue
		}

		pf, err := x.view.Parse(ctx, f)
		if err != nil || pf.AST() == nil {
			continue
		}

		if n, ok := pf.Definitions()[identifier]; ok {
			return defFromNode(pf, n), nil
		}
	}

	return nil, nil
}

// refKindsFor returns the reference slots a definition kind can appear in.
//
// An exception is thrown (signatures) but never used as a field type;
// as an annotation type it is legal, since the compiler's get_type
// resolves any declared type. Enum values and consts live in value
// positions. Services are extends-only. Every other type can appear in
// field, signature, and annotation-type slots.
func refKindsFor(k DefinitionKind) []cache.RefKind {
	switch k {
	case DefinitionException:
		return []cache.RefKind{cache.RefSignatureType, cache.RefAnnotationType}
	case DefinitionEnumValue, DefinitionConst:
		return []cache.RefKind{cache.RefConstValue}
	case DefinitionService:
		return []cache.RefKind{cache.RefServiceExtends}
	case DefinitionStruct, DefinitionUnion, DefinitionEnum, DefinitionTypedef:
		return []cache.RefKind{cache.RefFieldType, cache.RefSignatureType, cache.RefAnnotationType}
	}

	return nil
}

// --- helpers ---

// resolveReference resolves a reference to its definition, dispatching on
// the grammar slot the reference sits in. Unresolvable references (parse
// errors, unknown names) yield nil, not an error.
func (x *Index) resolveReference(ctx context.Context, pf *cache.ParsedFile, r cache.Reference) (*Resolved, error) {
	switch r.Kind {
	case cache.RefFieldType, cache.RefSignatureType, cache.RefAnnotationType:
		ident, ok := r.Node.(*syntax.Identifier)
		if !ok {
			return nil, nil
		}

		return x.ResolveType(ctx, pf, typeReference(ident.Text))
	case cache.RefConstValue:
		v, ok := r.Node.(*syntax.ConstValue)
		if !ok {
			return nil, nil
		}

		return x.ResolveValue(ctx, pf, v)
	case cache.RefServiceExtends:
		ident, ok := r.Node.(*syntax.Identifier)
		if !ok {
			return nil, nil
		}

		return x.ResolveService(ctx, pf, ident)
	}

	return nil, nil
}

// typeReference builds a FieldType for a reference text, for resolution
// by name: the index resolves the text, the original node only carries it.
func typeReference(name string) *syntax.FieldType {
	return &syntax.FieldType{Kind: syntax.TypeIdent, Ident: &syntax.Identifier{Text: name}}
}

// sameDefinition reports whether resolved is the same definition as def:
// same file and same name.
func sameDefinition(resolved, def *Resolved) bool {
	if resolved == nil || def == nil {
		return false
	}

	return resolved.File == def.File && resolved.Name.Text == def.Name.Text
}

// referenceKind reports whether k is in kinds; an empty kinds matches all.
func referenceKind(k cache.RefKind, kinds []cache.RefKind) bool {
	return len(kinds) == 0 || slices.Contains(kinds, k)
}

// enumSegmentHit builds a hit covering one segment (off, seg) of the
// reference node's text, so a rename rewrites just that segment.
func enumSegmentHit(pf *cache.ParsedFile, node syntax.Node, off int, seg string) Hit {
	start, _ := pf.AST().Range(node)

	segStart := toLSPPosition(pf, syntax.Position{
		Line: start.Line, Col: start.Col, Offset: start.Offset + off,
	})
	segEnd := toLSPPosition(pf, syntax.Position{
		Line: start.Line, Col: start.Col, Offset: start.Offset + off + len(seg),
	})

	return Hit{
		File:  pf.URI(),
		Range: protocol.Range{Start: segStart, End: segEnd},
		Text:  seg,
		Kind:  cache.RefConstValue,
	}
}

// searchFiles returns the file itself followed by its transitive
// dependents (every file that includes it, directly or through other
// includes), deduplicated. Resolution is transitive, so reference search
// must be too: a definition reached through a chain of includes is
// referenced from every file in the chain.
func (x *Index) searchFiles(file uri.URI) []uri.URI {
	files := []uri.URI{file}

	for _, dep := range x.view.Dependents(file) {
		if dep != file {
			files = append(files, dep)
		}
	}

	return files
}

// matches returns hits for references in pf whose kind is in kinds and
// whose bare name equals bareName(name).
func (x *Index) matches(pf *cache.ParsedFile, name string, kinds []cache.RefKind) []Hit {
	var out []Hit

	for _, r := range pf.Index().References() {
		if !referenceKind(r.Kind, kinds) {
			continue
		}

		if bareName(r.Name) != bareName(name) {
			continue
		}

		out = append(out, Hit{
			File:  pf.URI(),
			Range: nodeRange(pf, r.Node),
			Text:  r.Name,
			Kind:  r.Kind,
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

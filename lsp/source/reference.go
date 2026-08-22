package source

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"go.lsp.dev/protocol"
	"go.lsp.dev/uri"

	"github.com/karitham/thrift-ls/lsp/cache"
	"github.com/karitham/thrift-ls/syntax"
)

// highlightKind maps a reference kind to the highlight type.
var highlightKind = map[cache.RefKind]protocol.DocumentHighlightKind{
	cache.RefFieldType:      protocol.DocumentHighlightKindText,
	cache.RefSignatureType:  protocol.DocumentHighlightKindText,
	cache.RefConstValue:     protocol.DocumentHighlightKindRead,
	cache.RefServiceExtends: protocol.DocumentHighlightKindText,
}

// Reference returns every usage of the symbol at pos, including usage
// in files that include the definition.
func Reference(ctx context.Context, view *cache.View, file uri.URI, pos protocol.Position) ([]protocol.Location, error) {
	refs, err := searchReferences(ctx, view, file, pos)
	if err != nil {
		return nil, err
	}

	locs := make([]protocol.Location, 0, len(refs))
	for _, r := range refs {
		if r.loc.URI == "" {
			continue
		}

		locs = append(locs, r.loc)
	}

	return locs, nil
}

// Highlight returns the document highlight ranges for the symbol at pos.
func Highlight(ctx context.Context, view *cache.View, file uri.URI, pos protocol.Position) ([]protocol.DocumentHighlight, error) {
	pf, target, err := resolveTarget(ctx, view, file, pos)
	if err != nil {
		return nil, err
	}

	refs, err := searchReferences(ctx, view, file, pos)
	if err != nil {
		return nil, err
	}

	out := make([]protocol.DocumentHighlight, 0, len(refs)+1)
	seen := map[protocol.Range]bool{}

	add := func(r protocol.Range, kind protocol.DocumentHighlightKind) {
		if seen[r] {
			return
		}

		seen[r] = true
		out = append(out, protocol.DocumentHighlight{Range: r, Kind: kind})
	}

	if id := target.identifier(); id != nil {
		add(nodeRange(pf, id), protocol.DocumentHighlightKindText)
	}

	for _, r := range refs {
		if r.loc.URI == file {
			kind, ok := highlightKind[r.kind]
			if !ok {
				kind = protocol.DocumentHighlightKindText
			}

			add(r.loc.Range, kind)
		}
	}

	sort.Slice(out, func(i, j int) bool {
		a, b := out[i].Range.Start, out[j].Range.Start
		if a.Line != b.Line {
			return a.Line < b.Line
		}

		return a.Character < b.Character
	})

	return out, nil
}

// searchReferences dispatches to the reference search for the target kind.
func searchReferences(ctx context.Context, view *cache.View, file uri.URI, pos protocol.Position) ([]indexHit, error) {
	pf, target, err := resolveTarget(ctx, view, file, pos)
	if err != nil {
		return nil, err
	}

	ix := NewIndex(view)

	switch target.kind {
	case TargetTypeName:
		return searchTypeNameRefs(ctx, ix, view, pf, target)
	case TargetConstValue:
		return searchConstValueRefs(ctx, ix, view, pf, target)
	case TargetService:
		return searchServiceRefs(ctx, ix, view, file, target.identifier().Text)
	case TargetDefinition:
		return searchDefRefs(ctx, ix, view, file, pf, target)
	}

	return nil, nil
}

// searchTypeNameRefs resolves the type reference and finds all usages.
func searchTypeNameRefs(ctx context.Context, ix *Index, view *cache.View, pf *cache.ParsedFile, target *target) ([]indexHit, error) {
	ft := target.parent.(*syntax.FieldType)
	typeName := typeReferenceName(ft)
	if typeName == "" || IsBasicType(typeName) {
		return nil, nil
	}

	def, err := ix.ResolveType(ctx, pf, ft)
	if err != nil {
		return nil, err
	}

	if def == nil {
		return nil, nil
	}

	loc, err := jumpInFile(ctx, view, def.File, def.Name)
	if err != nil {
		return nil, err
	}

	hits := []indexHit{{loc: loc, text: def.Name.Text, kind: cache.RefFieldType}}

	refs, err := ix.ReferencesTo(ctx, def, refKindsFor(def.Kind)...)
	if err != nil {
		return nil, err
	}

	for _, h := range refs {
		hits = append(hits, indexHit{loc: protocol.Location{URI: h.File, Range: h.Range}, text: h.Text, kind: h.Kind})
	}

	return hits, nil
}

// searchConstValueRefs resolves a const-value or enum-value reference.
func searchConstValueRefs(ctx context.Context, ix *Index, view *cache.View, pf *cache.ParsedFile, target *target) ([]indexHit, error) {
	value := target.node.(*syntax.ConstValue)

	def, err := ix.ResolveValue(ctx, pf, value)
	if err != nil {
		return nil, err
	}

	if def == nil {
		return nil, nil
	}

	loc, err := jumpInFile(ctx, view, def.File, def.Name)
	if err != nil {
		return nil, err
	}

	hits := []indexHit{{loc: loc, text: def.Name.Text, kind: cache.RefConstValue}}

	refs, err := ix.ReferencesTo(ctx, def, cache.RefConstValue)
	if err != nil {
		return nil, err
	}

	for _, h := range refs {
		hits = append(hits, indexHit{loc: protocol.Location{URI: h.File, Range: h.Range}, text: h.Text, kind: h.Kind})
	}

	return hits, nil
}

// searchServiceRefs finds the includes and extends referencing a service.
func searchServiceRefs(ctx context.Context, ix *Index, view *cache.View, file uri.URI, svcName string) ([]indexHit, error) {
	pf, err := view.Parse(ctx, file)
	if err != nil || pf.AST() == nil {
		return nil, err
	}

	def, err := ix.ResolveService(ctx, pf, &syntax.Identifier{Text: svcName})
	if err != nil || def == nil {
		return nil, nil
	}

	refs, err := ix.ReferencesTo(ctx, def, cache.RefServiceExtends)
	if err != nil {
		return nil, err
	}

	hits := make([]indexHit, 0, len(refs))
	for _, h := range refs {
		hits = append(hits, indexHit{loc: protocol.Location{URI: h.File, Range: h.Range}, text: h.Text, kind: h.Kind})
	}

	return hits, nil
}

// searchDefRefs handles references from a definition name: struct, union,
// exception, enum, typedef, const, enum value, and service names.
func searchDefRefs(ctx context.Context, ix *Index, view *cache.View, file uri.URI, pf *cache.ParsedFile, target *target) ([]indexHit, error) {
	id := target.identifier()
	if id == nil {
		return nil, nil
	}

	parent := target.parent

	var def *Resolved
	var kinds []cache.RefKind

	switch parent.(type) {
	case *syntax.Const:
		def = defFromNode(pf, parent)
		kinds = []cache.RefKind{cache.RefConstValue}
	case *syntax.EnumValue:
		def = defFromNode(pf, id)
		kinds = []cache.RefKind{cache.RefConstValue}
	case *syntax.Service:
		svcName := id.Text
		if strings.Contains(svcName, ".") {
			include, _ := parseIdent(file, pf.AST().Includes(), svcName)

			resolver := view.Resolver()
			if path := resolver.GetIncludePath(pf.AST(), include); path != "" {
				file = resolver.ResolveInclude(file, path)
			}
		} else {
			svcName = fmt.Sprintf("%s.%s", includeNameOf(file), svcName)
		}

		return searchServiceRefs(ctx, ix, view, file, svcName)
	default:
		kind, ok := definitionKindOf(parent)
		if !ok {
			return nil, nil
		}

		if _, ok := validReferenceDefinitionType[kind]; !ok {
			return nil, nil
		}

		def = defFromNode(pf, parent)
		kinds = refKindsFor(kind)

		// Renaming an enum definition also touches value positions
		// qualified with the enum name ("Color.RED").
		if kind == DefinitionEnum {
			kinds = append(kinds, cache.RefConstValue)
		}
	}

	refs, err := ix.ReferencesTo(ctx, def, kinds...)
	if err != nil {
		return nil, err
	}

	hits := make([]indexHit, 0, len(refs))
	for _, h := range refs {
		hits = append(hits, indexHit{loc: protocol.Location{URI: h.File, Range: h.Range}, text: h.Text, kind: h.Kind})
	}

	return hits, nil
}

// definitionKindOf maps a definition node to its kind.
func definitionKindOf(n syntax.Node) (DefinitionKind, bool) {
	switch v := n.(type) {
	case *syntax.Struct:
		switch v.Kind {
		case syntax.StructDecl:
			return DefinitionStruct, true
		case syntax.UnionDecl:
			return DefinitionUnion, true
		case syntax.ExceptionDecl:
			return DefinitionException, true
		}
	case *syntax.Enum:
		return DefinitionEnum, true
	case *syntax.Typedef:
		return DefinitionTypedef, true
	case *syntax.Const:
		return DefinitionConst, true
	case *syntax.Service:
		return DefinitionService, true
	}

	return DefinitionNone, false
}

// indexHit is a referenceSearch result: location, text as written, and
// reference kind — the replacement for referenceHit.
type indexHit struct {
	loc  protocol.Location
	text string
	kind cache.RefKind
}

// validReferenceDefinitionType lists definition kinds that can have type
// references (i.e. everything except services and consts).
var validReferenceDefinitionType = map[DefinitionKind]struct{}{
	DefinitionStruct:    {},
	DefinitionUnion:     {},
	DefinitionEnum:      {},
	DefinitionException: {},
	DefinitionTypedef:   {},
}

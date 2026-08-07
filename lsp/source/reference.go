package source

import (
	"context"
	"fmt"
	"log/slog"
	"sort"
	"strings"

	"go.lsp.dev/protocol"
	"go.lsp.dev/uri"

	"github.com/karitham/thrift-ls/lsp/cache"
	"github.com/karitham/thrift-ls/syntax"
)

var validReferenceDefinitionType = map[DefinitionKind]struct{}{
	DefinitionStruct:    {},
	DefinitionUnion:     {},
	DefinitionEnum:      {},
	DefinitionException: {},
	DefinitionTypedef:   {},
}

// Reference returns the locations of all references to the definition under
// the cursor: type definitions, constant values, enum values, and services.
func Reference(ctx context.Context, ss *cache.Snapshot, file uri.URI, pos protocol.Position) (res []protocol.Location, err error) {
	pf, target, err := resolveTarget(ctx, ss, file, pos)
	if err != nil {
		return nil, err
	}

	refs, err := searchReferences(ctx, ss, file, pf, target)
	if err != nil {
		return nil, err
	}

	return hits(refs), nil
}

// Highlight returns the references of the identifier at pos within the
// same file, for document highlighting. The identifier itself is always
// included, so the cursor word stays highlighted.
func Highlight(ctx context.Context, ss *cache.Snapshot, file uri.URI, pos protocol.Position) ([]protocol.DocumentHighlight, error) {
	pf, target, err := resolveTarget(ctx, ss, file, pos)
	if err != nil {
		return nil, err
	}

	refs, err := searchReferences(ctx, ss, file, pf, target)
	if err != nil {
		return nil, err
	}

	out := make([]protocol.DocumentHighlight, 0, len(refs)+1)
	seen := map[protocol.Range]bool{}

	add := func(r protocol.Range) {
		if seen[r] {
			return
		}

		seen[r] = true
		out = append(out, protocol.DocumentHighlight{Range: r, Kind: protocol.DocumentHighlightKindText})
	}

	// The identifier at the cursor is always highlighted; the reference
	// search already includes it when the cursor sits on a usage, so the
	// set dedups.
	if id := target.identifier(); id != nil {
		add(nodeRange(pf.AST(), id))
	}

	for _, r := range refs {
		if r.loc.URI == file {
			add(r.loc.Range)
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
func searchReferences(ctx context.Context, ss *cache.Snapshot, file uri.URI, pf *cache.ParsedFile, target *target) ([]referenceHit, error) {
	switch target.kind {
	case TargetTypeName:
		return searchTypeNameReferences(ctx, ss, file, pf, target)
	case TargetConstValue:
		return searchConstValueReferences(ctx, ss, file, pf, target)
	case TargetService:
		return searchServiceReferences(ctx, ss, file, target.identifier().Text)
	case TargetDefinition:
		return searchDefinitionReferences(ctx, ss, file, pf, target)
	}

	return nil, nil
}

// searchDefinitionReferences handles references from a definition name:
// const and enum value names reference constant usages; struct, union,
// enum, exception, and typedef names reference type usages.
func searchDefinitionReferences(ctx context.Context, ss *cache.Snapshot, file uri.URI, pf *cache.ParsedFile, target *target) (res []referenceHit, err error) {
	id := target.identifier()
	if id == nil {
		return res, err
	}

	parent := target.parent
	switch parent.(type) {
	case *syntax.Const:
		typeName := fmt.Sprintf("%s.%s", includeNameOf(file), id.Text)

		return searchConstValueIdentifierReferences(ctx, ss, file, typeName)
	case *syntax.EnumValue:
		enum, ok := grandparent(target.path).(*syntax.Enum)
		if !ok {
			return res, err
		}

		typeName := fmt.Sprintf("%s.%s.%s", includeNameOf(file), enum.Name.Text, id.Text)

		return searchConstValueIdentifierReferences(ctx, ss, file, typeName)
	case *syntax.Service:
		svcName := id.Text
		if strings.Contains(svcName, ".") {
			include, _ := parseIdent(file, pf.AST().Includes(), svcName)

			resolver := ss.Resolver()
			if path := resolver.GetIncludePath(pf.AST(), include); path != "" {
				file = resolver.ResolveInclude(file, path)
			}
		} else {
			svcName = fmt.Sprintf("%s.%s", includeNameOf(file), svcName)
		}

		return searchServiceReferences(ctx, ss, file, svcName)
	}

	kind, ok := definitionKindOf(parent)
	if !ok {
		return res, err
	}

	if _, ok := validReferenceDefinitionType[kind]; !ok {
		return res, err
	}

	typeName := fmt.Sprintf("%s.%s", includeNameOf(file), id.Text)

	return searchIdentifierReferences(ctx, ss, file, typeName, kind)
}

func grandparent(path []syntax.Node) syntax.Node {
	if len(path) < 3 {
		return nil
	}

	return path[len(path)-3]
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

func searchTypeNameReferences(ctx context.Context, ss *cache.Snapshot, file uri.URI, pf *cache.ParsedFile, target *target) (res []referenceHit, err error) {
	res = make([]referenceHit, 0)
	ft := target.parent.(*syntax.FieldType)

	typeName := typeReferenceName(ft)
	if typeName == "" || IsBasicType(typeName) {
		return res, err
	}

	// Search the type definition.
	definitionFile, identifierNode, definitionType, err := FindTypeDefinition(ctx, ss, file, pf.AST(), ft)
	if err != nil {
		return res, err
	}

	if identifierNode == nil {
		return res, err
	}

	loc, err := jumpInFile(ctx, ss, definitionFile, identifierNode)
	if err != nil {
		return res, err
	}

	res = append(res, referenceHit{loc: loc, text: identifierNode.Text})

	// Search usages of the type name.
	locations, err := searchIdentifierReferences(ctx, ss, definitionFile, typeName, definitionType)
	if err != nil {
		return res, err
	}

	res = append(res, locations...)

	return res, err
}

func searchServiceReferences(ctx context.Context, ss *cache.Snapshot, file uri.URI, svcName string) (res []referenceHit, err error) {
	slog.Debug("searching service references", "file", file, "svcName", svcName)

	locations, err := searchServiceDefinitionReferences(ctx, ss, file, strings.TrimPrefix(svcName, fmt.Sprintf("%s.", includeNameOf(file))))
	if err != nil {
		return nil, err
	}

	res = append(res, locations...)

	for _, referenceFile := range referenceFiles(ss, file) {
		// References in including files may use the bare name or the
		// include-qualified name; the match function accepts both.
		locations, err := searchServiceDefinitionReferences(ctx, ss, referenceFile, svcName)
		if err != nil {
			return nil, err
		}

		res = append(res, locations...)
	}

	return res, err
}

// referenceFiles returns the files that include the given file, per the
// include graph.
func referenceFiles(ss *cache.Snapshot, file uri.URI) []uri.URI {
	includeNode := ss.Graph().Get(file)
	if includeNode == nil {
		return nil
	}

	if len(includeNode.InDegree()) == 0 && len(includeNode.OutDegree()) == 0 {
		ss.Graph().Debug()
	}

	return includeNode.InDegree()
}

func searchServiceDefinitionReferences(ctx context.Context, ss *cache.Snapshot, file uri.URI, svcName string) (res []referenceHit, err error) {
	pf, err := ss.Parse(ctx, file)
	if err != nil {
		return res, err
	}

	if pf.AST() == nil {
		return res, err
	}

	for _, svc := range pf.AST().Services() {
		if svc.Extends == nil {
			continue
		}

		// Accept both the bare name and the include-qualified literal.
		if bareName(svc.Extends.Text) != bareName(svcName) {
			continue
		}

		res = append(res, referenceHit{loc: jump(file, pf.AST(), svc.Extends), text: svc.Extends.Text})
	}

	return res, err
}

func searchIdentifierReferences(ctx context.Context, ss *cache.Snapshot, file uri.URI, typeName string, definitionType DefinitionKind) (res []referenceHit, err error) {
	slog.Debug("searching identifier references", "file", file, "typeName", typeName)

	locations, err := searchDefinitionIdentifierReferences(ctx, ss, file,
		strings.TrimPrefix(typeName, fmt.Sprintf("%s.", includeNameOf(file))), definitionType)
	if err != nil {
		return nil, err
	}

	res = append(res, locations...)

	for _, referenceFile := range referenceFiles(ss, file) {
		// References in including files may use the bare name or the
		// include-qualified name; the match function accepts both.
		locations, err := searchDefinitionIdentifierReferences(ctx, ss, referenceFile, typeName, definitionType)
		if err != nil {
			return nil, err
		}

		res = append(res, locations...)
	}

	return res, err
}

// searchDefinitionIdentifierReferences finds type references matching
// typeName in one file: field types, function return types, arguments,
// throws, typedef types, and const types.
func searchDefinitionIdentifierReferences(ctx context.Context, ss *cache.Snapshot, file uri.URI, typeName string, definitionType DefinitionKind) (res []referenceHit, err error) {
	pf, err := ss.Parse(ctx, file)
	if err != nil {
		return res, err
	}

	if pf.AST() == nil {
		return res, err
	}

	jumpFieldType := func(ft *syntax.FieldType) {
		if ft == nil {
			return
		}

		// Accept every reference form of the same definition: bare
		// ("Test"), include-name-qualified ("user.Test"), and
		// file-base-qualified ("user.thrift.Test").
		if bareName(typeReferenceName(ft)) != bareName(typeName) {
			return
		}

		res = append(res, referenceHit{loc: jump(file, pf.AST(), ft.Ident), text: ft.Ident.Text})
	}

	var searchFieldType func(ft *syntax.FieldType)

	searchFieldType = func(ft *syntax.FieldType) {
		if ft == nil {
			return
		}

		if ft.KeyType != nil {
			searchFieldType(ft.KeyType)
		}

		if ft.ValueType != nil {
			searchFieldType(ft.ValueType)
		}

		jumpFieldType(ft)
	}
	jumpField := func(field *syntax.Field) {
		searchFieldType(field.Type)
	}
	processStructLike := func(fields []*syntax.Field) {
		for _, field := range fields {
			jumpField(field)
		}
	}

	for _, svc := range pf.AST().Services() {
		for _, fn := range svc.Functions {
			searchFieldType(fn.Type)
			processStructLike(fn.Args)

			if fn.Throws != nil {
				processStructLike(fn.Throws.Fields)
			}
		}
	}

	if definitionType == DefinitionException {
		return res, err
	}

	for _, st := range pf.AST().Structs() {
		processStructLike(st.Fields)
	}

	for _, st := range pf.AST().Unions() {
		processStructLike(st.Fields)
	}

	for _, st := range pf.AST().Exceptions() {
		processStructLike(st.Fields)
	}

	for _, typedef := range pf.AST().Typedefs() {
		searchFieldType(typedef.Type)
	}

	for _, cst := range pf.AST().Consts() {
		searchFieldType(cst.Type)
	}

	return res, err
}

func searchConstValueReferences(ctx context.Context, ss *cache.Snapshot, file uri.URI, pf *cache.ParsedFile, target *target) (res []referenceHit, err error) {
	res = make([]referenceHit, 0)
	value := target.node.(*syntax.ConstValue)

	definitionFile, identifierNode, err := FindConstValueDefinition(ctx, ss, file, pf.AST(), value)
	if err != nil {
		return res, err
	}

	if identifierNode == nil {
		return res, err
	}

	loc, err := jumpInFile(ctx, ss, definitionFile, identifierNode)
	if err != nil {
		return res, err
	}

	res = append(res, referenceHit{loc: loc, text: identifierNode.Text})

	locations, err := searchConstValueIdentifierReferences(ctx, ss, definitionFile, value.Text)
	if err != nil {
		return res, err
	}

	res = append(res, locations...)

	return res, err
}

// searchConstValueIdentifierReferences finds usages of a const or enum
// value name: field default values and const values.
func searchConstValueIdentifierReferences(ctx context.Context, ss *cache.Snapshot, file uri.URI, valueName string) (res []referenceHit, err error) {
	locations, err := searchConstValueIdentifierReference(ctx, ss, file, strings.TrimPrefix(valueName, fmt.Sprintf("%s.", includeNameOf(file))))
	if err != nil {
		return nil, err
	}

	res = append(res, locations...)

	for _, referenceFile := range referenceFiles(ss, file) {
		// References in including files may use the bare name or the
		// include-qualified name; the match function accepts both.
		locations, err := searchConstValueIdentifierReference(ctx, ss, referenceFile, valueName)
		if err != nil {
			return nil, err
		}

		res = append(res, locations...)
	}

	return res, err
}

func searchConstValueIdentifierReference(ctx context.Context, ss *cache.Snapshot, file uri.URI, valueName string) (res []referenceHit, err error) {
	pf, err := ss.Parse(ctx, file)
	if err != nil {
		return res, err
	}

	if pf.AST() == nil {
		return res, err
	}

	jumpValue := func(v *syntax.ConstValue) {
		if v != nil && v.Kind == syntax.ValueIdent && bareName(v.Text) == bareName(valueName) {
			res = append(res, referenceHit{loc: jump(file, pf.AST(), v), text: v.Text})
		}
	}
	processStructLike := func(fields []*syntax.Field) {
		for _, field := range fields {
			jumpValue(field.Value)
		}
	}

	for _, st := range pf.AST().Structs() {
		processStructLike(st.Fields)
	}

	for _, st := range pf.AST().Unions() {
		processStructLike(st.Fields)
	}

	for _, st := range pf.AST().Exceptions() {
		processStructLike(st.Fields)
	}

	for _, cst := range pf.AST().Consts() {
		jumpValue(cst.Value)
	}

	for _, svc := range pf.AST().Services() {
		for _, fn := range svc.Functions {
			processStructLike(fn.Args)

			if fn.Throws != nil {
				processStructLike(fn.Throws.Fields)
			}
		}
	}

	return res, err
}

package source

import (
	"context"
	"errors"
	"fmt"
	"log/slog"

	"go.lsp.dev/protocol"
	"go.lsp.dev/uri"

	"github.com/karitham/thrift-ls/lsp/cache"
	"github.com/karitham/thrift-ls/syntax"
)

// UnusedIncludeCheck reports an include no reference in the file resolves
// into. Unused includes bloat the compile and make the dependency graph
// look worse than it is.
type UnusedIncludeCheck struct{}

func (c *UnusedIncludeCheck) Name() string {
	return "UnusedIncludeCheck"
}

func (c *UnusedIncludeCheck) Diagnostic(ctx context.Context, ss *cache.Snapshot, changeFiles []uri.URI) (DiagnosticResult, error) {
	res := make(DiagnosticResult)

	for _, file := range changeFiles {
		items, err := c.diagnostic(ctx, ss, file)
		if err != nil {
			return nil, err
		}

		res[file] = items
	}

	return res, nil
}

func (c *UnusedIncludeCheck) diagnostic(ctx context.Context, ss *cache.Snapshot, file uri.URI) ([]protocol.Diagnostic, error) {
	pf, err := ss.Parse(ctx, file)
	if err != nil {
		return nil, err
	}

	if pf.AST() == nil {
		return nil, errors.New("parse ast failed")
	}

	for _, err := range pf.Errors() {
		slog.Debug("parse failed", "err", err)
	}

	return unusedIncludeDiagnostics(ctx, ss, file, pf), nil
}

// unusedIncludeDiagnostics warns on every include whose target file never
// receives a resolved reference from this document. A reference is a type
// name, a constant value identifier, or a service extends clause, used
// qualified ("base.Type") or unqualified (resolving through the include
// chain).
func unusedIncludeDiagnostics(ctx context.Context, ss *cache.Snapshot, file uri.URI, pf *cache.ParsedFile) []protocol.Diagnostic {
	includes := pf.AST().Includes()
	if len(includes) == 0 {
		return nil
	}

	used := usedIncludes(ctx, ss, file, pf)

	var ret []protocol.Diagnostic

	for _, inc := range includes {
		if used[inc] {
			continue
		}

		ret = append(ret, protocol.Diagnostic{
			Range:    nodeRange(pf, inc),
			Severity: protocol.DiagnosticSeverityWarning,
			Code:     protocol.String(CodeUnusedInclude),
			Source:   protocol.NewOptional("thrift-ls"),
			Message:  protocol.String(fmt.Sprintf("unused include %q", inc.PathText())),
		})
	}

	return ret
}

// usedIncludes marks every include that at least one reference in the
// document resolves into. Resolution goes through the definition finders,
// which handle both qualified ("base.Type") and unqualified names that
// resolve through the include chain.
func usedIncludes(ctx context.Context, ss *cache.Snapshot, file uri.URI, pf *cache.ParsedFile) map[*syntax.Include]bool {
	resolver := ss.Resolver()
	includeByFile := make(map[uri.URI]*syntax.Include)

	for _, inc := range pf.AST().Includes() {
		if p := inc.PathText(); p != "" {
			includeByFile[resolver.ResolveInclude(file, p)] = inc
		}
	}

	used := make(map[*syntax.Include]bool)
	seen := make(map[string]bool)

	for _, name := range referencedNames(pf.AST()) {
		if seen[name] {
			continue
		}
		seen[name] = true

		if dst, ok := resolveReferenceFile(ctx, ss, file, pf.AST(), name); ok {
			if inc, ok := includeByFile[dst]; ok {
				used[inc] = true
			}
		}
	}

	return used
}

// resolveReferenceFile returns the file a reference name resolves to, or
// false when it resolves nowhere or into the current file. Type, const
// value, and service references are all considered; each finder resolves
// include-qualified names itself.
func resolveReferenceFile(ctx context.Context, ss *cache.Snapshot, file uri.URI, ast *syntax.Document, name string) (uri.URI, bool) {
	ft := &syntax.FieldType{Kind: syntax.TypeIdent, Ident: &syntax.Identifier{Text: name}}
	if dst, id, _, err := FindTypeDefinition(ctx, ss, file, ast, ft); err == nil && id != nil && dst != file {
		return dst, true
	}

	cv := &syntax.ConstValue{Kind: syntax.ValueIdent, Text: name}
	if dst, id, err := FindConstValueDefinition(ctx, ss, file, ast, cv); err == nil && id != nil && dst != file {
		return dst, true
	}

	id := &syntax.Identifier{Text: name}
	if dst, found, err := FindServiceDefinition(ctx, ss, file, ast, id); err == nil && found != nil && dst != file {
		return dst, true
	}

	return "", false
}

// referencedNames collects every identifier used in a reference position:
// field, argument, throws, return, typedef, and const types; const value
// identifiers; and service extends.
func referencedNames(doc *syntax.Document) []string {
	var names []string

	addType := func(t *syntax.FieldType) {
		walkTypeIdents(t, func(text string) { names = append(names, text) })
	}
	addValue := func(v *syntax.ConstValue) {
		walkValueIdents(v, func(text string) { names = append(names, text) })
	}

	doc.WalkFieldLists(func(fields []*syntax.Field, _ syntax.FieldListKind) {
		for _, f := range fields {
			addType(f.Type)
			addValue(f.Value)
		}
	})

	for _, td := range doc.Typedefs() {
		addType(td.Type)
	}

	for _, cs := range doc.Consts() {
		addType(cs.Type)
		addValue(cs.Value)
	}

	for _, svc := range doc.Services() {
		if svc.Extends != nil {
			names = append(names, svc.Extends.Text)
		}

		for _, fn := range svc.Functions {
			addType(fn.Type)

			for _, arg := range fn.Args {
				addType(arg.Type)
			}

			if fn.Throws != nil {
				for _, f := range fn.Throws.Fields {
					addType(f.Type)
					addValue(f.Value)
				}
			}
		}
	}

	return names
}

// walkTypeIdents calls f with every identifier of a type reference,
// including nested container types.
func walkTypeIdents(t *syntax.FieldType, f func(string)) {
	if t == nil {
		return
	}

	switch t.Kind {
	case syntax.TypeIdent:
		if t.Ident != nil {
			f(t.Ident.Text)
		}
	case syntax.TypeMap, syntax.TypeList, syntax.TypeSet:
		walkTypeIdents(t.KeyType, f)
		walkTypeIdents(t.ValueType, f)
	}
}

// walkValueIdents calls f with every identifier of a constant value,
// descending into maps and lists.
func walkValueIdents(v *syntax.ConstValue, f func(string)) {
	if v == nil {
		return
	}

	switch v.Kind {
	case syntax.ValueIdent:
		f(v.Text)
	case syntax.ValueList:
		for _, item := range v.List {
			walkValueIdents(item, f)
		}
	case syntax.ValueMap:
		for _, entry := range v.Map {
			walkValueIdents(entry.Key, f)
			walkValueIdents(entry.Value, f)
		}
	}
}

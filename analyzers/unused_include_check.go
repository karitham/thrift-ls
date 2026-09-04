package analyzers

import (
	"context"
	"fmt"

	"go.lsp.dev/uri"

	"github.com/karitham/thrift-ls/sema"
	"github.com/karitham/thrift-ls/store"
	"github.com/karitham/thrift-ls/syntax"
)

// UnusedIncludeCheck reports an include no reference in the file resolves
// into. Unused includes bloat the compile and make the dependency graph
// look worse than it is.
type UnusedIncludeCheck struct{}

func (c *UnusedIncludeCheck) Name() string {
	return "UnusedIncludeCheck"
}

func (c *UnusedIncludeCheck) AnalyzeFile(ctx context.Context, f sema.File) ([]sema.Diagnostic, error) {
	return unusedIncludeDiagnostics(ctx, f, f.PF), nil
}

// unusedIncludeDiagnostics warns on every include whose target file never
// receives a resolved reference from this document. A reference is a type
// name, a constant value identifier, or a service extends clause, used
// qualified ("base.Type") or unqualified (resolving through the include
// chain).
func unusedIncludeDiagnostics(ctx context.Context, f sema.File, pf *store.ParsedFile) []sema.Diagnostic {
	includes := pf.AST().Includes()
	if len(includes) == 0 {
		return nil
	}

	used := usedIncludes(ctx, f, pf)

	var ret []sema.Diagnostic

	content, contentErr := pf.Content()

	for _, inc := range includes {
		if used[inc] {
			continue
		}

		d := sema.Diagnostic{
			Span:     sema.SpanOf(pf, inc),
			Severity: sema.SeverityWarning,
			Code:     sema.CodeUnusedInclude,
			Message:  fmt.Sprintf("unused include %q", inc.PathText()),
		}

		// The include statement is a statement of its own: the fix
		// deletes its whole line.
		if contentErr == nil {
			d.Fixes = append(d.Fixes, sema.Fix{
				Title: fmt.Sprintf("Remove unused include %q", inc.PathText()),
				Edits: []sema.Edit{{Span: sema.LineSpan(content, d.Span.Start)}},
			})
		}

		ret = append(ret, d)
	}

	return ret
}

// usedIncludes marks every include that at least one reference in the
// document resolves into. Resolution goes through the run's shared
// cross-file index, which handles both qualified ("base.Type") and
// unqualified names that resolve through the include chain.
func usedIncludes(ctx context.Context, f sema.File, pf *store.ParsedFile) map[*syntax.Include]bool {
	resolver := f.View().Resolver()
	includeByFile := make(map[uri.URI]*syntax.Include)

	for _, inc := range pf.AST().Includes() {
		if p := inc.PathText(); p != "" {
			includeByFile[resolver.ResolveInclude(ctx, f.URI, p)] = inc
		}
	}

	used := make(map[*syntax.Include]bool)
	seen := make(map[string]bool)
	ix := f.Index()

	for _, ref := range pf.Index().References() {
		if seen[ref.Name] {
			continue
		}
		seen[ref.Name] = true

		if dst, ok := resolveReferenceFile(ctx, ix, pf, ref); ok {
			if inc, ok := includeByFile[dst]; ok {
				used[inc] = true
			}
		}
	}

	return used
}

// resolveReferenceFile returns the file the reference resolves to, or
// false when it resolves nowhere or into the current file. Type, const
// value, and service references resolve through their own finder.
func resolveReferenceFile(ctx context.Context, ix *sema.Index, pf *store.ParsedFile, ref store.Reference) (uri.URI, bool) {
	var def *sema.Resolved
	var err error

	switch ref.Kind {
	case store.RefFieldType, store.RefSignatureType, store.RefAnnotationType:
		id, ok := ref.Node.(*syntax.Identifier)
		if !ok {
			return "", false
		}

		ft := &syntax.FieldType{Kind: syntax.TypeIdent, Ident: id}
		def, err = ix.ResolveType(ctx, pf, ft)
	case store.RefConstValue:
		cv, ok := ref.Node.(*syntax.ConstValue)
		if !ok {
			return "", false
		}

		def, err = ix.ResolveValue(ctx, pf, cv)
	case store.RefServiceExtends:
		id, ok := ref.Node.(*syntax.Identifier)
		if !ok {
			return "", false
		}

		def, err = ix.ResolveService(ctx, pf, id)
	}

	if err != nil || def == nil || def.File == pf.URI() {
		return "", false
	}

	return def.File, true
}

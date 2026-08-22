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

func (c *UnusedIncludeCheck) Diagnostic(ctx context.Context, view *cache.View, changeFiles []uri.URI) (DiagnosticResult, error) {
	res := make(DiagnosticResult)

	for _, file := range changeFiles {
		items, err := c.diagnostic(ctx, view, file)
		if err != nil {
			return nil, err
		}

		res[file] = items
	}

	return res, nil
}

func (c *UnusedIncludeCheck) diagnostic(ctx context.Context, view *cache.View, file uri.URI) ([]protocol.Diagnostic, error) {
	pf, err := view.Parse(ctx, file)
	if err != nil {
		return nil, err
	}

	if pf.AST() == nil {
		return nil, errors.New("parse ast failed")
	}

	for _, err := range pf.Errors() {
		slog.Debug("parse failed", "err", err)
	}

	return unusedIncludeDiagnostics(ctx, view, file, pf), nil
}

// unusedIncludeDiagnostics warns on every include whose target file never
// receives a resolved reference from this document. A reference is a type
// name, a constant value identifier, or a service extends clause, used
// qualified ("base.Type") or unqualified (resolving through the include
// chain).
func unusedIncludeDiagnostics(ctx context.Context, view *cache.View, file uri.URI, pf *cache.ParsedFile) []protocol.Diagnostic {
	includes := pf.AST().Includes()
	if len(includes) == 0 {
		return nil
	}

	used := usedIncludes(ctx, view, file, pf)

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
// document resolves into. Resolution goes through the per-file reference
// index, which handles both qualified ("base.Type") and unqualified names
// that resolve through the include chain.
func usedIncludes(ctx context.Context, view *cache.View, file uri.URI, pf *cache.ParsedFile) map[*syntax.Include]bool {
	resolver := view.Resolver()
	includeByFile := make(map[uri.URI]*syntax.Include)

	for _, inc := range pf.AST().Includes() {
		if p := inc.PathText(); p != "" {
			includeByFile[resolver.ResolveInclude(file, p)] = inc
		}
	}

	used := make(map[*syntax.Include]bool)
	seen := make(map[string]bool)
	ix := NewIndex(view)

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
func resolveReferenceFile(ctx context.Context, ix *Index, pf *cache.ParsedFile, ref cache.Reference) (uri.URI, bool) {
	var def *Resolved
	var err error

	switch ref.Kind {
	case cache.RefFieldType, cache.RefSignatureType:
		id, ok := ref.Node.(*syntax.Identifier)
		if !ok {
			return "", false
		}

		ft := &syntax.FieldType{Kind: syntax.TypeIdent, Ident: id}
		def, err = ix.ResolveType(ctx, pf, ft)
	case cache.RefConstValue:
		cv, ok := ref.Node.(*syntax.ConstValue)
		if !ok {
			return "", false
		}

		def, err = ix.ResolveValue(ctx, pf, cv)
	case cache.RefServiceExtends:
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

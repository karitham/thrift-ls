package analyzers

import (
	"context"
	"fmt"
	"strings"

	"github.com/karitham/thrift-ls/sema"
)

// IncludeShadowCheck warns when one include path matches files under more
// than one include path. The nearest include path wins; the warning names
// the rest.
type IncludeShadowCheck struct{}

func (c *IncludeShadowCheck) Name() string {
	return "IncludeShadowCheck"
}

func (c *IncludeShadowCheck) AnalyzeFile(ctx context.Context, f sema.File) ([]sema.Diagnostic, error) {
	pf := f.PF

	resolver := f.View().Resolver()

	var ret []sema.Diagnostic

	for _, inc := range pf.AST().Includes() {
		path := inc.PathText()
		if path == "" {
			continue
		}

		candidates := resolver.ResolveIncludeCandidates(ctx, f.URI, path)
		if len(candidates) < 2 {
			continue
		}

		losers := make([]string, 0, len(candidates)-1)
		for _, cand := range candidates[1:] {
			losers = append(losers, cand.FsPath())
		}

		ret = append(ret, sema.Diagnostic{
			Span:     sema.SpanOf(pf, inc),
			Severity: sema.SeverityWarning,
			Code:     sema.CodeIncludeShadow,
			Message: fmt.Sprintf(
				"include %q matches multiple include paths, using %q; also found in %s",
				path, candidates[0].FsPath(), strings.Join(losers, ", "),
			),
		})
	}

	return ret, nil
}

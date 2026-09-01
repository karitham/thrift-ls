package source

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"go.lsp.dev/protocol"
	"go.lsp.dev/uri"
)

// IncludeShadowCheck warns when one include path matches files under more
// than one include path. The nearest include path wins; the warning names
// the rest.
type IncludeShadowCheck struct{}

func (c *IncludeShadowCheck) Name() string {
	return "IncludeShadowCheck"
}

func (c *IncludeShadowCheck) Diagnostic(ctx context.Context, b *Batch, changeFiles []uri.URI) (DiagnosticResult, error) {
	res := make(DiagnosticResult)

	for _, file := range changeFiles {
		items, err := c.diagnostic(ctx, b, file)
		if err != nil {
			return nil, err
		}

		res[file] = items
	}

	return res, nil
}

func (c *IncludeShadowCheck) diagnostic(ctx context.Context, b *Batch, file uri.URI) ([]protocol.Diagnostic, error) {
	pf, err := b.Tree(ctx, file)
	if err != nil {
		return nil, err
	}

	if pf.AST() == nil {
		return nil, errors.New("parse ast failed")
	}

	resolver := b.View().Resolver()

	var ret []protocol.Diagnostic

	for _, inc := range pf.AST().Includes() {
		path := inc.PathText()
		if path == "" {
			continue
		}

		candidates := resolver.ResolveIncludeCandidates(file, path)
		if len(candidates) < 2 {
			continue
		}

		losers := make([]string, 0, len(candidates)-1)
		for _, cand := range candidates[1:] {
			losers = append(losers, cand.FsPath())
		}

		ret = append(ret, protocol.Diagnostic{
			Range:    nodeRange(pf, inc),
			Severity: protocol.DiagnosticSeverityWarning,
			Code:     protocol.String(CodeIncludeShadow),
			Source:   protocol.NewOptional("thrift-ls"),
			Message: protocol.String(fmt.Sprintf(
				"include %q matches multiple include paths, using %q; also found in %s",
				path, candidates[0].FsPath(), strings.Join(losers, ", "),
			)),
		})
	}

	return ret, nil
}

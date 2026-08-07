package source

import (
	"context"
	"fmt"
	"log/slog"

	"go.lsp.dev/protocol"
	"go.lsp.dev/uri"

	"github.com/karitham/thrift-ls/lsp/cache"
	"github.com/karitham/thrift-ls/syntax"
)

type CycleCheck struct{}

func (c *CycleCheck) Diagnostic(ctx context.Context, ss *cache.Snapshot, changeFiles []uri.URI) (DiagnosticResult, error) {
	includesMap := make(map[uri.URI][]Include)
	for _, file := range changeFiles {
		_ = getIncludes(ctx, ss, file, &includesMap)
	}

	cyclePairs := cycleDetect(&includesMap)

	return cycleToDiagnosticItems(cyclePairs), nil
}

func (c *CycleCheck) Name() string {
	return "CycleCheck"
}

func cycleToDiagnosticItems(pairs []CyclePair) DiagnosticResult {
	diagnostics := make(DiagnosticResult)
	for i := range pairs {
		diagnostics[pairs[i].file] = append(diagnostics[pairs[i].file], cyclePairToDiagnostic(pairs[i]))
	}

	return diagnostics
}

func cyclePairToDiagnostic(pair CyclePair) protocol.Diagnostic {
	res := protocol.Diagnostic{
		Range:    nodeRange(pair.include.pf, pair.include.include),
		Severity: protocol.DiagnosticSeverityWarning,
		Source:   protocol.NewOptional("thrift-ls"),
		Message:  protocol.String(fmt.Sprintf("cycle dependency in %s", pair.include.file)),
	}

	return res
}

type Include struct {
	file    uri.URI
	include *syntax.Include
	pf      *cache.ParsedFile
}

type CyclePair struct {
	file    uri.URI
	include Include
}

func cycleDetect(includesMap *map[uri.URI][]Include) []CyclePair {
	cyclePairs := make([]CyclePair, 0)

	for uri, includes := range *includesMap {
		for _, incI := range includes {
			for _, incJ := range (*includesMap)[incI.file] {
				if uri == incJ.file {
					cyclePairs = append(cyclePairs, CyclePair{
						file:    uri,
						include: incI,
					})
				}
			}
		}
	}

	return cyclePairs
}

func getIncludes(ctx context.Context, ss *cache.Snapshot, file uri.URI, includesMap *map[uri.URI][]Include) error {
	pf, err := ss.Parse(ctx, file)
	if err != nil {
		slog.Error("parse failed", "file", file, "err", err)

		return err
	}

	if pf.AST() == nil {
		slog.Error("parse ast failed", "errs", pf.AggregatedError())

		return pf.AggregatedError()
	}

	includes := pf.AST().Includes()
	resolver := ss.Resolver()

	for i := range includes {
		if includes[i].Path == nil {
			continue
		}

		includeURI := resolver.ResolveInclude(file, includes[i].PathText())
		(*includesMap)[file] = append((*includesMap)[file], Include{
			file:    includeURI,
			include: includes[i],
			pf:      pf,
		})

		if _, ok := (*includesMap)[includeURI]; ok {
			continue
		}

		_ = getIncludes(ctx, ss, includeURI, includesMap)
	}

	return nil
}

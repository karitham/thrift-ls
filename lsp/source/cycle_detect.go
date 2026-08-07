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

// cycleDetect returns every include edge that closes a cycle: the pair
// (file, include file->Y) is reported when Y transitively includes file.
// Cycles of any length are caught, including self-includes.
func cycleDetect(includesMap *map[uri.URI][]Include) []CyclePair {
	// reaches reports whether from can reach target via include edges,
	// cycle-safe via the seen set.
	var reaches func(from, target uri.URI, seen map[uri.URI]bool) bool

	reaches = func(from, target uri.URI, seen map[uri.URI]bool) bool {
		if from == target {
			return true
		}

		if seen[from] {
			return false
		}

		seen[from] = true

		for _, inc := range (*includesMap)[from] {
			if reaches(inc.file, target, seen) {
				return true
			}
		}

		return false
	}

	cyclePairs := make([]CyclePair, 0)

	for file, includes := range *includesMap {
		for _, inc := range includes {
			if reaches(inc.file, file, make(map[uri.URI]bool)) {
				cyclePairs = append(cyclePairs, CyclePair{
					file:    file,
					include: inc,
				})
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

package source

import (
	"context"
	"fmt"
	"log/slog"
	"slices"

	"go.lsp.dev/protocol"
	"go.lsp.dev/uri"

	"github.com/karitham/thrift-ls/lsp/cache"
	"github.com/karitham/thrift-ls/syntax"
)

// CycleCheck reports include edges that close a cycle: the include
// X -> Y is reported when Y transitively includes X back. Cycles of any
// length are caught, including self-includes.
type CycleCheck struct{}

func (c *CycleCheck) Diagnostic(ctx context.Context, ss *cache.Snapshot, changeFiles []uri.URI) (DiagnosticResult, error) {
	closure := make(map[uri.URI][]Include)
	for _, file := range changeFiles {
		_ = getIncludes(ctx, ss, file, &closure)
	}

	diagnostics := make(DiagnosticResult)
	for file, includes := range closure {
		// Reachability comes from the snapshot's include graph: parsing
		// the closure above registered exactly these edges via Register,
		// so there is no second graph to keep in sync. Dependents is
		// cycle-safe, so the walk terminates on the cycles it finds.
		deps := ss.Dependents(file)
		for _, inc := range includes {
			if !slices.Contains(deps, inc.file) {
				continue
			}

			diagnostics[file] = append(diagnostics[file], cycleDiagnostic(inc))
		}
	}

	return diagnostics, nil
}

func (c *CycleCheck) Name() string {
	return "CycleCheck"
}

// cycleDiagnostic builds the warning for one include edge that closes a
// cycle back to its including file.
func cycleDiagnostic(inc Include) protocol.Diagnostic {
	return protocol.Diagnostic{
		Range:    nodeRange(inc.pf, inc.include),
		Severity: protocol.DiagnosticSeverityWarning,
		Code:     protocol.String(CodeIncludeCycle),
		Source:   protocol.NewOptional("thrift-ls"),
		Message:  protocol.String(fmt.Sprintf("cycle dependency in %s", inc.file)),
	}
}

type Include struct {
	file    uri.URI
	include *syntax.Include
	pf      *cache.ParsedFile
}

// getIncludes collects the include closure of file into includesMap: the
// include edges of every file reachable from file, parsed through the
// snapshot so the ParsedFiles (and the graph edges Register records) are
// shared with the rest of the analysis.
func getIncludes(ctx context.Context, ss *cache.Snapshot, file uri.URI, includesMap *map[uri.URI][]Include) error {
	pf, err := ss.Parse(ctx, file)
	if err != nil {
		return err
	}

	if pf.AST() == nil {
		// The file does not parse; the Parse checker reports that. Cycle
		// detection just skips it — its include edges are unknown.
		slog.Debug("cycle check skipped: file does not parse", "file", file)

		return nil
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

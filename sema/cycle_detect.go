package sema

import (
	"context"
	"fmt"
	"log/slog"
	"slices"

	"go.lsp.dev/uri"

	"github.com/karitham/thrift-ls/store"
	"github.com/karitham/thrift-ls/syntax"
)

// CycleCheck reports include edges that close a cycle: the include
// X -> Y is reported when Y transitively includes X back. Cycles of any
// length are caught, including self-includes.
type CycleCheck struct{}

func (c *CycleCheck) Name() string {
	return "CycleCheck"
}

func (c *CycleCheck) Analyze(ctx context.Context, run *Run) error {
	view := run.View()

	closure := make(map[uri.URI][]Include)
	for _, file := range run.Files() {
		_ = getIncludes(ctx, view, file, &closure)
	}

	for file, includes := range closure {
		// Reachability comes from the view's include graph: parsing
		// the closure above registered exactly these edges via Register,
		// so there is no second graph to keep in sync. Dependents is
		// cycle-safe, so the walk terminates on the cycles it finds.
		deps := view.Dependents(file)
		for _, inc := range includes {
			if !slices.Contains(deps, inc.file) {
				continue
			}

			run.Add(file, cycleDiagnostic(inc))
		}
	}

	return nil
}

// cycleDiagnostic builds the warning for one include edge that closes a
// cycle back to its including file.
func cycleDiagnostic(inc Include) Diagnostic {
	return Diagnostic{
		Span:     SpanOf(inc.pf, inc.include),
		Severity: SeverityWarning,
		Code:     CodeIncludeCycle,
		Message:  fmt.Sprintf("cycle dependency in %s", inc.file),
	}
}

type Include struct {
	file    uri.URI
	include *syntax.Include
	pf      *store.ParsedFile
}

// getIncludes collects the include closure of file into includesMap: the
// include edges of every file reachable from file, parsed through the
// view so the ParsedFiles (and the include edges parsing records) are
// shared with the rest of the analysis.
func getIncludes(ctx context.Context, view Graph, file uri.URI, includesMap *map[uri.URI][]Include) error {
	pf, err := view.Parse(ctx, file)
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
	resolver := view.Resolver()

	for i := range includes {
		if includes[i].Path == nil {
			continue
		}

		includeURI := resolver.ResolveInclude(ctx, file, includes[i].PathText())
		(*includesMap)[file] = append((*includesMap)[file], Include{
			file:    includeURI,
			include: includes[i],
			pf:      pf,
		})

		if _, ok := (*includesMap)[includeURI]; ok {
			continue
		}

		_ = getIncludes(ctx, view, includeURI, includesMap)
	}

	return nil
}

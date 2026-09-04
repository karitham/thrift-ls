// Package analyzers holds thrift-ls's built-in analyses: the checks,
// fixers, and action providers composed into the default pipeline. It is
// deliberately outside sema so the checks dogfood sema's public API —
// anything an analyzer needs (Index, SpanOf, TokenSpan, LineSpan,
// EnumMemberValues, codes) must exist for third-party analyzer authors
// too. Shared, analyzer-useful abstractions graduate to sema; anything
// specific to one check stays here, private.
package analyzers

import (
	"github.com/karitham/thrift-ls/sema"
)

// Defaults returns the built-in analyzers.
func Defaults() []sema.Analyzer {
	return []sema.Analyzer{
		&CycleCheck{},
		&ParseCheck{},
		sema.EachFile(&FieldIDCheck{}),
		sema.EachFile(&DuplicateCheck{}),
		sema.EachFile(&EnumValueCheck{}),
		sema.EachFile(&UnusedIncludeCheck{}),
		sema.EachFile(&IncludeShadowCheck{}),
		sema.EachFile(&SemanticAnalysis{}),
		sema.EachFile(&NonScalarMapKeyCheck{}),
	}
}

// DefaultPipeline composes the built-in analyzers with their fixers and
// action providers.
func DefaultPipeline(cfg sema.Config) *sema.Pipeline {
	return sema.New(cfg, Defaults()).
		WithFixers(AddIncludeFixer{}).
		WithProviders(EnumValuesProvider{}, FieldQualifierProvider{})
}

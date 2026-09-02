package lsp

import (
	"github.com/karitham/thrift-ls/lsp/cache"
	"github.com/karitham/thrift-ls/options"
	"github.com/karitham/thrift-ls/sema"
)

// Layering, lowest to highest precedence:
//
//	defaults (options.Default)
//	+ Frontend.Defaults (per-installation or build-system defaults)
//	+ Project.Config, or the ConfigSource document for the project root
//	  (thrift-ls.json via lsp.FileConfigSource, or a build-system patch)
//	+ CLI overlay (--config pinning is a ConfigSource, -I/--printWidth are CLI)
//	+ LSP workspace settings (initializationOptions, didChangeConfiguration)
//
// Exception: include paths from a loader Project.Config are authoritative
// over the file document and CLI, because the build system owns resolution
// and a stray -I must not break it. All other keys follow the order above.

// ConfigSource resolves the config document for a project root directory.
// It returns the patch plus its origin, so build-system frontends can serve
// in-memory config while file discovery still pins diagnostics. A nil
// ConfigSource on Options means file discovery through the server's Files
// (lsp.FileConfigSource).
type ConfigSource = options.Source

// Analysis bundles the semantic extensions a frontend contributes.
// Analyzers run in the diagnostic pipeline, Fixers compute on-demand
// quickfixes for reported diagnostics, and Providers offer selection
// refactors. All three ride the same lint Disabled/Severity tuning.
type Analysis struct {
	Analyzers []sema.Analyzer
	Fixers    []sema.Fixer
	Providers []sema.ActionProvider
}

// Options is the complete frontend bundle for one server process.
// Files is the only filesystem seam: disk in production, in-memory in
// tests, build-system backed internally. Nil Files means the memoized disk
// source.
type Options struct {
	// Files serves file content and directory walks. Nil uses the memoized
	// disk source.
	Files cache.FileSource
	// Defaults sits directly above the builtin defaults. An empty patch
	// uses the package defaults.
	Defaults options.Patch
	// ConfigSource resolves the config document per project root. Nil
	// means file discovery through the server's Files
	// (lsp.FileConfigSource). Use options.PinnedSource to pin one document
	// (explicit --config) or to disable discovery (nil patch).
	ConfigSource ConfigSource
	// CLI overlays every view's config (flags like -I). Empty when the
	// process has no CLI layer (in-process use).
	CLI options.Patch
	// WorkspaceLoader replaces the default recursive workspace scan when set.
	WorkspaceLoader WorkspaceLoader
	// Analysis extensions appended to the builtin pipeline.
	Analysis Analysis
	// Version is reported in the initialize result. Empty uses ServerVersion.
	Version string
}

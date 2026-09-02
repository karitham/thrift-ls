// Package options is the configuration layer of thrift-ls. It loads and
// layers the thrift-ls.json document — formatting settings (owned by the
// formatter), include paths, and the log level — so configuration sources
// can override each other: defaults, a JSON config file, CLI flags, and LSP
// workspace settings.
//
// The config file is discovered by walking up from the file being
// formatted, like Biome's config discovery. THRIFT_LS_CONFIG overrides the
// search with an explicit path.
package options

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/karitham/thrift-ls/formatter"
)

// ConfigFileName is the JSON config file name.
const ConfigFileName = "thrift-ls.json"

// Patch is a partial set of options; nil fields are unset. The formatting
// fields are promoted from the embedded formatter patch and decode flat,
// so thrift-ls.json keeps its top-level keys ("printWidth", ...).
type Patch struct {
	formatter.FormatPatch

	IncludePaths *[]string   `json:"includePaths"`
	LogLevel     *int        `json:"logLevel"`
	Lint         *LintConfig `json:"lint"`
}

// Apply overlays p onto base: every set field of p replaces the
// corresponding field of base.
func (p Patch) Apply(base Patch) Patch {
	out := base
	out.FormatPatch = p.FormatPatch.Apply(base.FormatPatch)

	if p.IncludePaths != nil {
		out.IncludePaths = p.IncludePaths
	}

	if p.LogLevel != nil {
		out.LogLevel = p.LogLevel
	}

	if p.Lint != nil {
		out.Lint = p.Lint
	}

	return out
}

// LintConfig tunes the analysis pipeline: which analyzers run, and at what
// severity their diagnostics surface.
type LintConfig struct {
	// Disabled names analyzers (by Name) to skip.
	Disabled *[]string `json:"disabled"`

	// Severity overrides a diagnostic's severity by code.
	Severity *map[string]string `json:"severity"`
}

// Validate checks the lint settings for validity.
func (l LintConfig) Validate() error {
	if l.Severity == nil {
		return nil
	}

	for code, sev := range *l.Severity {
		switch sev {
		case "error", "warning", "info", "hint":
		default:
			return fmt.Errorf("lint: severity for %q must be one of \"error\", \"warning\", \"info\", \"hint\", got %q", code, sev)
		}
	}

	return nil
}

// Default returns the default options as a fully-set patch.
func Default() Patch {
	return Patch{FormatPatch: formatter.DefaultFormatPatch()}
}

// Validate checks every set field for validity.
func (p Patch) Validate() error {
	if err := p.FormatPatch.Validate(); err != nil {
		return err
	}

	if p.Lint != nil {
		return p.Lint.Validate()
	}

	return nil
}

// Parse parses a config document. Unknown keys are rejected so that typos
// and stale settings (e.g. the removed overrides feature) fail loudly.
// Include paths in the document are left as written; Load resolves them
// against the config file's directory.
func Parse(data []byte) (*Patch, error) {
	var p Patch

	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()

	if err := dec.Decode(&p); err != nil {
		return nil, err
	}

	if err := p.Validate(); err != nil {
		return nil, err
	}

	return &p, nil
}

// Load reads and parses a config file. Unknown keys are rejected so that
// typos and stale settings (e.g. the removed overrides feature) fail
// loudly. This is the CLI edge (plain disk I/O); the server discovers
// through its FileSource instead (see lsp.FileConfigSource).
func Load(path string) (*Patch, error) {
	// Relative include paths anchor to the config's directory, so the
	// config path must be absolute.
	abs, err := filepath.Abs(path)
	if err != nil {
		return nil, fmt.Errorf("options: %s: %w", path, err)
	}

	data, err := os.ReadFile(abs)
	if err != nil {
		return nil, err
	}

	p, err := Parse(data)
	if err != nil {
		return nil, fmt.Errorf("options: %s: %w", abs, err)
	}

	AnchorIncludes(p, filepath.Dir(abs))

	return p, nil
}

// AnchorIncludes resolves relative include paths in p against dir (the
// config file's directory), so resolution works the same no matter where
// the process was launched from. Absolute paths pass through.
func AnchorIncludes(p *Patch, dir string) {
	if p.IncludePaths == nil {
		return
	}

	anchored := make([]string, 0, len(*p.IncludePaths))
	for _, ip := range *p.IncludePaths {
		if filepath.IsAbs(ip) {
			anchored = append(anchored, ip)
		} else {
			anchored = append(anchored, filepath.Join(dir, ip))
		}
	}

	p.IncludePaths = &anchored
}

// FindConfig returns the absolute path of the config file for dir:
// THRIFT_LS_CONFIG when set, otherwise the nearest thrift-ls.json walking
// up from dir. An empty path means no config exists. The path is absolute
// because relative include paths in the config anchor to it.
func FindConfig(dir string) (string, error) {
	path := os.Getenv("THRIFT_LS_CONFIG")
	if path == "" {
		return FindNearestConfig(dir)
	}

	abs, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}

	return abs, nil
}

// FindNearestConfig returns the nearest thrift-ls.json found by walking from
// dir toward the filesystem root. It returns an absolute path, or an empty
// path when no config exists. It does not read THRIFT_LS_CONFIG.
func FindNearestConfig(dir string) (string, error) {
	abs, err := filepath.Abs(dir)
	if err != nil {
		return "", err
	}

	for d := abs; ; d = filepath.Dir(d) {
		path := filepath.Join(d, ConfigFileName)
		_, err := os.Stat(path)
		if err == nil {
			return path, nil
		}

		if !os.IsNotExist(err) {
			return "", err
		}

		if d == filepath.Dir(d) {
			return "", nil
		}
	}
}

// Resolved is one config lookup: the patch plus where it came from. Path
// is the source location for diagnostics (the thrift-ls.json path for file
// discovery, "" for in-memory sources). A nil Patch means no config.
type Resolved struct {
	Patch *Patch
	Path  string
}

// Source resolves the config patch for a directory. It returns the patch
// plus its origin, so callers can pin diagnostics to the file. Frontends
// backed by a build system serve in-memory patches with no path.
//
// Note the nil convention lives with the consumer: on lsp.Options a nil
// Source means file discovery through the server Files, and only
// options.PinnedSource(nil) disables it. A Source returning
// (Resolved{}, nil) also means no config.
type Source func(dir string) (Resolved, error)

// PinnedSource always returns patch, skipping discovery. A nil patch means
// defaults. An explicit --config flag builds one of these.
func PinnedSource(patch *Patch) Source {
	return func(string) (Resolved, error) {
		return Resolved{Patch: patch}, nil
	}
}

// Effective returns the effective options for a file: defaults with the
// config patch applied. cfg may be nil.
func Effective(cfg *Patch) Patch {
	p := Default()
	if cfg != nil {
		p = cfg.Apply(p)
	}

	return p
}

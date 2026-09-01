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

	IncludePaths *[]string `json:"includePaths"`
	LogLevel     *int      `json:"logLevel"`
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

	return out
}

// Default returns the default options as a fully-set patch.
func Default() Patch {
	return Patch{FormatPatch: formatter.DefaultFormatPatch()}
}

// Validate checks every set field for validity.
func (p Patch) Validate() error {
	return p.FormatPatch.Validate()
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
// loudly.
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

	// Include paths are relative to the config file, not the process CWD,
	// so resolution works the same for the CLI and the LSP no matter where
	// the server is launched from.
	if p.IncludePaths != nil {
		anchored := make([]string, 0, len(*p.IncludePaths))
		for _, ip := range *p.IncludePaths {
			if filepath.IsAbs(ip) {
				anchored = append(anchored, ip)
			} else {
				anchored = append(anchored, filepath.Join(filepath.Dir(abs), ip))
			}
		}

		p.IncludePaths = &anchored
	}

	return p, nil
}

// FindConfig returns the absolute path of the config file for dir:
// THRIFT_LS_CONFIG when set, otherwise the nearest thrift-ls.json walking
// up from dir. An empty path means no config exists. The path is absolute
// because relative include paths in the config anchor to it.
func FindConfig(dir string) (string, error) {
	path := os.Getenv("THRIFT_LS_CONFIG")

	if path == "" {
		var err error

		path, err = findConfig(dir)
		if err != nil {
			return "", err
		}
	}

	if path == "" {
		return "", nil
	}

	abs, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}

	return abs, nil
}

// findConfig walks up from dir to the nearest thrift-ls.json.
func findConfig(dir string) (string, error) {
	for d := dir; ; d = filepath.Dir(d) {
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

// Effective returns the effective options for a file: defaults with the
// config patch applied. cfg may be nil.
func Effective(cfg *Patch) Patch {
	p := Default()
	if cfg != nil {
		p = cfg.Apply(p)
	}

	return p
}

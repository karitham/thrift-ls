package lsp

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"

	"go.lsp.dev/uri"

	"github.com/karitham/thrift-ls/lsp/cache"
	"github.com/karitham/thrift-ls/options"
)

// FileConfigSource discovers thrift-ls.json through src: THRIFT_LS_CONFIG
// when set, otherwise the nearest thrift-ls.json walking up from the
// project root. It is the default ConfigSource, and the reason tests can
// run without touching disk — seed the FileSource with config documents
// and discovery resolves against them. Relative include paths anchor to
// the discovered file's location, as with options.Load.
//
// Reads use context.Background: resolution runs at view creation, outside
// any request scope. A missing candidate simply continues the walk; any
// other read or parse failure aborts with the candidate path attached.
func FileConfigSource(src cache.FileSource) ConfigSource {
	return func(dir string) (options.Resolved, error) {
		if env := os.Getenv("THRIFT_LS_CONFIG"); env != "" {
			abs, err := filepath.Abs(env)
			if err != nil {
				return options.Resolved{}, err
			}

			res, found, err := readConfigDocument(src, abs)
			if err != nil {
				return res, err
			}
			if !found {
				return options.Resolved{Path: abs}, fmt.Errorf("options: %s: %w", abs, fs.ErrNotExist)
			}

			return res, nil
		}

		abs, err := filepath.Abs(dir)
		if err != nil {
			return options.Resolved{}, err
		}

		for d := abs; ; d = filepath.Dir(d) {
			res, found, err := readConfigDocument(src, filepath.Join(d, options.ConfigFileName))
			if err != nil || found {
				return res, err
			}

			if d == filepath.Dir(d) {
				return options.Resolved{}, nil
			}
		}
	}
}

// readConfigDocument reads and parses one candidate config file through src.
// found is false when the file does not exist; any other failure carries
// the candidate path for diagnostics.
func readConfigDocument(src cache.FileSource, path string) (options.Resolved, bool, error) {
	fh, err := src.ReadFile(context.Background(), uri.File(path))
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return options.Resolved{}, false, nil
		}

		return options.Resolved{Path: path}, false, fmt.Errorf("options: %s: %w", path, err)
	}

	content, err := fh.Content()
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return options.Resolved{}, false, nil
		}

		return options.Resolved{Path: path}, false, fmt.Errorf("options: %s: %w", path, err)
	}

	p, err := options.Parse(content)
	if err != nil {
		return options.Resolved{Path: path}, true, fmt.Errorf("options: %s: %w", path, err)
	}

	options.AnchorIncludes(p, filepath.Dir(path))

	return options.Resolved{Patch: p, Path: path}, true, nil
}

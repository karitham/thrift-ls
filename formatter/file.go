package formatter

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/karitham/thrift-ls/syntax"
)

// FileOptions controls file-level formatting. Output is required unless Write
// is true. ResolveConfig receives ConfigPath and the file's absolute directory;
// a non-empty ConfigPath requires a resolver. When ConfigPath is empty, a
// resolver may discover a config. Patch overlays the resolved config, or the
// default formatting config when ResolveConfig is nil.
type FileOptions struct {
	Output        io.Writer
	Write         bool
	Diff          bool
	ConfigPath    string
	Patch         FormatPatch
	ResolveConfig func(path, dir string) (FormatPatch, error)
}

// FormatFile reads and formats file. It writes formatted output to Output by
// default, overwrites file when Write is true, and writes a unified diff when
// Diff is true. Write takes precedence over Diff. File permissions are
// preserved when overwriting an existing file.
func FormatFile(file string, opts FileOptions) error {
	if file == "" {
		return errors.New("must specify a thrift file to format, e.g. thrift-ls format file.thrift")
	}
	if !opts.Write && opts.Output == nil {
		return errors.New("formatter output is required when Write is false")
	}
	if opts.ConfigPath != "" && opts.ResolveConfig == nil {
		return errors.New("formatter ConfigPath requires ResolveConfig")
	}

	src, err := os.ReadFile(file)
	if err != nil {
		return err
	}

	absFile, err := filepath.Abs(file)
	if err != nil {
		return err
	}

	patch := FormatPatch{}
	if opts.ResolveConfig != nil {
		patch, err = opts.ResolveConfig(opts.ConfigPath, filepath.Dir(absFile))
		if err != nil {
			return err
		}
	}

	formatOptions, err := opts.Patch.Apply(patch).Options()
	if err != nil {
		return err
	}

	parsed, errs := syntax.Parse(src)
	if hasFileParseErrors(errs) {
		return fmt.Errorf("%s: file does not parse:\n%s", file, formatErrors(errs))
	}

	formatted, err := Format(parsed, formatOptions)
	if err != nil {
		return fmt.Errorf("%s: %w", file, err)
	}

	if _, errs := syntax.Parse([]byte(formatted)); hasFileParseErrors(errs) {
		return fmt.Errorf("%s: formatting produced invalid output", file)
	}

	switch {
	case opts.Write:
		permissions := os.FileMode(0o644)
		if info, statErr := os.Stat(file); statErr == nil {
			permissions = info.Mode()
		}

		return os.WriteFile(file, []byte(formatted), permissions)
	case opts.Diff:
		_, err = opts.Output.Write(diff("old", src, "new", []byte(formatted)))
	default:
		_, err = io.WriteString(opts.Output, formatted)
	}

	return err
}

func hasFileParseErrors(errs []syntax.Error) bool {
	for _, err := range errs {
		if err.Severity == syntax.SeverityError {
			return true
		}
	}

	return false
}

func formatErrors(errs []syntax.Error) string {
	var output strings.Builder

	for _, err := range errs {
		if err.Severity == syntax.SeverityError {
			fmt.Fprintf(&output, "  %s\n", err)
		}
	}

	return output.String()
}

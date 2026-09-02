package formatter

import (
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/karitham/thrift-ls/syntax"
)

// FileOptions controls file-level formatting. Output is required unless Write
// is true. Base is the already-resolved file or project config (empty means
// defaults); Override is the CLI patch applied on top of it. The caller owns
// config discovery, so the formatter stays pure I/O plus layout.
type FileOptions struct {
	Output   io.Writer
	Write    bool
	Diff     bool
	Base     FormatPatch
	Override FormatPatch
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

	src, err := os.ReadFile(file)
	if err != nil {
		return err
	}

	formatOptions, err := opts.Override.Apply(opts.Base).Options()
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

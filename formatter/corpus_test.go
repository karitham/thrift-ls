package formatter

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/karitham/thrift-ls/syntax"
)

// TestFormatRealWorldCorpus feeds every .thrift file under the repo's
// tests/ directories through the format -> reparse -> reformat pipeline.
// These are real-world IDLs (Evernote, Galaxy, line-protocol, lint
// fixtures): shapes the hand-written format cases do not cover.
//
// Invariants per file:
//   - the file parses with no errors (they are all valid IDLs)
//   - its formatted output parses with no errors
//   - formatting is idempotent: format(format(x)) == format(x)
func TestFormatRealWorldCorpus(t *testing.T) {
	roots := []string{
		filepath.Join("..", "tests", "made-in-abyss"),
		filepath.Join("..", "tests", "evernote-thrift"),
		filepath.Join("..", "tests", "galaxy-thrift-api"),
		filepath.Join("..", "tests", "line-protocol"),
	}

	files := make([]string, 0, 64)
	for _, root := range roots {
		err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
			if err != nil {
				return err
			}

			if !d.IsDir() && strings.HasSuffix(path, ".thrift") {
				files = append(files, path)
			}

			return nil
		})
		if err != nil {
			t.Fatalf("walk %s: %v", root, err)
		}
	}

	if len(files) == 0 {
		t.Fatal("no corpus files found; tests/ missing?")
	}

	for _, path := range files {
		t.Run(filepath.ToSlash(strings.TrimPrefix(path, ".."+string(filepath.Separator))), func(t *testing.T) {
			src, err := os.ReadFile(path)
			if err != nil {
				t.Fatalf("read: %v", err)
			}

			checkRoundTrip(t, src, DefaultOptions())

			// Off-default knobs must converge too: separator and break
			// regressions that only manifest away from defaults are
			// invisible to the defaults-only pass above.
			commas := DefaultOptions()
			for _, c := range AllConstructs {
				commas.Separator.Set(c, SeparatorComma)
			}
			checkRoundTrip(t, src, commas)

			breaks := DefaultOptions()
			for _, c := range AllConstructs {
				breaks.Break.Set(c, true)
			}
			checkRoundTrip(t, src, breaks)
		})
	}
}

// checkRoundTrip formats src with opts and asserts the output reparses
// and is a fixed point.
func checkRoundTrip(t *testing.T, src []byte, opts Options) {
	t.Helper()

	doc := parseBytes(t, src)

	formatted, ferr := Format(doc, opts)
	if ferr != nil {
		t.Fatalf("format: %v", ferr)
	}

	reparsed := parseBytes(t, []byte(formatted))

	again, ferr := Format(reparsed, opts)
	if ferr != nil {
		t.Fatalf("reformat: %v", ferr)
	}

	if again != formatted {
		t.Errorf("not idempotent\nfirst:\n%s\nsecond:\n%s", formatted, again)
	}
}

// parseBytes parses src and fails the test on any hard parse error.
func parseBytes(t *testing.T, src []byte) *syntax.Document {
	t.Helper()

	doc, errs := syntax.Parse(src)
	if hasParseErrors(errs) {
		t.Fatalf("unexpected parse errors: %v", errs)
	}

	return doc
}

package formatter

import (
	"fmt"
	"sync"
	"testing"

	"github.com/karitham/thrift-ls/syntax"
)

// TestFormatConcurrent pins the arena-pool invariant: Format must be safe
// to call from many goroutines at once. The pooled arena is the only
// shared state, so a data race here means the pool contract is broken.
// Run with -race in CI to make the check real.
func TestFormatConcurrent(t *testing.T) {
	srcs := make([]string, 0, 16)
	for i := range 16 {
		srcs = append(srcs, fmt.Sprintf(`struct Data%d {
  1: required i32 id,
  2: optional string name,
}

service Svc%d {
  Data%d get(1: i32 id),
}
`, i, i, i))
	}

	opts := testOpts(80)

	var wg sync.WaitGroup

	errs := make([]error, len(srcs))

	for i, src := range srcs {
		wg.Go(func() {
			doc, docErrs := syntax.Parse([]byte(src))
			if hasParseErrors(docErrs) {
				errs[i] = fmt.Errorf("parse: %v", docErrs)

				return
			}

			got, err := Format(doc, opts)
			if err != nil {
				errs[i] = err

				return
			}

			// Re-parse: a corrupted arena (shared regions between
			// goroutines) shows up as garbage output that no longer
			// matches the input shape.
			if _, reparses := syntax.Parse([]byte(got)); hasParseErrors(reparses) {
				errs[i] = fmt.Errorf("output does not reparse: %q (%v)", got, reparses)
			}
		})
	}

	wg.Wait()

	for i, err := range errs {
		if err != nil {
			t.Errorf("goroutine %d: %v", i, err)
		}
	}
}

// TestFormatConcurrentSameInput hammers one document from many goroutines:
// every result must be identical. Catches arena state leaking across calls
// even when each call is individually well-formed.
func TestFormatConcurrentSameInput(t *testing.T) {
	src := `struct Item {
  1: required i64 id,
  2: map<string, string> tags,
}
`
	doc, errs := syntax.Parse([]byte(src))
	if hasParseErrors(errs) {
		t.Fatalf("parse errors: %v", errs)
	}

	opts := testOpts(80)

	want := fmtSrc(t, src, opts)

	var wg sync.WaitGroup

	for range 32 {
		wg.Go(func() {
			got, err := Format(doc, opts)
			if err != nil {
				t.Errorf("Format: %v", err)

				return
			}

			if got != want {
				t.Errorf("concurrent format mismatch:\n got: %q\nwant: %q", got, want)
			}
		})
	}

	wg.Wait()
}

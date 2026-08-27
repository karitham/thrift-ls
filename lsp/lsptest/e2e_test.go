package lsptest

// E2E tests run the real thrift-ls binary over stdio. They pin the
// behaviors that partial degradation depends on: a session survives
// broken configuration, unresolvable includes degrade to reported but
// tolerated references, and healthy files keep working next to broken ones.

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

var binPath string

func TestMain(m *testing.M) {
	dir, err := os.MkdirTemp("", "thrift-ls-lsptest-*")
	if err != nil {
		fmt.Fprintln(os.Stderr, "mktemp:", err)
		os.Exit(1)
	}

	code := runTests(m, dir)
	os.RemoveAll(dir)
	os.Exit(code)
}

func runTests(m *testing.M, dir string) int {
	binPath = filepath.Join(dir, "thrift-ls")
	build := exec.Command("go", "build", "-o", binPath, ".")
	build.Dir = "../.."

	if out, berr := build.CombinedOutput(); berr != nil {
		fmt.Fprintf(os.Stderr, "build failed: %v\n%s\n", berr, out)

		return 1
	}

	return m.Run()
}

// newSession starts the built binary over dir, failing the test when the
// handshake does not complete.
func newSession(t *testing.T, dir string) *Server {
	t.Helper()

	s, err := New([]string{binPath}, dir, Options{})
	if err != nil {
		t.Fatalf("start server: %v", err)
	}

	t.Cleanup(func() { _ = s.Close() })

	return s
}

func writeFile(t *testing.T, dir, rel, content string) string {
	t.Helper()

	path := filepath.Join(dir, rel)
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	return path
}

func hasMessage(diags []Diagnostic, substr string) bool {
	for _, d := range diags {
		if strings.Contains(d.Message, substr) {
			return true
		}
	}

	return false
}

const (
	sevError   = 1
	sevWarning = 2
)

func TestOpenPublishesParseAndSemanticDiagnostics(t *testing.T) {
	root := t.TempDir()
	s := newSession(t, root)

	src := `include "missing.thrift"

struct A {
  1: NoSuchType field
  2: i32 ok
}
`
	path := writeFile(t, root, "broken.thrift", src)
	diags, err := s.Open(path, src)
	if err != nil {
		t.Fatalf("open: %v", err)
	}

	if !hasMessage(diags, "field type doesn't exist") {
		t.Errorf("missing undefined-type diagnostic, got %v", diags)
	}

	if !hasMessage(diags, "unused include") {
		t.Errorf("missing unused-include diagnostic, got %v", diags)
	}

	for _, d := range diags {
		if d.Severity != sevError && d.Severity != sevWarning {
			t.Errorf("diagnostic %q has unexpected severity %d", d.Message, d.Severity)
		}
	}
}

func TestChangeClearsResolvedDiagnostics(t *testing.T) {
	root := t.TempDir()
	s := newSession(t, root)

	path := writeFile(t, root, "doc.thrift", `struct A { 1: Missing x }`)

	diags, err := s.Open(path, `struct A { 1: Missing x }`)
	if err != nil {
		t.Fatalf("open: %v", err)
	}

	if !hasMessage(diags, "field type doesn't exist") {
		t.Fatalf("expected undefined-type diagnostic first, got %v", diags)
	}

	diags, err = s.Change(path, `struct A { 1: i32 x }`)
	if err != nil {
		t.Fatalf("change: %v", err)
	}

	if hasMessage(diags, "doesn't exist") {
		t.Errorf("error survived fixing edit, got %v", diags)
	}
}

// The editor launches the server with the project's directory as cwd, so a
// broken thrift-ls.json kills every buffer's session at startup. The
// session must instead degrade to defaults and announce the reason.
func TestInvalidConfigDegradesInsteadOfExiting(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "thrift-ls.json", `{ "printWidht": 100 }`)

	s := newSession(t, root)

	if !s.Alive() {
		t.Fatalf("server died during startup; stderr:\n%s", s.Stderr())
	}

	path := writeFile(t, root, "ok.thrift", "struct A { 1: i32 x }\n")
	if _, err := s.Open(path, "struct A { 1: i32 x }\n"); err != nil {
		t.Fatalf("open under invalid config: %v\nstderr:\n%s", err, s.Stderr())
	}

	out, ferr := s.Format(path)
	if ferr != nil {
		t.Fatalf("format under invalid config: %v", ferr)
	}

	if want := "struct A { 1: i32 x }\n"; out != want {
		t.Errorf("formatted = %q, want %q", out, want)
	}

	messages := strings.Join(s.Messages(), "\n")
	if !strings.Contains(messages, "thrift-ls.json") || !strings.Contains(messages, "continuing with default") {
		t.Errorf("server did not announce the config problem; messages: %v", s.Messages())
	}
}

// A reference into an unresolvable include must be reported as an error
// diagnostic, and interactive features must answer with empty results,
// not JSON-RPC errors. Qualified (missing.Foo) and bare (Foo) references
// take different resolution paths; both must degrade.
func TestUnresolvedIncludeDegradesToEmptyFeatureResults(t *testing.T) {
	for name, ref := range map[string]string{
		"qualified":        "missing.Foo",
		"bare":             "Foo",
		"orphan-qualifier": "b.Foo",
	} {
		t.Run(name, func(t *testing.T) {
			root := t.TempDir()
			s := newSession(t, root)

			src := `include "missing.thrift"

struct A {
  1: ` + ref + ` foo
  2: i32 x
}
`
			path := writeFile(t, root, "a.thrift", src)

			diags, err := s.Open(path, src)
			if err != nil {
				t.Fatalf("open: %v", err)
			}

			if !hasMessage(diags, "field type doesn't exist") {
				t.Fatalf("missing undefined-type diagnostic, got %v", diags)
			}

			pos, perr := IndexPosition(src, "Foo", 0)
			if perr != nil {
				t.Fatal(perr)
			}

			hover, herr := s.Hover(path, pos)
			if herr != nil {
				t.Errorf("hover over unresolved type returned error (want nil result): %v", herr)
			}

			if hover != nil {
				t.Errorf("hover over unresolved type should be empty, got %q", *hover)
			}

			defs, derr := s.Definition(path, pos)
			if derr != nil {
				t.Errorf("definition over unresolved type returned error (want empty): %v", derr)
			}

			if len(defs) != 0 {
				t.Errorf("definition over unresolved type should be empty, got %v", defs)
			}
		})
	}
}

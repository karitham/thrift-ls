package formatter

import (
	"strconv"
	"testing"

	"github.com/karitham/thrift-ls/syntax"
)

// fmtSrc formats src at the given width with the given options. It fails the
// test when parsing fails.
func fmtSrc(t *testing.T, src string, opts Options) string {
	t.Helper()
	doc, errs := syntax.Parse([]byte(src))
	for _, err := range errs {
		if err.Severity == syntax.SeverityError {
			t.Fatalf("parse errors: %v", errs)
		}
	}
	got, err := Format(doc, opts)
	if err != nil {
		t.Fatalf("Format: %v", err)
	}
	return got
}

// parseDoc parses src and fails the test on parse errors.
func parseDoc(t *testing.T, src string) *syntax.Document {
	t.Helper()
	doc, errs := syntax.Parse([]byte(src))
	for _, err := range errs {
		if err.Severity == syntax.SeverityError {
			t.Fatalf("parse errors: %v", errs)
		}
	}
	return doc
}

func testOpts(width int) Options {
	o := DefaultOptions()
	o.PrintWidth = width
	o.Indent = "  "
	o.TabWidth = 2
	return o
}

// runCase formats, checks idempotency, and re-parses the output.
func runCase(t *testing.T, src string, opts Options, want string) {
	t.Helper()
	got := fmtSrc(t, src, opts)
	if got != want {
		t.Errorf("format mismatch\n got: %q\nwant: %q", got, want)
		return
	}
	// Idempotency: formatting the output again must not change it.
	again := fmtSrc(t, got, opts)
	if again != got {
		t.Errorf("not idempotent:\n first: %q\nsecond: %q", got, again)
	}
	// Self-validation: the output must parse cleanly.
	_, errs := syntax.Parse([]byte(got))
	for _, err := range errs {
		if err.Severity == syntax.SeverityError {
			t.Errorf("formatted output does not parse: %v", errs)
			break
		}
	}
}

func TestFormatAnnotations(t *testing.T) {
	tests := []struct {
		name string
		src  string
		want string
	}{
		{
			name: "annotation before a service is preserved",
			src:  "@naming.PreviouslyKnownAs{'namespace_': 'x'}\nservice Foo {\n  void bar()\n}\n",
			want: "@naming.PreviouslyKnownAs{'namespace_': 'x'}\nservice Foo {\n  void bar()\n}\n",
		},
		{
			name: "empty annotation before an enum",
			src:  "@deprecation.Deprecated{}\nenum Status {\n  A\n  B\n}\n",
			want: "@deprecation.Deprecated{}\nenum Status { A, B }\n",
		},
		{
			name: "multiple annotations keep order",
			src:  "@a.B{}\n@c.D{'k': 'v'}\nservice Foo {\n\n}\n",
			want: "@a.B{}\n@c.D{'k': 'v'}\nservice Foo {\n\n}\n",
		},
		{
			name: "comment and annotation before a declaration",
			src:  "// keep me\n@naming.X{'a': 'b'}\nstruct S {}\n",
			want: "// keep me\n@naming.X{'a': 'b'}\nstruct S {}\n",
		},
		{
			name: "annotation after a blank line keeps the blank line",
			src:  "struct A {}\n\n@naming.X{}\nservice B {\n\n}\n",
			want: "struct A {}\n\n@naming.X{}\nservice B {\n\n}\n",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			runCase(t, tt.src, testOpts(80), tt.want)
		})
	}
}

func TestFormatHeaders(t *testing.T) {
	tests := []struct {
		name string
		src  string
		want string
	}{
		{
			name: "includes and namespaces",
			src:  "include   \"a.thrift\"\nnamespace go thrift\ncpp_include \"<vector>\"",
			want: "include \"a.thrift\"\nnamespace go thrift\ncpp_include \"<vector>\"\n",
		},
		{
			name: "namespace with star scope and annotations",
			src:  "namespace * all (tag = \"x\")",
			want: "namespace * all (tag = \"x\")\n",
		},
		{
			name: "blank lines between headers are preserved",
			src:  "include \"a.thrift\"\n\ninclude \"b.thrift\"\n\nnamespace go x",
			want: "include \"a.thrift\"\n\ninclude \"b.thrift\"\n\nnamespace go x\n",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			runCase(t, tt.src, testOpts(80), tt.want)
		})
	}
}

func TestFormatTypedefs(t *testing.T) {
	tests := []struct {
		name  string
		src   string
		width int
		want  string
	}{
		{
			name: "simple",
			src:  "typedef\ti64\tTimestamp",
			want: "typedef i64 Timestamp\n",
		},
		{
			name: "container with annotations",
			src:  "typedef map<string, list<i32>> Index (tag = \"x\")",
			want: "typedef map<string, list<i32>> Index (tag = \"x\")\n",
		},
		{
			name: "cpp_type",
			src:  "typedef list cpp_type \"std::vector\" < i32 > Vec",
			want: "typedef list cpp_type \"std::vector\" <i32> Vec\n",
		},
		{
			name:  "long annotations break",
			src:   "typedef string Id (id_type = \"uuid\", long_annotation = \"some value\")",
			width: 60,
			want:  "typedef string Id (\n  id_type = \"uuid\",\n  long_annotation = \"some value\"\n)\n",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			width := tt.width
			if width == 0 {
				width = 80
			}
			runCase(t, tt.src, testOpts(width), tt.want)
		})
	}
}

func TestFormatConsts(t *testing.T) {
	tests := []struct {
		name  string
		src   string
		width int
		want  string
	}{
		{
			name: "scalars",
			src:  "const i32 a = 0xa1\nconst double b = -1.5e-3\nconst string c = \"x\"",
			want: "const i32 a = 0xa1\nconst double b = -1.5e-3\nconst string c = \"x\"\n",
		},
		{
			name: "list fits",
			src:  "const list<i32> l = [1, 2, 3]",
			want: "const list<i32> l = [1, 2, 3]\n",
		},
		{
			name:  "list breaks",
			src:   "const list<i32> l = [1, 2, 3, 4, 5]",
			width: 30,
			want:  "const list<i32> l = [\n  1,\n  2,\n  3,\n  4,\n  5\n]\n",
		},
		{
			name:  "map breaks",
			src:   "const map<string, i32> m = {\"aaaa\": 1, \"bbbb\": 2}",
			width: 40,
			want:  "const map<string, i32> m = {\n  \"aaaa\": 1,\n  \"bbbb\": 2\n}\n",
		},
		{
			name: "nested values",
			src:  "const list<list<i32>> n = [[1, 2], [3]]",
			want: "const list<list<i32>> n = [[1, 2], [3]]\n",
		},
		{
			name: "empty list and map",
			src:  "const list<i32> a = []\nconst map<string, i32> b = {}",
			want: "const list<i32> a = []\nconst map<string, i32> b = {}\n",
		},
		{
			name:  "value list breaks inside const",
			src:   "const list<i32> l = [1, 2, 3, 4, 5, 6, 7, 8]",
			width: 40,
			want:  "const list<i32> l = [\n  1,\n  2,\n  3,\n  4,\n  5,\n  6,\n  7,\n  8\n]\n",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			width := tt.width
			if width == 0 {
				width = 40
			}
			runCase(t, tt.src, testOpts(width), tt.want)
		})
	}
}

func TestFormatStructs(t *testing.T) {
	tests := []struct {
		name  string
		src   string
		width int
		want  string
	}{
		{
			name: "empty struct",
			src:  "struct Empty {}",
			want: "struct Empty {}\n",
		},
		{
			name: "small struct stays flat",
			src:  "struct S {\n  1: i32 a\n}",
			want: "struct S { 1: i32 a }\n",
		},
		{
			name:  "fields align",
			src:   "struct S {\n  1: i64 id\n  2: string name\n  10: i32 x\n}",
			width: 40,
			want:  "struct S {\n  1:  i64    id\n  2:  string name\n  10: i32    x\n}\n",
		},
		{
			name:  "requiredness column aligns",
			src:   "struct S {\n  1: required i64 id\n  2: string name\n}",
			width: 40,
			want:  "struct S {\n  1: required i64    id\n  2:          string name\n}\n",
		},
		{
			name:  "union and exception keywords",
			src:   "union U {\n  1: i32 a\n}\nexception E {\n  1: string msg\n}",
			width: 15,
			want:  "union U {\n  1: i32 a\n}\nexception E {\n  1: string msg\n}\n",
		},
		{
			name:  "implicit ids and defaults",
			src:   "struct S {\n  i32 a\n  string b = \"x\"\n}",
			width: 30,
			want:  "struct S {\n  i32    a\n  string b = \"x\"\n}\n",
		},
		{
			name: "annotations on struct",
			src:  "struct S {\n  1: i32 a\n} (tag = \"x\")",
			want: "struct S { 1: i32 a } (tag = \"x\")\n",
		},
		{
			name:  "annotations break when too long",
			src:   "struct S {\n  1: i32 a\n} (tag = \"x\")",
			width: 10,
			want:  "struct S {\n  1: i32 a\n} (\n  tag = \"x\"\n)\n",
		},
		{
			name:  "annotations stay flat on the closing line",
			src:   "struct S {\n  1: i32 a\n} (tag = \"x\")",
			width: 15,
			want:  "struct S {\n  1: i32 a\n} (tag = \"x\")\n",
		},
		{
			name:  "field annotations",
			src:   "struct S {\n  1: i32 a (tag = \"y\")\n}",
			width: 30,
			want:  "struct S {\n  1: i32 a (tag = \"y\")\n}\n",
		},
		{
			name:  "type annotations before name",
			src:   "struct S {\n  1: map<i32, string> (tag = \"t\") m\n}",
			width: 40,
			want:  "struct S {\n  1: map<i32, string> (tag = \"t\") m\n}\n",
		},
		{
			name:  "field reference",
			src:   "struct S {\n  1: i32 &parent\n}",
			width: 20,
			want:  "struct S {\n  1: i32 &parent\n}\n",
		},
		{
			name: "semicolon separators normalized",
			src:  "struct S {\n  1: i32 a;\n  2: string b;\n}",
			want: "struct S { 1: i32 a, 2: string b }\n",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			width := tt.width
			if width == 0 {
				width = 80
			}
			runCase(t, tt.src, testOpts(width), tt.want)
		})
	}
}

func TestFormatEnums(t *testing.T) {
	tests := []struct {
		name  string
		src   string
		width int
		want  string
	}{
		{
			name: "empty enum",
			src:  "enum E {}",
			want: "enum E {}\n",
		},
		{
			name:  "values align equals",
			src:   "enum E {\n  A = 1\n  LONG = 2\n}",
			width: 20,
			want:  "enum E {\n  A    = 1\n  LONG = 2\n}\n",
		},
		{
			name: "auto-increment values",
			src:  "enum E {\n  A = 1,\n  B,\n  C = 0x10\n}",
			want: "enum E { A = 1, B, C = 0x10 }\n",
		},
		{
			name: "negative value",
			src:  "enum E {\n  A = -1\n}",
			want: "enum E { A = -1 }\n",
		},
		{
			name: "annotations",
			src:  "enum E {\n  A (a_anno = \"y\")\n} (e_anno = \"x\")",
			want: "enum E { A (a_anno = \"y\") } (e_anno = \"x\")\n",
		},
		{
			name:  "annotations break when too long",
			src:   "enum E {\n  A (a_anno = \"y\")\n} (e_anno = \"x\")",
			width: 12,
			want:  "enum E {\n  A (\n    a_anno = \"y\"\n  )\n} (\n  e_anno = \"x\"\n)\n",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			width := tt.width
			if width == 0 {
				width = 80
			}
			runCase(t, tt.src, testOpts(width), tt.want)
		})
	}
}

func TestFormatFunctions(t *testing.T) {
	tests := []struct {
		name  string
		src   string
		width int
		want  string
	}{
		{
			name:  "short signature stays flat",
			src:   "service S {\n  void ping()\n}",
			width: 80,
			want:  "service S {\n  void ping()\n}\n",
		},
		{
			name:  "oneway void",
			src:   "service S {\n  oneway void fire()\n}",
			width: 80,
			want:  "service S {\n  oneway void fire()\n}\n",
		},
		{
			name:  "async normalizes to oneway",
			src:   "service S {\n  async void fire()\n}",
			width: 80,
			want:  "service S {\n  oneway void fire()\n}\n",
		},
		{
			name:  "signature with args fits",
			src:   "service S {\n  User getUser(1: i64 id, 2: string name)\n}",
			width: 80,
			want:  "service S {\n  User getUser(1: i64 id, 2: string name)\n}\n",
		},
		{
			name:  "throws breaks before args",
			src:   "service S {\n  i32 getUser(1: i64 id, 2: string name) throws (NotFound e)\n}",
			width: 50,
			want:  "service S {\n  i32 getUser(1: i64 id, 2: string name) throws (\n    NotFound e\n  )\n}\n",
		},
		{
			name:  "everything breaks when signature is long",
			src:   "service S {\n  i32 getUser(1: i64 id, 2: string name) throws (NotFound e)\n}",
			width: 45,
			want:  "service S {\n  i32 getUser(\n    1: i64 id,\n    2: string name\n  ) throws (\n    NotFound e\n  )\n}\n",
		},
		{
			name:  "args break without throws",
			src:   "service S {\n  i32 getUser(1: i64 id, 2: string name)\n}",
			width: 35,
			want:  "service S {\n  i32 getUser(\n    1: i64 id,\n    2: string name\n  )\n}\n",
		},
		{
			name:  "one arg stays flat",
			src:   "service S {\n  i32 getUser(1: i64 id) throws (NotFound e)\n}",
			width: 60,
			want:  "service S {\n  i32 getUser(1: i64 id) throws (NotFound e)\n}\n",
		},
		{
			name:  "function annotations",
			src:   "service S {\n  void f() (f_anno = \"x\")\n}",
			width: 80,
			want:  "service S {\n  void f() (f_anno = \"x\")\n}\n",
		},
		{
			name:  "empty throws stays inline",
			src:   "service S {\n  void f() throws ()\n}",
			width: 80,
			want:  "service S {\n  void f() throws ()\n}\n",
		},
		{
			name:  "extends",
			src:   "service Child extends Parent {\n  void f()\n}",
			width: 80,
			want:  "service Child extends Parent {\n  void f()\n}\n",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			runCase(t, tt.src, testOpts(tt.width), tt.want)
		})
	}
}

func TestFormatComments(t *testing.T) {
	tests := []struct {
		name  string
		src   string
		width int
		want  string
	}{
		{
			name:  "leading comments",
			src:   "// before\nstruct S {\n  1: i32 a\n}",
			width: 15,
			want:  "// before\nstruct S {\n  1: i32 a\n}\n",
		},
		{
			name:  "doc comment",
			src:   "/** doc */\nstruct S {\n  1: i32 a\n}",
			width: 15,
			want:  "/** doc */\nstruct S {\n  1: i32 a\n}\n",
		},
		{
			name:  "end-of-line comment forces break",
			src:   "struct S {\n  1: i32 a // eol\n}",
			width: 80,
			want:  "struct S {\n  1: i32 a // eol\n}\n",
		},
		{
			name:  "field leading comment",
			src:   "struct S {\n  // c\n  1: i32 a\n}",
			width: 80,
			want:  "struct S {\n  // c\n  1: i32 a\n}\n",
		},
		{
			name:  "blank lines between fields preserved",
			src:   "struct S {\n  1: i32 a\n\n  2: string b\n}",
			width: 80,
			want:  "struct S {\n  1: i32 a\n\n  2: string b\n}\n",
		},
		{
			name: "comment at end of file",
			src:  "struct S {\n  1: i32 a\n}\n// tail",
			want: "struct S { 1: i32 a }\n// tail\n",
		},
		{
			name:  "hash comment",
			src:   "# hash\nstruct S {\n  1: i32 a\n}",
			width: 15,
			want:  "# hash\nstruct S {\n  1: i32 a\n}\n",
		},
		{
			name:  "block comment",
			src:   "/* block */\nstruct S {\n  1: i32 a\n}",
			width: 15,
			want:  "/* block */\nstruct S {\n  1: i32 a\n}\n",
		},
		{
			name: "comments between definitions",
			src:  "struct A {\n  1: i32 a\n}\n\n// b comment\nstruct B {\n  1: i32 b\n}",
			want: "struct A { 1: i32 a }\n\n// b comment\nstruct B { 1: i32 b }\n",
		},
		{
			name:  "comment inside function args forces broken",
			src:   "service S {\n  void f(1: i32 a // arg comment\n  )\n}",
			width: 80,
			want:  "service S {\n  void f(\n    1: i32 a // arg comment\n  )\n}\n",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			width := tt.width
			if width == 0 {
				width = 80
			}
			runCase(t, tt.src, testOpts(width), tt.want)
		})
	}
}

func TestFormatOptions(t *testing.T) {
	tests := []struct {
		name string
		opts Options
		src  string
		want string
	}{
		{
			name: "comma add",
			opts: func() Options {
				o := testOpts(30)
				o.FieldLineComma = CommaAdd
				return o
			}(),
			src:  "struct S {\n  1: i32 a\n  2: string b\n}",
			want: "struct S {\n  1: i32    a,\n  2: string b,\n}\n",
		},
		{
			name: "comma remove",
			opts: func() Options {
				o := testOpts(30)
				o.FieldLineComma = CommaRemove
				return o
			}(),
			src:  "struct S {\n  1: i32 a,\n  2: string b,\n}",
			want: "struct S {\n  1: i32    a\n  2: string b\n}\n",
		},
		{
			name: "align disable",
			opts: func() Options {
				o := testOpts(40)
				o.Align = AlignDisable
				return o
			}(),
			src:  "struct S {\n  1: required i64 id\n  2: string name\n}",
			want: "struct S {\n  1: required i64 id\n  2: string name\n}\n",
		},
		{
			name: "align assign",
			opts: func() Options {
				o := testOpts(40)
				o.Align = AlignAssign
				return o
			}(),
			src:  "struct S {\n  1: i32 a = 5\n  2: string longer = \"x\"\n}",
			want: "struct S {\n  1: i32 a      = 5\n  2: string longer = \"x\"\n}\n",
		},
		{
			name: "trailing newline by default",
			src:  "struct S {\n  1: i32 a\n}",
			want: "struct S { 1: i32 a }\n",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			runCase(t, tt.src, tt.opts, tt.want)
		})
	}
}

func TestFormatRejectsNilDocument(t *testing.T) {
	if _, err := Format(nil, testOpts(80)); err == nil {
		t.Error("expected error formatting nil document")
	}
}

func TestFormatWholeDocument(t *testing.T) {
	src := `include "shared.thrift"

namespace go example

// A user.
struct User {
  1: required i64 id
  2: optional string name = "bob"
}

enum Role {
  ADMIN = 1,
  USER
}

service UserService {
  User getUser(1: i64 id) throws (NotFound e)
  oneway void notify(1: User user)
}
`
	want := `include "shared.thrift"

namespace go example

// A user.
struct User {
  1: required i64    id
  2: optional string name = "bob"
}

enum Role { ADMIN = 1, USER }

service UserService {
  User getUser(1: i64 id) throws (NotFound e)
  oneway void notify(1: User user)
}
`
	runCase(t, src, testOpts(60), want)
}

func TestFormatIsIdempotent(t *testing.T) {
	sources := []string{
		"struct S {\n  1: required i64 id\n  2: optional string name\n}",
		"service S {\n  i32 f(1: i64 a, 2: string b) throws (E e)\n}",
		"const map<string, list<i32>> m = {\"a\": [1, 2], \"b\": []}",
		"enum E {\n  A = 1,\n  B\n} (tag = \"x\")",
		"// c\nstruct S {\n  1: i32 a // eol\n\n  2: string b\n}",
	}
	for i, src := range sources {
		t.Run("case-"+strconv.Itoa(i), func(t *testing.T) {
			first := fmtSrc(t, src, testOpts(40))
			second := fmtSrc(t, first, testOpts(40))
			if first != second {
				t.Errorf("not idempotent:\nfirst: %q\nsecond: %q", first, second)
			}
		})
	}
}

func TestFormatNode(t *testing.T) {
	src := `// leading
struct User {
  1: required i64 id
} (tag = "x")`
	doc := parseDoc(t, src)
	want := `// leading
struct User { 1: required i64 id } (tag = "x")`
	got, err := FormatNode(doc, doc.Structs()[0], testOpts(80))
	if err != nil {
		t.Fatalf("FormatNode: %v", err)
	}
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

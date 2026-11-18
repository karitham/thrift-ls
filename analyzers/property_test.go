// Package analyzers_test checks the built-in analyzers against models of
// their documented contracts: valid generated documents, one injected defect
// at a time, lint-config metamorphics, and the tests/ corpus.
package analyzers_test

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
	"unicode/utf8"

	"go.lsp.dev/uri"

	"github.com/karitham/thrift-ls/analyzers"
	"github.com/karitham/thrift-ls/analyzertest"
	"github.com/karitham/thrift-ls/internal/proptest"
	"github.com/karitham/thrift-ls/sema"
	"github.com/karitham/thrift-ls/store"
)

// testTB is the slice of *testing.T and *proptest.T the helpers need. Taking
// the interface keeps one helper usable from a plain test and from a fuzz
// target, whose proptest.T embeds its own *testing.T.
type testTB interface {
	Logf(format string, args ...any)
	Fatalf(format string, args ...any)
}

// position maps a byte offset in content to the parser coordinates the
// analyzers report: 1-based line, 1-based rune column.
func position(content string, offset int) (int, int) {
	line := 1
	lineStart := 0

	for i := range offset {
		if content[i] == '\n' {
			line++
			lineStart = i + 1
		}
	}

	return line, 1 + utf8.RuneCountInString(content[lineStart:offset])
}

// want is one predicted diagnostic. text is the exact source its span must
// cover; line, when non-zero, pins the 1-based start line. Messages are only
// checked by substring, so the model never pins wording.
type want struct {
	code   string
	text   string
	sev    sema.Severity
	line   int
	msgHas []string
	msgNot string
}

func wantErr(code, text string) want {
	return want{code: code, text: text, sev: sema.SeverityError}
}

func wantWarn(code, text string) want {
	return want{code: code, text: text, sev: sema.SeverityWarning}
}

func (w want) on(line int) want { w.line = line; return w }

func (w want) saying(substrings ...string) want {
	w.msgHas = append(w.msgHas, substrings...)

	return w
}

// notSaying requires the message to omit a substring: it is how the model
// separates "implicitly 6" from the unknowable value after a broken constant.
func (w want) notSaying(sub string) want { w.msgNot = sub; return w }

func (w want) String() string {
	return fmt.Sprintf("%s sev=%d text=%q line=%d msg~%v", w.code, w.sev, w.text, w.line, w.msgHas)
}

// actual is one reported diagnostic reduced to the model's vocabulary. line
// and col are the span's start in parser coordinates; text is the exact
// source the span covers.
type actual struct {
	code string
	text string
	sev  sema.Severity
	line int
	col  int
	msg  string
}

func (a actual) String() string {
	return fmt.Sprintf("%s sev=%d text=%q at %d:%d %q", a.code, a.sev, a.text, a.line, a.col, a.msg)
}

// program is one generated document graph: its files, the diagnostics the
// model predicts for each, and the include paths and analysis targets it
// needs.
type program struct {
	files        []string
	body         map[string]*strings.Builder
	wants        map[string][]want
	includePaths []string
	targets      []string
}

func newProgram() *program {
	return &program{body: map[string]*strings.Builder{}, wants: map[string][]want{}}
}

// file starts name if it does not exist yet.
func (p *program) file(name string) {
	if _, ok := p.body[name]; ok {
		return
	}

	p.files = append(p.files, name)
	p.body[name] = &strings.Builder{}
}

// add appends one source line to name and returns the 1-based line number it
// occupies. The line is always newline-terminated, so every span the model
// names sits on a line it recorded.
func (p *program) add(name, line string) int {
	p.file(name)

	b := p.body[name]
	n := strings.Count(b.String(), "\n") + 1
	b.WriteString(line)
	b.WriteString("\n")

	return n
}

func (p *program) expect(name string, ws ...want) {
	p.wants[name] = append(p.wants[name], ws...)
}

func (p *program) content(name string) string {
	b, ok := p.body[name]
	if !ok {
		return ""
	}

	return b.String()
}

// dump renders every file with line numbers, for failure output.
func (p *program) dump() string {
	var b strings.Builder

	for _, name := range p.files {
		fmt.Fprintf(&b, "--- %s\n", name)

		for i, line := range strings.Split(strings.TrimSuffix(p.content(name), "\n"), "\n") {
			fmt.Fprintf(&b, "%3d| %s\n", i+1, line)
		}
	}

	if len(p.includePaths) > 0 {
		fmt.Fprintf(&b, "include paths: %v\n", p.includePaths)
	}

	return b.String()
}

// targetURIs returns the analysis targets: the explicit ones, or every file.
func (p *program) targetURIs() []uri.URI {
	names := p.targets
	if len(names) == 0 {
		names = p.files
	}

	uris := make([]uri.URI, 0, len(names))
	for _, name := range names {
		uris = append(uris, analyzertest.URI(name))
	}

	return uris
}

// view builds the in-memory view the program runs against: memFS keeps the
// suite off the disk while still exercising include-path candidates.
func (p *program) view(tb testTB) *store.View {
	contents := make(map[uri.URI][]byte, len(p.files))
	for _, name := range p.files {
		contents[analyzertest.URI(name)] = []byte(p.content(name))
	}

	v := store.NewView(uri.File("/tmp"), store.NewMemFS(contents), p.includePaths)

	for _, name := range p.files {
		if _, err := v.Parse(context.Background(), analyzertest.URI(name)); err != nil {
			tb.Fatalf("parse %s: %v\n%s", name, err, p.dump())
		}
	}

	return v
}

// analyze runs the default analyzers over v with cfg.
func (p *program) analyze(tb testTB, v *store.View, cfg sema.Config) sema.Report {
	report, err := analyzers.DefaultPipeline(cfg).Run(context.Background(), v, p.targetURIs())
	if err != nil {
		tb.Fatalf("pipeline: %v\n%s", err, p.dump())
	}

	return report
}

// full builds a fresh view and runs the default analyzers over it.
func (p *program) full(tb testTB) sema.Report {
	return p.analyze(tb, p.view(tb), sema.Config{})
}

// collect reduces a report to actuals, checking span sanity on the way: every
// span sits inside the file, starts no later than it ends, carries the
// coordinates its offsets imply, and covers source text unless it is a
// zero-width parse diagnostic. It also rejects diagnostics keyed by a file
// the program does not know.
func (p *program) collect(tb testTB, report sema.Report) map[string][]actual {
	known := make(map[uri.URI]string, len(p.files))
	for _, name := range p.files {
		known[analyzertest.URI(name)] = name
	}

	out := make(map[string][]actual, len(report))

	for u, diags := range report {
		name, ok := known[u]
		if !ok {
			tb.Fatalf("report holds diagnostics for %s, which is not part of the program", u)
		}

		content := p.content(name)

		for _, d := range diags {
			a, problem := spanActual(content, d)
			if problem != "" {
				tb.Fatalf("%s: %s\n%s", name, problem, p.dump())
			}

			out[name] = append(out[name], a)
		}
	}

	return out
}

// verify compares the collected diagnostics against the model, per file, as
// multisets: every predicted diagnostic must appear once with its code, span
// text, severity and line, and nothing may appear that the model does not
// predict.
func (p *program) verify(tb testTB, got map[string][]actual) {
	var problems []string

	for _, name := range p.files {
		gots := slices.Clone(got[name])
		used := make([]bool, len(gots))

		for _, w := range p.wants[name] {
			matched := false

			for i, g := range gots {
				if used[i] || g.code != w.code || g.text != w.text || g.sev != w.sev {
					continue
				}

				if w.line != 0 && g.line != w.line {
					continue
				}

				if !messageMatches(g.msg, w) {
					continue
				}

				used[i] = true
				matched = true

				break
			}

			if !matched {
				problems = append(problems, fmt.Sprintf("%s: missing %s", name, w))
			}
		}

		for i, g := range gots {
			if !used[i] {
				problems = append(problems, fmt.Sprintf("%s: unexpected %s", name, g))
			}
		}
	}

	if len(problems) > 0 {
		tb.Fatalf("diagnostic model mismatch:\n%s\n\nprogram:\n%s", strings.Join(problems, "\n"), p.dump())
	}
}

func messageMatches(msg string, w want) bool {
	if w.msgNot != "" && strings.Contains(msg, w.msgNot) {
		return false
	}

	for _, sub := range w.msgHas {
		if !strings.Contains(msg, sub) {
			return false
		}
	}

	return true
}

// namer hands out identifiers unique across the whole generated program, so
// no two definitions can collide by accident. A defect injector that needs a
// collision spells both names out instead.
type namer struct{ n int }

func (nm *namer) next(prefix string) string {
	nm.n++

	return fmt.Sprintf("%s%d", prefix, nm.n)
}

// scalarTypes are the base types the valid generator emits.
var scalarTypes = []string{"bool", "i16", "i32", "i64", "double", "string"}

// constShape is one type-correct constant the generator can emit.
type constShape struct{ typ, val string }

var constShapes = []constShape{
	{"i32", "42"},
	{"i64", "9000000000"},
	{"double", "1.5"},
	{"string", `"hello"`},
	{"bool", "true"},
	{"set<i32>", "[2, 3, 5]"},
	{"list<string>", `["a", "b"]`},
	{"map<string, i32>", `{"a": 1, "b": 2}`},
	{"map<i32, string>", `{1: "a", 2: "b"}`},
	{"list<list<i32>>", "[[1], [2]]"},
}

// defaultFor returns a literal matching typ for the field types the generator
// emits.
func defaultFor(typ string) (string, bool) {
	switch typ {
	case "i16", "i32", "i64":
		return "7", true
	case "double":
		return "1.5", true
	case "string":
		return `"d"`, true
	case "bool":
		return "false", true
	case "list<i32>":
		return "[1, 2]", true
	case "set<string>":
		return `["a", "b"]`, true
	case "map<string, i32>":
		return `{"a": 1}`, true
	}

	return "", false
}

// fieldIDs draws n distinct field ids in [1, 32767], weighted toward the
// boundaries so both ends of the accepted range appear.
func fieldIDs(t *proptest.T, n int) []int {
	pool := []int{1, 2, 3, 5, 8, 13, 42, 100, 999, 32766, 32767}

	return proptest.SliceOfNDistinct(proptest.SampledFrom(pool), n, n, func(v int) int { return v }).Draw(t)
}

// enumValues draws n distinct explicit enum constants.
func enumValues(t *proptest.T, n int) []int {
	pool := []int{0, 1, 2, 3, 5, 10, 100, 1000, 1 << 20, 1<<31 - 1}

	return proptest.SliceOfNDistinct(proptest.SampledFrom(pool), n, n, func(v int) int { return v }).Draw(t)
}

// drawType draws a field type that resolves: a scalar, one of the named types
// defined so far, or a container over those. Map keys stay scalar, because a
// struct or container key is exactly the defect NonScalarMapKeyCheck reports.
func drawType(t *proptest.T, named []string) string {
	base := append(slices.Clone(scalarTypes), named...)

	switch proptest.IntRange(0, 3).Draw(t) {
	case 0:
		return proptest.SampledFrom(base).Draw(t)
	case 1:
		return "list<" + proptest.SampledFrom(base).Draw(t) + ">"
	case 2:
		return "set<" + proptest.SampledFrom(base).Draw(t) + ">"
	default:
		key := proptest.SampledFrom(scalarTypes).Draw(t)

		return "map<" + key + ", " + proptest.SampledFrom(base).Draw(t) + ">"
	}
}

// renderFunction renders one service function: a possibly-void return type,
// uniquely named arguments with distinct ids, and an optional throws list over
// a declared exception.
func renderFunction(t *proptest.T, nm *namer, named, exceptions []string) string {
	ret := "void"
	if proptest.Bool().Draw(t) {
		ret = drawType(t, named)
	}

	argCount := proptest.IntRange(0, 2).Draw(t)
	ids := fieldIDs(t, argCount)

	args := make([]string, 0, argCount)
	for i := range argCount {
		args = append(args, fmt.Sprintf("%d: %s %s", ids[i], proptest.SampledFrom(scalarTypes).Draw(t), nm.next("a")))
	}

	line := fmt.Sprintf("  %s %s(%s)", ret, nm.next("fn"), strings.Join(args, ", "))

	if len(exceptions) > 0 && proptest.Bool().Draw(t) {
		line += fmt.Sprintf(" throws (1: %s %s)", proptest.SampledFrom(exceptions).Draw(t), nm.next("e"))
	}

	return line
}

// renderValid renders a document graph that is valid by construction: every
// reference resolves, every scope holds distinct names and field ids, every
// include is used by a field, and every literal matches its declared type. Any
// diagnostic the checks report on it is therefore a false positive.
func renderValid(t *proptest.T) *program {
	p := newProgram()
	nm := &namer{}

	// Include targets: self-contained files, no includes of their own.
	nInc := proptest.IntRange(0, 2).Draw(t)
	incFiles := make([]string, nInc)
	incTypes := make([]string, nInc)

	for i := range nInc {
		incFiles[i] = fmt.Sprintf("inc%d.thrift", i)
		incTypes[i] = fmt.Sprintf("Inc%dS", i)

		p.file(incFiles[i])
		p.add(incFiles[i], "struct "+incTypes[i]+" {")
		p.add(incFiles[i], "  1: i32 v,")

		if proptest.Bool().Draw(t) {
			p.add(incFiles[i], "  2: optional string label,")
		}

		p.add(incFiles[i], "}")
	}

	main := "main.thrift"
	p.file(main)

	var included []int

	for i := range nInc {
		if proptest.Bool().Draw(t) {
			included = append(included, i)
			p.add(main, fmt.Sprintf("include %q", incFiles[i]))
		}
	}

	if len(included) > 0 {
		p.add(main, "")
	}

	// Named types in definition order, so every use follows its definition.
	var named []string

	for range proptest.IntRange(0, 2).Draw(t) {
		name := nm.next("E")

		p.add(main, "enum "+name+" {")

		for _, v := range enumValues(t, proptest.IntRange(1, 4).Draw(t)) {
			p.add(main, fmt.Sprintf("  %s = %d,", nm.next("v"), v))
		}

		p.add(main, "}")

		named = append(named, name)
	}

	for range proptest.IntRange(0, 1).Draw(t) {
		name := nm.next("T")

		switch proptest.IntRange(0, 2).Draw(t) {
		case 0:
			p.add(main, "typedef i32 "+name)
		case 1:
			p.add(main, "typedef list<string> "+name)
		default:
			p.add(main, "typedef map<string, i32> "+name)
		}

		named = append(named, name)
	}

	structs := proptest.IntRange(0, 3).Draw(t)
	if len(included) > 0 && structs == 0 {
		structs = 1
	}

	var exceptions []string

	for i := range structs {
		kind := proptest.SampledFrom([]string{"struct", "struct", "union", "exception"}).Draw(t)
		name := nm.next("S")

		fields := proptest.IntRange(0, 3).Draw(t)
		if kind != "struct" && fields == 0 {
			// union and exception need at least one member.
			fields = 1
		}

		// The first struct-like carries a field per included file, so every
		// include is used by construction.
		if i == 0 {
			fields += len(included)
		}

		ids := fieldIDs(t, fields)

		p.add(main, kind+" "+name+" {")

		for j := range fields {
			var typ string
			if i == 0 && j < len(included) {
				typ = incTypes[included[j]]
			} else {
				typ = drawType(t, named)
			}

			line := fmt.Sprintf("  %d: %s%s %s",
				ids[j],
				proptest.SampledFrom([]string{"", "required ", "optional "}).Draw(t),
				typ,
				nm.next("f"),
			)

			if def, ok := defaultFor(typ); ok && proptest.Bool().Draw(t) {
				line += " = " + def
			}

			p.add(main, line+",")
		}

		p.add(main, "}")

		if kind == "exception" {
			exceptions = append(exceptions, name)
		}

		named = append(named, name)
	}

	for range proptest.IntRange(0, 2).Draw(t) {
		shape := constShapes[proptest.IntRange(0, len(constShapes)-1).Draw(t)]
		p.add(main, "const "+shape.typ+" "+nm.next("K")+" = "+shape.val)
	}

	for range proptest.IntRange(0, 1).Draw(t) {
		p.add(main, "service "+nm.next("Svc")+" {")

		for range proptest.IntRange(0, 3).Draw(t) {
			p.add(main, renderFunction(t, nm, named, exceptions)+",")
		}

		p.add(main, "}")
	}

	// Case near-misses: the duplicate check is case-sensitive, so names that
	// differ only in case are legal in one scope and must stay silent.
	if proptest.Bool().Draw(t) {
		name := nm.next("S")
		field := nm.next("f")

		p.add(main, "struct "+name+" {")
		p.add(main, "  1: i32 "+field+",")
		p.add(main, "}")

		p.add(main, "struct "+strings.ToLower(name)+" {")
		p.add(main, "  1: i32 "+nm.next("f")+",")
		p.add(main, "  2: i32 "+strings.ToUpper(field)+",")
		p.add(main, "}")
	}

	return p
}

// spanActual reduces one diagnostic to an actual and returns a problem
// description when its span is unsound: outside the document, unordered,
// carrying coordinates its offsets contradict, or empty for a code that names
// a source range.
func spanActual(content string, d sema.Diagnostic) (actual, string) {
	start, end := d.Span.Start.Offset, d.Span.End.Offset

	if start < 0 || end > len(content) || start > end {
		return actual{}, fmt.Sprintf("%s span [%d,%d) is outside the %d-byte document", d.Code, start, end, len(content))
	}

	line, col := position(content, start)
	if line != d.Span.Start.Line || col != d.Span.Start.Col {
		return actual{}, fmt.Sprintf("%s span start offset %d is %d:%d, reported %d:%d",
			d.Code, start, line, col, d.Span.Start.Line, d.Span.Start.Col)
	}

	endLine, endCol := position(content, end)
	if endLine != d.Span.End.Line || endCol != d.Span.End.Col {
		return actual{}, fmt.Sprintf("%s span end offset %d is %d:%d, reported %d:%d",
			d.Code, end, endLine, endCol, d.Span.End.Line, d.Span.End.Col)
	}

	text := content[start:end]
	if text == "" && d.Code != sema.CodeParseError {
		return actual{}, fmt.Sprintf("%s span at %d:%d is empty but the code names a source range", d.Code, line, col)
	}

	return actual{code: d.Code, text: text, sev: d.Severity, line: line, col: col, msg: d.Message}, ""
}

// baseDoc adds the small valid document graph most defect cases build on:
// inc.thrift defines IncS, and main.thrift includes it and uses it from one
// field, so the injected defect is the only finding.
func baseDoc(p *program) string {
	inc := "inc.thrift"
	p.file(inc)
	p.add(inc, "struct IncS {")
	p.add(inc, "  1: i32 v,")
	p.add(inc, "}")

	main := "main.thrift"
	p.file(main)
	p.add(main, `include "inc.thrift"`)
	p.add(main, "struct Base {")
	p.add(main, "  1: IncS s,")
	p.add(main, "  2: i32 keep,")
	p.add(main, "}")

	return main
}

// defect names one injected fault class.
type defect int

const (
	defDupStructName defect = iota + 1
	defDupCrossKind
	defDupField
	defDupFunction
	defDupArgument
	defDupThrows
	defDupEnumMember
	defDupEnumValue
	defEnumChainCollision
	defEnumOverflowChain
	defFieldIDDuplicate
	defFieldIDOutOfRange
	defMapKeyDuplicate
	defMapKeyListDuplicate
	defSetValueDuplicate
	defNearMissNumString
	defUnusedInclude
	defShadowedInclude
	defNonScalarStructKey
	defNonScalarTypedefKey
	defNonScalarListKey
	defSelfCycle
	defTwoCycle
	defThreeCycle
	defUndefinedType
	defUndefinedValueDefault
	defUndefinedValueConst
	defMismatchStringForInt
	defMismatchIntForString
	defMismatchNested
	defMismatchBoolFromInt
)

var allDefects = []defect{
	defDupStructName, defDupCrossKind, defDupField, defDupFunction, defDupArgument,
	defDupThrows, defDupEnumMember, defDupEnumValue, defEnumChainCollision,
	defEnumOverflowChain, defFieldIDDuplicate, defFieldIDOutOfRange,
	defMapKeyDuplicate, defMapKeyListDuplicate, defSetValueDuplicate,
	defNearMissNumString, defUnusedInclude, defShadowedInclude,
	defNonScalarStructKey, defNonScalarTypedefKey, defNonScalarListKey,
	defSelfCycle, defTwoCycle, defThreeCycle, defUndefinedType,
	defUndefinedValueDefault, defUndefinedValueConst, defMismatchStringForInt,
	defMismatchIntForString, defMismatchNested, defMismatchBoolFromInt,
}

func (d defect) String() string {
	switch d {
	case defDupStructName:
		return "duplicate top-level struct name"
	case defDupCrossKind:
		return "duplicate top-level name across kinds"
	case defDupField:
		return "duplicate field name"
	case defDupFunction:
		return "duplicate service function name"
	case defDupArgument:
		return "duplicate function argument name"
	case defDupThrows:
		return "duplicate throws field name"
	case defDupEnumMember:
		return "duplicate enum member name"
	case defDupEnumValue:
		return "duplicate explicit enum value"
	case defEnumChainCollision:
		return "explicit enum value colliding with the implicit chain"
	case defEnumOverflowChain:
		return "implicit enum values after a broken constant"
	case defFieldIDDuplicate:
		return "duplicate field id"
	case defFieldIDOutOfRange:
		return "field id outside [1, 32767]"
	case defMapKeyDuplicate:
		return "duplicate map key"
	case defMapKeyListDuplicate:
		return "duplicate list-valued map key"
	case defSetValueDuplicate:
		return "duplicate set value"
	case defNearMissNumString:
		return "near-miss numeric and string map keys"
	case defUnusedInclude:
		return "unused include"
	case defShadowedInclude:
		return "shadowed include"
	case defNonScalarStructKey:
		return "non-scalar map key: struct"
	case defNonScalarTypedefKey:
		return "non-scalar map key: typedef to struct"
	case defNonScalarListKey:
		return "non-scalar map key: list"
	case defSelfCycle:
		return "self include cycle"
	case defTwoCycle:
		return "two-file include cycle"
	case defThreeCycle:
		return "three-file include cycle"
	case defUndefinedType:
		return "undefined type"
	case defUndefinedValueDefault:
		return "undefined value in a field default"
	case defUndefinedValueConst:
		return "undefined value in a const"
	case defMismatchStringForInt:
		return "string literal for an int field"
	case defMismatchIntForString:
		return "int literal for a string field"
	case defMismatchNested:
		return "mismatched literal inside a list"
	case defMismatchBoolFromInt:
		return "int literal for a bool field"
	}

	return "defect"
}

// renderDefect renders the document graph for one defect class: exactly one
// thing in it is wrong, and the expectations say what the checks must report
// about it.
func renderDefect(t *proptest.T, p *program, d defect) {
	switch d {
	case defDupStructName:
		main := baseDoc(p)
		name := "Dup" + strconv.Itoa(proptest.IntRange(1, 3).Draw(t))

		p.add(main, "struct "+name+" {")
		p.add(main, "  1: i32 first,")
		p.add(main, "}")

		line := p.add(main, "struct "+name+" {")
		p.add(main, "  1: i32 second,")
		p.add(main, "}")

		p.expect(main, wantErr(sema.CodeDuplicateDef, name).on(line).saying("duplicate struct "+name))

	case defDupCrossKind:
		main := baseDoc(p)
		name := "Ambig" + strconv.Itoa(proptest.IntRange(1, 3).Draw(t))

		p.add(main, "struct "+name+" {")
		p.add(main, "  1: i32 only,")
		p.add(main, "}")

		line := p.add(main, "enum "+name+" {")
		p.add(main, "  A = 0,")
		p.add(main, "}")

		// Top-level definitions share one scope whatever their kind, and the
		// check walks structs before enums, so the enum's name repeats.
		p.expect(main, wantErr(sema.CodeDuplicateDef, name).on(line).saying("duplicate enum "+name))

	case defDupField:
		main := baseDoc(p)
		ids := fieldIDs(t, 2)
		name := "same"

		p.add(main, "struct Twins {")
		p.add(main, fmt.Sprintf("  %d: i32 %s,", ids[0], name))
		line := p.add(main, fmt.Sprintf("  %d: i32 %s,", ids[1], name))
		p.add(main, "}")

		p.expect(main, wantErr(sema.CodeDuplicateDef, name).on(line).saying("duplicate field "+name))

	case defDupFunction:
		main := baseDoc(p)
		name := "twice"

		p.add(main, "service Svc {")
		p.add(main, "  void "+name+"(),")
		line := p.add(main, "  void "+name+"(),")
		p.add(main, "}")

		p.expect(main, wantErr(sema.CodeDuplicateDef, name).on(line).saying("duplicate function "+name))

	case defDupArgument:
		main := baseDoc(p)
		ids := fieldIDs(t, 2)
		name := "depth"

		p.add(main, "service Svc {")
		line := p.add(main, fmt.Sprintf("  void call(%d: i32 %s, %d: i32 %s),", ids[0], name, ids[1], name))
		p.add(main, "}")

		p.expect(main, wantErr(sema.CodeDuplicateDef, name).on(line).saying("duplicate argument "+name))

	case defDupThrows:
		main := baseDoc(p)
		ids := fieldIDs(t, 2)
		name := "err"

		p.add(main, "service Svc {")
		line := p.add(main, fmt.Sprintf("  void call() throws (%d: IncS %s, %d: IncS %s),", ids[0], name, ids[1], name))
		p.add(main, "}")

		p.expect(main, wantErr(sema.CodeDuplicateDef, name).on(line).saying("duplicate field "+name))

	case defDupEnumMember:
		main := baseDoc(p)

		p.add(main, "enum Members {")
		p.add(main, "  A = 0,")
		line := p.add(main, "  A = 7,")
		p.add(main, "}")

		p.expect(main, wantErr(sema.CodeDuplicateDef, "A").on(line).saying("duplicate member A"))

	case defDupEnumValue:
		main := baseDoc(p)
		value := proptest.SampledFrom([]int{1, 2, 5}).Draw(t)

		p.add(main, "enum Values {")
		p.add(main, fmt.Sprintf("  First = %d,", value))
		line := p.add(main, fmt.Sprintf("  Second = %d,", value))
		p.add(main, "}")

		p.expect(main, wantErr(sema.CodeDuplicateEnumVal, "Second").on(line).saying("duplicates First"))

	case defEnumChainCollision:
		main := baseDoc(p)
		first := proptest.IntRange(0, 5).Draw(t)

		p.add(main, "enum Chain {")
		p.add(main, fmt.Sprintf("  A = %d,", first))
		lineImplicit := p.add(main, "  B,")
		lineCollide := p.add(main, fmt.Sprintf("  C = %d,", first+1))
		p.add(main, "}")

		// B auto-increments onto A+1, the value C states explicitly.
		p.expect(main, wantWarn(sema.CodeImplicitEnumValue, "B").on(lineImplicit).saying("implicitly"))
		p.expect(main, wantErr(sema.CodeDuplicateEnumVal, "C").on(lineCollide).saying("duplicates B"))

	case defEnumOverflowChain:
		main := baseDoc(p)

		p.add(main, "enum Overflow {")
		p.add(main, "  Broken = 99999999999999999999,")
		lineUnknown := p.add(main, "  Unknown,")
		p.add(main, "  Settled = 5,")
		lineKnown := p.add(main, "  Known,")
		p.add(main, "}")

		// The constant overflows int64, so the chain is unknowable until
		// Settled re-anchors it: the value is omitted from Unknown's message.
		p.expect(main, wantWarn(sema.CodeImplicitEnumValue, "Unknown").on(lineUnknown).notSaying("implicitly"))
		p.expect(main, wantWarn(sema.CodeImplicitEnumValue, "Known").on(lineKnown).saying("implicitly 6"))

	case defFieldIDDuplicate:
		main := baseDoc(p)
		id := proptest.SampledFrom([]string{"1", "2", "42", "32767"}).Draw(t)

		p.add(main, "struct Clash {")
		line1 := p.add(main, "  "+id+": i32 a,")
		line2 := p.add(main, "  "+id+": i32 b,")
		p.add(main, "}")

		p.expect(main, wantErr(sema.CodeFieldIDConflict, id).on(line1))
		p.expect(main, wantErr(sema.CodeFieldIDConflict, id).on(line2))

	case defFieldIDOutOfRange:
		main := baseDoc(p)
		lit := proptest.SampledFrom([]string{"0", "-1", "32768", "0x8000", "2147483648"}).Draw(t)

		p.add(main, "struct Bad {")
		line := p.add(main, "  "+lit+": i32 value,")
		p.add(main, "}")

		p.expect(main, wantErr(sema.CodeFieldIDRange, lit).on(line))

	case defMapKeyDuplicate:
		main := baseDoc(p)
		c := proptest.SampledFrom(mapKeyCases).Draw(t)

		entries := make([]string, 0, len(c.keys))
		for i, key := range c.keys {
			entries = append(entries, fmt.Sprintf("%s: %d", key, i+1))
		}

		line := p.add(main, "const "+c.typ+" M = { "+strings.Join(entries, ", ")+" }")

		for _, dup := range c.dups {
			p.expect(main, wantErr(sema.CodeDuplicateValue, dup).on(line).saying("duplicate map key"))
		}

	case defMapKeyListDuplicate:
		main := baseDoc(p)

		line := p.add(main, "const map<list<i32>, i32> M = { [1, 2]: 3, [1, 2]: 4 }")

		// The key type is a container: the non-scalar check reports the type,
		// the duplicate check the repeated literal.
		p.expect(main, wantWarn(sema.CodeNonScalarMapKey, "list<i32>").on(line).saying("found list"))
		p.expect(main, wantErr(sema.CodeDuplicateValue, "[1, 2]").on(line).saying("duplicate map key"))

	case defSetValueDuplicate:
		main := baseDoc(p)
		c := proptest.SampledFrom(setValueCases).Draw(t)

		line := p.add(main, "const "+c.typ+" S = [ "+strings.Join(c.vals, ", ")+" ]")

		for _, dup := range c.dups {
			p.expect(main, wantErr(sema.CodeDuplicateValue, dup).on(line).saying("duplicate set value"))
		}

	case defNearMissNumString:
		main := baseDoc(p)

		// valueKey canonicalizes integers and strips string quotes, so 1 and
		// "1" never collide; the integer is a type mismatch, nothing more.
		line := p.add(main, `const map<string, i32> M = { 1: 1, "1": 2 }`)

		p.expect(main, wantErr(sema.CodeValueTypeMismatch, "1").on(line).saying("expect string"))

	case defUnusedInclude:
		main := "main.thrift"
		p.file(main)

		line := p.add(main, `include "unused.thrift"`)
		p.add(main, "struct User {")
		p.add(main, "  1: i32 value,")
		p.add(main, "}")

		inc := "unused.thrift"
		p.file(inc)
		p.add(inc, "struct Unused {")
		p.add(inc, "  1: i32 v,")
		p.add(inc, "}")

		p.expect(main, wantWarn(sema.CodeUnusedInclude, `include "unused.thrift"`).on(line).saying("unused include"))

	case defShadowedInclude:
		main := "main.thrift"
		p.file(main)

		line := p.add(main, `include "recipes/stew.thrift"`)
		p.add(main, "struct Party {")
		p.add(main, "  1: Stew stew,")
		p.add(main, "}")

		for _, root := range []string{"camp-a", "camp-b"} {
			name := root + "/recipes/stew.thrift"
			p.file(name)
			p.add(name, "struct Stew {")
			p.add(name, "  1: i32 servings,")
			p.add(name, "}")
		}

		// Both include paths hold the file; the nearer one by config order
		// wins and the other is named as shadowed. Sharing /tmp with the
		// document's directory ties them, so config order breaks the tie.
		p.includePaths = []string{"/tmp/camp-a", "/tmp/camp-b"}

		p.expect(main, wantWarn(sema.CodeIncludeShadow, `include "recipes/stew.thrift"`).on(line).
			saying("matches multiple include paths", "/tmp/camp-a/recipes/stew.thrift"))

	case defNonScalarStructKey:
		main := baseDoc(p)

		p.add(main, "struct KeyStone {")
		p.add(main, "  1: i32 depth,")
		p.add(main, "}")
		p.add(main, "struct Vault {")
		line := p.add(main, "  1: map<KeyStone, i32> byStruct,")
		p.add(main, "}")

		p.expect(main, wantWarn(sema.CodeNonScalarMapKey, "KeyStone").on(line).saying("found struct"))

	case defNonScalarTypedefKey:
		main := baseDoc(p)

		p.add(main, "typedef IncS KeyAlias")
		p.add(main, "struct Vault {")
		line := p.add(main, "  1: map<KeyAlias, i32> byAlias,")
		p.add(main, "}")

		p.expect(main, wantWarn(sema.CodeNonScalarMapKey, "KeyAlias").on(line).saying("found struct"))

	case defNonScalarListKey:
		main := baseDoc(p)

		p.add(main, "struct Vault {")
		line := p.add(main, "  1: map<list<i32>, i32> byList,")
		p.add(main, "}")

		p.expect(main, wantWarn(sema.CodeNonScalarMapKey, "list<i32>").on(line).saying("found list"))

	case defSelfCycle:
		main := "main.thrift"
		p.file(main)

		line := p.add(main, `include "main.thrift"`)
		p.add(main, "struct Alone {")
		p.add(main, "  1: i32 value,")
		p.add(main, "}")

		// A self include closes a one-edge cycle; nothing can reference the
		// file through it, so the statement is also an unused include.
		p.expect(main, wantWarn(sema.CodeIncludeCycle, `include "main.thrift"`).on(line))
		p.expect(main, wantWarn(sema.CodeUnusedInclude, `include "main.thrift"`).on(line))

	case defTwoCycle:
		main := "main.thrift"
		p.file(main)
		p.add(main, `include "other.thrift"`)
		p.add(main, "struct MS {")
		p.add(main, "  1: OS other,")
		p.add(main, "}")

		other := "other.thrift"
		p.file(other)
		p.add(other, `include "main.thrift"`)
		p.add(other, "struct OS {")
		p.add(other, "  1: MS main,")
		p.add(other, "}")

		// Analyze main alone: the cycle closure pulls other in, and each
		// file's edge back to the other is reported exactly once.
		p.targets = []string{main}
		p.expect(main, wantWarn(sema.CodeIncludeCycle, `include "other.thrift"`))
		p.expect(other, wantWarn(sema.CodeIncludeCycle, `include "main.thrift"`))

	case defThreeCycle:
		main := "main.thrift"
		p.file(main)
		p.add(main, `include "a.thrift"`)
		p.add(main, "struct MS {")
		p.add(main, "  1: AS a,")
		p.add(main, "}")

		a := "a.thrift"
		p.file(a)
		p.add(a, `include "b.thrift"`)
		p.add(a, "struct AS {")
		p.add(a, "  1: BS b,")
		p.add(a, "}")

		b := "b.thrift"
		p.file(b)
		p.add(b, `include "main.thrift"`)
		p.add(b, "struct BS {")
		p.add(b, "  1: MS main,")
		p.add(b, "}")

		p.targets = []string{main}
		p.expect(main, wantWarn(sema.CodeIncludeCycle, `include "a.thrift"`))
		p.expect(a, wantWarn(sema.CodeIncludeCycle, `include "b.thrift"`))
		p.expect(b, wantWarn(sema.CodeIncludeCycle, `include "main.thrift"`))

	case defUndefinedType:
		main := baseDoc(p)
		name := proptest.SampledFrom([]string{"Missing", "Nope", "Absent"}).Draw(t)

		p.add(main, "struct Bad {")
		line := p.add(main, "  1: "+name+" value,")
		p.add(main, "}")

		p.expect(main, wantErr(sema.CodeUndefinedType, name).on(line))

	case defUndefinedValueDefault:
		main := baseDoc(p)
		name := proptest.SampledFrom([]string{"Missing", "Nope", "Absent"}).Draw(t)

		p.add(main, "struct Bad {")
		line := p.add(main, "  1: i32 value = "+name+",")
		p.add(main, "}")

		p.expect(main, wantErr(sema.CodeUndefinedValue, name).on(line))

	case defUndefinedValueConst:
		main := baseDoc(p)
		name := proptest.SampledFrom([]string{"Missing", "Nope", "Absent"}).Draw(t)

		line := p.add(main, "const i32 K = "+name)

		p.expect(main, wantErr(sema.CodeUndefinedValue, name).on(line))

	case defMismatchStringForInt:
		main := baseDoc(p)

		p.add(main, "struct Bad {")
		line := p.add(main, `  1: i32 value = "text",`)
		p.add(main, "}")

		p.expect(main, wantErr(sema.CodeValueTypeMismatch, `"text"`).on(line).saying("expect i32"))

	case defMismatchIntForString:
		main := baseDoc(p)

		p.add(main, "struct Bad {")
		line := p.add(main, "  1: string value = 4,")
		p.add(main, "}")

		p.expect(main, wantErr(sema.CodeValueTypeMismatch, "4").on(line).saying("expect string"))

	case defMismatchNested:
		main := baseDoc(p)

		p.add(main, "struct Bad {")
		line := p.add(main, `  1: list<i32> values = [1, "x"],`)
		p.add(main, "}")

		p.expect(main, wantErr(sema.CodeValueTypeMismatch, `"x"`).on(line).saying("expect i32"))

	case defMismatchBoolFromInt:
		main := baseDoc(p)

		p.add(main, "struct Bad {")
		line := p.add(main, "  1: bool value = 1,")
		p.add(main, "}")

		p.expect(main, wantErr(sema.CodeValueTypeMismatch, "1").on(line).saying("expect bool"))
	}
}

// mapKeyCases are map literals with one repeated key, spelled the ways the
// normalizer folds together: quote style, hex versus decimal, and a third
// occurrence of an already-reported key.
var mapKeyCases = []struct {
	typ  string
	keys []string
	dups []string
}{
	{"map<string, i32>", []string{`"k"`, `"k"`}, []string{`"k"`}},
	{"map<string, i32>", []string{`"k"`, `'k'`}, []string{`'k'`}},
	{"map<i32, i32>", []string{"0x10", "16"}, []string{"16"}},
	{"map<i32, i32>", []string{"1", "0x1"}, []string{"0x1"}},
	{"map<i32, i32>", []string{"1", "0x1", "1"}, []string{"0x1", "1"}},
}

// setValueCases are set literals with one repeated value.
var setValueCases = []struct {
	typ  string
	vals []string
	dups []string
}{
	{"set<string>", []string{`"x"`, `'x'`}, []string{`'x'`}},
	{"set<i32>", []string{"4", "4"}, []string{"4"}},
}

// renderCase renders either a valid document or one carrying a single defect,
// so the lint-config properties see both shapes.
func renderCase(t *proptest.T) *program {
	if proptest.Bool().Draw(t) {
		p := newProgram()
		renderDefect(t, p, proptest.SampledFrom(allDefects).Draw(t))

		return p
	}

	return renderValid(t)
}

// analyzerNames lists the checks the default pipeline runs.
func analyzerNames() []string {
	defaults := analyzers.Defaults()
	names := make([]string, 0, len(defaults))

	for _, a := range defaults {
		names = append(names, a.Name())
	}

	return names
}

// allBut returns every name except keep, for a single-analyzer run.
func allBut(names []string, keep string) []string {
	out := make([]string, 0, len(names)-1)

	for _, n := range names {
		if n != keep {
			out = append(out, n)
		}
	}

	return out
}

// signature reduces an actual to a comparable string. Diagnostics are
// compared order-insensitively here, because only the determinism property
// pins emission order.
func signature(a actual) string {
	return fmt.Sprintf("%s|%q|%d|%d:%d|%s", a.code, a.text, a.sev, a.line, a.col, a.msg)
}

func signatures(as []actual) []string {
	out := make([]string, 0, len(as))

	for _, a := range as {
		out = append(out, signature(a))
	}

	slices.Sort(out)

	return out
}

// removeSignatures subtracts the remove multiset from from.
func removeSignatures(from, remove []string) []string {
	counts := make(map[string]int, len(remove))
	for _, s := range remove {
		counts[s]++
	}

	out := make([]string, 0, len(from))

	for _, s := range from {
		if counts[s] > 0 {
			counts[s]--

			continue
		}

		out = append(out, s)
	}

	slices.Sort(out)

	return out
}

// propertySeeds are the fuzz inputs plain `go test` replays before fuzzing.
// They mix empty, textual and binary streams so a seedless run still varies
// the draws.
var propertySeeds = proptest.Corpus{
	[]byte(""),
	[]byte("valid thrift document"),
	[]byte("cycles, duplicate names and unused includes"),
	{0x00, 0x01, 0x02, 0x7f},
	{0xff, 0xfe, 0x80, 0x00},
	[]byte("near-miss keys: 0x10 vs 16"),
}

// FuzzValidDocumentsProduceNoFindings is the acceptance direction: the
// generator only emits valid Thrift, so any diagnostic is a false positive.
func FuzzValidDocumentsProduceNoFindings(f *testing.F) {
	proptest.Fuzz(f, propertySeeds, func(tb *testing.T, seed []byte) {
		t := proptest.New(tb, seed)

		p := renderValid(t)
		p.verify(t, p.collect(t, p.full(t)))
	})
}

// Test_ValidNearMissesProduceNoFindings checks that legal constructs which
// resemble defects stay silent.
func Test_ValidNearMissesProduceNoFindings(t *testing.T) {
	cases := []struct {
		name   string
		source string
	}{
		{
			name:   "list keeps duplicate values",
			source: "const list<i32> L = [1, 1]\nconst list<string> N = [\"x\", 'x']\n",
		},
		{
			name:   "names differing only by case",
			source: "struct Foo {\n  1: i32 x,\n}\nstruct foo {\n  1: i32 X,\n}\n",
		},
		{
			name:   "enum-keyed map",
			source: "enum Rune {\n  A = 0,\n  B = 1,\n}\nstruct S {\n  1: map<Rune, i32> byRune,\n}\n",
		},
		{
			name:   "field id boundaries",
			source: "struct S {\n  1: i32 first,\n  32767: i32 last,\n}\n",
		},
		{
			name:   "near-miss normalized keys",
			source: "const map<string, i32> M = { \"1\": 1, \"01\": 2, \"k\": 3, \"K\": 4 }\nconst map<i32, i32> N = { 0x10: 1, 17: 2, 1: 3 }\n",
		},
		{
			name:   "same field name in different scopes",
			source: "struct A {\n  1: i32 id,\n}\nstruct B {\n  1: i32 id,\n}\nservice Svc {\n  void call(1: i32 id),\n}\n",
		},
		{
			name:   "distinct explicit enum values",
			source: "enum E {\n  A = 0,\n  B = 1,\n  C = 100,\n}\n",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			p := newProgram()
			main := "main.thrift"
			p.file(main)

			for _, line := range strings.Split(strings.TrimSuffix(tc.source, "\n"), "\n") {
				p.add(main, line)
			}

			p.verify(t, p.collect(t, p.full(t)))
		})
	}
}

// FuzzSingleDefectIsDetected injects one defect per case and asserts the
// report matches the model exactly: the offending code, at the offending
// span, once, and nothing else.
func FuzzSingleDefectIsDetected(f *testing.F) {
	proptest.Fuzz(f, propertySeeds, func(tb *testing.T, seed []byte) {
		t := proptest.New(tb, seed)

		p := newProgram()
		renderDefect(t, p, proptest.SampledFrom(allDefects).Draw(t))
		p.verify(t, p.collect(t, p.full(t)))
	})
}

// Test_SingleDefectIsDetected replays every defect class over the property
// seeds, so plain `go test` names each one instead of relying on which defect
// a seed happens to draw.
func Test_SingleDefectIsDetected(t *testing.T) {
	for _, d := range allDefects {
		t.Run(d.String(), func(t *testing.T) {
			propertySeeds.Run(t, func(ft *proptest.T) {
				p := newProgram()
				renderDefect(ft, p, d)
				p.verify(ft, p.collect(ft, p.full(ft)))
			})
		})
	}
}

// FuzzDisabledAnalyzersRemoveExactlyTheirFindings is the lint-config
// metamorphic: the combined report is the union of the per-analyzer reports,
// and Disabled removes exactly the named check's findings.
func FuzzDisabledAnalyzersRemoveExactlyTheirFindings(f *testing.F) {
	proptest.Fuzz(f, propertySeeds, func(tb *testing.T, seed []byte) {
		t := proptest.New(tb, seed)

		p := renderCase(t)
		v := p.view(t)

		full := p.collect(t, p.analyze(t, v, sema.Config{}))
		names := analyzerNames()

		want := make(map[string][]string, len(full))
		for file, as := range full {
			want[file] = signatures(as)
		}

		per := make(map[string]map[string][]actual, len(names))
		union := make(map[string][]string)

		for _, name := range names {
			per[name] = p.collect(t, p.analyze(t, v, sema.Config{Disabled: allBut(names, name)}))

			for file, as := range per[name] {
				union[file] = append(union[file], signatures(as)...)
			}
		}

		for file := range union {
			slices.Sort(union[file])
		}

		for file := range want {
			if !slices.Equal(want[file], union[file]) {
				t.Fatalf("%s: combined report is not the union of the per-analyzer reports:\nwith all: %v\nper-check: %v\n%s",
					file, want[file], union[file], p.dump())
			}
		}

		if len(union) != len(want) {
			t.Fatalf("per-analyzer runs report files the combined run does not: %v vs %v\n%s", union, want, p.dump())
		}

		disabled := proptest.SampledFrom(names).Draw(t)
		got := p.collect(t, p.analyze(t, v, sema.Config{Disabled: []string{disabled}}))

		gotSigs := make(map[string][]string, len(got))
		for file, as := range got {
			gotSigs[file] = signatures(as)
		}

		wantMinus := make(map[string][]string, len(want))
		for file, sigs := range want {
			if remaining := removeSignatures(sigs, signatures(per[disabled][file])); len(remaining) > 0 {
				wantMinus[file] = remaining
			}
		}

		for file := range wantMinus {
			if !slices.Equal(gotSigs[file], wantMinus[file]) {
				t.Fatalf("%s: Disabled=[%s] did not remove exactly that check's findings:\nwant: %v\ngot:  %v\n%s",
					file, disabled, wantMinus[file], gotSigs[file], p.dump())
			}
		}

		if len(gotSigs) != len(wantMinus) {
			t.Fatalf("Disabled=[%s] left files with findings the model does not predict: %v vs %v\n%s",
				disabled, gotSigs, wantMinus, p.dump())
		}
	})
}

// FuzzSeverityOverridesChangeOnlySeverity checks that a Severity override
// rewrites severity and nothing else: same diagnostics, same spans, same
// messages, same order.
func FuzzSeverityOverridesChangeOnlySeverity(f *testing.F) {
	proptest.Fuzz(f, propertySeeds, func(tb *testing.T, seed []byte) {
		t := proptest.New(tb, seed)

		p := renderCase(t)
		v := p.view(t)

		base := p.collect(t, p.analyze(t, v, sema.Config{}))

		var codes []string
		for _, as := range base {
			for _, a := range as {
				if !slices.Contains(codes, a.code) {
					codes = append(codes, a.code)
				}
			}
		}

		if len(codes) == 0 {
			// A valid program has nothing to override.
			return
		}

		code := proptest.SampledFrom(codes).Draw(t)
		sev := proptest.SampledFrom([]sema.Severity{
			sema.SeverityError, sema.SeverityWarning, sema.SeverityInfo, sema.SeverityHint,
		}).Draw(t)

		got := p.collect(t, p.analyze(t, v, sema.Config{Severity: map[string]sema.Severity{code: sev}}))

		for _, file := range p.files {
			if len(base[file]) != len(got[file]) {
				t.Fatalf("%s: severity override changed the diagnostic count: %v vs %v\n%s",
					file, base[file], got[file], p.dump())
			}

			for i := range base[file] {
				wantSev := base[file][i].sev
				if base[file][i].code == code {
					wantSev = sev
				}

				if got[file][i].code != base[file][i].code ||
					got[file][i].text != base[file][i].text ||
					got[file][i].line != base[file][i].line ||
					got[file][i].col != base[file][i].col ||
					got[file][i].msg != base[file][i].msg ||
					got[file][i].sev != wantSev {
					t.Fatalf("%s: severity override changed more than severity for %s:\nbefore: %s\nafter:  %s\n%s",
						file, code, base[file][i], got[file][i], p.dump())
				}
			}
		}
	})
}

// FuzzReportIsDeterministic checks that fresh views report diagnostics in
// the same order.
func FuzzReportIsDeterministic(f *testing.F) {
	proptest.Fuzz(f, propertySeeds, func(tb *testing.T, seed []byte) {
		t := proptest.New(tb, seed)

		p := renderCase(t)

		first := p.collect(t, p.analyze(t, p.view(t), sema.Config{}))
		second := p.collect(t, p.analyze(t, p.view(t), sema.Config{}))

		for _, file := range p.files {
			if !slices.Equal(first[file], second[file]) {
				t.Fatalf("%s: diagnostics differ between two runs over identical documents:\nfirst:  %v\nsecond: %v\n%s",
					file, first[file], second[file], p.dump())
			}
		}
	})
}

// corpusExpectations lists the diagnostics the tests/ corpus must produce by
// code. A nil map claims the file is valid Thrift.
var corpusExpectations = []struct {
	path  string
	codes map[string]int
}{
	{"tests/e2e/annotations/annotations.thrift", map[string]int{sema.CodeUndefinedType: 1}},
	{"tests/e2e/enums/enums.thrift", nil},
	{"tests/e2e/field_line_comma/fields.thrift", nil},
	{"tests/e2e/fields/fields.thrift", nil},
	{"tests/evernote-thrift/src/Errors.thrift", map[string]int{sema.CodeImplicitEnumValue: 3}},
	{"tests/evernote-thrift/src/Limits.thrift", nil},
	{"tests/evernote-thrift/src/NoteStore.thrift", map[string]int{sema.CodeUnusedInclude: 1}},
	{"tests/evernote-thrift/src/Types.thrift", map[string]int{sema.CodeUnusedInclude: 1}},
	{"tests/evernote-thrift/src/UserStore.thrift", nil},
	{"tests/evernote-thrift/src/test.thrift", nil},
	{"tests/galaxy-thrift-api/io/RSFile.thrift", nil},
	{"tests/galaxy-thrift-api/sds/Admin.thrift", map[string]int{sema.CodeUnusedInclude: 1}},
	{"tests/galaxy-thrift-api/sds/Authentication.thrift", nil},
	{"tests/galaxy-thrift-api/sds/Common.thrift", nil},
	{"tests/galaxy-thrift-api/sds/Errors.thrift", nil},
	{"tests/galaxy-thrift-api/sds/SLFile.thrift", nil},
	{"tests/galaxy-thrift-api/sds/Table.thrift", map[string]int{
		sema.CodeParseError: 2, sema.CodeImplicitEnumValue: 3, sema.CodeUnusedInclude: 1,
	}},
	{"tests/line-protocol/line.thrift", nil},
	{"tests/made-in-abyss/abyss.thrift", nil},
	{"tests/made-in-abyss/cycle_a.thrift", map[string]int{sema.CodeIncludeCycle: 1}},
	{"tests/made-in-abyss/cycle_b.thrift", map[string]int{sema.CodeIncludeCycle: 1}},
	{"tests/made-in-abyss/delvers.thrift", nil},
	{"tests/made-in-abyss/lints.thrift", map[string]int{
		sema.CodeUnusedInclude: 1, sema.CodeFieldIDConflict: 4, sema.CodeFieldIDRange: 3,
		sema.CodeDuplicateDef: 6, sema.CodeDuplicateEnumVal: 1, sema.CodeDuplicateValue: 2,
		sema.CodeImplicitEnumValue: 5, sema.CodeUndefinedType: 2, sema.CodeNonScalarMapKey: 1,
	}},
	{"tests/made-in-abyss/orth.thrift", nil},
	{"tests/made-in-abyss/unused.thrift", nil},
}

// analyzeCorpusFile runs the default pipeline over one file on disk, with the
// file as the only analysis target and its directory as the view root, and
// returns its diagnostics as actuals with spans already verified.
func analyzeCorpusFile(t *testing.T, path string) []actual {
	t.Helper()

	abs, err := filepath.Abs(path)
	if err != nil {
		t.Fatalf("abs %s: %v", path, err)
	}

	content, err := os.ReadFile(abs)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}

	view := store.NewView(uri.File(filepath.Dir(abs)), store.NewDiskFS(), nil)
	u := uri.File(abs)

	if _, err := view.Parse(t.Context(), u); err != nil {
		t.Fatalf("parse %s: %v", path, err)
	}

	report, err := analyzers.DefaultPipeline(sema.Config{}).Run(t.Context(), view, []uri.URI{u})
	if err != nil {
		t.Fatalf("pipeline %s: %v", path, err)
	}

	out := make([]actual, 0, len(report[u]))

	for _, d := range report[u] {
		a, problem := spanActual(string(content), d)
		if problem != "" {
			t.Fatalf("%s: %s", path, problem)
		}

		out = append(out, a)
	}

	return out
}

// Test_CorpusReplay replays every corpus fixture through the default
// pipeline and checks the finding set against the hand-verified model, plus
// the code-specific predicate each check's doc comment implies and run-to-run
// determinism.
func Test_CorpusReplay(t *testing.T) {
	covered := make(map[string]bool, len(corpusExpectations))
	for _, tc := range corpusExpectations {
		covered[tc.path] = true
	}

	err := filepath.WalkDir(filepath.Join("..", "tests"), func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}

		if d.IsDir() || !strings.HasSuffix(path, ".thrift") {
			return nil
		}

		name := strings.TrimPrefix(filepath.ToSlash(path), "../")
		if !covered[name] {
			t.Errorf("corpus file %s has no expectation; add it to corpusExpectations", name)
		}

		return nil
	})
	if err != nil {
		t.Fatalf("walk corpus: %v", err)
	}

	for _, tc := range corpusExpectations {
		t.Run(tc.path, func(t *testing.T) {
			first := analyzeCorpusFile(t, filepath.Join("..", tc.path))

			counts := make(map[string]int, len(first))
			for _, a := range first {
				counts[a.code]++
			}

			for code, want := range tc.codes {
				if counts[code] != want {
					t.Errorf("%s count = %d, want %d (%v)", code, counts[code], want, first)
				}
			}

			for code, got := range counts {
				if tc.codes[code] != got {
					t.Errorf("unexpected %s finding (count %d): %v", code, got, first)
				}
			}

			for _, a := range first {
				checkCorpusDiagnostic(t, a)
			}

			second := analyzeCorpusFile(t, filepath.Join("..", tc.path))
			if !slices.Equal(first, second) {
				t.Errorf("diagnostics differ between two runs:\nfirst:  %v\nsecond: %v", first, second)
			}
		})
	}
}

// checkCorpusDiagnostic checks one corpus finding against the predicate its
// code implies, independently of the analyzer's own bookkeeping.
func checkCorpusDiagnostic(t *testing.T, a actual) {
	t.Helper()

	switch a.code {
	case sema.CodeFieldIDRange:
		if n, err := strconv.ParseInt(a.text, 0, 64); err == nil && n >= 1 && n <= 32767 {
			t.Errorf("field-id-range reported for in-range id %q", a.text)
		}
	case sema.CodeFieldIDConflict:
		if _, err := strconv.ParseInt(a.text, 0, 64); err != nil {
			// Only an overflowing literal fails to parse; it is out of range.
			if !strings.Contains(err.Error(), "value out of range") {
				t.Errorf("field-id-conflict reported for %q, which is not an integer", a.text)
			}
		}
	case sema.CodeImplicitEnumValue:
		if !isIdentifier(a.text) {
			t.Errorf("implicit-enum-value span %q is not a member name", a.text)
		}

		if i := strings.Index(a.msg, "(implicitly "); i >= 0 {
			digits := strings.TrimSuffix(a.msg[i+len("(implicitly "):], ")")
			if _, err := strconv.ParseInt(digits, 10, 64); err != nil {
				t.Errorf("implicit-enum-value message %q carries no value", a.msg)
			}
		}
	case sema.CodeUnusedInclude, sema.CodeIncludeCycle, sema.CodeIncludeShadow:
		if !strings.HasPrefix(a.text, "include ") {
			t.Errorf("%s span %q is not an include statement", a.code, a.text)
		}
	case sema.CodeParseError:
		if a.sev != sema.SeverityError && a.sev != sema.SeverityWarning {
			t.Errorf("parse diagnostic with severity %d", a.sev)
		}
	case sema.CodeUndefinedType, sema.CodeUndefinedValue:
		if !isIdentifier(a.text) {
			t.Errorf("%s span %q is not an identifier", a.code, a.text)
		}
	case sema.CodeDuplicateDef, sema.CodeDuplicateValue, sema.CodeDuplicateEnumVal,
		sema.CodeValueTypeMismatch, sema.CodeNonScalarMapKey, sema.CodeUnknownAnnotation:
		if strings.TrimSpace(a.text) == "" {
			t.Errorf("%s span is blank", a.code)
		}
	}
}

func isIdentifier(s string) bool {
	if s == "" {
		return false
	}

	for _, r := range s {
		switch {
		case r == '_':
		case r >= 'a' && r <= 'z':
		case r >= 'A' && r <= 'Z':
		case r >= '0' && r <= '9':
		default:
			return false
		}
	}

	return true
}

// Test_CorpusLintsFixture is the corpus file that deliberately contains one
// instance of every check, so its full finding set is a model-verified case:
// the fixture's own comment documents each expected diagnostic, and this test
// spells them out with the line and span text they must land on.
func Test_CorpusLintsFixture(t *testing.T) {
	model := []struct {
		line int
		code string
		text string
	}{
		{37, sema.CodeUnusedInclude, `include "unused.thrift"`},
		{41, sema.CodeFieldIDConflict, "1"},
		{42, sema.CodeFieldIDConflict, "1"},
		{49, sema.CodeFieldIDRange, "40000"},
		{50, sema.CodeFieldIDRange, "0"},
		{51, sema.CodeFieldIDRange, "-1"},
		{60, sema.CodeDuplicateDef, "Reg"},
		{67, sema.CodeDuplicateDef, "same_name"},
		{74, sema.CodeFieldIDConflict, "1"},
		{75, sema.CodeFieldIDConflict, "1"},
		{75, sema.CodeDuplicateDef, "repeat"},
		{83, sema.CodeImplicitEnumValue, "STAR_COMPASS"},
		{85, sema.CodeImplicitEnumValue, "UNHEARD_BELL"},
		{87, sema.CodeImplicitEnumValue, "CROSSED_STILLS"},
		{93, sema.CodeDuplicateEnumVal, "MITTY"},
		{98, sema.CodeImplicitEnumValue, "RIKO"},
		{99, sema.CodeImplicitEnumValue, "NANACHI"},
		{100, sema.CodeDuplicateDef, "RIKO"},
		{106, sema.CodeDuplicateDef, "depth"},
		{107, sema.CodeDuplicateDef, "descend"},
		{113, sema.CodeUndefinedType, "MissingRelic"},
		{114, sema.CodeUndefinedType, "MissingLayer"},
		{121, sema.CodeDuplicateValue, "4"},
		{122, sema.CodeDuplicateValue, `"zone1"`},
		{132, sema.CodeNonScalarMapKey, "KeyStone"},
	}

	got := analyzeCorpusFile(t, filepath.Join("..", "tests", "made-in-abyss", "lints.thrift"))
	used := make([]bool, len(got))

	var problems []string

	for _, w := range model {
		matched := false

		for i, g := range got {
			if used[i] || g.line != w.line || g.code != w.code || g.text != w.text {
				continue
			}

			used[i] = true
			matched = true

			break
		}

		if !matched {
			problems = append(problems, fmt.Sprintf("missing %s at line %d (%q)", w.code, w.line, w.text))
		}
	}

	for i, g := range got {
		if !used[i] {
			problems = append(problems, fmt.Sprintf("unexpected %s", g))
		}
	}

	if len(problems) > 0 {
		t.Fatalf("lints.thrift model mismatch:\n%s", strings.Join(problems, "\n"))
	}
}

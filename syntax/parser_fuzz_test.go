package syntax

import (
	"reflect"
	"testing"
)

// FuzzParse checks parser invariants that hold for every input:
//
//   - parsing never panics or loops forever, even on malformed input
//   - Parse always returns a document whose node ranges are well-formed and
//     consistent with the token stream (via checkDoc)
//   - errors are sorted by source offset
//   - parsing is deterministic
func FuzzParse(f *testing.F) {
	for _, seed := range [][]byte{
		[]byte(""),
		[]byte("struct S {\n  1: i32 id\n}"),
		[]byte("service Foo extends Bar {\n  oneway void ping() throws (E e)\n}"),
		[]byte("const map<string, list<i32>> M = {\"a\": [1, 2]}"),
		[]byte("enum E (anno = \"x\") {\n  A = 1,\n  B\n}"),
		[]byte("typedef i32 T (bare, a = \"1\"; b = '2')"),
		[]byte("namespace * all"),
		[]byte("include \"foo.thrift\""),
		[]byte("struct S {"),
		[]byte("const i32 x ="),
		[]byte("void"),
		[]byte("("),
		[]byte("}"),
		[]byte("struct S {\n  1: void bad\n}"),
		[]byte("list<list<map<i32, string>>>"),
	} {
		f.Add(seed)
	}

	f.Fuzz(func(t *testing.T, src []byte) {
		doc, errs := Parse(src)

		if doc == nil {
			t.Fatal("Parse returned nil document")
		}

		checkDoc(t, doc)

		for i := 1; i < len(errs); i++ {
			if errs[i-1].Offset > errs[i].Offset {
				t.Fatalf("errors not sorted by offset: %v", errs)
			}
		}

		// Determinism.
		doc2, errs2 := Parse(src)
		if !reflect.DeepEqual(doc, doc2) || !reflect.DeepEqual(errs, errs2) {
			t.Fatal("Parse is not deterministic")
		}
	})
}

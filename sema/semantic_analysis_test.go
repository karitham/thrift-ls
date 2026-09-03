package sema

import (
	"context"
	"slices"
	"testing"

	"github.com/stretchr/testify/assert"
	"go.lsp.dev/uri"

	"github.com/karitham/thrift-ls/store"
)

func Test_SemanticAnalysis_Diagnostic(t *testing.T) {
	file1 := `struct Student {
	1: required string name,
	2: required User user1,
	3: required Student user2,
}
// line 6
	struct Student {}
// line 8
union Test {
	1: required string name,
	2: required User user1,
	3: required Student user2 = TestEnum.User, // enum doesn't exist
}
// line 14
exception TestError {
	1: required string name,
	2: required User user1,
	3: required Student user2,
}
// line 20
service TestService {
	Student Get(1: User user1) throws(1: TestError err1, 2: DoesNotExistError err2)
}
// line 24
struct TestContainer {
	1: required list<Student> Students
	2: required i32 failed1 = true
	3: required i32 failed2 = ""
	4: required string failed3 = true
	5: required string failed4 = 71

	100: required i32 user2 = 1
	101: required i64 user3 = 2
	102: required bool isUser = true
}
// line 36
struct TestUUID {
	1: required uuid id
}
`
	view := buildSnapshotForTest(t, []*store.FileChange{
		{
			URI:     "file:///tmp/user.thrift",
			Version: 0,
			Content: []byte(file1),
			From:    store.FileChangeTypeDidOpen,
		},
	})

	type args struct {
		ctx         context.Context
		view        *store.View
		changeFiles []uri.URI
	}

	tests := []struct {
		name      string
		args      args
		want      map[uri.URI][]diagCmp
		assertion assert.ErrorAssertionFunc
	}{
		{
			name: "case 1",
			args: args{
				ctx:  t.Context(),
				view: view,
				changeFiles: []uri.URI{
					"file:///tmp/user.thrift",
				},
			},
			want: map[uri.URI][]diagCmp{
				"file:///tmp/user.thrift": {
					{
						StartLine: 2 + 1, StartCol: 13 + 1, EndLine: 2 + 1, EndCol: 17 + 1,
						Severity: SeverityError,
						Code:     CodeUndefinedType,
						Message:  "field type doesn't exist",
					},
					{
						StartLine: 10 + 1, StartCol: 13 + 1, EndLine: 10 + 1, EndCol: 17 + 1,
						Severity: SeverityError,
						Code:     CodeUndefinedType,
						Message:  "field type doesn't exist",
					},
					{
						StartLine: 11 + 1, StartCol: 29 + 1, EndLine: 11 + 1, EndCol: 42 + 1,
						Severity: SeverityError,
						Code:     CodeUndefinedValue,
						Message:  "default value doesn't exist",
					},
					{
						StartLine: 16 + 1, StartCol: 13 + 1, EndLine: 16 + 1, EndCol: 17 + 1,
						Severity: SeverityError,
						Code:     CodeUndefinedType,
						Message:  "field type doesn't exist",
					},
					{
						StartLine: 21 + 1, StartCol: 16 + 1, EndLine: 21 + 1, EndCol: 20 + 1,
						Severity: SeverityError,
						Code:     CodeUndefinedType,
						Message:  "field type doesn't exist",
					},
					{
						StartLine: 21 + 1, StartCol: 57 + 1, EndLine: 21 + 1, EndCol: 74 + 1,
						Severity: SeverityError,
						Code:     CodeUndefinedType,
						Message:  "field type doesn't exist",
					},
					{
						StartLine: 26 + 1, StartCol: 27 + 1, EndLine: 26 + 1, EndCol: 31 + 1,
						Severity: SeverityError,
						Code:     CodeValueTypeMismatch,
						Message:  "expect i32 but got bool",
					},
					{
						StartLine: 27 + 1, StartCol: 27 + 1, EndLine: 27 + 1, EndCol: 29 + 1,
						Severity: SeverityError,
						Code:     CodeValueTypeMismatch,
						Message:  "expect i32 but got string",
					},
					{
						StartLine: 28 + 1, StartCol: 30 + 1, EndLine: 28 + 1, EndCol: 34 + 1,
						Severity: SeverityError,
						Code:     CodeValueTypeMismatch,
						Message:  "expect string but got bool",
					},
					{
						StartLine: 29 + 1, StartCol: 30 + 1, EndLine: 29 + 1, EndCol: 32 + 1,
						Severity: SeverityError,
						Code:     CodeValueTypeMismatch,
						Message:  "expect string but got int",
					},
				},
			},
			assertion: assert.NoError,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := &SemanticAnalysis{}
			report, err := New(Config{}, []Analyzer{EachFile(c)}).Run(tt.args.ctx, tt.args.view, tt.args.changeFiles)

			for key := range report {
				slices.SortStableFunc(report[key], func(a, b Diagnostic) int {
					if a.Span.Start.Line != b.Span.Start.Line {
						return a.Span.Start.Line - b.Span.Start.Line
					}

					return a.Span.Start.Col - b.Span.Start.Col
				})
			}

			got := make(map[uri.URI][]diagCmp, len(report))
			for key, ds := range report {
				got[key] = cmpAll(ds)
			}

			tt.assertion(t, err)
			assert.Equal(t, tt.want, got)
		})
	}
}

func Test_NonScalarMapKeyCheck(t *testing.T) {
	tests := []struct {
		name    string
		content string
		want    []string // expected messages
	}{
		{
			name:    "base type keys are fine",
			content: "struct S {\n  1: map<string, i32> m,\n}\n",
			want:    nil,
		},
		{
			name:    "struct key",
			content: "struct K { 1: i32 a }\nstruct S {\n  1: map<K, i32> m,\n}\n",
			want:    []string{"map key must be a scalar type, found struct"},
		},
		{
			name:    "list key",
			content: "struct S {\n  1: map<list<i32>, i32> m,\n}\n",
			want:    []string{"map key must be a scalar type, found list"},
		},
		{
			name:    "map key",
			content: "struct S {\n  1: map<map<string, i32>, i32> m,\n}\n",
			want:    []string{"map key must be a scalar type, found map"},
		},
		{
			name:    "enum key is fine",
			content: "enum E { A = 1 }\nstruct S {\n  1: map<E, i32> m,\n}\n",
			want:    nil,
		},
		{
			name:    "typedef to struct is rejected",
			content: "struct K { 1: i32 a }\ntypedef K Alias\nstruct S {\n  1: map<Alias, i32> m,\n}\n",
			want:    []string{"map key must be a scalar type, found struct"},
		},
		{
			name:    "typedef to scalar is fine",
			content: "typedef i64 Id\ntypedef Id Id2\nstruct S {\n  1: map<Id2, i32> m,\n}\n",
			want:    nil,
		},
		{
			name:    "nested container key is rejected",
			content: "struct S {\n  1: map<list<map<string, i32>>, i32> m,\n}\n",
			want:    []string{"map key must be a scalar type, found list"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			view := buildSnapshotForTest(t, []*store.FileChange{
				{
					URI:     "file:///tmp/user.thrift",
					Version: 0,
					Content: []byte(tt.content),
					From:    store.FileChangeTypeDidOpen,
				},
			})

			got := runOne(t, EachFile(&NonScalarMapKeyCheck{}), view, "file:///tmp/user.thrift")["file:///tmp/user.thrift"]

			var msgs []string
			for _, d := range got {
				msgs = append(msgs, d.Message)
			}

			assert.Equal(t, tt.want, msgs)
		})
	}
}

// Test_SemanticAnalysis_StructuredAnnotations pins the structured
// annotation checks: every @Name must resolve to a declared type, like the
// upfluence compiler's parse-time check. The annotation's value is opaque
// here — its identifiers resolve against the annotation type, not the
// global scope, so they are not value-checked.
func Test_SemanticAnalysis_StructuredAnnotations(t *testing.T) {
	tests := []struct {
		name    string
		content string
		want    []string // expected messages
	}{
		{
			name:    "annotation type exists",
			content: "struct Naming { 1: optional string ns }\n@Naming{'ns': 'x'}\nstruct S {}\n",
			want:    nil,
		},
		{
			name:    "annotation type doesn't exist",
			content: "@Naming{'ns': 'x'}\nstruct S {}\n",
			want:    []string{"annotation type doesn't exist"},
		},
		{
			name:    "field annotation",
			content: "@Nope(1)\nstruct S {\n  1: i32 a\n}\n",
			want:    []string{"annotation type doesn't exist"},
		},
		{
			name:    "function annotation",
			content: "service S {\n  @Nope('x') void f()\n}\n",
			want:    []string{"annotation type doesn't exist"},
		},
		{
			name:    "value identifiers are not global consts",
			content: "struct A { 1: i32 a }\n@A(DoesNotExist)\nstruct S {}\n",
			want:    nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			view := buildSnapshotForTest(t, []*store.FileChange{
				{
					URI:     "file:///tmp/user.thrift",
					Version: 0,
					Content: []byte(tt.content),
					From:    store.FileChangeTypeDidOpen,
				},
			})

			got := analyzeOne(t, view, "file:///tmp/user.thrift")

			var msgs []string
			for _, d := range got {
				msgs = append(msgs, d.Message)
			}

			assert.Equal(t, tt.want, msgs)
		})
	}
}

// Test_SemanticAnalysis_WalkCoverage pins the checks that are driven by the
// document walk: nested container default values are existence-checked,
// const types are existence-checked, and diagnostics come out in document
// order (a function's return type before its arguments).
func Test_SemanticAnalysis_WalkCoverage(t *testing.T) {
	tests := []struct {
		name    string
		content string
		want    []string // expected messages, in document order
	}{
		{
			name:    "nested list idents are checked",
			content: "struct S {}\nstruct T {\n  1: required list<S> xs = [Nope1, Nope2],\n}\n",
			want: []string{
				"default value doesn't exist",
				"default value doesn't exist",
			},
		},
		{
			name:    "nested map idents are checked, resolved ones stay quiet",
			content: "enum E { A }\nstruct T {\n  1: required list<E> good = [E.A],\n  2: required map<string, E> bad = {\"k\": Missing},\n}\n",
			want: []string{
				"default value doesn't exist",
			},
		},
		{
			name:    "undefined const type is reported",
			content: "const Missing c = 1\n",
			want: []string{
				"field type doesn't exist",
			},
		},
		{
			name:    "return type is diagnosed before its arguments",
			content: "service S {\n  Undefined ret(1: Undefined a) throws (1: Undefined e),\n}\n",
			want: []string{
				"field type doesn't exist",
				"field type doesn't exist",
				"field type doesn't exist",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			view := buildSnapshotForTest(t, []*store.FileChange{
				{
					URI:     "file:///tmp/user.thrift",
					Version: 0,
					Content: []byte(tt.content),
					From:    store.FileChangeTypeDidOpen,
				},
			})

			got := analyzeOne(t, view, "file:///tmp/user.thrift")

			var msgs []string
			for _, d := range got {
				msgs = append(msgs, d.Message)
			}

			assert.Equal(t, tt.want, msgs)
		})
	}
}

// Test_SemanticAnalysis_ConstValueType pins value-kind matching against
// underlying types: typedef chains resolve before comparing, consts are
// checked like field defaults, and an unresolvable type is left to the
// existence check instead of producing a cascading mismatch.
func Test_SemanticAnalysis_ConstValueType(t *testing.T) {
	tests := []struct {
		name    string
		content string
		want    []string // value-type-mismatch messages, in document order
	}{
		{
			name:    "typedef'd map accepts a map literal",
			content: "typedef map<string, string> RelicMap\nstruct Delver {\n  1: required RelicMap relics = {'gansi': 'bell'},\n}\n",
		},
		{
			name:    "typedef'd string accepts a string literal",
			content: "typedef string CurseNote\nstruct Delver {\n  1: required CurseNote note = \"bell\",\n}\n",
		},
		{
			name:    "typedef'd list accepts a list literal",
			content: "typedef list<i32> DepthLog\nstruct Delver {\n  1: required DepthLog dives = [1, 2],\n}\n",
		},
		{
			name:    "typedef'd set accepts a list literal",
			content: "typedef set<i32> WhistleSet\nstruct Delver {\n  1: required WhistleSet whistles = [1],\n}\n",
		},
		{
			name:    "const with typedef'd map accepts a map literal",
			content: "typedef map<string, string> RelicMap\nconst RelicMap RIKO_BAG = {'bell': 'gansi'}\n",
		},
		{
			name:    "enum field accepts an int literal",
			content: "enum WhistleRank { BLACK, RED }\nstruct Delver {\n  1: required WhistleRank rank = 1,\n}\n",
		},
		{
			name:    "bool const reference against a typedef'd bool is accepted",
			content: "const bool HAS_DELVED = true\ntypedef bool DelvedFlag\nstruct Delver {\n  1: required DelvedFlag delved = HAS_DELVED,\n}\n",
		},
		{
			name:    "non-bool const reference against bool is reported",
			content: "const string RIKO = \"riko\"\nstruct Delver {\n  1: required bool is_hollowed = RIKO,\n}\n",
			want:    []string{"expect bool but got identifier"},
		},
		{
			name:    "mismatch through a typedef reports the underlying kind",
			content: "typedef string CurseNote\nstruct Delver {\n  1: required CurseNote note = 71,\n}\n",
			want:    []string{"expect string but got int"},
		},
		{
			name:    "const with typedef'd map rejects a string literal",
			content: "typedef map<string, string> RelicMap\nconst RelicMap RIKO_BAG = \"nope\"\n",
			want:    []string{"expect map but got string"},
		},
		{
			name:    "map literal initializes a struct-typed value",
			content: "struct Relic {\n  1: string name,\n}\nstruct Delver {\n  1: required Relic relic = {'name': 'bell'},\n}\n",
		},
		{
			name:    "map literal against an enum type is reported",
			content: "enum WhistleRank { BLACK, RED }\nstruct Delver {\n  1: required WhistleRank rank = {'a': 'b'},\n}\n",
			want:    []string{"expect WhistleRank but got map"},
		},
		{
			name:    "int literal against string is reported",
			content: "struct Delver {\n  1: required string name = 71,\n}\n",
			want:    []string{"expect string but got int"},
		},
		{
			name:    "unresolvable type produces no cascading mismatch",
			content: "struct Delver {\n  1: required VoidStone stone = {'a': 'b'},\n}\n",
		},
		{
			name:    "map entry values are checked against the value type",
			content: "typedef map<string, i32> DepthLog\nstruct Delver {\n  1: required DepthLog dives = {'abyss': 'sixth'},\n}\n",
			want:    []string{"expect i32 but got string"},
		},
		{
			name:    "map entry keys are checked against the key type",
			content: "struct Delver {\n  1: required map<i32, string> dives = {'one': 'bell'},\n}\n",
			want:    []string{"expect i32 but got string"},
		},
		{
			name:    "list elements are checked against the element type",
			content: "struct Delver {\n  1: required list<i32> dives = [1, 'sixth'],\n}\n",
			want:    []string{"expect i32 but got string"},
		},
		{
			name:    "nested container entries resolve through typedefs",
			content: "typedef map<string, list<i32>> DiveRecord\nstruct Delver {\n  1: required DiveRecord dives = {'riko': [1, 6]},\n}\n",
		},
		{
			name:    "deeply nested entry mismatches report the innermost type",
			content: "typedef map<string, list<i32>> DiveRecord\nstruct Delver {\n  1: required DiveRecord dives = {'riko': ['sixth layer']},\n}\n",
			want:    []string{"expect i32 but got string"},
		},
		{
			name:    "struct literal field values are checked against field types",
			content: "struct Relic {\n  1: string name,\n  2: i32 lucerium_value,\n}\nstruct Delver {\n  1: required Relic relic = {'name': 'bell', 'lucerium_value': 'many'},\n}\n",
			want:    []string{"expect i32 but got string"},
		},
		{
			name:    "unknown struct literal field is reported",
			content: "struct Relic {\n  1: string name,\n}\nstruct Delver {\n  1: required Relic relic = {'curse': 'bell'},\n}\n",
			want:    []string{"no field named \"curse\" in Relic"},
		},
		{
			name:    "non-string struct literal key is reported",
			content: "struct Relic {\n  1: string name,\n}\nstruct Delver {\n  1: required Relic relic = {1: 'bell'},\n}\n",
			want:    []string{"expect field name but got int"},
		},
		{
			name:    "struct literal through a typedef resolves field types",
			content: "typedef Relic AncientRelic\nstruct Relic {\n  1: string name,\n}\nstruct Delver {\n  1: required AncientRelic relic = {'name': 'bell'},\n}\n",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			view := buildSnapshotForTest(t, []*store.FileChange{
				{
					URI:     "file:///tmp/orth.thrift",
					Version: 0,
					Content: []byte(tt.content),
					From:    store.FileChangeTypeDidOpen,
				},
			})

			got := analyzeOne(t, view, "file:///tmp/orth.thrift")

			var msgs []string
			for _, d := range got {
				if d.Code == CodeValueTypeMismatch {
					msgs = append(msgs, d.Message)
				}
			}

			assert.Equal(t, tt.want, msgs)
		})
	}
}

// Test_SemanticAnalysis_ConstValueType_CrossFile pins that a typedef in an
// included file classifies values in the including file, and that value
// identifiers keep resolving in the referencing file's scope even when the
// type was reached through the include.
func Test_SemanticAnalysis_ConstValueType_CrossFile(t *testing.T) {
	t.Run("typedef'd map from the include accepts a map literal", func(t *testing.T) {
		abyss := "typedef map<string, string> RelicMap\n"
		orth := "include \"abyss.thrift\"\nstruct Delver {\n  1: required abyss.RelicMap relics = {'gansi': 'bell'},\n}\n"

		view := crossSnap(t, "/tmp/orth.thrift", orth, "/tmp/abyss.thrift", abyss)

		got := analyzeOne(t, view, fu("/tmp/orth.thrift"))
		assert.Empty(t, got)
	})

	t.Run("bool const reference through a typedef'd bool in the include", func(t *testing.T) {
		abyss := "typedef bool DelvedFlag\n"
		orth := "include \"abyss.thrift\"\nconst bool HAS_DELVED = true\nstruct Delver {\n  1: required abyss.DelvedFlag delved = HAS_DELVED,\n}\n"

		view := crossSnap(t, "/tmp/orth.thrift", orth, "/tmp/abyss.thrift", abyss)

		got := analyzeOne(t, view, fu("/tmp/orth.thrift"))
		assert.Empty(t, got)
	})

	t.Run("struct literal field values classify in the struct's file", func(t *testing.T) {
		// RelicRarity resolves in abyss.thrift's scope; the literal's
		// ranges map in orth.thrift's.
		abyss := "typedef i32 RelicRarity\nstruct Relic {\n  1: required RelicRarity lucerium_value,\n}\n"
		orth := "include \"abyss.thrift\"\nconst abyss.Relic THE_BELL = {'lucerium_value': 'many'}\n"

		view := crossSnap(t, "/tmp/orth.thrift", orth, "/tmp/abyss.thrift", abyss)

		got := analyzeOne(t, view, fu("/tmp/orth.thrift"))

		var msgs []string
		for _, d := range got {
			if d.Code == CodeValueTypeMismatch {
				msgs = append(msgs, d.Message)
			}
		}

		assert.Equal(t, []string{"expect i32 but got string"}, msgs)
	})
}

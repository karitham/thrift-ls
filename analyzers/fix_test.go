package analyzers

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/karitham/thrift-ls/analyzertest"
	"github.com/karitham/thrift-ls/sema"
)

// addIncludeFixes returns the fixes the fixer offers for the undefined-type
// diagnostics the analysis produced for file.
func addIncludeFixes(t *testing.T, files map[string]string, file string) []sema.Fix {
	t.Helper()

	view := analyzertest.View(t, files)
	u := analyzertest.URI(file)
	report := analyzertest.RunOnView(t, sema.EachFile(&SemanticAnalysis{}), view, u)
	diags := report[u]

	f := analyzertest.File(t, view, file)

	var got []sema.Fix
	for _, d := range diags {
		if d.Code == sema.CodeUndefinedType {
			got = append(got, AddIncludeFixer{}.Fix(t.Context(), f, d)...)
		}
	}

	return got
}

func Test_AddIncludeFixer(t *testing.T) {
	t.Run("adds the include defining the missing type", func(t *testing.T) {
		content := "struct S {\n  1: User u,\n}\n"

		fixes := addIncludeFixes(t, map[string]string{
			"shared.thrift": "struct User {\n  1: i32 id,\n}\n",
			"user.thrift":   content,
		}, "user.thrift")
		require.Len(t, fixes, 1)
		assert.Equal(t, `Add include "shared.thrift"`, fixes[0].Title)
		assert.Equal(t, "include \"shared.thrift\"\n", fixes[0].Edits[0].NewText)

		out, applied, _, err := sema.Apply([]byte(content), fixes)
		require.NoError(t, err)
		require.Len(t, applied, 1)
		assert.Equal(t, "include \"shared.thrift\"\nstruct S {\n  1: User u,\n}\n", string(out))
	})

	t.Run("inserts after existing includes", func(t *testing.T) {
		content := "include \"base.thrift\"\n\nstruct S {\n  1: User u,\n}\n"

		fixes := addIncludeFixes(t, map[string]string{
			"shared.thrift": "struct User {\n  1: i32 id,\n}\n",
			"user.thrift":   content,
		}, "user.thrift")
		require.Len(t, fixes, 1)

		out, applied, _, err := sema.Apply([]byte(content), fixes)
		require.NoError(t, err)
		require.Len(t, applied, 1)
		assert.Equal(t, "include \"base.thrift\"\ninclude \"shared.thrift\"\n\nstruct S {\n  1: User u,\n}\n", string(out))
	})

	t.Run("a type defined nowhere offers no fix", func(t *testing.T) {
		assert.Empty(t, addIncludeFixes(t, map[string]string{
			"user.thrift": "struct S {\n  1: Ghost u,\n}\n",
		}, "user.thrift"))
	})

	t.Run("a type defined in the same file offers no fix", func(t *testing.T) {
		assert.Empty(t, addIncludeFixes(t, map[string]string{
			"user.thrift": "struct User {}\nstruct S {\n  1: User u,\n}\n",
		}, "user.thrift"))
	})
}

// Test_UnusedIncludeCheck_InlineFix pins the inline quickfix: the unused
// include line is deleted whole, trailing newline included.
func Test_UnusedIncludeCheck_InlineFix(t *testing.T) {
	content := "include \"shared.thrift\"\nstruct S { 1: i32 a }\n"

	report := analyzertest.Run(t, sema.EachFile(&UnusedIncludeCheck{}), map[string]string{
		"user.thrift": content,
	}, "user.thrift")
	diags := report[analyzertest.URI("user.thrift")]
	require.Len(t, diags, 1)
	require.Len(t, diags[0].Fixes, 1)

	fix := diags[0].Fixes[0]
	assert.Equal(t, `Remove unused include "shared.thrift"`, fix.Title)

	out, applied, _, err := sema.Apply([]byte(content), diags[0].Fixes)
	require.NoError(t, err)
	require.Len(t, applied, 1)
	assert.Equal(t, "struct S { 1: i32 a }\n", string(out))
}

// TestFixAllEnumValues drives the whole fix loop over a file with implicit
// enum members: every member ends up with the value the compiler would
// auto-increment, and the fixed file passes the check on the next pass.
func TestFixAllEnumValues(t *testing.T) {
	t.Parallel()

	files := map[string]string{
		"user.thrift": "enum Color {\n  RED,\n  GREEN = 2,\n  BLUE,\n}\n",
	}

	res := analyzertest.RunFixAll(t, DefaultPipeline(sema.Config{}), files, "user.thrift")

	require.Equal(t, 2, res.Applied)
	require.Empty(t, res.Skipped)

	// Every implicit member gains its auto-incremented value.
	require.Equal(t, "enum Color {\n  RED = 0,\n  GREEN = 2,\n  BLUE = 3,\n}\n", files["user.thrift"])

	// The fixed file passes the check on the next pass.
	require.Empty(t, res.Remaining[analyzertest.URI("user.thrift")])
}

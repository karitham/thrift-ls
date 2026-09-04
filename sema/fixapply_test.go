package sema_test

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/require"
	"go.lsp.dev/uri"

	"github.com/karitham/thrift-ls/analyzers"
	"github.com/karitham/thrift-ls/analyzertest"
	"github.com/karitham/thrift-ls/sema"
	"github.com/karitham/thrift-ls/store"
	"github.com/karitham/thrift-ls/syntax"
)

func spanOf(start, end int) sema.Span {
	return sema.Span{
		Start: syntax.Position{Line: 1, Col: start + 1, Offset: start},
		End:   syntax.Position{Line: 1, Col: end + 1, Offset: end},
	}
}

func insertAt(offset int, text string) sema.Fix {
	return sema.Fix{Title: "insert", Edits: []sema.Edit{{Span: spanOf(offset, offset), NewText: text}}}
}

func replaceRange(start, end int, text string) sema.Fix {
	return sema.Fix{Title: "replace", Edits: []sema.Edit{{Span: spanOf(start, end), NewText: text}}}
}

func TestApply(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		content  string
		fixes    []sema.Fix
		want     string
		wantAp   []string // titles of applied fixes, in order
		wantSkip []string // titles of skipped fixes
		wantErr  bool
	}{
		{
			name:    "insertion",
			content: "ab",
			fixes:   []sema.Fix{insertAt(1, "X")},
			want:    "aXb",
			wantAp:  []string{"insert"},
		},
		{
			name:    "replacement",
			content: "abcd",
			fixes:   []sema.Fix{replaceRange(1, 3, "XY")},
			want:    "aXYd",
			wantAp:  []string{"replace"},
		},
		{
			name:    "deletion",
			content: "abcd",
			fixes:   []sema.Fix{replaceRange(1, 3, "")},
			want:    "ad",
			wantAp:  []string{"replace"},
		},
		{
			name:    "later edits shift nothing",
			content: "abcd",
			fixes:   []sema.Fix{replaceRange(0, 1, "X"), replaceRange(3, 4, "Y")},
			want:    "XbcY",
			wantAp:  []string{"replace", "replace"},
		},
		{
			name:     "overlapping fixes: first wins, second skipped whole",
			content:  "abcd",
			fixes:    []sema.Fix{replaceRange(1, 3, "X"), replaceRange(2, 4, "Y")},
			want:     "aXd",
			wantAp:   []string{"replace"},
			wantSkip: []string{"replace"},
		},
		{
			name:    "overlapping fixes: all-or-nothing per fix",
			content: "abcdef",
			fixes: []sema.Fix{{
				Title: "multi",
				Edits: []sema.Edit{
					{Span: spanOf(0, 1), NewText: "X"},
					{Span: spanOf(3, 4), NewText: "Y"},
				},
			}, replaceRange(3, 5, "Z")},
			want:     "XbcYef",
			wantAp:   []string{"multi"},
			wantSkip: []string{"replace"},
		},
		{
			name:    "self-overlapping fix skipped whole",
			content: "abcd",
			fixes: []sema.Fix{{
				Title: "self",
				Edits: []sema.Edit{
					{Span: spanOf(0, 2), NewText: "X"},
					{Span: spanOf(1, 3), NewText: "Y"},
				},
			}},
			want:     "abcd",
			wantSkip: []string{"self"},
		},
		{
			name:    "adjacent edits do not overlap",
			content: "abcd",
			fixes:   []sema.Fix{replaceRange(0, 2, "X"), replaceRange(2, 4, "Y")},
			want:    "XY",
			wantAp:  []string{"replace", "replace"},
		},
		{
			name:    "same-offset insertions land in argument order",
			content: "ab",
			fixes:   []sema.Fix{insertAt(1, "1"), insertAt(1, "2")},
			want:    "a12b",
			wantAp:  []string{"insert", "insert"},
		},
		{
			name:    "insertion at a replacement's start lands before it",
			content: "abcd",
			fixes:   []sema.Fix{replaceRange(1, 3, "R"), insertAt(1, "I")},
			want:    "aIRd",
			wantAp:  []string{"replace", "insert"},
		},
		{
			name:    "insertion and replacement commute at the same offset",
			content: "abcd",
			fixes:   []sema.Fix{insertAt(1, "I"), replaceRange(1, 3, "R")},
			want:    "aIRd",
			wantAp:  []string{"insert", "replace"},
		},
		{
			name:    "out-of-range edit is an error",
			content: "ab",
			fixes:   []sema.Fix{replaceRange(1, 5, "X")},
			wantErr: true,
		},
		{
			name:    "inverted edit is an error",
			content: "ab",
			fixes:   []sema.Fix{replaceRange(2, 1, "X")},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			out, applied, skipped, err := sema.Apply([]byte(tt.content), tt.fixes)

			if tt.wantErr {
				require.Error(t, err)

				return
			}

			require.NoError(t, err)
			require.Equal(t, tt.want, string(out))

			var gotAp []string
			for _, fx := range applied {
				gotAp = append(gotAp, fx.Title)
			}

			var gotSkip []string
			for _, fx := range skipped {
				gotSkip = append(gotSkip, fx.Title)
			}

			require.Equal(t, tt.wantAp, gotAp)
			require.Equal(t, tt.wantSkip, gotSkip)
		})
	}
}

// TestFixAllGreenfield fixes a new file referencing existing types: the
// undefined type resolves through the workspace, the include lands in the
// target file only, and the untouched files keep their content.
func TestFixAllGreenfield(t *testing.T) {
	t.Parallel()

	files := map[string]string{
		"a.thrift": "struct Foo {}\n",
		"b.thrift": "struct Bar {\n  1: Foo f\n}\n",
		// Fixable on its own, but not a target: must stay untouched.
		"c.thrift": "include \"a.thrift\"\n\nstruct Other {}\n",
	}

	b := analyzertest.URI("b.thrift")

	res := analyzertest.RunFixAll(t, analyzers.DefaultPipeline(sema.Config{}), files, "b.thrift")

	require.Equal(t, 1, res.Applied)
	require.Equal(t, []uri.URI{b}, res.FixedFiles)
	require.Empty(t, res.Skipped)
	require.Equal(t, 2, res.Passes, "pass 1 adds the include, pass 2 finds nothing")

	require.Equal(t, "include \"a.thrift\"\nstruct Bar {\n  1: Foo f\n}\n", files["b.thrift"])

	// Nothing but the target changed.
	require.Equal(t, "struct Foo {}\n", files["a.thrift"])
	require.Equal(t, "include \"a.thrift\"\n\nstruct Other {}\n", files["c.thrift"])

	// The remaining report is clean for the target.
	require.Empty(t, res.Remaining[b])
}

// TestFixAllParseErrorGuard refuses to fix files that do not parse: their
// fixable diagnostics are skipped with a reason, and their content is
// untouched.
func TestFixAllParseErrorGuard(t *testing.T) {
	t.Parallel()

	brokenContent := "include \"a.thrift\"\n\nstruct Broken {\n  1: i32 a\n  @@@\n}\n"
	files := map[string]string{
		// The include parses; the body does not.
		"broken.thrift": brokenContent,
	}

	broken := analyzertest.URI("broken.thrift")

	// Applied == 0 means persist never ran: FixAll only persists files
	// with applied fixes, so the content assertion below pins that
	// nothing was written for the unparseable file.
	res := analyzertest.RunFixAll(t, analyzers.DefaultPipeline(sema.Config{}), files, "broken.thrift")

	require.Zero(t, res.Applied)
	require.Empty(t, res.FixedFiles)
	require.Equal(t, brokenContent, files["broken.thrift"])

	for _, s := range res.Skipped {
		require.Equal(t, "file has parse errors", s.Reason)
		require.Equal(t, broken, s.File)
	}
	require.NotEmpty(t, res.Skipped, "the parse-error guard must report what it skipped")
}

// TestFixAllPersistFailure keeps the partial summary: fixes persisted
// before the failure count as applied and their files are listed, so the
// caller can report the mutations a failed run already made.
func TestFixAllPersistFailure(t *testing.T) {
	t.Parallel()

	folder := t.TempDir()
	files := map[uri.URI][]byte{
		uri.File(folder + "/a.thrift"): []byte("struct Bar {\n  1: Foo f\n}\n"),
		// Fixable (unused include), but its persist fails.
		uri.File(folder + "/b.thrift"): []byte("include \"a.thrift\"\n\nstruct Other {}\n"),
		uri.File(folder + "/c.thrift"): []byte("struct Foo {}\n"),
	}

	view := store.NewView(uri.File(folder), store.NewMemFS(files), nil)

	persist := func(_ context.Context, u uri.URI, content []byte) error {
		files[u] = content

		if u.FsPath() == folder+"/b.thrift" {
			return errors.New("disk full")
		}

		return nil
	}

	res, err := analyzers.DefaultPipeline(sema.Config{}).FixAll(t.Context(), view,
		[]uri.URI{uri.File(folder + "/a.thrift"), uri.File(folder + "/b.thrift")}, persist)

	require.Error(t, err)
	require.ErrorContains(t, err, "b.thrift")
	require.Equal(t, 1, res.Applied, "a.thrift's fix persisted before the failure")
	require.Equal(t, []uri.URI{uri.File(folder + "/a.thrift")}, res.FixedFiles)
	require.Contains(t, string(files[uri.File(folder+"/a.thrift")]), "include \"c.thrift\"")
}

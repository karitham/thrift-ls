package sema

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/require"
	"go.lsp.dev/uri"

	"github.com/karitham/thrift-ls/lsp/cache"
	"github.com/karitham/thrift-ls/syntax"
)

func spanOf(start, end int) Span {
	return Span{
		Start: syntax.Position{Line: 1, Col: start + 1, Offset: start},
		End:   syntax.Position{Line: 1, Col: end + 1, Offset: end},
	}
}

func insertAt(offset int, text string) Fix {
	return Fix{Title: "insert", Edits: []Edit{{Span: spanOf(offset, offset), NewText: text}}}
}

func replaceRange(start, end int, text string) Fix {
	return Fix{Title: "replace", Edits: []Edit{{Span: spanOf(start, end), NewText: text}}}
}

func TestApply(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		content  string
		fixes    []Fix
		want     string
		wantAp   []string // titles of applied fixes, in order
		wantSkip []string // titles of skipped fixes
		wantErr  bool
	}{
		{
			name:    "insertion",
			content: "ab",
			fixes:   []Fix{insertAt(1, "X")},
			want:    "aXb",
			wantAp:  []string{"insert"},
		},
		{
			name:    "replacement",
			content: "abcd",
			fixes:   []Fix{replaceRange(1, 3, "XY")},
			want:    "aXYd",
			wantAp:  []string{"replace"},
		},
		{
			name:    "deletion",
			content: "abcd",
			fixes:   []Fix{replaceRange(1, 3, "")},
			want:    "ad",
			wantAp:  []string{"replace"},
		},
		{
			name:    "later edits shift nothing",
			content: "abcd",
			fixes:   []Fix{replaceRange(0, 1, "X"), replaceRange(3, 4, "Y")},
			want:    "XbcY",
			wantAp:  []string{"replace", "replace"},
		},
		{
			name:     "overlapping fixes: first wins, second skipped whole",
			content:  "abcd",
			fixes:    []Fix{replaceRange(1, 3, "X"), replaceRange(2, 4, "Y")},
			want:     "aXd",
			wantAp:   []string{"replace"},
			wantSkip: []string{"replace"},
		},
		{
			name:    "overlapping fixes: all-or-nothing per fix",
			content: "abcdef",
			fixes: []Fix{{
				Title: "multi",
				Edits: []Edit{
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
			fixes: []Fix{{
				Title: "self",
				Edits: []Edit{
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
			fixes:   []Fix{replaceRange(0, 2, "X"), replaceRange(2, 4, "Y")},
			want:    "XY",
			wantAp:  []string{"replace", "replace"},
		},
		{
			name:    "same-offset insertions land in argument order",
			content: "ab",
			fixes:   []Fix{insertAt(1, "1"), insertAt(1, "2")},
			want:    "a12b",
			wantAp:  []string{"insert", "insert"},
		},
		{
			name:    "insertion at a replacement's start lands before it",
			content: "abcd",
			fixes:   []Fix{replaceRange(1, 3, "R"), insertAt(1, "I")},
			want:    "aIRd",
			wantAp:  []string{"replace", "insert"},
		},
		{
			name:    "insertion and replacement commute at the same offset",
			content: "abcd",
			fixes:   []Fix{insertAt(1, "I"), replaceRange(1, 3, "R")},
			want:    "aIRd",
			wantAp:  []string{"insert", "replace"},
		},
		{
			name:    "out-of-range edit is an error",
			content: "ab",
			fixes:   []Fix{replaceRange(1, 5, "X")},
			wantErr: true,
		},
		{
			name:    "inverted edit is an error",
			content: "ab",
			fixes:   []Fix{replaceRange(2, 1, "X")},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			out, applied, skipped, err := Apply([]byte(tt.content), tt.fixes)

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

	folder := t.TempDir()
	files := map[uri.URI][]byte{
		uri.File(folder + "/a.thrift"): []byte("struct Foo {}\n"),
		uri.File(folder + "/b.thrift"): []byte("struct Bar {\n  1: Foo f\n}\n"),
		// Fixable on its own, but not a target: must stay untouched.
		uri.File(folder + "/c.thrift"): []byte("include \"a.thrift\"\n\nstruct Other {}\n"),
	}

	view := cache.NewView(uri.File(folder), cache.NewMemFS(files), nil)

	b := uri.File(folder + "/b.thrift")
	written := make(map[uri.URI][]byte)

	res, err := DefaultPipeline(Config{}).FixAll(t.Context(), view, []uri.URI{b},
		func(_ context.Context, u uri.URI, content []byte) error {
			files[u] = content
			written[u] = content

			return nil
		})
	require.NoError(t, err)

	require.Equal(t, 1, res.Applied)
	require.Equal(t, []uri.URI{b}, res.FixedFiles)
	require.Empty(t, res.Skipped)
	require.Equal(t, 2, res.Passes, "pass 1 adds the include, pass 2 finds nothing")

	require.Equal(t, []byte("include \"a.thrift\"\nstruct Bar {\n  1: Foo f\n}\n"), written[b])

	// Nothing but the target changed.
	require.NotContains(t, written, uri.File(folder+"/a.thrift"))
	require.NotContains(t, written, uri.File(folder+"/c.thrift"))
	require.Equal(t, []byte("struct Foo {}\n"), files[uri.File(folder+"/a.thrift")])

	// The remaining report is clean for the target.
	require.Empty(t, res.Remaining[b])
}

// TestFixAllParseErrorGuard refuses to fix files that do not parse: their
// fixable diagnostics are skipped with a reason, and their content is
// untouched.
func TestFixAllParseErrorGuard(t *testing.T) {
	t.Parallel()

	folder := t.TempDir()
	files := map[uri.URI][]byte{
		// The include parses; the body does not.
		uri.File(folder + "/broken.thrift"): []byte("include \"a.thrift\"\n\nstruct Broken {\n  1: i32 a\n  @@@\n}\n"),
	}

	view := cache.NewView(uri.File(folder), cache.NewMemFS(files), nil)

	broken := uri.File(folder + "/broken.thrift")

	res, err := DefaultPipeline(Config{}).FixAll(t.Context(), view, []uri.URI{broken},
		func(_ context.Context, _ uri.URI, _ []byte) error {
			t.Error("nothing should be written for an unparseable file")

			return nil
		})
	require.NoError(t, err)

	require.Zero(t, res.Applied)
	require.Empty(t, res.FixedFiles)
	require.Equal(t, []byte("include \"a.thrift\"\n\nstruct Broken {\n  1: i32 a\n  @@@\n}\n"), files[broken])

	for _, s := range res.Skipped {
		require.Equal(t, "file has parse errors", s.Reason)
		require.Equal(t, broken, s.File)
	}
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

	view := cache.NewView(uri.File(folder), cache.NewMemFS(files), nil)

	persist := func(_ context.Context, u uri.URI, content []byte) error {
		files[u] = content

		if u.FsPath() == folder+"/b.thrift" {
			return errors.New("disk full")
		}

		return nil
	}

	res, err := DefaultPipeline(Config{}).FixAll(t.Context(), view,
		[]uri.URI{uri.File(folder + "/a.thrift"), uri.File(folder + "/b.thrift")}, persist)

	require.Error(t, err)
	require.ErrorContains(t, err, "b.thrift")
	require.Equal(t, 1, res.Applied, "a.thrift's fix persisted before the failure")
	require.Equal(t, []uri.URI{uri.File(folder + "/a.thrift")}, res.FixedFiles)
	require.Contains(t, string(files[uri.File(folder+"/a.thrift")]), "include \"c.thrift\"")
}

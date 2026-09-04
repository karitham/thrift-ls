package analyzertest_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/karitham/thrift-ls/analyzertest"
	"github.com/karitham/thrift-ls/sema"
	"github.com/karitham/thrift-ls/syntax"
)

type replaceFooAnalyzer struct{}

func (replaceFooAnalyzer) Name() string {
	return "replace-foo"
}

func (replaceFooAnalyzer) Analyze(ctx context.Context, run *sema.Run) error {
	for _, file := range run.Files() {
		pf, err := run.View().Parse(ctx, file)
		if err != nil {
			return err
		}

		content, err := pf.Content()
		if err != nil {
			return err
		}

		if string(content) != "struct Foo {}\n" {
			continue
		}

		run.Add(file, sema.Diagnostic{
			Span: sema.Span{
				Start: syntax.Position{Line: 1, Col: 8, Offset: 7},
				End:   syntax.Position{Line: 1, Col: 11, Offset: 10},
			},
			Fixes: []sema.Fix{{
				Title: "replace foo",
				Edits: []sema.Edit{{
					Span: sema.Span{
						Start: syntax.Position{Line: 1, Col: 8, Offset: 7},
						End:   syntax.Position{Line: 1, Col: 11, Offset: 10},
					},
					NewText: "Bar",
				}},
			}},
		})
	}

	return nil
}

func TestRunFixAllUpdatesOnlyTargets(t *testing.T) {
	files := map[string]string{
		"a.thrift": "struct Foo {}\n",
		"b.thrift": "struct Foo {}\n",
	}
	pipeline := sema.New(sema.Config{}, []sema.Analyzer{replaceFooAnalyzer{}})

	result := analyzertest.RunFixAll(t, pipeline, files, "a.thrift")

	require.Equal(t, 1, result.Applied)
	require.Equal(t, 2, result.Passes)
	require.Equal(t, "struct Bar {}\n", files["a.thrift"])
	require.Equal(t, "struct Foo {}\n", files["b.thrift"])
	require.Empty(t, result.Remaining[analyzertest.URI("a.thrift")])
}

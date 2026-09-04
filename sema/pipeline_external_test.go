package sema_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.lsp.dev/uri"

	"github.com/karitham/thrift-ls/sema"
	"github.com/karitham/thrift-ls/store"
)

type customRunAnalyzer struct {
	called bool
}

func (c *customRunAnalyzer) Name() string {
	return "CustomRunAnalyzer"
}

func (c *customRunAnalyzer) Analyze(ctx context.Context, run *sema.Run) error {
	c.called = true

	view := run.View()
	ix := run.Index()
	files := run.Files()

	if view == nil || ix == nil || len(files) == 0 {
		return nil
	}

	for _, file := range files {
		pf, err := view.Parse(ctx, file)
		if err != nil || pf.AST() == nil {
			continue
		}

		run.Add(file, sema.Diagnostic{
			Span:     sema.SpanOf(pf, pf.AST()),
			Severity: sema.SeverityInfo,
			Code:     "custom-check",
			Message:  "whole run analyzer ok",
		})
	}

	return nil
}

func TestAnalyzerPublicInterface(t *testing.T) {
	u := uri.File("/test.thrift")
	view := store.BuildViewForTest([]*store.FileChange{
		{
			URI:     u,
			Content: []byte("struct User { 1: string name }"),
			From:    store.FileChangeTypeDidOpen,
		},
	})

	analyzer := &customRunAnalyzer{}
	pipeline := sema.New(sema.Config{}, []sema.Analyzer{analyzer})

	report, err := pipeline.Run(t.Context(), view, []uri.URI{u})
	require.NoError(t, err)
	assert.True(t, analyzer.called)
	assert.Len(t, report[u], 1)
	assert.Equal(t, "custom-check", report[u][0].Code)
}

package main

import (
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func Test_CollectThriftFiles(t *testing.T) {
	// A single file.
	files, err := collectThriftFiles("tests/made-in-abyss/lints.thrift")
	require.NoError(t, err)
	require.Len(t, files, 1)
	assert.True(t, filepath.IsAbs(files[0]))

	// A folder, recursively, in lexical order.
	files, err = collectThriftFiles("tests/made-in-abyss")
	require.NoError(t, err)

	var names []string
	for _, f := range files {
		names = append(names, filepath.Base(f))
	}
	assert.Equal(t, []string{
		"abyss.thrift", "cycle_a.thrift", "cycle_b.thrift",
		"delvers.thrift", "lints.thrift", "orth.thrift", "unused.thrift",
	}, names)

	// A missing path is an error.
	_, err = collectThriftFiles("tests/does-not-exist")
	require.Error(t, err)
}

package cache

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.lsp.dev/uri"
)

func TestSessionViews(t *testing.T) {
	folderA := uri.File("/tmp/a")
	folderB := uri.File("/tmp/b")

	tests := []struct {
		name    string
		setup   func(s *Session)
		folders []uri.URI
	}{
		{
			name:    "no views",
			setup:   func(s *Session) {},
			folders: nil,
		},
		{
			name: "one view",
			setup: func(s *Session) {
				s.AddView(folderA, nil)
			},
			folders: []uri.URI{folderA},
		},
		{
			name: "views in registration order",
			setup: func(s *Session) {
				s.AddView(folderB, nil)
				s.AddView(folderA, nil)
			},
			folders: []uri.URI{folderB, folderA},
		},
		{
			name: "removed view disappears",
			setup: func(s *Session) {
				s.AddView(folderA, nil)
				s.AddView(folderB, nil)
				s.RemoveView(folderA)
			},
			folders: []uri.URI{folderB},
		},
		{
			name: "removing an untracked folder is a no-op",
			setup: func(s *Session) {
				s.AddView(folderA, nil)
				s.RemoveView(folderB)
			},
			folders: []uri.URI{folderA},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := NewSession(NewMemoizedFS())
			tt.setup(s)

			views := s.Views()

			var got []uri.URI
			for _, v := range views {
				got = append(got, v.Folder())
			}

			assert.Equal(t, tt.folders, got)
		})
	}
}

func TestSessionAddViewDedups(t *testing.T) {
	s := NewSession(NewMemoizedFS())

	folder := uri.File("/tmp/a")
	first := s.AddView(folder, nil)
	second := s.AddView(folder, nil)

	assert.Same(t, first, second)
	assert.Len(t, s.Views(), 1)
}

func TestSessionRemoveViewForgetsMappings(t *testing.T) {
	s := NewSession(NewMemoizedFS())

	folder := uri.File("/tmp/a")
	other := uri.File("/tmp/b")

	s.AddView(folder, nil)
	s.AddView(other, nil)

	fileA := uri.File("/tmp/a/one.thrift")
	fileB := uri.File("/tmp/b/two.thrift")

	// Warm the per-URI view cache.
	viewA, err := s.ViewOf(fileA)
	require.NoError(t, err)
	require.Equal(t, folder, viewA.Folder())

	viewB, err := s.ViewOf(fileB)
	require.NoError(t, err)
	require.Equal(t, other, viewB.Folder())

	// Removing the folder drops the cached mapping, so the file resolves
	// to the remaining view.
	s.RemoveView(folder)

	view, err := s.ViewOf(fileA)
	require.NoError(t, err)
	assert.Equal(t, other, view.Folder())

	// A fresh lookup of the other folder's file still resolves.
	view, err = s.ViewOf(fileB)
	require.NoError(t, err)
	assert.Equal(t, other, view.Folder())
}

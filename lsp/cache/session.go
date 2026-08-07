package cache

import (
	"context"
	"fmt"
	"math/rand"
	"sync"

	"go.lsp.dev/uri"
)

type Session struct {
	id int64

	// cache is shared global
	cache *Cache

	viewMu  sync.Mutex
	views   []*View
	viewMap map[uri.URI]*View // map of URI->best view

	// session holds overlayFS to manage file content
	// view, snapshot only holds FileSource to read from overlayFS
	*overlayFS
}

func NewSession(cache *Cache) *Session {
	sess := &Session{
		id:        rand.Int63(),
		cache:     cache,
		views:     make([]*View, 0),
		viewMap:   make(map[uri.URI]*View),
		overlayFS: NewOverlayFS(cache),
	}

	return sess
}

// AddView registers a view for the workspace folder, returning the
// existing view when the folder is already tracked.
func (s *Session) AddView(folder uri.URI) *View {
	s.viewMu.Lock()
	defer s.viewMu.Unlock()

	for _, v := range s.views {
		if v.folder == folder {
			return v
		}
	}

	view := NewView(folder.Path(), folder, s.overlayFS, s.cache.IncludePaths)
	s.views = append(s.views, view)

	return view
}

// RemoveView drops the view for the workspace folder and forgets every
// cached file-to-view mapping that pointed at it, so ViewOf re-resolves
// against the remaining folders.
func (s *Session) RemoveView(folder uri.URI) {
	s.viewMu.Lock()
	defer s.viewMu.Unlock()

	for i, v := range s.views {
		if v.folder != folder {
			continue
		}

		s.views = append(s.views[:i], s.views[i+1:]...)
		for file, view := range s.viewMap {
			if view == v {
				delete(s.viewMap, file)
			}
		}

		return
	}
}

// Views returns the workspace folders' views.
func (s *Session) Views() []*View {
	s.viewMu.Lock()
	defer s.viewMu.Unlock()

	return append([]*View(nil), s.views...)
}

func (s *Session) ViewOf(fileURI uri.URI) (*View, error) {
	s.viewMu.Lock()
	defer s.viewMu.Unlock()

	if view, ok := s.viewMap[fileURI]; ok {
		return view, nil
	}

	if len(s.views) == 0 {
		return nil, fmt.Errorf("views is nil")
	}

	for i := range s.views {
		if s.views[i].ContainsFile(fileURI) {
			s.viewMap[fileURI] = s.views[i]

			return s.views[i], nil
		}
	}

	for i := range s.views {
		if s.views[i].FileKnown(fileURI) {
			s.viewMap[fileURI] = s.views[i]

			return s.views[i], nil
		}
	}

	return s.views[0], nil
}

func (s *Session) UpdateOverlayFS(ctx context.Context, changes []*FileChange) error {
	return s.Update(ctx, changes)
}

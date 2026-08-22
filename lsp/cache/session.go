package cache

import (
	"context"
	"fmt"
	"sync"

	"go.lsp.dev/uri"
)

type Session struct {
	// fs is the underlying file source (disk in production, in-memory in
	// tests); the embedded overlayFS serves open-editor content over it.
	fs FileSource

	viewMu  sync.Mutex
	views   []*View
	viewMap map[uri.URI]*View // map of URI->best view

	// The session owns the overlayFS: open-editor content lives here, and
	// views read through it.
	*overlayFS
}

func NewSession(fs FileSource) *Session {
	sess := &Session{
		fs:        fs,
		views:     make([]*View, 0),
		viewMap:   make(map[uri.URI]*View),
		overlayFS: NewOverlayFS(fs),
	}

	return sess
}

// AddView registers a view for the workspace folder, returning the
// existing view when the folder is already tracked. includePaths and
// AddView registers a view for the workspace folder, returning the
// existing view when the folder is already tracked. includePaths is the
// folder's resolved include configuration; the view fixes it at creation.
func (s *Session) AddView(folder uri.URI, includePaths []string) *View {
	s.viewMu.Lock()
	defer s.viewMu.Unlock()

	for _, v := range s.views {
		if v.folder == folder {
			return v
		}
	}

	view := NewView(folder, s.overlayFS, includePaths)
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

	// Fallback: the file is not inside any view's folder (an include
	// outside the root, or a stray URI). The first view is the session's
	// default; silently treating it as no error mirrors single-root
	// server deployments where every file belongs to the one view.
	return s.views[0], nil
}

func (s *Session) UpdateOverlayFS(ctx context.Context, changes []*FileChange) error {
	return s.Update(ctx, changes)
}

// WalkFiles enumerates the file source under root. Open overlays are
// already known to the session via didOpen, so this walks the underlying
// source (the disk in production).
func (s *Session) WalkFiles(ctx context.Context, root uri.URI, fn func(uri.URI) error) error {
	return s.overlayFS.WalkFiles(ctx, root, fn)
}

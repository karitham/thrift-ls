package cache

import (
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"go.lsp.dev/uri"

	"github.com/karitham/thrift-ls/syntax"
)

// gundam-themed URIs, fixed for deterministic sort order across tests:
// char < federation.gundam < mobile_suit.zeon < strike_rouge.
const (
	charURI        = "file:///tmp/char.thrift"
	federationURI  = "file:///tmp/federation.gundam.thrift"
	mobileSuitURI  = "file:///tmp/mobile_suit.zeon.thrift"
	strikeRougeURI = "file:///tmp/strike_rouge.thrift"
)

// buildTestContext registers the given include edges, where the map value is
// the list of files the key file includes, and returns the context.
func buildTestContext(t *testing.T, edges map[string][]string) *Context {
	t.Helper()

	c := NewContext()

	for file, includes := range edges {
		inc := make([]*syntax.Include, 0, len(includes))
		for _, includePath := range includes {
			inc = append(inc, &syntax.Include{Path: &syntax.Token{Text: includePath}})
		}

		c.Register(uri.URI(file), inc, resolveTestInclude)
	}

	return c
}

// resolveTestInclude resolves an include path relative to the including
// file's directory, mirroring the snapshot resolver's relative fallback.
func resolveTestInclude(cur uri.URI, includePath string) uri.URI {
	return uri.File(filepath.Join(filepath.Dir(cur.Path()), includePath))
}

func Test_Context_Dependents(t *testing.T) {
	for _, tt := range []struct {
		name  string
		edges map[string][]string
		file  string
		want  []uri.URI
	}{
		{
			name: "linear chain",
			edges: map[string][]string{
				strikeRougeURI: {"federation.gundam.thrift"},
				federationURI:  {"mobile_suit.zeon.thrift"},
			},
			file: mobileSuitURI,
			want: []uri.URI{federationURI, strikeRougeURI},
		},
		{
			name: "diamond",
			edges: map[string][]string{
				strikeRougeURI: {"federation.gundam.thrift", "mobile_suit.zeon.thrift"},
				federationURI:  {"char.thrift"},
				mobileSuitURI:  {"char.thrift"},
			},
			file: charURI,
			want: []uri.URI{federationURI, mobileSuitURI, strikeRougeURI},
		},
		{
			name: "three cycle terminates",
			edges: map[string][]string{
				strikeRougeURI: {"federation.gundam.thrift"},
				federationURI:  {"mobile_suit.zeon.thrift"},
				mobileSuitURI:  {"strike_rouge.thrift"},
			},
			file: strikeRougeURI,
			want: []uri.URI{federationURI, mobileSuitURI, strikeRougeURI},
		},
		{
			name: "self include terminates",
			edges: map[string][]string{
				"file:///tmp/side_effect.thrift": {"side_effect.thrift"},
			},
			file: "file:///tmp/side_effect.thrift",
			want: []uri.URI{"file:///tmp/side_effect.thrift"},
		},
		{
			name: "file with no includers",
			edges: map[string][]string{
				strikeRougeURI: {"federation.gundam.thrift"},
			},
			file: strikeRougeURI,
			want: []uri.URI{},
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			c := buildTestContext(t, tt.edges)

			got := c.Dependents(uri.URI(tt.file))
			assert.Equal(t, tt.want, got)
		})
	}
}

func Test_Context_RegisterChangedIncludes(t *testing.T) {
	for _, tt := range []struct {
		name           string
		edges          map[string][]string
		file           string
		registerWith   []string
		wantAffected   []uri.URI
		wantDependents []uri.URI
	}{
		{
			// A->B->A cycle: dropping A's edge to B also drops A from the
			// dependent set (A only reached A through its own include).
			// The return value is the union of old and new dependents.
			name: "cycle re-register returns union of old and new dependents",
			edges: map[string][]string{
				strikeRougeURI: {"federation.gundam.thrift"},
				federationURI:  {"strike_rouge.thrift"},
			},
			file:           strikeRougeURI,
			registerWith:   []string{},
			wantAffected:   []uri.URI{federationURI, strikeRougeURI},
			wantDependents: []uri.URI{federationURI},
		},
		{
			// Nothing includes A, so neither A's old nor new dependents are
			// affected; the changed file itself is the caller's concern.
			name: "re-register with no includers affects no dependents",
			edges: map[string][]string{
				strikeRougeURI: {"federation.gundam.thrift"},
			},
			file:           strikeRougeURI,
			registerWith:   []string{"mobile_suit.zeon.thrift"},
			wantAffected:   []uri.URI{},
			wantDependents: []uri.URI{},
		},
		{
			name: "re-register unchanged edges returns current dependents",
			edges: map[string][]string{
				strikeRougeURI: {"federation.gundam.thrift"},
			},
			file:           federationURI,
			registerWith:   []string{},
			wantAffected:   []uri.URI{strikeRougeURI},
			wantDependents: []uri.URI{strikeRougeURI},
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			c := buildTestContext(t, tt.edges)

			inc := make([]*syntax.Include, 0, len(tt.registerWith))
			for _, includePath := range tt.registerWith {
				inc = append(inc, &syntax.Include{Path: &syntax.Token{Text: includePath}})
			}

			gotAffected := c.Register(uri.URI(tt.file), inc, resolveTestInclude)

			assert.Equal(t, tt.wantAffected, gotAffected)
			assert.Equal(t, tt.wantDependents, c.Dependents(uri.URI(tt.file)))
		})
	}
}

func Test_Context_Forget(t *testing.T) {
	c := buildTestContext(t, map[string][]string{
		strikeRougeURI: {"federation.gundam.thrift"},
		federationURI:  {"mobile_suit.zeon.thrift"},
	})

	got := c.Forget(uri.URI(federationURI))

	assert.Equal(t, []uri.URI{strikeRougeURI}, got)

	// B's edges are gone: nothing is reachable through it any more.
	node := c.graph.Get(uri.URI(federationURI))
	assert.NotNil(t, node)
	assert.Empty(t, node.OutDegree())

	// A still includes B, so B keeps its dependents.
	assert.Equal(t, []uri.URI{strikeRougeURI}, c.Dependents(uri.URI(federationURI)))
	// C's includer chain is gone until B is re-parsed.
	assert.Empty(t, c.Dependents(uri.URI(mobileSuitURI)))
}

func Test_Context_DuplicateIncludes(t *testing.T) {
	c := NewContext()

	duplicates := []*syntax.Include{
		{Path: &syntax.Token{Text: "federation.gundam.thrift"}},
		{Path: &syntax.Token{Text: "federation.gundam.thrift"}},
	}

	c.Register(uri.URI(strikeRougeURI), duplicates, resolveTestInclude)

	node := c.graph.Get(uri.URI(strikeRougeURI))
	assert.NotNil(t, node)
	assert.Equal(t, []uri.URI{federationURI}, node.OutDegree())

	// the include target records exactly one dependent
	assert.Equal(t, []uri.URI{strikeRougeURI}, c.Dependents(uri.URI(federationURI)))
}

func Test_Context_UnknownInclude(t *testing.T) {
	c := NewContext()

	// A resolve func returning an unresolvable URI must not crash Register;
	// the unknown target still records its dependent.
	unknown := &syntax.Include{Path: &syntax.Token{Text: "missing/nonexistent.thrift"}}
	unknownURI := uri.File(filepath.Join("/tmp", "missing", "nonexistent.thrift"))

	c.Register(uri.URI(strikeRougeURI), []*syntax.Include{unknown}, func(uri.URI, string) uri.URI {
		return unknownURI
	})

	assert.Equal(t, []uri.URI{uri.URI(strikeRougeURI)}, c.Dependents(unknownURI))
	assert.Empty(t, c.Dependents(uri.URI(strikeRougeURI)))
}

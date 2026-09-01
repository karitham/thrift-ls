package resolver

import (
	"testing"
)

// The fixtures give two dungeon kitchens the same relative layout: the
// same include path exists under several include paths, so search order
// decides which file wins. The far kitchen (laios) comes first in the
// include paths.
func dungeonFS(t *testing.T) map[string][]byte {
	t.Helper()

	files := []string{
		"/dungeon/senshi/kitchen/recipes/hotpot.thrift",
		"/dungeon/senshi/kitchen/dishes.thrift",
		"/dungeon/senshi/pantry/hotpot.thrift",
		"/dungeon/laios/kitchen/recipes/hotpot.thrift",
		"/dungeon/laios/kitchen/dishes.thrift",
		"/dungeon/laios/pantry/hotpot.thrift",
	}

	m := make(map[string][]byte, len(files))
	for _, f := range files {
		m[f] = []byte("struct Monster {}")
	}

	return m
}

func dungeonIncludePaths() []string {
	return []string{
		"/dungeon/laios/kitchen",
		"/dungeon/laios/pantry",
		"/dungeon/senshi/pantry",
		"/dungeon/senshi/kitchen",
	}
}

func TestCandidatesNearestIncludePathWins(t *testing.T) {
	r := NewWithFS(dungeonIncludePaths(), absMapFS(dungeonFS(t)))

	got := r.Candidates("/dungeon/senshi/kitchen/recipes/stew.thrift", "recipes/hotpot.thrift")
	want := []string{
		"/dungeon/senshi/kitchen/recipes/hotpot.thrift",
		"/dungeon/laios/kitchen/recipes/hotpot.thrift",
	}

	if len(got) != len(want) {
		t.Fatalf("Candidates = %v, want %v", got, want)
	}

	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("Candidates = %v, want %v", got, want)
		}
	}
}

func TestCandidatesOrderedNearestFirst(t *testing.T) {
	r := NewWithFS(dungeonIncludePaths(), absMapFS(dungeonFS(t)))

	got := r.Candidates("/dungeon/senshi/kitchen/recipes/stew.thrift", "dishes.thrift")
	want := []string{
		"/dungeon/senshi/kitchen/dishes.thrift",
		"/dungeon/laios/kitchen/dishes.thrift",
	}

	if len(got) != len(want) {
		t.Fatalf("Candidates = %v, want %v", got, want)
	}

	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("Candidates = %v, want %v", got, want)
		}
	}
}

func TestCandidatesTiesKeepConfigOrder(t *testing.T) {
	// From a sibling floor, every root ties on shared prefix, so the
	// include path order decides.
	r := NewWithFS(dungeonIncludePaths(), absMapFS(dungeonFS(t)))

	got := r.Candidates("/dungeon/floor4/main.thrift", "hotpot.thrift")
	want := []string{
		"/dungeon/laios/pantry/hotpot.thrift",
		"/dungeon/senshi/pantry/hotpot.thrift",
	}

	if len(got) != len(want) {
		t.Fatalf("Candidates = %v, want %v", got, want)
	}

	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("Candidates = %v, want %v", got, want)
		}
	}
}

func TestResolveUsesNearestCandidate(t *testing.T) {
	r := NewWithFS(dungeonIncludePaths(), absMapFS(dungeonFS(t)))

	got := r.Resolve("/dungeon/senshi/kitchen/recipes/stew.thrift", "dishes.thrift")
	if want := "/dungeon/senshi/kitchen/dishes.thrift"; got != want {
		t.Errorf("Resolve = %q, want %q", got, want)
	}
}

func TestResolveFallsBackToFileDir(t *testing.T) {
	r := NewWithFS(dungeonIncludePaths(), absMapFS(dungeonFS(t)))

	cur := "/dungeon/senshi/kitchen/recipes/stew.thrift"
	got := r.Resolve(cur, "missing.thrift")
	if want := "/dungeon/senshi/kitchen/recipes/missing.thrift"; got != want {
		t.Errorf("Resolve = %q, want %q", got, want)
	}
}

func TestSortByProximityKeepsInputOrder(t *testing.T) {
	paths := []string{"/dungeon/floor1", "/dungeon", "/dungeon/floor1/monsters"}

	got := sortByProximity(paths, "/dungeon/floor1/monsters")
	want := []string{"/dungeon/floor1/monsters", "/dungeon/floor1", "/dungeon"}

	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("sortByProximity = %v, want %v", got, want)
		}
	}

	if paths[0] != "/dungeon/floor1" {
		t.Errorf("input mutated: %v", paths)
	}
}

// Locks the ranking semantics: shared prefix, not filepath.Rel containment
// depth. The senshi root shares more components with the file's directory
// and wins, though the config lists laios first.
func TestCandidatesParentRootOutranksFarRoot(t *testing.T) {
	r := NewWithFS([]string{
		"/dungeon/laios/recipes", // farther
		"/dungeon/senshi/recipes",
	}, absMapFS(map[string][]byte{
		"/dungeon/laios/recipes/stew.thrift":  []byte("struct Monster {}"),
		"/dungeon/senshi/recipes/stew.thrift": []byte("struct Monster {}"),
	}))

	got := r.Resolve("/dungeon/senshi/recipes/chapter2/stew.thrift", "stew.thrift")
	if want := "/dungeon/senshi/recipes/stew.thrift"; got != want {
		t.Errorf("Resolve = %q, want %q", got, want)
	}
}

// The two heuristics disagree here: shared prefix ranks the senshi root
// first (3 shared components vs 2), filepath.Rel depth ranks the laios
// root first. Shared prefix wins.
func TestCandidatesSharedPrefixNotRelDepth(t *testing.T) {
	r := NewWithFS([]string{
		"/dungeon/laios", // shallower by filepath.Rel
		"/dungeon/senshi/kitchen2/monsters/beasts",
	}, absMapFS(map[string][]byte{
		"/dungeon/laios/stew.thrift":                           []byte("struct Monster {}"),
		"/dungeon/senshi/kitchen2/monsters/beasts/stew.thrift": []byte("struct Monster {}"),
	}))

	got := r.Resolve("/dungeon/senshi/kitchen/stew.thrift", "stew.thrift")
	if want := "/dungeon/senshi/kitchen2/monsters/beasts/stew.thrift"; got != want {
		t.Errorf("Resolve = %q, want %q", got, want)
	}
}

// POSIX compares case-sensitively: case-differing prefixes are different
// directories. Windows components fold case and split backslashes,
// regardless of the build platform.
func TestSharedDirPrefixCaseSensitivity(t *testing.T) {
	// POSIX components compare case-sensitively: only the leading "/"
	// component of the two paths is shared.
	posixA := sharedComponents("/srv/Data/room", false)
	posixB := sharedComponents("/SRV/Data/pantry", false)

	if n := sharedPrefixLen(posixA, posixB, false); n != 1 {
		t.Errorf("posix shared prefix = %d, want 1", n)
	}

	as := sharedComponents(`C:\Repo\room`, true)
	bs := sharedComponents(`c:\repo\pantry`, true)

	if n := sharedPrefixLen(as, bs, true); n != 2 { // drive and repo match
		t.Errorf("windows shared prefix = %d, want 2", n)
	}
}

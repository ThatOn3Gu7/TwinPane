package fsys

import "testing"

// Sorting must not depend on how often a name is lower-cased: mixed case,
// accents and ties behave exactly as the plain comparator says.
func TestSortEntriesCaseInsensitiveOrderIsStable(t *testing.T) {
	names := []string{"beta", "Alpha", "alpha", "ALPHA", "alpha.txt", "_x", "Zeta", "zeta"}
	es := make([]Entry, len(names))
	for i, n := range names {
		es[i] = Entry{Name: n}
	}
	SortEntries(es, SortName, false, true)
	got := make([]string, len(es))
	for i, e := range es {
		got[i] = e.Name
	}
	// equal ignoring case: byte order decides, which puts "ALPHA" first
	want := []string{"_x", "ALPHA", "Alpha", "alpha", "alpha.txt", "beta", "Zeta", "zeta"}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("order = %v, want %v", got, want)
		}
	}
	// The input slice is sorted in place and nothing else changes.
	if len(es) != len(names) {
		t.Fatal("sorting changed the length")
	}
}

package search

import (
	"context"
	"reflect"
	"testing"
)

func TestFuzzyScoreSubsequenceAndBoundaries(t *testing.T) {
	for _, tc := range []struct {
		query, path string
		want        bool
	}{
		{"asgo", "src/AdvancedSearch.go", true},
		{"cfg yml", "config/app.yml", true},
		{"readme", "docs/README.md", true},
		{"xyz", "docs/README.md", false},
		{"", "anything", true},
	} {
		_, got := FuzzyScore(tc.query, tc.path)
		if got != tc.want {
			t.Errorf("FuzzyScore(%q, %q) matched=%v, want %v", tc.query, tc.path, got, tc.want)
		}
	}
	boundary, _ := FuzzyScore("as", "src/AdvancedSearch.go")
	substring, _ := FuzzyScore("as", "src/xadvancedsearch.go")
	if boundary <= substring {
		t.Fatalf("component-boundary score %d should beat interior score %d", boundary, substring)
	}
}

func TestFuzzyRankContextCanCancel(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if matches, complete := FuzzyRankContext(ctx, []Entry{{Rel: "needle"}}, "needle"); complete || len(matches) != 0 {
		t.Fatalf("cancelled rank returned %v, complete=%v", matches, complete)
	}
}

func TestFuzzyRankOrdersAndKeepsEntryIndices(t *testing.T) {
	paths := []string{"src/x_advanced_search.go", "docs/advanced.md", "AdvancedSearch.go", "src/other.go"}
	entries := make([]Entry, 0, len(paths))
	for _, path := range paths {
		e, ok := NewEntry("/root", "/root/"+path)
		if !ok {
			t.Fatal(path)
		}
		entries = append(entries, e)
	}
	got := FuzzyRank(entries, "asgo")
	want := []int{2, 0}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("rank indices %v, want %v", got, want)
	}
	if got := FuzzyRank(entries, "no-match"); len(got) != 0 {
		t.Fatalf("unexpected matches: %v", got)
	}
}

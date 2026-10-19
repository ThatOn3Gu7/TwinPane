package search

import (
	"context"
	"sort"
	"strings"
	"unicode"
)

// FuzzyScore reports whether query is a case-insensitive subsequence of
// candidate and, when it is, returns a score (higher is better). Whitespace
// separates independent terms; every term must match somewhere in candidate.
// This deliberately small scorer follows the useful parts of fzf-style
// matching: consecutive characters and path/component boundaries rank well,
// while gaps, leading characters, and long paths are penalized.
func FuzzyScore(query, candidate string) (int, bool) {
	terms := strings.Fields(strings.ToLower(query))
	if len(terms) == 0 {
		return 0, true
	}

	original := []rune(candidate)
	lower := []rune(strings.ToLower(candidate))
	score := 0
	for _, term := range terms {
		needle := []rune(term)
		pos, first, previous := 0, -1, -2
		for _, wanted := range needle {
			found := -1
			for pos < len(lower) {
				if lower[pos] == wanted {
					found = pos
					break
				}
				pos++
			}
			if found < 0 {
				return 0, false
			}
			if first < 0 {
				first = found
				score -= found * 3 // prefer matches near the start
			}
			if found == previous+1 {
				score += 12 // consecutive match
			} else if previous >= 0 {
				score -= found - previous - 1 // penalize a gap
			}
			if found == 0 || fuzzyBoundary(original, found) {
				score += 10 // beginning of a path/component/word
			}
			previous = found
			pos = found + 1
		}
		score += len(needle) * 3
	}
	// Prefer shorter relative paths when their match quality is otherwise
	// equal. The penalty is intentionally small so it cannot outweigh a good
	// contiguous/boundary match.
	score -= len(original) / 100
	return score, true
}

func fuzzyBoundary(candidate []rune, at int) bool {
	if at <= 0 || at >= len(candidate) {
		return at == 0
	}
	previous, current := candidate[at-1], candidate[at]
	if strings.ContainsRune("/\\_.- ", previous) {
		return true
	}
	return unicode.IsLower(previous) && unicode.IsUpper(current)
}

// FuzzyRank returns indices of entries whose relative paths match query,
// ordered by fuzzy score (best first), then by shorter path and lexical path.
// The returned slice is independent of entries and is safe to keep while the
// index receives later batches.
func FuzzyRank(entries []Entry, query string) []int {
	matches, _ := FuzzyRankContext(context.Background(), entries, query)
	return matches
}

// FuzzyRankContext is FuzzyRank with cooperative cancellation while scoring
// large snapshots. complete is false if ctx was canceled before ranking
// finished.
func FuzzyRankContext(ctx context.Context, entries []Entry, query string) ([]int, bool) {
	if ctx.Err() != nil {
		return nil, false
	}
	if strings.TrimSpace(query) == "" {
		matches := make([]int, len(entries))
		for i := range entries {
			if i&255 == 0 && ctx.Err() != nil {
				return nil, false
			}
			matches[i] = i
		}
		return matches, true
	}
	matches := make([]int, 0, len(entries))
	scores := make(map[int]int, len(entries)/4)
	for i := range entries {
		if i&255 == 0 && ctx.Err() != nil {
			return nil, false
		}
		score, ok := FuzzyScore(query, filepathSlash(entries[i].Rel))
		if !ok {
			continue
		}
		matches = append(matches, i)
		scores[i] = score
	}
	sort.SliceStable(matches, func(i, j int) bool {
		a, b := matches[i], matches[j]
		if scores[a] != scores[b] {
			return scores[a] > scores[b]
		}
		ra, rb := entries[a].Rel, entries[b].Rel
		if len(ra) != len(rb) {
			return len(ra) < len(rb)
		}
		return strings.ToLower(ra) < strings.ToLower(rb)
	})
	return matches, ctx.Err() == nil
}

// filepathSlash normalizes the relative paths used by the index so query
// matching behaves the same on Windows and Unix-like systems.
func filepathSlash(path string) string { return strings.ReplaceAll(path, "\\", "/") }

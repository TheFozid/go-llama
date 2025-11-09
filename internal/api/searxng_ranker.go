package api

import (
	"sort"
	"strings"
)

// rankAndFilterResults reorders and trims SearxNG results to keep only the
// best-matching half based on lexical similarity to the search query.
// It is lightweight and purely heuristic — no LLM or external libraries.
func rankAndFilterResults(results []SearxResult, query string) []SearxResult {
	n := len(results)
	if n == 0 {
		return results
	}

	// Try to build query tokens; may be empty.
	queryTokens := toKeywordSet(query)

	// If we can't extract any meaningful tokens from the query,
	// just keep the first half of the results.
	if len(queryTokens) == 0 {
		keep := n / 2
		if keep < 1 {
			keep = 1
		}
		return results[:keep]
	}

	type scored struct {
		idx   int
		score float64
	}

	scores := make([]scored, 0, n)
	nonZero := 0

	for i, r := range results {
		titleTokens := toKeywordSet(r.Title)
		snippetTokens := toKeywordSet(r.Content)

		titleMatches := countIntersection(queryTokens, titleTokens)
		snippetMatches := countIntersection(queryTokens, snippetTokens)

		score := 2.0*float64(titleMatches) + 1.0*float64(snippetMatches)
		if score > 0 {
			nonZero++
		}
		scores = append(scores, scored{idx: i, score: score})
	}

	// If every score is zero, we can't meaningfully rank; just keep first half.
	if nonZero == 0 {
		keep := n / 2
		if keep < 1 {
			keep = 1
		}
		return results[:keep]
	}

	sort.SliceStable(scores, func(i, j int) bool {
		return scores[i].score > scores[j].score
	})

	keep := n / 2
	if keep < 1 {
		keep = 1
	}

	filtered := make([]SearxResult, 0, keep)
	for _, sc := range scores[:keep] {
		filtered = append(filtered, results[sc.idx])
	}
	return filtered
}

// toKeywordSet converts text into a set of normalized tokens.
// It reuses the global tokenRe and stopwords defined in searxng_enrich.go.
func toKeywordSet(text string) map[string]struct{} {
	text = strings.ToLower(strings.TrimSpace(text))
	if text == "" {
		return nil
	}

	toks := tokenRe.FindAllString(text, -1)
	if len(toks) == 0 {
		return nil
	}

	set := make(map[string]struct{}, len(toks))
	for _, t := range toks {
		t = strings.Trim(t, "-_/")
		if t == "" {
			continue
		}
		if _, stop := stopwords[t]; stop {
			continue
		}
		set[t] = struct{}{}
	}
	if len(set) == 0 {
		return nil
	}
	return set
}

func countIntersection(a, b map[string]struct{}) int {
	if len(a) == 0 || len(b) == 0 {
		return 0
	}
	// Iterate over the smaller map for efficiency.
	if len(a) > len(b) {
		a, b = b, a
	}
	count := 0
	for k := range a {
		if _, ok := b[k]; ok {
			count++
		}
	}
	return count
}

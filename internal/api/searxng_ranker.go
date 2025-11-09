package api

import (
	"regexp"
	"sort"
	"strings"
)

// rankAndFilterResults reorders and trims SearxNG results to keep only the best-matching
// half based on lexical similarity to the search query. It is lightweight and purely
// heuristic, using token overlap and title weighting — no LLM or external libraries.
func rankAndFilterResults(results []struct {
	Title   string `json:"title"`
	URL     string `json:"url"`
	Content string `json:"content"`
}, query string) []struct {
	Title   string `json:"title"`
	URL     string `json:"url"`
	Content string `json:"content"`
} {
	if len(results) == 0 || query == "" {
		return results
	}

	queryTokens := toKeywordSet(query)
	if len(queryTokens) == 0 {
		return results
	}

	type scored struct {
		idx   int
		score float64
	}
	scores := make([]scored, 0, len(results))

	for i, r := range results {
		titleTokens := toKeywordSet(r.Title)
		snippetTokens := toKeywordSet(r.Content)
		if len(titleTokens) == 0 && len(snippetTokens) == 0 {
			continue
		}
		titleMatches := countIntersection(queryTokens, titleTokens)
		snippetMatches := countIntersection(queryTokens, snippetTokens)
		score := 2.0*float64(titleMatches) + 1.0*float64(snippetMatches)
		if score > 0 {
			scores = append(scores, scored{idx: i, score: score})
		}
	}

	if len(scores) == 0 {
		return results
	}

	sort.SliceStable(scores, func(i, j int) bool {
		return scores[i].score > scores[j].score
	})

	// Keep top 50% (at least 1)
	keep := len(scores) / 2
	if keep < 1 {
		keep = 1
	}
	top := scores[:keep]

	filtered := make([]struct {
		Title   string `json:"title"`
		URL     string `json:"url"`
		Content string `json:"content"`
	}, 0, keep)

	for _, sc := range top {
		filtered = append(filtered, results[sc.idx])
	}
	return filtered
}

// toKeywordSet converts text into a set of normalized tokens, reusing logic
// from query_cleaner.go (stopwords, trimming, lowercasing).
func toKeywordSet(text string) map[string]struct{} {
	text = strings.ToLower(text)
	tokenRe := regexp.MustCompile(`[\p{L}\p{N}\-_/]+`)
	toks := tokenRe.FindAllString(text, -1)
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
	return set
}

func countIntersection(a, b map[string]struct{}) int {
	count := 0
	for k := range a {
		if _, ok := b[k]; ok {
			count++
		}
	}
	return count
}

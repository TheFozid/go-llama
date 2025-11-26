package api

import (
	"math"
	"regexp"
	"sort"
	"strings"
)

// SearxResult mirrors the relevant part of searxResp.Results used elsewhere
type SearxResult struct {
	Title   string `json:"title"`
	URL     string `json:"url"`
	Content string `json:"content"`
}

// rankAndFilterResults ranks results by relevance to the query (title + snippet)
// and returns only the top 50%. It uses simple token overlap and phrase matching.
func rankAndFilterResults(query string, results []SearxResult) []SearxResult {
	if len(results) == 0 || strings.TrimSpace(query) == "" {
		return results
	}

	// --- Normalize & tokenize query ---
	query = strings.ToLower(strings.TrimSpace(query))
	tokenRe := regexp.MustCompile(`[\p{L}\p{N}]+`)
	tokens := tokenRe.FindAllString(query, -1)
	if len(tokens) == 0 {
		return results
	}

	// Minimal stopwords for filtering noise
	stop := map[string]bool{
		"the": true, "a": true, "an": true, "and": true, "or": true, "of": true,
		"in": true, "on": true, "to": true, "for": true, "by": true, "with": true,
		"at": true, "from": true, "is": true, "are": true, "was": true, "were": true,
		"be": true, "been": true, "being": true, "it": true, "its": true,
	}
	var qTokens []string
	for _, t := range tokens {
		if !stop[t] && len(t) > 1 {
			qTokens = append(qTokens, t)
		}
	}
	if len(qTokens) == 0 {
		qTokens = tokens
	}

	// --- Score each result ---
	type scored struct {
		item  SearxResult
		score int
	}
	scoredList := make([]scored, 0, len(results))

	fullPhrase := strings.Join(qTokens, " ")
	for _, r := range results {
		title := strings.ToLower(r.Title)
		snippet := strings.ToLower(r.Content)

		titleHits := 0
		snippetHits := 0
		for _, tok := range qTokens {
			if strings.Contains(title, tok) {
				titleHits++
			}
			if strings.Contains(snippet, tok) {
				snippetHits++
			}
		}

		phraseBonus := 0
		if strings.Contains(title, fullPhrase) {
			phraseBonus += 2
		} else if strings.Contains(snippet, fullPhrase) {
			phraseBonus++
		}

		score := titleHits*2 + snippetHits + phraseBonus
		// Normalize by document length to avoid bias toward longer snippets
		textLen := float64(len(title) + len(snippet) + 10)
		normalizedScore := float64(score) / math.Log(textLen)
		scoredList = append(scoredList, scored{item: r, score: int(normalizedScore * 100)})
	}

	// --- Sort descending by score ---
	sort.SliceStable(scoredList, func(i, j int) bool {
		return scoredList[i].score > scoredList[j].score
	})

	// --- Keep top 50% ---
	cut := len(scoredList) / 2
	if cut < 1 {
		cut = 1
	}
	filtered := make([]SearxResult, 0, cut)
	for i := 0; i < cut; i++ {
		filtered = append(filtered, scoredList[i].item)
	}

	return filtered
}

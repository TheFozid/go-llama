package api

import (
	"regexp"
	"strings"
)

// cleanForSearch takes the full user prompt and returns a concise,
// search-optimized query string for SearxNG.
// It removes filler and stopwords, normalises case, limits keyword count,
// and preserves quoted phrases ("like this") verbatim.
func cleanForSearch(prompt string) string {
	if prompt == "" {
		return ""
	}

	// --- Extract quoted phrases first ---
	quoteRe := regexp.MustCompile(`"([^"]+)"`)
	matches := quoteRe.FindAllString(prompt, -1)
	quotedPhrases := make([]string, 0, len(matches))
	for _, m := range matches {
		quotedPhrases = append(quotedPhrases, strings.TrimSpace(m))
	}
	// Remove quoted segments from the main text for cleaning
	prompt = quoteRe.ReplaceAllString(prompt, " ")

	// Normalise whitespace and case
	prompt = strings.ToLower(strings.TrimSpace(prompt))
	prompt = regexp.MustCompile(`\s+`).ReplaceAllString(prompt, " ")

	// Remove leading polite / filler phrases
	fillerPatterns := []string{
		`(?i)\b(can you|could you|please|would you|tell me|explain|show me|i want to know|give me|find me|what is|what are|how does|why does|who is|where is|in your opinion)\b`,
	}
	for _, pat := range fillerPatterns {
		re := regexp.MustCompile(pat)
		prompt = re.ReplaceAllString(prompt, "")
	}
	prompt = strings.TrimSpace(prompt)

	// Tokenize remaining text
	tokenRe := regexp.MustCompile(`[\p{L}\p{N}\-_/]+`)
	tokens := tokenRe.FindAllString(prompt, -1)
	if len(tokens) == 0 && len(quotedPhrases) > 0 {
		return strings.Join(quotedPhrases, " ")
	}

	// Minimal stopword list
	stopwords := map[string]struct{}{
		"a": {}, "an": {}, "the": {}, "and": {}, "or": {}, "but": {}, "if": {}, "then": {}, "so": {},
		"as": {}, "of": {}, "on": {}, "in": {}, "to": {}, "for": {}, "by": {}, "with": {}, "at": {}, "from": {},
		"is": {}, "are": {}, "was": {}, "were": {}, "be": {}, "been": {}, "being": {},
		"it": {}, "its": {}, "this": {}, "that": {}, "these": {}, "those": {}, "what": {}, "which": {},
		"who": {}, "whom": {}, "whose": {}, "about": {}, "into": {}, "over": {}, "under": {}, "between": {},
		"through": {}, "during": {}, "before": {}, "after": {}, "up": {}, "down": {}, "out": {}, "off": {},
		"again": {}, "further": {}, "more": {}, "most": {}, "some": {}, "such": {}, "no": {}, "nor": {},
		"not": {}, "only": {}, "own": {}, "same": {}, "than": {}, "too": {}, "very": {}, "can": {}, "could": {},
		"should": {}, "would": {}, "may": {}, "might": {}, "will": {}, "shall": {}, "do": {}, "does": {}, "did": {},
		"done": {}, "have": {}, "has": {}, "had": {}, "having": {}, "also": {}, "we": {}, "our": {}, "you": {},
		"your": {}, "they": {}, "their": {}, "he": {}, "she": {}, "i": {}, "me": {}, "my": {}, "mine": {},
		"here": {}, "there": {}, "when": {}, "where": {}, "why": {}, "how": {}, "what's": {}, "it's": {},
	}

	// Collect cleaned keywords
	keywords := make([]string, 0, len(tokens))
	seen := map[string]struct{}{}
	for _, tok := range tokens {
		tok = strings.Trim(tok, "-_/")
		if tok == "" {
			continue
		}
		if _, skip := stopwords[tok]; skip {
			continue
		}
		if _, seenBefore := seen[tok]; seenBefore {
			continue
		}
		seen[tok] = struct{}{}
		keywords = append(keywords, tok)
		if len(keywords) >= 12 {
			break
		}
	}

	// Merge keywords + quoted phrases
	finalParts := append(quotedPhrases, keywords...)
	if len(finalParts) == 0 {
		return ""
	}
	query := strings.Join(finalParts, " ")
	query = regexp.MustCompile(`[^\p{L}\p{N}\s"']+`).ReplaceAllString(query, "")
	return strings.TrimSpace(query)
}

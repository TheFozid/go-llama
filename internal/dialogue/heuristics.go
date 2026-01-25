package dialogue

import (
    "strings"
)

// extractURLsFromSearchResults extracts valid http/https URLs from search output lines.
func extractURLsFromSearchResults(searchOutput string) []string {
    urls := []string{}
    lines := strings.Split(searchOutput, "\n")

    for _, line := range lines {
        // SearXNG format: "    URL: https://example.com"
        if strings.Contains(line, "URL: ") {
            parts := strings.Split(line, "URL: ")
            if len(parts) > 1 {
                url := strings.TrimSpace(parts[1])
                // Validate it's a proper URL
                if strings.HasPrefix(url, "http://") || strings.HasPrefix(url, "https://") {
                    urls = append(urls, url)
                }
            }
        }
    }

    return urls
}

// extractSearchKeywords intelligently extracts 2-5 keywords from goal description
func extractSearchKeywords(goalDesc string) string {
    // Remove common prefixes
    desc := strings.ToLower(goalDesc)
    desc = strings.TrimPrefix(desc, "to research and model ")
    desc = strings.TrimPrefix(desc, "to research ")
    desc = strings.TrimPrefix(desc, "research ")
    desc = strings.TrimPrefix(desc, "learn about: ")
    desc = strings.TrimPrefix(desc, "learn about ")
    desc = strings.TrimPrefix(desc, "explore ")
    desc = strings.TrimPrefix(desc, "investigate ")
    desc = strings.TrimPrefix(desc, "analyze ")
    desc = strings.TrimPrefix(desc, "understand ")

    // Remove filler words
    fillerWords := []string{
        "the", "a", "an", "and", "or", "but", "in", "on", "at", "to", "for",
        "of", "with", "by", "from", "as", "is", "was", "are", "were", "been",
        "be", "have", "has", "had", "do", "does", "did", "will", "would",
        "should", "could", "may", "might", "can", "based", "using", "through",
        "emphasizing", "focusing", "quiet", "steady", "ordinary", "routine",
    }

    words := strings.Fields(desc)
    keywords := []string{}

    for _, word := range words {
        // Remove punctuation
        word = strings.Trim(word, ".,;:!?—-\"'()")

        // Skip if empty or too short
        if word == "" || len(word) < 3 {
            continue
        }

        // Skip filler words
        isFiller := false
        for _, filler := range fillerWords {
            if word == filler {
                isFiller = true
                break
            }
        }

        if !isFiller {
            keywords = append(keywords, word)
        }

        // Stop at 5 keywords
        if len(keywords) >= 5 {
            break
        }
    }

    // Join into search query
    if len(keywords) == 0 {
        // Fallback: use first 30 chars of original
        if len(goalDesc) > 30 {
            return goalDesc[:30]
        }
        return goalDesc
    }

    return strings.Join(keywords, " ")
}

// extractSignificantKeywords extracts meaningful words from a goal description
func extractSignificantKeywords(text string) []string {
    text = strings.ToLower(text)

    // Remove common prefixes
    text = strings.TrimPrefix(text, "learn about: ")
    text = strings.TrimPrefix(text, "research ")
    text = strings.TrimPrefix(text, "develop ")
    text = strings.TrimPrefix(text, "create ")
    text = strings.TrimPrefix(text, "need ")
    text = strings.TrimPrefix(text, "deep ")
    text = strings.TrimPrefix(text, "deeper ")

    // Stop words to ignore
    stopWords := map[string]bool{
        "the": true, "a": true, "an": true, "and": true, "or": true, "but": true,
        "in": true, "on": true, "at": true, "to": true, "for": true, "of": true,
        "with": true, "by": true, "from": true, "as": true, "is": true, "was": true,
        "are": true, "were": true, "been": true, "be": true, "have": true, "has": true,
        "had": true, "do": true, "does": true, "did": true, "will": true, "would": true,
        "should": true, "could": true, "may": true, "might": true, "can": true,
        "based": true, "using": true, "about": true, "learn": true, "research": true,
    }

    words := strings.Fields(text)
    keywords := []string{}

    for _, word := range words {
        // Remove punctuation
        word = strings.Trim(word, ".,;:!?—-\"'()")

        // Skip if too short, empty, or stop word
        if len(word) < 4 || stopWords[word] {
            continue
        }

        keywords = append(keywords, word)
    }

    return keywords
}

// calculateKeywordOverlap computes Jaccard similarity between two keyword sets
func calculateKeywordOverlap(keywords1, keywords2 []string) float64 {
    if len(keywords1) == 0 || len(keywords2) == 0 {
        return 0.0
    }

    // Convert to sets
    set1 := make(map[string]bool)
    set2 := make(map[string]bool)

    for _, kw := range keywords1 {
        set1[kw] = true
    }
    for _, kw := range keywords2 {
        set2[kw] = true
    }

    // Count intersection
    intersection := 0
    for kw := range set1 {
        if set2[kw] {
            intersection++
        }
    }

    // Count union
    union := len(set1)
    for kw := range set2 {
        if !set1[kw] {
            union++
        }
    }

    if union == 0 {
        return 0.0
    }

    // Jaccard similarity
    return float64(intersection) / float64(union)
}

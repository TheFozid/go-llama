package api

import (
	"context"
	"io"
	"log"
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/PuerkitoBio/goquery"
)

// enrichAndSummarize fetches a web page and returns a short summary.
// If fetching or summarization fails, it falls back to the provided snippet.
func enrichAndSummarize(urlStr, fallbackSnippet string) string {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, urlStr, nil)
	if err != nil {
		return fallbackSnippet
	}
	req.Header.Set("User-Agent", "Go-Llama/1.0 (+https://github.com/TheFozid/go-llama)")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		log.Printf("⚠️  fetch failed for %s: %v", urlStr, err)
		return fallbackSnippet
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fallbackSnippet
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20)) // limit 1 MB
	if err != nil {
		return fallbackSnippet
	}

	text := extractReadableText(string(body))
	if len(text) < 200 {
		return fallbackSnippet
	}

	summary := summarizeText(text, 3) // roughly 3 sentences
	if summary == "" {
		return fallbackSnippet
	}

	return summary
}

// extractReadableText extracts visible text content from HTML.
// It uses goquery if available; otherwise falls back to regex stripping.
func extractReadableText(html string) string {
	doc, err := goquery.NewDocumentFromReader(strings.NewReader(html))
	if err != nil {
		// fallback to basic tag strip
		re := regexp.MustCompile(`<[^>]+>`)
		return re.ReplaceAllString(html, " ")
	}
	text := strings.TrimSpace(doc.Find("body").Text())
	text = regexp.MustCompile(`\s+`).ReplaceAllString(text, " ")
	return text
}

// summarizeText returns the first N sentences of a long string.
// It’s intentionally lightweight — no LLM, just simple sentence splitting.
func summarizeText(text string, maxSentences int) string {
	// naive sentence split
	sentences := regexp.MustCompile(`(?m)([.!?])\s+`).Split(text, -1)
	if len(sentences) == 0 {
		return ""
	}

	if len(sentences) > maxSentences {
		sentences = sentences[:maxSentences]
	}
	out := strings.Join(sentences, ". ")
	out = strings.TrimSpace(out)
	return out
}

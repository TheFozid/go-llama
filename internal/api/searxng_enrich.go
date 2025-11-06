package api

import (
	"context"
	"fmt"
	"unicode/utf8"
	"io"
	"log"
	"net/http"
	"regexp"
	"sort"
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

	summary := summarizeText(text, 4) // roughly 3 sentences
	if summary == "" {
		return fallbackSnippet
	}

	return summary
}

// extractReadableText extracts visible text content from HTML.
// It removes boilerplate (headers, navs, footers, ads, etc.).
func extractReadableText(html string) string {
	doc, err := goquery.NewDocumentFromReader(strings.NewReader(html))
	if err != nil {
		re := regexp.MustCompile(`<[^>]+>`)
		return re.ReplaceAllString(html, " ")
	}

	// Remove obvious non-content elements.
	doc.Find("header, nav, footer, aside, script, style, noscript, svg, menu, form").Each(func(_ int, s *goquery.Selection) {
		s.Remove()
	})

	// Remove common ad/promo/sidebar elements by class or id.
	junkPatterns := []string{"nav", "menu", "header", "footer", "sidebar", "banner", "cookie", "ad", "promo", "share", "search", "modal", "popup"}
	for _, pattern := range junkPatterns {
		doc.Find(fmt.Sprintf("[class*=%q], [id*=%q]", pattern, pattern)).Each(func(_ int, s *goquery.Selection) {
			s.Remove()
		})
	}

	// Grab all paragraphs and article text.
	var builder strings.Builder
	doc.Find("article, main, section, p").Each(func(_ int, s *goquery.Selection) {
		text := strings.TrimSpace(s.Text())
		if len(text) > 0 {
			builder.WriteString(text)
			if !strings.HasSuffix(text, ".") {
				builder.WriteString(". ")
			} else {
				builder.WriteString(" ")
			}
		}
	})

	text := builder.String()
	text = regexp.MustCompile(`\s+`).ReplaceAllString(text, " ")
	return strings.TrimSpace(text)
}

// summarizeText condenses long article text into a short, non-repetitive summary.
// It is robust: if aggressive filtering yields nothing, it falls back to the first N sentences.
func summarizeText(text string, maxSentences int) string {
	text = strings.TrimSpace(regexp.MustCompile(`\s+`).ReplaceAllString(text, " "))
	if text == "" || maxSentences <= 0 {
		return ""
	}

	// Split into sentences while preserving end punctuation.
	sentRe := regexp.MustCompile(`[^.!?]*[.!?]`)
	raw := sentRe.FindAllString(text, -1)
	if len(raw) == 0 {
		if len(text) > 500 {
			return text[:500] + "..."
		}
		return text
	}

	// Global word frequency (simple importance estimate).
	wordRe := regexp.MustCompile(`\pL+`)
	freq := map[string]int{}
	for _, w := range wordRe.FindAllString(strings.ToLower(text), -1) {
		if len(w) > 2 {
			freq[w]++
		}
	}

	type sent struct {
		s    string
		idx  int
		score int
	}
	var sents []sent
	for i, s := range raw {
		sc := 0
		for _, w := range wordRe.FindAllString(strings.ToLower(s), -1) {
			sc += freq[w]
		}
		sents = append(sents, sent{s: strings.TrimSpace(s), idx: i, score: sc})
	}

	// Rank by score descending; take a candidate pool wider than final N.
	sort.Slice(sents, func(i, j int) bool { return sents[i].score > sents[j].score })
	k := maxSentences * 3
	if k > len(sents) {
		k = len(sents)
	}
	cands := sents[:k]

	// Deduplicate & trim extremes; keep sentences of reasonable size.
	minRunes, maxRunes := 40, 400
	simThresh := 0.7
	chosen := make([]sent, 0, maxSentences)
	for _, c := range cands {
		runes := utf8.RuneCountInString(c.s)
		if runes < minRunes || runes > maxRunes {
			continue
		}
		dup := false
		for _, d := range chosen {
			if jaccardSimilarity(c.s, d.s) > simThresh {
				dup = true
				break
			}
		}
		if !dup {
			chosen = append(chosen, c)
			if len(chosen) == maxSentences {
				break
			}
		}
	}

	// If filters nuked everything, fall back to the first N sentences in order.
	if len(chosen) == 0 {
		n := maxSentences
		if n > len(raw) {
			n = len(raw)
		}
		return strings.TrimSpace(strings.Join(raw[:n], " "))
	}

	// Restore original order and join.
	sort.Slice(chosen, func(i, j int) bool { return chosen[i].idx < chosen[j].idx })
	out := make([]string, 0, len(chosen))
	for _, c := range chosen {
		out = append(out, c.s)
	}
	return strings.TrimSpace(strings.Join(out, " "))
}

func jaccardSimilarity(a, b string) float64 {
	wordRe := regexp.MustCompile(`\pL+`)
	wa := wordRe.FindAllString(strings.ToLower(a), -1)
	wb := wordRe.FindAllString(strings.ToLower(b), -1)

	setA := map[string]struct{}{}
	setB := map[string]struct{}{}
	for _, w := range wa {
		setA[w] = struct{}{}
	}
	for _, w := range wb {
		setB[w] = struct{}{}
	}

	inter := 0
	union := len(setA)
	for w := range setB {
		if _, ok := setA[w]; ok {
			inter++
		} else {
			union++
		}
	}
	if union == 0 {
		return 0
	}
	return float64(inter) / float64(union)
}

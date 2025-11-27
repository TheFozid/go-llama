package api

import (
	"bytes"
	"context"
	"fmt"
	"log"
	"io"
	"mime"
	"net/http"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/PuerkitoBio/goquery"
	"golang.org/x/net/html/charset"
)

// Package-level HTTP client for reuse across enrichment calls
var enrichHTTPClient = &http.Client{
	Timeout: 10 * time.Second,
	Transport: &http.Transport{
		Proxy:               http.ProxyFromEnvironment,
		MaxIdleConns:        128,
		MaxIdleConnsPerHost: 32,
		IdleConnTimeout:     90 * time.Second,
		DisableCompression:  false,
	},
}

// Pre-compiled regexes for performance
var (
	spaceReGlobal    = regexp.MustCompile(`\s+`)
	tokenReGlobal    = regexp.MustCompile(`[\p{L}\p{N}\-_/]+`)
	acronymReGlobal  = regexp.MustCompile(`\b[A-Z]{2,}\b`)
	sentenceReGlobal = regexp.MustCompile(`(?m)([^.!?]*[.!?])`)
	siteFilterRe     = regexp.MustCompile(`(?i)\bsite:[^\s]+`)
	urlFilterRe      = regexp.MustCompile(`https?://\S+`)
)

// Cache for enriched content (simple in-memory cache)
var (
	enrichCache = make(map[string]string)
	cacheMu     sync.RWMutex
)

// Paragraph represents a single paragraph with metadata
type Paragraph struct {
	Text     string
	Position int
	Length   int
}

// ScoredParagraph pairs a paragraph with its relevance score
type ScoredParagraph struct {
	Para  Paragraph
	Score float64
}

// enrichAndSummarize fetches a URL and returns a compact, LLM-optimized summary
func enrichAndSummarize(urlStr, fallbackSnippet, query string) string {
	// Check cache first
	cacheKey := urlStr + "|" + query
	cacheMu.RLock()
	if cached, ok := enrichCache[cacheKey]; ok {
		cacheMu.RUnlock()
		return cached
	}
	cacheMu.RUnlock()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, urlStr, nil)
	if err != nil {
		return fallbackSnippet
	}
	req.Header.Set("User-Agent", "Go-Llama/1.1 (+https://github.com/TheFozid/go-llama)")
	req.Header.Set("Accept", "text/html,application/xhtml+xml;q=0.9,*/*;q=0.1")

	resp, err := enrichHTTPClient.Do(req)
	if err != nil {
		log.Printf("⚠️ fetch failed: %s: %v", urlStr, err)
		return fallbackSnippet
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fallbackSnippet
	}

	// Verify content type is HTML
	ct := resp.Header.Get("Content-Type")
	mt, params, err := mime.ParseMediaType(ct)
	if err != nil {
		mt = ""
	}
	if mt != "" && !strings.Contains(mt, "text/html") && !strings.Contains(mt, "application/xhtml+xml") {
		return fallbackSnippet
	}

	// Read up to 2 MiB
	const maxRead = 2 << 20
	buf := make([]byte, maxRead)
	n, _ := resp.Body.Read(buf)
	body := buf[:n]

	for n < maxRead {
		m, err := resp.Body.Read(buf[n:])
		n += m
		if err != nil || m == 0 {
			break
		}
	}
	body = buf[:n]

	// Decode charset
	decoded := body
	if cs, ok := params["charset"]; ok && !strings.EqualFold(cs, "utf-8") {
		r, err := charset.NewReaderLabel(cs, bytes.NewReader(body))
		if err == nil {
			if b2, err := ioReadAllCap(r, maxRead); err == nil {
				decoded = b2
			}
		}
	}
	if len(decoded) == 0 && (mt == "" || strings.Contains(mt, "html")) {
		if enc, name, _ := charset.DetermineEncoding(body, ct); !strings.EqualFold(name, "utf-8") {
			r := enc.NewDecoder().Reader(bytes.NewReader(body))
			if b2, err := ioReadAllCap(r, maxRead); err == nil {
				decoded = b2
			}
		}
	}
	if len(decoded) == 0 {
		decoded = body
	}

	html := string(decoded)

	// Extract paragraphs as separate units
	paragraphs := extractParagraphs(html)

	// If extraction failed or too dynamic, check
	if len(paragraphs) == 0 {
		if looksDynamic(html) {
			log.Printf("⚠️ Skipping dynamic site: %s", urlStr)
			return fallbackSnippet
		}
		return fallbackSnippet
	}

	// Build query tokens for relevance scoring
	queryTokens := buildQueryTokensForRanking(query)

	// Apply intelligent differential compression
	summary := intelligentSummarize(paragraphs, 1000, queryTokens)

	if summary == "" || utf8.RuneCountInString(summary) < 100 {
		return fallbackSnippet
	}

	// Quality check: compare our enriched snippet vs SearXNG's original
	enrichedScore := scoreSnippetQuality(summary, query)
	fallbackScore := scoreSnippetQuality(fallbackSnippet, query)

	// Use enriched only if it's better (at least 10 points higher, or if fallback is poor)
	var finalSnippet string
	useEnriched := false

	if enrichedScore >= fallbackScore+10 {
		useEnriched = true
	} else if fallbackScore < 50 && enrichedScore > fallbackScore {
		useEnriched = true
	}

	if useEnriched {
		finalSnippet = summary
		log.Printf("✅ Enriched (score: %d) vs SearXNG (score: %d): %s", enrichedScore, fallbackScore, urlStr)
	} else {
		finalSnippet = fallbackSnippet
		log.Printf("⚠️ SearXNG (score: %d) vs Enriched (score: %d): %s", fallbackScore, enrichedScore, urlStr)
	}

	// Cache the result
	cacheMu.Lock()
	if len(enrichCache) > 100 {
		enrichCache = make(map[string]string)
	}
	enrichCache[cacheKey] = finalSnippet
	cacheMu.Unlock()

	return finalSnippet
}

// extractParagraphs extracts all substantial paragraphs from the page
func extractParagraphs(html string) []Paragraph {
	doc, err := goquery.NewDocumentFromReader(strings.NewReader(html))
	if err != nil {
		return nil
	}

	// Remove junk
	doc.Find("script, style, noscript, iframe, svg, canvas, template, link, meta, form, button, input, select, textarea").Remove()
	doc.Find("nav, footer, aside, header, .nav, .menu, .footer, .sidebar, .comments, .comment, .related, .share, .social").Remove()

	// Find main content container (prioritize semantic tags)
	var mainContainer *goquery.Selection
	mainContainer = doc.Find("article").First()
	if mainContainer.Length() == 0 {
		mainContainer = doc.Find("main, [role='main']").First()
	}
	if mainContainer.Length() == 0 {
		mainContainer = doc.Find(".article-body, .article-content, .post-content, .entry-content, .story-body, .post-body").First()
	}
	if mainContainer.Length() == 0 {
		mainContainer = doc.Find("body")
	}

	// Extract all paragraphs
	var paragraphs []Paragraph
	position := 0

	mainContainer.Find("p").Each(func(i int, p *goquery.Selection) {
		text := strings.TrimSpace(compactWhitespace(p.Text()))

		// Skip very short paragraphs
		if utf8.RuneCountInString(text) < 30 {
			return
		}

		// Skip paragraphs that are mostly links
		linkText := ""
		p.Find("a").Each(func(_ int, a *goquery.Selection) {
			linkText += a.Text()
		})
		linkRatio := float64(len(linkText)) / float64(len(text))
		if linkRatio > 0.6 {
			return
		}

		paragraphs = append(paragraphs, Paragraph{
			Text:     text,
			Position: position,
			Length:   utf8.RuneCountInString(text),
		})
		position++
	})

	return paragraphs
}

// scoreParagraphRelevance scores a paragraph based on query relevance and quality
func scoreParagraphRelevance(para Paragraph, queryTokens []string) float64 {
	text := strings.ToLower(para.Text)
	score := 0.0

	// 1. Query term coverage (most important)
	queryHits := 0
	totalQueryTerms := len(queryTokens)
	for _, token := range queryTokens {
		if strings.Contains(text, token) {
			queryHits++
		}
	}
	if totalQueryTerms > 0 {
		coverage := float64(queryHits) / float64(totalQueryTerms)
		score += coverage * 50.0 // Up to 50 points
	}

	// 2. Position bias (earlier paragraphs often more relevant)
	if para.Position == 0 {
		score += 20.0
	} else if para.Position == 1 {
		score += 15.0
	} else if para.Position == 2 {
		score += 10.0
	} else if para.Position <= 5 {
		score += 5.0
	}

	// 3. Information density (numbers, dates, proper nouns)
	hasNumbers := strings.ContainsAny(text, "0123456789")
	if hasNumbers {
		score += 10.0
	}

	// Count capitalized words (likely proper nouns)
	words := strings.Fields(para.Text)
	properNouns := 0
	for _, word := range words {
		if len(word) > 1 && unicode.IsUpper(rune(word[0])) {
			properNouns++
		}
	}
	if len(words) > 0 {
		properNounRatio := float64(properNouns) / float64(len(words))
		score += properNounRatio * 15.0
	}

	// 4. Length bonus (prefer substantial paragraphs)
	if para.Length >= 100 && para.Length <= 300 {
		score += 10.0
	} else if para.Length > 50 {
		score += 5.0
	}

	return score
}

// compressParagraph applies differential compression based on relevance rank
func compressParagraph(para Paragraph, rankPercentile float64, queryTokens []string) string {
	if rankPercentile >= 0.8 {
		// Top 20%: Keep almost verbatim
		return para.Text

	} else if rankPercentile >= 0.4 {
		// Middle 40%: Extract 1-2 key sentences
		sentences := splitSentences(para.Text)
		if len(sentences) == 0 {
			return para.Text
		}

		// Score each sentence by query relevance
		type scoredSent struct {
			text  string
			score float64
		}
		var scored []scoredSent

		qset := make(map[string]bool)
		for _, qt := range queryTokens {
			qset[qt] = true
		}

		for _, sent := range sentences {
			sentLower := strings.ToLower(sent)
			hits := 0
			for token := range qset {
				if strings.Contains(sentLower, token) {
					hits++
				}
			}
			scored = append(scored, scoredSent{
				text:  sent,
				score: float64(hits),
			})
		}

		// Sort by relevance
		sort.Slice(scored, func(i, j int) bool {
			return scored[i].score > scored[j].score
		})

		// Take top 1-2 sentences
		keepCount := 1
		if len(scored) >= 3 && rankPercentile >= 0.6 {
			keepCount = 2
		}

		var result []string
		for i := 0; i < keepCount && i < len(scored); i++ {
			result = append(result, scored[i].text)
		}

		return strings.Join(result, " ")

	} else {
		// Bottom 40%: Heavy compression or discard
		sentLower := strings.ToLower(para.Text)
		queryHits := 0
		for _, token := range queryTokens {
			if strings.Contains(sentLower, token) {
				queryHits++
			}
		}

		if queryHits >= 2 {
			// Has some relevance, keep one sentence
			sentences := splitSentences(para.Text)
			if len(sentences) > 0 {
				return sentences[0]
			}
		}

		// Otherwise discard
		return ""
	}
}

// intelligentSummarize applies content-aware differential compression
func intelligentSummarize(paras []Paragraph, targetChars int, queryTokens []string) string {
	if len(paras) == 0 {
		return ""
	}

	// Score all paragraphs
	var scored []ScoredParagraph
	for _, para := range paras {
		score := scoreParagraphRelevance(para, queryTokens)
		scored = append(scored, ScoredParagraph{
			Para:  para,
			Score: score,
		})
	}

	// Sort by score to determine percentiles
	sort.Slice(scored, func(i, j int) bool {
		return scored[i].Score > scored[j].Score
	})

	// Calculate percentile for each
	percentiles := make(map[int]float64)
	for i, sp := range scored {
		percentile := 1.0 - (float64(i) / float64(len(scored)))
		percentiles[sp.Para.Position] = percentile
	}

	// Re-sort by original position to maintain narrative flow
	sort.Slice(scored, func(i, j int) bool {
		return scored[i].Para.Position < scored[j].Para.Position
	})

	// Compress each paragraph based on its rank
	var compressed []string
	totalChars := 0

	for _, sp := range scored {
		percentile := percentiles[sp.Para.Position]
		compressedPara := compressParagraph(sp.Para, percentile, queryTokens)

		if compressedPara == "" {
			continue
		}

		// Check if approaching limit
		if totalChars+len(compressedPara) > targetChars && totalChars > targetChars/2 {
			break
		}

		compressed = append(compressed, compressedPara)
		totalChars += len(compressedPara)
	}

	if len(compressed) == 0 {
		return ""
	}

	return strings.Join(compressed, " ")
}

// scoreSnippetQuality rates a snippet based on information density and relevance
func scoreSnippetQuality(snippet, query string) int {
	if snippet == "" {
		return 0
	}

	snippet = strings.ToLower(strings.TrimSpace(snippet))
	query = strings.ToLower(strings.TrimSpace(query))

	score := 0

	// Length score
	length := len(snippet)
	if length >= 100 && length <= 500 {
		score += 30
	} else if length > 50 && length < 100 {
		score += 15
	} else if length > 500 && length <= 1000 {
		score += 20
	} else if length < 50 {
		score += 5
	}

	// Query term coverage
	queryTokens := strings.Fields(query)
	hitCount := 0
	for _, token := range queryTokens {
		if len(token) > 2 && strings.Contains(snippet, token) {
			hitCount++
		}
	}
	if len(queryTokens) > 0 {
		coverage := float64(hitCount) / float64(len(queryTokens))
		score += int(coverage * 30)
	}

	// Sentence structure
	sentenceCount := strings.Count(snippet, ".") + strings.Count(snippet, "!") + strings.Count(snippet, "?")
	if sentenceCount >= 2 && sentenceCount <= 5 {
		score += 20
	} else if sentenceCount == 1 {
		score += 10
	}

	// Information density
	hasNumbers := strings.ContainsAny(snippet, "0123456789")
	if hasNumbers {
		score += 10
	}

	// Penalize junk
	junkPhrases := []string{
		"click here", "subscribe", "sign up", "register", "login",
		"cookie", "javascript", "browser", "search for", "powered by",
		"copyright", "all rights reserved", "terms of service",
	}
	junkCount := 0
	for _, junk := range junkPhrases {
		if strings.Contains(snippet, junk) {
			junkCount++
		}
	}
	score -= (junkCount * 15)

	if score < 0 {
		score = 0
	}
	if score > 100 {
		score = 100
	}

	return score
}

// buildQueryTokensForRanking turns a raw query into tokens for relevance scoring
func buildQueryTokensForRanking(query string) []string {
	if query == "" {
		return nil
	}

	q := siteFilterRe.ReplaceAllString(query, " ")
	q = urlFilterRe.ReplaceAllString(q, " ")
	q = strings.ToLower(strings.TrimSpace(q))
	q = spaceReGlobal.ReplaceAllString(q, " ")

	toks := tokenReGlobal.FindAllString(q, -1)
	if len(toks) == 0 {
		return nil
	}

	out := make([]string, 0, len(toks))
	seen := map[string]struct{}{}
	for _, t := range toks {
		t = strings.Trim(t, "-_/")
		if t == "" {
			continue
		}
		if _, stop := stopwords[t]; stop {
			continue
		}
		if len(t) < 2 && !isNumeric(t) {
			continue
		}
		stem := lightStem(t)
		if _, ok := seen[stem]; ok {
			continue
		}
		seen[stem] = struct{}{}
		out = append(out, stem)
	}
	return out
}

// looksDynamic returns true if HTML likely requires client-side JS rendering
func looksDynamic(html string) bool {
	headCutoff := 120 * 1024
	if len(html) > headCutoff {
		html = html[:headCutoff]
	}
	lower := strings.ToLower(html)

	scriptCount := strings.Count(lower, "<script")
	if scriptCount == 0 {
		return false
	}

	spaMarkers := []string{
		"reactdom.render", "__next", "next-data", "next.config",
		"vite", "webpackjsonp", "vue.runtime", "nuxt", "svelte",
		"astro", "hydration", "data-reactroot", "data-hydration",
		"window.__app__", "window.__data__", "app.mount(",
	}
	matches := 0
	for _, m := range spaMarkers {
		if strings.Contains(lower, m) {
			matches++
		}
	}

	if strings.Contains(lower, "please enable javascript") ||
		strings.Contains(lower, "requires javascript") ||
		strings.Contains(lower, "enable your browser to view this") {
		return true
	}

	if matches >= 2 {
		return true
	}

	textCount := countLetters(lower)
	if scriptCount > 40 && textCount < 1200 {
		return true
	}

	if strings.Contains(lower, "id=\"root\"") || strings.Contains(lower, "id='root'") ||
		strings.Contains(lower, "id=\"app\"") || strings.Contains(lower, "id='app'") {
		if textCount > 1500 {
			return false
		}
		if matches >= 1 {
			return true
		}
	}

	if strings.Contains(lower, "article") ||
		strings.Contains(lower, "<main") ||
		strings.Count(lower, "<p>") > 5 {
		return false
	}

	return false
}

// Helper functions

func ioReadAllCap(r io.Reader, cap int) ([]byte, error) {
	var out bytes.Buffer
	tmp := make([]byte, 32*1024)
	for out.Len() < cap {
		n, err := r.Read(tmp)
		if n > 0 {
			remain := cap - out.Len()
			if n > remain {
				n = remain
			}
			out.Write(tmp[:n])
		}
		if err != nil {
			if err == io.EOF {
				break
			}
			return out.Bytes(), err
		}
	}
	return out.Bytes(), nil
}

func countLetters(s string) int {
	n := 0
	for _, r := range s {
		if unicode.IsLetter(r) {
			n++
		}
	}
	return n
}

func compactWhitespace(s string) string {
	return spaceReGlobal.ReplaceAllString(s, " ")
}

func normalizeQuotes(s string) string {
	replacer := strings.NewReplacer(
		"'", "'",
		"'", "'",
		""", `"`,
		""", `"`,
		"–", "-",
		"—", "-",
		"…", "...",
	)
	return replacer.Replace(s)
}

func splitSentences(s string) []string {
	m := sentenceReGlobal.FindAllString(s, -1)
	if len(m) == 0 {
		return []string{s}
	}
	var out []string
	for _, x := range m {
		x = strings.TrimSpace(x)
		if x != "" {
			out = append(out, x)
		}
	}
	return out
}

var stopwords = map[string]struct{}{
	"a": {}, "an": {}, "the": {}, "and": {}, "or": {}, "but": {}, "if": {}, "then": {}, "so": {},
	"as": {}, "of": {}, "on": {}, "in": {}, "to": {}, "for": {}, "by": {}, "with": {}, "at": {}, "from": {},
	"is": {}, "are": {}, "was": {}, "were": {}, "be": {}, "been": {}, "being": {}, "it": {}, "its": {},
	"this": {}, "that": {}, "these": {}, "those": {}, "which": {}, "who": {}, "whom": {}, "whose": {},
	"about": {}, "into": {}, "over": {}, "under": {}, "between": {}, "through": {}, "during": {}, "before": {}, "after": {},
	"up": {}, "down": {}, "out": {}, "off": {}, "again": {}, "further": {}, "more": {}, "most": {}, "some": {}, "such": {},
	"no": {}, "nor": {}, "not": {}, "only": {}, "own": {}, "same": {}, "than": {}, "too": {}, "very": {}, "can": {}, "could": {},
	"should": {}, "would": {}, "may": {}, "might": {}, "will": {}, "shall": {}, "do": {}, "does": {}, "did": {}, "done": {},
	"have": {}, "has": {}, "had": {}, "having": {}, "also": {}, "via": {}, "using": {}, "use": {},
	"we": {}, "our": {}, "you": {}, "your": {}, "they": {}, "their": {}, "he": {}, "she": {}, "i": {},
	"here": {}, "there": {}, "when": {}, "where": {}, "why": {}, "how": {}, "what": {},
	"article": {}, "read": {}, "click": {}, "share": {}, "subscribe": {}, "login": {}, "sign": {}, "privacy": {}, "policy": {}, "terms": {},
}

func isNumeric(s string) bool {
	for _, r := range s {
		if !unicode.IsDigit(r) {
			return false
		}
	}
	return len(s) > 0
}

func lightStem(s string) string {
	for _, suf := range []string{"ing", "ers", "er", "ies", "ied", "ly", "ed", "s"} {
		if len(s) > len(suf)+2 && strings.HasSuffix(s, suf) {
			return s[:len(s)-len(suf)]
		}
	}
	return s
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

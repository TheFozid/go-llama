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

// enrichAndSummarize fetches a URL and returns a compact, LLM-optimized summary
// of the main static HTML content (~500–1000 chars). It biases the summary toward
// the given query when provided, helping small LLMs focus on relevant text. If
// anything fails, it returns the provided fallback snippet.
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
// */

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
		mt = "" // be conservative; will check HTML markers below
	}
	if mt != "" && !strings.Contains(mt, "text/html") && !strings.Contains(mt, "application/xhtml+xml") {
		return fallbackSnippet
	}

	// Read up to 2 MiB to keep memory bounded
	const maxRead = 2 << 20
	buf := make([]byte, maxRead)
	n, _ := resp.Body.Read(buf)
	body := buf[:n]

	// If there might be more, keep reading with a cap (avoid partial tags)
	for n < maxRead {
		m, err := resp.Body.Read(buf[n:])
		n += m
		if err != nil || m == 0 {
			break
		}
	}
	body = buf[:n]

	// Decode charset if specified and not utf-8
	decoded := body
	if cs, ok := params["charset"]; ok && !strings.EqualFold(cs, "utf-8") {
		r, err := charset.NewReaderLabel(cs, bytes.NewReader(body))
		if err == nil {
			if b2, err := ioReadAllCap(r, maxRead); err == nil {
				decoded = b2
			}
		}
	}
	// Fallback: try to detect encoding heuristically if mt is HTML-ish
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

	// Parse & extract main content FIRST
	mainText := extractMainContent(html)
	mainText = strings.TrimSpace(compactWhitespace(mainText))

	// If we got good content, use it regardless of framework detection
	if utf8.RuneCountInString(mainText) >= 200 {
		// We have content - proceed with summarization
		// (The framework detection doesn't matter if content exists)
	} else {
		// Only NOW check if it's dynamic (explains why we got no content)
		if looksDynamic(html) {
			// It's a SPA with no server-rendered content
			return fallbackSnippet
		}
		// Not dynamic but still no content - generic failure
		return fallbackSnippet
	}

// Build query tokens for relevance weighting (may be empty)
queryTokens := buildQueryTokensForRanking(query)

// Produce LLM-optimized compressed summary (~500–1000 chars),
// biased toward content overlapping with the query tokens.
	summary := summarizeForLLM(mainText, 1000, queryTokens)
	if summary == "" {
		return fallbackSnippet
	}
	
	// Cache the result
	cacheMu.Lock()
	if len(enrichCache) > 100 { // Simple LRU: clear if too large
		enrichCache = make(map[string]string)
	}
	enrichCache[cacheKey] = summary
	cacheMu.Unlock()
	
	return summary

}

// looksDynamic returns true if HTML likely requires client-side JS rendering.
// The logic is tuned to avoid false positives on pre-rendered static pages
// that use modern front-end wrappers (like BBC, Guardian, etc.).
func looksDynamic(html string) bool {
	// Work only with the first ~120 KB; enough for head+initial body
	headCutoff := 120 * 1024
	if len(html) > headCutoff {
		html = html[:headCutoff]
	}
	lower := strings.ToLower(html)

	// Count <script> tags quickly
	scriptCount := strings.Count(lower, "<script")
	if scriptCount == 0 {
		return false // no scripts → definitely static
	}

	// SPA / dynamic framework indicators
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

	// Hard JS-requires markers
	if strings.Contains(lower, "please enable javascript") ||
		strings.Contains(lower, "requires javascript") ||
		strings.Contains(lower, "enable your browser to view this") {
		return true
	}

	// If we detect multiple framework markers, assume SPA
	if matches >= 2 {
		return true
	}

	// Moderate script density can be fine (analytics, ads, etc.)
	// Only skip if extreme and little visible text.
	textCount := countLetters(lower)
	if scriptCount > 40 && textCount < 1200 {
		return true
	}

	// Avoid penalizing static builds that use an <div id="root"> wrapper
	// unless we also see hydration markers or no readable content.
	if strings.Contains(lower, "id=\"root\"") || strings.Contains(lower, "id='root'") ||
		strings.Contains(lower, "id=\"app\"") || strings.Contains(lower, "id='app'") {
		// Check if there's meaningful text (a few hundred letters)
		if textCount > 1500 {
			return false
		}
		if matches >= 1 {
			return true
		}
	}

	// Check for pre-rendered content markers (likely has real content despite framework)
	if strings.Contains(lower, "article") || 
	   strings.Contains(lower, "<main") ||
	   strings.Count(lower, "<p>") > 5 {
		return false
	}

	// Looks fine — treat as static
	return false
}



// extractMainContent finds the primary content block using text/link-density scoring
// (Readability/Boilerpipe-like), with multiple fallbacks for convoluted layouts.
func extractMainContent(html string) string {
	doc, err := goquery.NewDocumentFromReader(strings.NewReader(html))
	if err != nil {
		return ""
	}

	// Remove obvious non-content elements by tag
	doc.Find("script, style, noscript, iframe, svg, canvas, template, link, meta, form, button, input, select, textarea").Remove()

	// Remove hidden elements (style attrs / ARIA / hidden attr)
	doc.Find("[hidden], [aria-hidden=true], [style*=\"display:none\"], [style*=\"visibility:hidden\"]").Each(func(_ int, s *goquery.Selection) {
		s.Remove()
	})

	// Conservative class/id filters (token-aware; avoid substring collisions like 'ad' in 'education')
	dropTokens := []string{
		"header", "footer", "nav", "aside", "sidebar", "breadcrumb", "menu",
		"cookie", "consent", "banner", "advert", "ads", "promo", "share",
		"social", "subscribe", "signup", "login", "modal", "popup", "newsletter",
		"comments", "comment", "related", "recommend", "sponsored",
		"paywall", "paywall-overlay", "overlay", "lightbox", "toolbar",
	}
	for _, t := range dropTokens {
		selector := fmt.Sprintf("[class~=\"\\b%s\\b\"], [id~=\"\\b%s\\b\"]", t, t)
		doc.Find(selector).Each(func(_ int, s *goquery.Selection) {
			s.Remove()
		})
	}

	// Candidate containers to score
	candidates := doc.Find("article, main, .content, .post, .entry, .article, .post-content, .story, .page-content, section, div")

	type block struct {
		el        *goquery.Selection
		text      string
		lenRunes  int
		linkRatio float64
		score     float64
	}
	var blocks []block

	candidates.Each(func(_ int, s *goquery.Selection) {
		// Skip tiny containers (quick prefilter)
		pCount := s.Find("p").Length()
		if pCount == 0 && s.Children().Length() == 0 {
			return
		}
		txt := nodeVisibleText(s)
		txt = strings.TrimSpace(compactWhitespace(txt))
		if utf8.RuneCountInString(txt) < 150 {
			return
		}
		total := float64(len(txt))
		linkTxt := nodeLinkText(s)
		lr := 0.0
		if total > 0 {
			lr = float64(len(linkTxt)) / total
		}
		score := densityScore(txt, lr)
		blocks = append(blocks, block{
			el:        s,
			text:      txt,
			lenRunes:  utf8.RuneCountInString(txt),
			linkRatio: lr,
			score:     score,
		})
	})

	if len(blocks) == 0 {
		// Coarse fallback: concatenate paragraphs in document order
		return fallbackParagraphs(doc)
	}

	// Pick top K blocks by score and merge nearby siblings to avoid fragmentation
	sort.Slice(blocks, func(i, j int) bool { return blocks[i].score > blocks[j].score })
	topK := 5
	if topK > len(blocks) {
		topK = len(blocks)
	}
	best := blocks[:topK]

	var merged strings.Builder
	
	// Take top 3 blocks to get comprehensive content without massive duplication
	// Don't merge siblings - causes 2-3x text repetition when blocks are adjacent
	seen := map[string]bool{}
	blocksToUse := 3
	if blocksToUse > len(best) {
		blocksToUse = len(best)
	}
	
	for i := 0; i < blocksToUse; i++ {
		b := best[i]
		// Avoid adding exact duplicate blocks
		if seen[b.text] {
			continue
		}
		seen[b.text] = true
		
		merged.WriteString(b.text)
		merged.WriteString(" ")
	}

	out := strings.TrimSpace(compactWhitespace(merged.String()))
	if utf8.RuneCountInString(out) >= 200 {
		return out
	}
	// Final fallback
	return fallbackParagraphs(doc)
}

func summarizeForLLM(text string, targetChars int, queryTokens []string) string {
	if targetChars <= 0 {
		targetChars = 1000  // Increased default since length isn't the priority
	}
	text = normalizeQuotes(compactWhitespace(text))

	// Split into sentences
	sentences := splitSentences(text)
	if len(sentences) == 0 {
		sentences = []string{text}
	}

	// Score each sentence based on relevance and information density
	type scoredSentence struct {
		text  string
		score float64
	}
	
	var scored []scoredSentence
	
	// Prebuild a set for query tokens for fast overlap checks
	qset := map[string]struct{}{}
	for _, qt := range queryTokens {
		qset[qt] = struct{}{}
	}

	// Deduplicate sentences BEFORE scoring to prevent duplicates inflating scores
	seenSentences := make(map[string]bool)

	for _, s := range sentences {
		s = strings.TrimSpace(s)
		if len(s) < 20 {  // Skip very short sentences
			continue
		}
		
		// Skip duplicate sentences (normalize for comparison)
		sNormalized := strings.ToLower(s)
		if seenSentences[sNormalized] {
			continue
		}
		seenSentences[sNormalized] = true
		
		// Extract keywords for scoring
		kws := keywordsFrom(s)
		if len(kws) == 0 {
			continue
		}

		// Base score: information density (numbers, proper nouns, unique terms)
		baseScore := infoDensityScore(s, kws)
		
		// Boost score for query relevance
		overlap := 0
		if len(qset) > 0 {
			for _, kw := range kws {
				if _, ok := qset[kw]; ok {
					overlap++
				}
			}
		}
		
		// Query-relevant sentences get significant boost
		finalScore := baseScore + (3.0 * float64(overlap))
		
		scored = append(scored, scoredSentence{
			text:  s,
			score: finalScore,
		})
	}

	if len(scored) == 0 {
		return ""
	}

	// Sort by score (highest first)
	sort.Slice(scored, func(i, j int) bool {
		return scored[i].score > scored[j].score
	})

	// Build summary by taking top-scored sentences until we hit target length
	var result strings.Builder
	totalChars := 0
	addedSentences := make(map[string]bool) // Safety layer - shouldn't be needed but ensures no duplicates
	
	for _, sent := range scored {
		// Double-check we haven't added this sentence already
		sentNormalized := strings.ToLower(strings.TrimSpace(sent.text))
		if addedSentences[sentNormalized] {
			continue
		}
		
		// Would this sentence fit?
		sentLen := len(sent.text)
		if totalChars > 0 && totalChars+sentLen+1 > targetChars {
			break
		}
		
		// Add sentence
		if totalChars > 0 {
			result.WriteString(" ")
		}
		result.WriteString(sent.text)
		addedSentences[sentNormalized] = true
		totalChars += sentLen + 1
		
		// Stop if we have enough content (at least 3 good sentences or hit target)
		if result.Len() >= int(float64(targetChars)*0.8) && strings.Count(result.String(), ".") >= 3 {
			break
		}
	}

	summary := result.String()
	
	// Fallback if we got nothing useful
	if summary == "" && len(sentences) > 0 {
		// Just take the first few sentences up to target
		return squeezeToChars(strings.Join(sentences[:min(3, len(sentences))], " "), targetChars)
	}

	return summary
}

// buildQueryTokensForRanking turns a raw query string into a set of
// stemmed, de-duplicated tokens for relevance scoring. It explicitly
// strips search operators like "site:example.com" and raw URLs so
// they don't pollute the ranking.
func buildQueryTokensForRanking(query string) []string {
	if query == "" {
		return nil
	}

	// Remove site: filters (e.g. "site:bbc.co.uk") and raw URLs
	q := siteFilterRe.ReplaceAllString(query, " ")
	q = urlFilterRe.ReplaceAllString(q, " ")

	// Normalize whitespace and case
	q = strings.ToLower(strings.TrimSpace(q))
	q = spaceReGlobal.ReplaceAllString(q, " ")

	// Tokenize using the same tokenRe / stemming / stopwords as content
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
	if len(out) == 0 {
		return nil
	}
	return out
}


// -------- helpers (I/O, text scoring, tokenization) --------

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

func nodeVisibleText(sel *goquery.Selection) string {
	// Extract text but avoid alt/title duplication
	// Keep <p>, <li>, <h1..h6>, <blockquote>, <pre>, <code>
	var b strings.Builder
	sel.Find("p, li, h1, h2, h3, h4, h5, h6, blockquote, pre, code").Each(func(_ int, s *goquery.Selection) {
		t := s.Text()
		t = strings.TrimSpace(t)
		if t != "" {
			b.WriteString(t)
			if !strings.HasSuffix(t, ".") && !strings.HasSuffix(t, "!") && !strings.HasSuffix(t, "?") {
				b.WriteString(". ")
			} else {
				b.WriteString(" ")
			}
		}
	})
	// If empty, fallback to the node's full text
	if b.Len() == 0 {
		return sel.Text()
	}
	return b.String()
}

func nodeLinkText(sel *goquery.Selection) string {
	var b strings.Builder
	sel.Find("a").Each(func(_ int, a *goquery.Selection) {
		t := strings.TrimSpace(a.Text())
		if t != "" {
			b.WriteString(t)
			b.WriteString(" ")
		}
	})
	return b.String()
}

func densityScore(text string, linkRatio float64) float64 {
	// Text length (log-scaled), punctuation density, low link ratio
	l := float64(utf8.RuneCountInString(text))
	if l <= 0 {
		return 0
	}
	punct := 0
	for _, r := range text {
		if r == '.' || r == ',' || r == ';' || r == ':' {
			punct++
		}
	}
	pd := float64(punct) / l
	score := (1.0 + 2.2*pd) * (l / (1.0 + 3.0*linkRatio))
	return score
}

func fallbackParagraphs(doc *goquery.Document) string {
	var b strings.Builder
	count := 0
	doc.Find("p").Each(func(_ int, p *goquery.Selection) {
		t := strings.TrimSpace(compactWhitespace(p.Text()))
		if utf8.RuneCountInString(t) >= 40 {
			if count > 0 {
				b.WriteString(" ")
			}
			b.WriteString(t)
			count++
		}
	})
	return b.String()
}

func compactWhitespace(s string) string {
    return spaceReGlobal.ReplaceAllString(s, " ")
}

func normalizeQuotes(s string) string {
	// Normalize various unicode quotes to ASCII
replacer := strings.NewReplacer(
    "’", "'",
    "‘", "'",
    "“", `"`,
    "”", `"`,
    "–", "-",
    "—", "-",
    "…", "...",
)
	return replacer.Replace(s)
}

func splitSentences(s string) []string {
	// Simple splitter respecting common terminators and preserving boundaries
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
	"we": {}, "our": {}, "you": {}, "your": {}, "they": {}, "their": {}, "he": {}, "she": {}, "it’s": {}, "i": {},
	"here": {}, "there": {}, "when": {}, "where": {}, "why": {}, "how": {}, "what": {},
	"article": {}, "read": {}, "click": {}, "share": {}, "subscribe": {}, "login": {}, "sign": {}, "privacy": {}, "policy": {}, "terms": {},
}


func keywordsFrom(s string) []string {
	lower := strings.ToLower(s)
	toks := tokenReGlobal.FindAllString(lower, -1)
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
		// Drop very short tokens unless numeric
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

func infoDensityScore(sent string, kws []string) float64 {
	// Score favors numbers, uppercase acronyms, and unique keywords
	numbers := 0
	acronyms := 0
	for _, r := range sent {
		if unicode.IsDigit(r) {
			numbers++
		}
	}
	acronyms = len(acronymReGlobal.FindAllString(sent, -1))
	return float64(len(kws)) + 1.5*float64(numbers) + 2.0*float64(acronyms)
}

func isNumeric(s string) bool {
	for _, r := range s {
		if !unicode.IsDigit(r) {
			return false
		}
	}
	return len(s) > 0
}

// very light stemmer: drop common English suffixes; safe for LLM compression
func lightStem(s string) string {
	for _, suf := range []string{"ing", "ers", "er", "ies", "ied", "ly", "ed", "s"} {
		if len(s) > len(suf)+2 && strings.HasSuffix(s, suf) {
			return s[:len(s)-len(suf)]
		}
	}
	return s
}

func squeezeToChars(s string, limit int) string {
	if len(s) <= limit {
		return s
	}
	// try to cut on a boundary
	cut := limit
	for cut > 0 && !unicode.IsSpace(rune(s[cut-1])) {
		cut--
	}
	if cut < limit/2 {
		cut = limit
	}
	return strings.TrimSpace(s[:cut]) + "..."
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

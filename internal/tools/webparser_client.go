// internal/tools/webparser_client.go
package tools

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/PuerkitoBio/goquery"
)

// WebParserClient handles HTTP fetching and HTML parsing
type WebParserClient struct {
	httpClient *http.Client
	userAgent  string
	maxSizeMB  int
}

// ParsedContent represents cleaned web page content
type ParsedContent struct {
	URL             string
	Title           string
	CleanText       string
	Headings        []string
	WordCount       int
	EstimatedTokens int
}

// ContentChunk represents a portion of content for pagination
type ContentChunk struct {
	Index   int    // 0-based chunk index
	Text    string // Chunk content
	Tokens  int    // Estimated tokens
	Heading string // Nearest heading before this chunk
}

// PageMetadata represents high-level page information
type PageMetadata struct {
	URL          string
	Title        string
	TotalTokens  int
	TotalChunks  int // At 500 tokens per chunk
	Headings     []string
	BriefSummary string // First 200 chars of clean text
}

// NewWebParserClient creates a new web parser client
func NewWebParserClient(timeout time.Duration, userAgent string, maxSizeMB int) *WebParserClient {
	return &WebParserClient{
		httpClient: &http.Client{
			Timeout: timeout,
			CheckRedirect: func(req *http.Request, via []*http.Request) error {
				if len(via) >= 10 {
					return fmt.Errorf("too many redirects")
				}
				return nil
			},
		},
		userAgent: userAgent,
		maxSizeMB: maxSizeMB,
	}
}

// FetchAndParse fetches a URL and parses it into clean content
func (c *WebParserClient) FetchAndParse(ctx context.Context, url string) (*ParsedContent, error) {
	// Fetch HTML
	html, err := c.fetchHTML(ctx, url)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch URL: %w", err)
	}

	// Parse HTML into clean content
	content, err := c.parseHTML(url, html)
	if err != nil {
		return nil, fmt.Errorf("failed to parse HTML: %w", err)
	}

	return content, nil
}

// fetchHTML retrieves HTML content from a URL
func (c *WebParserClient) fetchHTML(ctx context.Context, url string) (string, error) {
	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return "", fmt.Errorf("failed to create request: %w", err)
	}

    // Use configured User-Agent and set standard browser headers to bypass 403 blocks
    req.Header.Set("User-Agent", c.userAgent)
    req.Header.Set("Accept", "text/html,application/xhtml+xml,application/xml;q=0.9,image/avif,image/webp,image/apng,*/*;q=0.8")
    req.Header.Set("Accept-Language", "en-GB,en;q=0.9")
    req.Header.Set("Accept-Encoding", "gzip, deflate, br")
    req.Header.Set("DNT", "1")
    req.Header.Set("Connection", "keep-alive")
    req.Header.Set("Upgrade-Insecure-Requests", "1")
    req.Header.Set("Sec-Fetch-Dest", "document")
    req.Header.Set("Sec-Fetch-Mode", "navigate")
    req.Header.Set("Sec-Fetch-Site", "none")
    req.Header.Set("Sec-Fetch-User", "?1")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("failed to fetch: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("HTTP %d: %s", resp.StatusCode, resp.Status)
	}

	// Check content type
	contentType := resp.Header.Get("Content-Type")
	if !strings.Contains(contentType, "text/html") && !strings.Contains(contentType, "application/xhtml") {
		return "", fmt.Errorf("unsupported content type: %s", contentType)
	}

	// Read with size limit
	maxBytes := int64(c.maxSizeMB * 1024 * 1024)
	limitedReader := io.LimitReader(resp.Body, maxBytes)

	body, err := io.ReadAll(limitedReader)
	if err != nil {
		return "", fmt.Errorf("failed to read body: %w", err)
	}

	if int64(len(body)) >= maxBytes {
		return "", fmt.Errorf("content exceeds size limit of %dMB", c.maxSizeMB)
	}

	return string(body), nil
}

// parseHTML extracts clean text from HTML
func (c *WebParserClient) parseHTML(url, html string) (*ParsedContent, error) {
	doc, err := goquery.NewDocumentFromReader(strings.NewReader(html))
	if err != nil {
		return nil, fmt.Errorf("failed to parse HTML: %w", err)
	}

	// Extract title
	title := doc.Find("title").First().Text()
	title = strings.TrimSpace(title)

	// Remove unwanted elements
	doc.Find("script, style, nav, aside, footer, header, iframe, noscript").Remove()

	// Extract headings
	headings := []string{}
	doc.Find("h1, h2, h3, h4").Each(func(i int, s *goquery.Selection) {
		heading := strings.TrimSpace(s.Text())
		if heading != "" {
			headings = append(headings, heading)
		}
	})

	// Extract main content (prefer <article> or <main>, fallback to <body>)
	var contentSelection *goquery.Selection
	if article := doc.Find("article").First(); article.Length() > 0 {
		contentSelection = article
	} else if main := doc.Find("main").First(); main.Length() > 0 {
		contentSelection = main
	} else {
		contentSelection = doc.Find("body")
	}

	// Extract text
	cleanText := c.extractText(contentSelection)

	// Calculate metrics
	wordCount := len(strings.Fields(cleanText))
	estimatedTokens := c.EstimateTokens(cleanText)

	return &ParsedContent{
		URL:             url,
		Title:           title,
		CleanText:       cleanText,
		Headings:        headings,
		WordCount:       wordCount,
		EstimatedTokens: estimatedTokens,
	}, nil
}

// extractText recursively extracts text from a selection, preserving structure
func (c *WebParserClient) extractText(sel *goquery.Selection) string {
	var builder strings.Builder

	sel.Contents().Each(func(i int, s *goquery.Selection) {
		nodeName := goquery.NodeName(s)

		switch nodeName {
		case "#text":
			text := s.Text()
			text = strings.TrimSpace(text)
			if text != "" {
				builder.WriteString(text)
				builder.WriteString(" ")
			}

		case "br":
			builder.WriteString("\n")

		case "p", "div", "h1", "h2", "h3", "h4", "h5", "h6", "li", "blockquote":
			// Block elements: extract text and add newlines
			innerText := c.extractText(s)
			if innerText != "" {
				builder.WriteString(strings.TrimSpace(innerText))
				builder.WriteString("\n\n")
			}

		default:
			// Inline elements: extract text recursively
			innerText := c.extractText(s)
			if innerText != "" {
				builder.WriteString(innerText)
			}
		}
	})

	return builder.String()
}

// ExtractMetadata extracts high-level page information
func (c *WebParserClient) ExtractMetadata(content *ParsedContent) *PageMetadata {
	totalChunks := (content.EstimatedTokens + 499) / 500 // Round up, 500 tokens per chunk

	briefSummary := content.CleanText
	if len(briefSummary) > 200 {
		briefSummary = briefSummary[:200] + "..."
	}

	return &PageMetadata{
		URL:          content.URL,
		Title:        content.Title,
		TotalTokens:  content.EstimatedTokens,
		TotalChunks:  totalChunks,
		Headings:     content.Headings,
		BriefSummary: briefSummary,
	}
}

// ChunkContent splits content into manageable chunks at natural boundaries
func (c *WebParserClient) ChunkContent(content *ParsedContent, chunkSizeChars int) []ContentChunk {
	text := content.CleanText
	if len(text) == 0 {
		return []ContentChunk{}
	}

	chunks := []ContentChunk{}
	currentHeading := ""
	headingIndex := 0

	// Track headings and their positions
	headingPositions := c.findHeadingPositions(text, content.Headings)

	for len(text) > 0 {
		// Update current heading if we've passed one
		for headingIndex < len(headingPositions) && headingPositions[headingIndex].Position < len(content.CleanText)-len(text) {
			currentHeading = headingPositions[headingIndex].Text
			headingIndex++
		}

		// Determine chunk size
		chunkSize := chunkSizeChars
		if len(text) <= chunkSize {
			chunkSize = len(text)
		}

		// Find natural break point
		breakPoint := c.findBreakPoint(text, chunkSize)
		chunkText := strings.TrimSpace(text[:breakPoint])

		// Create chunk
		if chunkText != "" {
			chunks = append(chunks, ContentChunk{
				Index:   len(chunks),
				Text:    chunkText,
				Tokens:  c.EstimateTokens(chunkText),
				Heading: currentHeading,
			})
		}

		// Move to next chunk
		text = text[breakPoint:]
	}

	return chunks
}

// findHeadingPositions finds the positions of headings in the text
func (c *WebParserClient) findHeadingPositions(text string, headings []string) []struct {
	Position int
	Text     string
} {
	positions := []struct {
		Position int
		Text     string
	}{}

	for _, heading := range headings {
		pos := strings.Index(text, heading)
		if pos != -1 {
			positions = append(positions, struct {
				Position int
				Text     string
			}{
				Position: pos,
				Text:     heading,
			})
		}
	}

	return positions
}

// findBreakPoint finds the best place to break text near targetSize
func (c *WebParserClient) findBreakPoint(text string, targetSize int) int {
	if len(text) <= targetSize {
		return len(text)
	}

	// Look for break points in order of preference
	searchStart := targetSize - 200 // Look back up to 200 chars
	if searchStart < 0 {
		searchStart = 0
	}

	searchText := text[searchStart:targetSize]

	// Priority 1: Paragraph break (\n\n)
	if idx := strings.LastIndex(searchText, "\n\n"); idx != -1 {
		return searchStart + idx + 2
	}

	// Priority 2: Single newline
	if idx := strings.LastIndex(searchText, "\n"); idx != -1 {
		return searchStart + idx + 1
	}

	// Priority 3: Sentence end (. followed by space)
	if idx := strings.LastIndex(searchText, ". "); idx != -1 {
		return searchStart + idx + 2
	}

	// Priority 4: Any space
	if idx := strings.LastIndex(searchText, " "); idx != -1 {
		return searchStart + idx + 1
	}

	// Fallback: break at target size
	return targetSize
}

// EstimateTokens estimates token count from text length
// Uses ~4 characters per token with 10% buffer
func (c *WebParserClient) EstimateTokens(text string) int {
	if text == "" {
		return 0
	}
	return int(float64(len(text)) / 4.0 * 1.1)
}

// GetChunk retrieves a specific chunk by index
func (c *WebParserClient) GetChunk(content *ParsedContent, chunkIndex int, chunkSizeChars int) (*ContentChunk, error) {
	chunks := c.ChunkContent(content, chunkSizeChars)

	if chunkIndex < 0 || chunkIndex >= len(chunks) {
		return nil, fmt.Errorf("chunk index %d out of range (total chunks: %d)", chunkIndex, len(chunks))
	}

	return &chunks[chunkIndex], nil
}

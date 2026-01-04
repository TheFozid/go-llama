// internal/tools/searxng_client.go
package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"time"
)

// SearXNGClient handles communication with SearXNG search engine
type SearXNGClient struct {
	BaseURL    string
	HTTPClient *http.Client
}

// NewSearXNGClient creates a new SearXNG client
func NewSearXNGClient(baseURL string, timeout time.Duration) *SearXNGClient {
	return &SearXNGClient{
		BaseURL: baseURL,
		HTTPClient: &http.Client{
			Timeout: timeout,
		},
	}
}

// SearchResult represents a single search result from SearXNG
type SearchResult struct {
	Title   string `json:"title"`
	URL     string `json:"url"`
	Content string `json:"content"`
	Engine  string `json:"engine,omitempty"`
	Score   float64 `json:"score,omitempty"`
}

// SearchResponse represents the full response from SearXNG
type SearchResponse struct {
	Query          string         `json:"query"`
	NumberOfResults int           `json:"number_of_results"`
	Results        []SearchResult `json:"results"`
}

// Search performs a search query against SearXNG
func (c *SearXNGClient) Search(ctx context.Context, query string, maxResults int) (*SearchResponse, error) {
	// Build search URL
	u, err := url.Parse(c.BaseURL)
	if err != nil {
		return nil, fmt.Errorf("invalid base URL: %w", err)
	}

	q := u.Query()
	q.Set("q", query)
	q.Set("format", "json")
	u.RawQuery = q.Encode()

	// Create request
	req, err := http.NewRequestWithContext(ctx, "GET", u.String(), nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	// Execute request
	resp, err := c.HTTPClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("search request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("SearXNG returned status %d: %s", resp.StatusCode, string(body))
	}

	// Parse response
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response: %w", err)
	}

	var searxResults struct {
		Query          string `json:"query"`
		NumberOfResults int    `json:"number_of_results"`
		Results        []struct {
			Title   string `json:"title"`
			URL     string `json:"url"`
			Content string `json:"content"`
			Engine  string `json:"engine"`
		} `json:"results"`
	}

	if err := json.Unmarshal(body, &searxResults); err != nil {
		return nil, fmt.Errorf("failed to parse response: %w", err)
	}

	// Convert to our format and limit results
	results := make([]SearchResult, 0)
	limit := maxResults
	if limit <= 0 || limit > len(searxResults.Results) {
		limit = len(searxResults.Results)
	}

	for i := 0; i < limit; i++ {
		r := searxResults.Results[i]
		results = append(results, SearchResult{
			Title:   r.Title,
			URL:     r.URL,
			Content: r.Content,
			Engine:  r.Engine,
		})
	}

	return &SearchResponse{
		Query:          searxResults.Query,
		NumberOfResults: searxResults.NumberOfResults,
		Results:        results,
	}, nil
}

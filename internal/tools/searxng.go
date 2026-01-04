// internal/tools/searxng.go
package tools

import (
	"context"
	"fmt"
	"strings"
	"time"
)

// SearXNGTool implements the Tool interface for web searching
type SearXNGTool struct {
	client *SearXNGClient
	config ToolConfig
}

// NewSearXNGTool creates a new SearXNG search tool
func NewSearXNGTool(baseURL string, config ToolConfig) *SearXNGTool {
	// Use idle timeout for client (longer timeout)
	timeout := config.TimeoutIdle
	if timeout == 0 {
		timeout = 60 * time.Second
	}

	return &SearXNGTool{
		client: NewSearXNGClient(baseURL, timeout),
		config: config,
	}
}

// Name returns the tool identifier
func (t *SearXNGTool) Name() string {
	return ToolNameSearch
}

// Description returns what the tool does
func (t *SearXNGTool) Description() string {
	return "Search the web using SearXNG meta-search engine"
}

// RequiresAuth returns false (no auth needed for search)
func (t *SearXNGTool) RequiresAuth() bool {
	return false
}

// Execute performs a web search
// Expected params:
//   - "query" (string): search query
//   - "max_results" (int, optional): max number of results
//   - "is_interactive" (bool, optional): execution context
func (t *SearXNGTool) Execute(ctx context.Context, params map[string]interface{}) (*ToolResult, error) {
	startTime := time.Now()

	// Extract query parameter
	query, ok := params["query"].(string)
	if !ok || query == "" {
		return &ToolResult{
			Success:  false,
			Error:    "missing or invalid 'query' parameter",
			Duration: time.Since(startTime),
		}, fmt.Errorf("missing query parameter")
	}

	// Determine max results based on context
	maxResults := t.config.MaxResultsIdle
	if isInteractive, ok := params["is_interactive"].(bool); ok && isInteractive {
		maxResults = t.config.MaxResultsInteractive
	}

	// Allow override from params
	if mr, ok := params["max_results"].(int); ok && mr > 0 {
		maxResults = mr
	}

	// Perform search
	response, err := t.client.Search(ctx, query, maxResults)
	if err != nil {
		return &ToolResult{
			Success:  false,
			Error:    err.Error(),
			Duration: time.Since(startTime),
		}, err
	}

	// Format output
	output := t.formatSearchResults(response)

	// Build metadata
	metadata := map[string]interface{}{
		"query":             response.Query,
		"total_results":     response.NumberOfResults,
		"returned_results":  len(response.Results),
		"sources":           t.extractSources(response),
	}

	return &ToolResult{
		Success:  true,
		Output:   output,
		Duration: time.Since(startTime),
		Metadata: metadata,
	}, nil
}

// formatSearchResults creates a readable summary of search results
func (t *SearXNGTool) formatSearchResults(response *SearchResponse) string {
	var builder strings.Builder

	builder.WriteString(fmt.Sprintf("Found %d results for: %s\n\n", 
		len(response.Results), response.Query))

	for i, result := range response.Results {
		builder.WriteString(fmt.Sprintf("[%d] %s\n", i+1, result.Title))
		builder.WriteString(fmt.Sprintf("    URL: %s\n", result.URL))
		
		// Truncate content to ~200 chars
		content := result.Content
		if len(content) > 200 {
			content = content[:200] + "..."
		}
		builder.WriteString(fmt.Sprintf("    %s\n\n", content))
	}

	return builder.String()
}

// extractSources pulls out the URLs for metadata
func (t *SearXNGTool) extractSources(response *SearchResponse) []string {
	sources := make([]string, 0, len(response.Results))
	for _, result := range response.Results {
		sources = append(sources, result.URL)
	}
	return sources
}

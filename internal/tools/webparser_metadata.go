// internal/tools/webparser_metadata.go
package tools

import (
	"context"
	"fmt"
	"strings"
	"time"
)

// WebParserMetadataTool provides lightweight page metadata without full parsing
type WebParserMetadataTool struct {
	client *WebParserClient
	config ToolConfig
}

// NewWebParserMetadataTool creates a new metadata tool
func NewWebParserMetadataTool(userAgent string, config ToolConfig) *WebParserMetadataTool {
	// Use interactive timeout for metadata (should be fast)
	timeout := time.Duration(config.TimeoutInteractive) * time.Second
	if timeout == 0 {
		timeout = 10 * time.Second
	}

	maxSizeMB := 10 // Default from config
	
	return &WebParserMetadataTool{
		client: NewWebParserClient(timeout, userAgent, maxSizeMB),
		config: config,
	}
}

// Name returns the tool identifier
func (t *WebParserMetadataTool) Name() string {
	return "web_parse_metadata"
}

// Description returns what the tool does
func (t *WebParserMetadataTool) Description() string {
	return "Get lightweight metadata about a web page (title, structure, length) without full parsing - fast for decision making"
}

// RequiresAuth returns false (no auth needed)
func (t *WebParserMetadataTool) RequiresAuth() bool {
	return false
}

// Execute retrieves page metadata
// Expected params:
//   - "url" (string): URL to analyze
func (t *WebParserMetadataTool) Execute(ctx context.Context, params map[string]interface{}) (*ToolResult, error) {
	startTime := time.Now()

	// Extract URL parameter
	url, ok := params["url"].(string)
	if !ok || url == "" {
		return &ToolResult{
			Success:  false,
			Error:    "missing or invalid 'url' parameter",
			Duration: time.Since(startTime),
		}, fmt.Errorf("missing url parameter")
	}

	// Validate URL
	if !strings.HasPrefix(url, "http://") && !strings.HasPrefix(url, "https://") {
		return &ToolResult{
			Success:  false,
			Error:    "URL must start with http:// or https://",
			Duration: time.Since(startTime),
		}, fmt.Errorf("invalid URL scheme")
	}

	// Fetch and parse page
	content, err := t.client.FetchAndParse(ctx, url)
	if err != nil {
		return &ToolResult{
			Success:  false,
			Error:    fmt.Sprintf("Failed to fetch page: %v", err),
			Duration: time.Since(startTime),
		}, err
	}

	// Extract metadata
	metadata := t.client.ExtractMetadata(content)

	// Format output
	output := t.formatMetadata(metadata)

	// Build metadata map
	metadataMap := map[string]interface{}{
		"url":          metadata.URL,
		"title":        metadata.Title,
		"total_tokens": metadata.TotalTokens,
		"total_chunks": metadata.TotalChunks,
		"headings":     metadata.Headings,
		"word_count":   content.WordCount,
	}

	return &ToolResult{
		Success:  true,
		Output:   output,
		Duration: time.Since(startTime),
		Metadata: metadataMap,
	}, nil
}

// formatMetadata creates a readable summary of page metadata
func (t *WebParserMetadataTool) formatMetadata(metadata *PageMetadata) string {
	var builder strings.Builder

	builder.WriteString(fmt.Sprintf("=== WEB PAGE METADATA ===\n\n"))
	builder.WriteString(fmt.Sprintf("URL: %s\n", metadata.URL))
	builder.WriteString(fmt.Sprintf("Title: %s\n\n", metadata.Title))

	builder.WriteString(fmt.Sprintf("Size: ~%d tokens (%d chunks at 500 tokens/chunk)\n\n",
		metadata.TotalTokens, metadata.TotalChunks))

	// List headings (structure overview)
	if len(metadata.Headings) > 0 {
		builder.WriteString("Page Structure:\n")
		headingCount := len(metadata.Headings)
		if headingCount > 10 {
			// Show first 10 headings
			for i := 0; i < 10; i++ {
				builder.WriteString(fmt.Sprintf("  %d. %s\n", i+1, metadata.Headings[i]))
			}
			builder.WriteString(fmt.Sprintf("  ... and %d more sections\n", headingCount-10))
		} else {
			for i, heading := range metadata.Headings {
				builder.WriteString(fmt.Sprintf("  %d. %s\n", i+1, heading))
			}
		}
		builder.WriteString("\n")
	}

	// Brief content preview
	builder.WriteString("Content Preview:\n")
	builder.WriteString(fmt.Sprintf("%s\n\n", metadata.BriefSummary))

	// Usage suggestions
	builder.WriteString("Next Steps:\n")
	if metadata.TotalChunks == 1 {
		builder.WriteString("- Use 'web_parse_general' to get full summary (small page)\n")
	} else if metadata.TotalChunks <= 3 {
		builder.WriteString("- Use 'web_parse_general' for full auto-summary\n")
		builder.WriteString("- Or 'web_parse_contextual' with specific focus\n")
	} else {
		builder.WriteString("- Use 'web_parse_contextual' with specific purpose for targeted summary\n")
		builder.WriteString(fmt.Sprintf("- Or 'web_parse_chunked' to read specific chunks (0-%d)\n", metadata.TotalChunks-1))
	}

	return builder.String()
}

// internal/tools/webparser_chunked.go
package tools

import (
	"context"
	"fmt"
	"strings"
	"time"
)

// WebParserChunkedTool provides chunk-by-chunk access to large web pages
type WebParserChunkedTool struct {
	client        *WebParserClient
	config        ToolConfig
	maxPageSizeMB int
	chunkSizeChars int // Default chunk size in characters
}

// NewWebParserChunkedTool creates a new chunked access tool
func NewWebParserChunkedTool(userAgent string, maxPageSizeMB int, chunkSize int, config ToolConfig) *WebParserChunkedTool {
	// Use idle timeout (longer for large page fetching)
	timeout := time.Duration(config.TimeoutIdle) * time.Second
	if timeout == 0 {
		timeout = 240 * time.Second
	}

	if chunkSize == 0 {
		chunkSize = 2000 // Default: ~500 tokens per chunk
	}

	return &WebParserChunkedTool{
		client:         NewWebParserClient(timeout, userAgent, maxPageSizeMB),
		config:         config,
		maxPageSizeMB:  maxPageSizeMB,
		chunkSizeChars: chunkSize,
	}
}

// Name returns the tool identifier
func (t *WebParserChunkedTool) Name() string {
	return "web_parse_chunked"
}

// Description returns what the tool does
func (t *WebParserChunkedTool) Description() string {
	return "Access specific chunks of a web page by index (500 tokens per chunk) - enables reading large documents incrementally"
}

// RequiresAuth returns false (no auth needed)
func (t *WebParserChunkedTool) RequiresAuth() bool {
	return false
}

// Execute retrieves a specific chunk of content
// Expected params:
//   - "url" (string): URL to parse
//   - "chunk_index" (int): Which chunk to retrieve (0-based)
//   - "chunk_size" (int, optional): Custom chunk size in characters (default: 2000)
func (t *WebParserChunkedTool) Execute(ctx context.Context, params map[string]interface{}) (*ToolResult, error) {
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

	// Extract chunk_index parameter
	var chunkIndex int
	switch v := params["chunk_index"].(type) {
	case int:
		chunkIndex = v
	case float64:
		chunkIndex = int(v)
	default:
		return &ToolResult{
			Success:  false,
			Error:    "missing or invalid 'chunk_index' parameter (must be integer)",
			Duration: time.Since(startTime),
		}, fmt.Errorf("missing chunk_index parameter")
	}

	// Optional custom chunk size
	chunkSize := t.chunkSizeChars
	if customSize, ok := params["chunk_size"].(int); ok && customSize > 0 {
		chunkSize = customSize
	} else if customSizeFloat, ok := params["chunk_size"].(float64); ok && customSizeFloat > 0 {
		chunkSize = int(customSizeFloat)
	}

	// Validate URL
	if !strings.HasPrefix(url, "http://") && !strings.HasPrefix(url, "https://") {
		return &ToolResult{
			Success:  false,
			Error:    "URL must start with http:// or https://",
			Duration: time.Since(startTime),
		}, fmt.Errorf("invalid URL scheme")
	}

	// Validate chunk_index
	if chunkIndex < 0 {
		return &ToolResult{
			Success:  false,
			Error:    "chunk_index must be >= 0",
			Duration: time.Since(startTime),
		}, fmt.Errorf("invalid chunk_index")
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

	// Get specific chunk
	chunk, err := t.client.GetChunk(content, chunkIndex, chunkSize)
	if err != nil {
		// Provide helpful error with total chunk count
		chunks := t.client.ChunkContent(content, chunkSize)
		return &ToolResult{
			Success:  false,
			Error:    fmt.Sprintf("Chunk index %d out of range (total chunks: %d, valid range: 0-%d)", chunkIndex, len(chunks), len(chunks)-1),
			Duration: time.Since(startTime),
		}, err
	}

	// Get total chunk count for context
	allChunks := t.client.ChunkContent(content, chunkSize)

	// Format output
	output := t.formatOutput(content, chunk, len(allChunks))

	// Build metadata
	metadata := map[string]interface{}{
		"url":             content.URL,
		"title":           content.Title,
		"chunk_index":     chunkIndex,
		"total_chunks":    len(allChunks),
		"chunk_tokens":    chunk.Tokens,
		"chunk_heading":   chunk.Heading,
		"next_chunk":      chunkIndex + 1,
		"has_more":        chunkIndex+1 < len(allChunks),
		"original_tokens": content.EstimatedTokens,
	}

	return &ToolResult{
		Success:  true,
		Output:   output,
		Duration: time.Since(startTime),
		Metadata: metadata,
	}, nil
}

// formatOutput creates readable chunk output with context
func (t *WebParserChunkedTool) formatOutput(content *ParsedContent, chunk *ContentChunk, totalChunks int) string {
	var builder strings.Builder

	builder.WriteString(fmt.Sprintf("=== CHUNK %d of %d ===\n\n", chunk.Index+1, totalChunks))
	builder.WriteString(fmt.Sprintf("Source: %s\n", content.Title))
	builder.WriteString(fmt.Sprintf("URL: %s\n", content.URL))
	
	if chunk.Heading != "" {
		builder.WriteString(fmt.Sprintf("Section: %s\n", chunk.Heading))
	}
	
	builder.WriteString(fmt.Sprintf("Chunk Size: ~%d tokens\n", chunk.Tokens))
	builder.WriteString(fmt.Sprintf("Progress: %.1f%%\n\n", float64(chunk.Index+1)/float64(totalChunks)*100))

	builder.WriteString("--- CONTENT START ---\n\n")
	builder.WriteString(chunk.Text)
	builder.WriteString("\n\n--- CONTENT END ---\n\n")

	// Navigation hints
	if chunk.Index+1 < totalChunks {
		builder.WriteString(fmt.Sprintf("Next: Use chunk_index=%d to continue reading\n", chunk.Index+1))
	} else {
		builder.WriteString("✓ End of document reached\n")
	}

	return builder.String()
}

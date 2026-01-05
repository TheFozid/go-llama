// internal/tools/webparser_general.go
package tools

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// WebParserGeneralTool provides automatic summarization of web pages
type WebParserGeneralTool struct {
	client           *WebParserClient
	llmURL           string
	llmModel         string
	llmClient        *http.Client
	config           ToolConfig
	maxPageSizeMB    int
}

// NewWebParserGeneralTool creates a new general summarization tool
func NewWebParserGeneralTool(userAgent string, llmURL string, llmModel string, maxPageSizeMB int, config ToolConfig) *WebParserGeneralTool {
	// Use context-appropriate timeout
	timeout := time.Duration(config.TimeoutIdle) * time.Second
	if timeout == 0 {
		timeout = 30 * time.Second
	}

	return &WebParserGeneralTool{
		client:        NewWebParserClient(timeout, userAgent, maxPageSizeMB),
		llmURL:        llmURL,
		llmModel:      llmModel,
		llmClient:     &http.Client{Timeout: 60 * time.Second},
		config:        config,
		maxPageSizeMB: maxPageSizeMB,
	}
}

// Name returns the tool identifier
func (t *WebParserGeneralTool) Name() string {
	return "web_parse_general"
}

// Description returns what the tool does
func (t *WebParserGeneralTool) Description() string {
	return "Parse and automatically summarize a web page into key points (500 token summary) - good for quick fact-checking"
}

// RequiresAuth returns false (no auth needed)
func (t *WebParserGeneralTool) RequiresAuth() bool {
	return false
}

// Execute parses and summarizes a web page
// Expected params:
//   - "url" (string): URL to parse and summarize
func (t *WebParserGeneralTool) Execute(ctx context.Context, params map[string]interface{}) (*ToolResult, error) {
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

	// Check if page is too large for general summary
	if content.EstimatedTokens > 10000 {
		return &ToolResult{
			Success:  false,
			Error:    fmt.Sprintf("Page too large (%d tokens). Use 'web_parse_contextual' with specific focus or 'web_parse_chunked' for selective reading", content.EstimatedTokens),
			Duration: time.Since(startTime),
		}, fmt.Errorf("page too large for general summary")
	}

	// Generate summary using LLM
	summary, tokensUsed, err := t.summarizeContent(ctx, content)
	if err != nil {
		return &ToolResult{
			Success:  false,
			Error:    fmt.Sprintf("Failed to generate summary: %v", err),
			Duration: time.Since(startTime),
		}, err
	}

	// Format output
	output := t.formatOutput(content, summary)

	// Build metadata
	metadata := map[string]interface{}{
		"url":           content.URL,
		"title":         content.Title,
		"original_size": content.EstimatedTokens,
		"summary_size":  t.client.EstimateTokens(summary),
		"compression":   fmt.Sprintf("%.1f%%", float64(t.client.EstimateTokens(summary))/float64(content.EstimatedTokens)*100),
	}

	return &ToolResult{
		Success:    true,
		Output:     output,
		Duration:   time.Since(startTime),
		TokensUsed: tokensUsed,
		Metadata:   metadata,
	}, nil
}

// summarizeContent generates a summary using the compression LLM
func (t *WebParserGeneralTool) summarizeContent(ctx context.Context, content *ParsedContent) (string, int, error) {
	// Build prompt
	prompt := fmt.Sprintf(`Summarize this web page in 2-3 clear paragraphs (maximum 500 tokens).

Title: %s
URL: %s

Content:
%s

Provide:
1. Main topic/purpose (1-2 sentences)
2. Key points (3-5 bullet points)
3. Important details or conclusions (1 paragraph)

Focus on factual information. Be concise and accurate.`, 
		content.Title, 
		content.URL, 
		t.truncateForPrompt(content.CleanText, 8000))

	reqBody := map[string]interface{}{
		"model": t.llmModel,
		"messages": []map[string]string{
			{
				"role":    "system",
				"content": "You are a web content summarizer. Extract key information concisely and accurately.",
			},
			{
				"role":    "user",
				"content": prompt,
			},
		},
		"stream":      false,
		"temperature": 0.3, // Low temperature for consistent, factual summaries
	}

	jsonData, err := json.Marshal(reqBody)
	if err != nil {
		return "", 0, fmt.Errorf("failed to marshal request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, "POST", t.llmURL, bytes.NewBuffer(jsonData))
	if err != nil {
		return "", 0, fmt.Errorf("failed to create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := t.llmClient.Do(req)
	if err != nil {
		return "", 0, fmt.Errorf("failed to send request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return "", 0, fmt.Errorf("LLM returned status %d: %s", resp.StatusCode, string(body))
	}

	var result struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
		Usage struct {
			TotalTokens int `json:"total_tokens"`
		} `json:"usage"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return "", 0, fmt.Errorf("failed to decode response: %w", err)
	}

	if len(result.Choices) == 0 {
		return "", 0, fmt.Errorf("no choices returned from LLM")
	}

	summary := strings.TrimSpace(result.Choices[0].Message.Content)
	tokensUsed := result.Usage.TotalTokens

	return summary, tokensUsed, nil
}

// truncateForPrompt limits content size for LLM prompt
func (t *WebParserGeneralTool) truncateForPrompt(text string, maxChars int) string {
	if len(text) <= maxChars {
		return text
	}
	return text[:maxChars] + "\n\n[Content truncated for summarization...]"
}

// formatOutput creates a readable summary output
func (t *WebParserGeneralTool) formatOutput(content *ParsedContent, summary string) string {
	var builder strings.Builder

	builder.WriteString(fmt.Sprintf("=== WEB PAGE SUMMARY ===\n\n"))
	builder.WriteString(fmt.Sprintf("Title: %s\n", content.Title))
	builder.WriteString(fmt.Sprintf("URL: %s\n", content.URL))
	builder.WriteString(fmt.Sprintf("Original Size: ~%d tokens\n\n", content.EstimatedTokens))

	builder.WriteString("Summary:\n")
	builder.WriteString(summary)
	builder.WriteString("\n")

	return builder.String()
}

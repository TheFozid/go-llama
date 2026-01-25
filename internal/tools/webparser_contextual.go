// internal/tools/webparser_contextual.go
package tools

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"
	"time"

    "go-llama/internal/config"
)

// WebParserContextualTool provides purpose-driven summarization of web pages
type WebParserContextualTool struct {
	client           *WebParserClient
	llmURL           string
	llmModel         string
	llmClient        *http.Client
	config           ToolConfig
	maxPageSizeMB    int
}

// NewWebParserContextualTool creates a new contextual summarization tool
func NewWebParserContextualTool(userAgent string, llmURL string, llmModel string, maxPageSizeMB int, config ToolConfig) *WebParserContextualTool {
	// Use idle timeout (longer for thorough extraction)
	timeout := time.Duration(config.TimeoutIdle) * time.Second
	if timeout == 0 {
		timeout = 241 * time.Second
	}

	// LLM timeout should be 80% of tool timeout to allow cleanup time
	llmTimeout := timeout * 8 / 10
	if llmTimeout < 360*time.Second {
		llmTimeout = 360 * time.Second // Minimum 2 minutes for contextual analysis
	}
	
	// Configure HTTP transport for better timeout handling
	transport := &http.Transport{
		MaxIdleConns:          10,
		IdleConnTimeout:       90 * time.Second,
		TLSHandshakeTimeout:   10 * time.Second,
		ResponseHeaderTimeout: 360 * time.Second, // Fail fast if LLM doesn't start responding
		DisableKeepAlives:     false,
	}
	
	return &WebParserContextualTool{
		client:        NewWebParserClient(timeout, userAgent, maxPageSizeMB),
		llmURL:        llmURL,
		llmModel:      llmModel,
		llmClient:     &http.Client{
			Timeout:   llmTimeout,
			Transport: transport,
		},
		config:        config,
		maxPageSizeMB: maxPageSizeMB,
	}
}

// Name returns the tool identifier
func (t *WebParserContextualTool) Name() string {
	return "web_parse_contextual"
}

// Description returns what the tool does
func (t *WebParserContextualTool) Description() string {
	return "Parse web page and extract information relevant to a specific purpose (1500 token targeted summary) - best for goal-driven research"
}

// RequiresAuth returns false (no auth needed)
func (t *WebParserContextualTool) RequiresAuth() bool {
	return false
}

// Execute parses a page and extracts contextually relevant information
// Expected params:
//   - "url" (string): URL to parse
//   - "purpose" (string): What you're looking for / why you need this info
//   - "focus_areas" (string, optional): Specific topics or sections to prioritize
func (t *WebParserContextualTool) Execute(ctx context.Context, params map[string]interface{}) (*ToolResult, error) {
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

	// Extract purpose parameter
	purpose, ok := params["purpose"].(string)
	if !ok || purpose == "" {
		return &ToolResult{
			Success:  false,
			Error:    "missing or invalid 'purpose' parameter - explain what information you need and why",
			Duration: time.Since(startTime),
		}, fmt.Errorf("missing purpose parameter")
	}

	// Optional: focus areas
	focusAreas, _ := params["focus_areas"].(string)

	// Validate URL
	if !strings.HasPrefix(url, "http://") && !strings.HasPrefix(url, "https://") {
		return &ToolResult{
			Success:  false,
			Error:    "URL must start with http:// or https://",
			Duration: time.Since(startTime),
		}, fmt.Errorf("invalid URL scheme")
	}
	
	// Check for known problematic URL patterns
	urlLower := strings.ToLower(url)
	if strings.HasSuffix(urlLower, ".pdf") {
		return &ToolResult{
			Success:  false,
			Error:    "Cannot parse PDF files - please search for HTML content instead",
			Duration: time.Since(startTime),
		}, fmt.Errorf("PDF files not supported")
	}
	
	if strings.Contains(urlLower, "/login") || 
	   strings.Contains(urlLower, "/signin") ||
	   strings.Contains(urlLower, "/subscribe") {
		return &ToolResult{
			Success:  false,
			Error:    "URL appears to require authentication - skipping",
			Duration: time.Since(startTime),
		}, fmt.Errorf("authentication required")
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

	// Check if page is excessively large
	if content.EstimatedTokens > 20000 {
		return &ToolResult{
			Success:  false,
			Error:    fmt.Sprintf("Page too large (%d tokens). Use 'web_parse_chunked' to read specific sections, or use 'web_parse_metadata' to see structure first", content.EstimatedTokens),
			Duration: time.Since(startTime),
		}, fmt.Errorf("page too large for contextual summary")
	}

// Check if context is already cancelled before expensive LLM call
	if ctx.Err() != nil {
		return &ToolResult{
			Success:  false,
			Error:    fmt.Sprintf("Operation cancelled before extraction: %v", ctx.Err()),
			Duration: time.Since(startTime),
		}, ctx.Err()
	}
	
	// Generate contextual summary using LLM
	log.Printf("[WebParser] Beginning LLM extraction (page size: %d tokens)", content.EstimatedTokens)
	summary, tokensUsed, err := t.extractContextualInfo(ctx, content, purpose, focusAreas)
	if err != nil {
		// Check if this was a timeout
		errMsg := fmt.Sprintf("Failed to extract information: %v", err)
		if ctx.Err() == context.DeadlineExceeded {
			errMsg = fmt.Sprintf("Extraction timed out after %s: %v", time.Since(startTime), err)
		}
		
		return &ToolResult{
			Success:  false,
			Error:    errMsg,
			Duration: time.Since(startTime),
		}, err
	}

	// Format output
	output := t.formatOutput(content, purpose, summary)

	// Build metadata
	metadata := map[string]interface{}{
		"url":           content.URL,
		"title":         content.Title,
		"purpose":       purpose,
		"original_size": content.EstimatedTokens,
		"extract_size":  t.client.EstimateTokens(summary),
	}

	return &ToolResult{
		Success:    true,
		Output:     output,
		Duration:   time.Since(startTime),
		TokensUsed: tokensUsed,
		Metadata:   metadata,
	}, nil
}

// extractContextualInfo uses LLM to extract relevant information based on purpose
func (t *WebParserContextualTool) extractContextualInfo(ctx context.Context, content *ParsedContent, purpose string, focusAreas string) (string, int, error) {
	log.Printf("[WebParser] Starting contextual extraction (URL: %s, purpose: %s)", 
		content.URL, truncateString(purpose, 60))
	startTime := time.Now()
	
	// Build contextual prompt
	var promptBuilder strings.Builder
	
	promptBuilder.WriteString(fmt.Sprintf("Extract information from this web page relevant to the following purpose:\n\n"))
	promptBuilder.WriteString(fmt.Sprintf("PURPOSE: %s\n\n", purpose))
	
	if focusAreas != "" {
		promptBuilder.WriteString(fmt.Sprintf("FOCUS ON: %s\n\n", focusAreas))
	}
	
	promptBuilder.WriteString(fmt.Sprintf("PAGE TITLE: %s\n", content.Title))
	promptBuilder.WriteString(fmt.Sprintf("PAGE URL: %s\n\n", content.URL))
	
	// Include headings for context
	if len(content.Headings) > 0 {
		promptBuilder.WriteString("PAGE STRUCTURE:\n")
		for i, heading := range content.Headings {
			promptBuilder.WriteString(fmt.Sprintf("  %d. %s\n", i+1, heading))
			if i >= 9 { // Limit headings in prompt
				promptBuilder.WriteString(fmt.Sprintf("  ... and %d more sections\n", len(content.Headings)-10))
				break
			}
		}
		promptBuilder.WriteString("\n")
	}
	
	promptBuilder.WriteString("CONTENT:\n")
	promptBuilder.WriteString(t.truncateForPrompt(content.CleanText, 12000))
	promptBuilder.WriteString("\n\n")
	
	promptBuilder.WriteString("Extract the following:\n")
	promptBuilder.WriteString("1. All facts, data, and claims directly relevant to the purpose\n")
	promptBuilder.WriteString("2. Supporting evidence or examples\n")
	promptBuilder.WriteString("3. Any contradictions or alternative viewpoints mentioned\n")
	promptBuilder.WriteString("4. Key technical details or methodologies if applicable\n")
	promptBuilder.WriteString("5. Conclusions or implications related to the purpose\n\n")
	promptBuilder.WriteString("Maximum 1500 tokens. Be thorough but focused. If information isn't present, say so clearly.")

	prompt := promptBuilder.String()

	reqBody := map[string]interface{}{
		"model": t.llmModel,
		"messages": []map[string]string{
			{
				"role":    "system",
				"content": "You are a targeted information extractor. Extract only information relevant to the user's stated purpose. Be precise, thorough, and cite specific claims from the source.",
			},
			{
				"role":    "user",
				"content": prompt,
			},
		},
		"stream":      false,
		"temperature": 0.3, // Low temperature for accurate extraction
	}

    jsonData, err := json.Marshal(reqBody)
    if err != nil {
        return "", 0, fmt.Errorf("failed to marshal request: %w", err)
    }

req, err := http.NewRequestWithContext(ctx, "POST", config.GetChatURL(t.llmURL), bytes.NewBuffer(jsonData))
	if err != nil {
		return "", 0, fmt.Errorf("failed to create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	log.Printf("[WebParser] Sending LLM request (timeout: %s)", t.llmClient.Timeout)
	llmStartTime := time.Now()
	
	resp, err := t.llmClient.Do(req)
	if err != nil {
		elapsed := time.Since(llmStartTime)
		if ctx.Err() == context.DeadlineExceeded {
			log.Printf("[WebParser] LLM request timed out after %s", elapsed)
			return "", 0, fmt.Errorf("LLM timeout after %s: %w", elapsed, err)
		}
		log.Printf("[WebParser] LLM request failed after %s: %v", elapsed, err)
		return "", 0, fmt.Errorf("failed to send request: %w", err)
	}
	defer resp.Body.Close()
	
	log.Printf("[WebParser] LLM response received in %s", time.Since(llmStartTime))

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

extracted := strings.TrimSpace(result.Choices[0].Message.Content)
	tokensUsed := result.Usage.TotalTokens

	totalDuration := time.Since(startTime)
	log.Printf("[WebParser] Contextual extraction complete in %s (%d tokens)", 
		totalDuration, tokensUsed)

	return extracted, tokensUsed, nil
}

// truncateString helper for logging
func truncateString(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "..."
}

// truncateForPrompt limits content size for LLM prompt
func (t *WebParserContextualTool) truncateForPrompt(text string, maxChars int) string {
	if len(text) <= maxChars {
		return text
	}
	return text[:maxChars] + "\n\n[Content truncated - use 'web_parse_chunked' for full access]"
}

// formatOutput creates a readable extraction output
func (t *WebParserContextualTool) formatOutput(content *ParsedContent, purpose string, extraction string) string {
	var builder strings.Builder

	builder.WriteString(fmt.Sprintf("=== CONTEXTUAL EXTRACTION ===\n\n"))
	builder.WriteString(fmt.Sprintf("Source: %s\n", content.Title))
	builder.WriteString(fmt.Sprintf("URL: %s\n", content.URL))
	builder.WriteString(fmt.Sprintf("Purpose: %s\n\n", purpose))

	builder.WriteString("Extracted Information:\n")
	builder.WriteString(extraction)
	builder.WriteString("\n")

	return builder.String()
}

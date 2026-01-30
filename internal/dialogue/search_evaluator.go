package dialogue

import (
	"context"
	"fmt"
	"log"
	"strings"
)

// SearchEvaluation represents the LLM's evaluation of search results
type SearchEvaluation struct {
	BestURL       string   `json:"best_url"`
	Reasoning     string   `json:"reasoning"`
	FallbackURLs  []string `json:"fallback_urls"`
	SkippedURLs   []string `json:"skipped_urls"`
	Confidence    float64  `json:"confidence"`
	ShouldProceed bool     `json:"should_proceed"`
}

// evaluateSearchResults uses LLM to analyze search results and select best URLs
func (e *Engine) evaluateSearchResults(ctx context.Context, searchOutput string, goalDescription string) (*SearchEvaluation, error) {
	// Extract URLs and metadata from search output
	urls := extractURLsFromSearchResults(searchOutput)
	
	if len(urls) == 0 {
		return nil, fmt.Errorf("no URLs found in search results")
	}
	
	// Build prompt for LLM evaluation
	prompt := e.buildSearchEvaluationPrompt(searchOutput, goalDescription, urls)
	
	// Call LLM via queue with S-expression response
	log.Printf("[SearchEval] Requesting LLM evaluation of %d search results", len(urls))
	response, tokens, err := e.callLLMWithStructuredReasoning(ctx, prompt, false, "")
	if err != nil {
		return nil, fmt.Errorf("LLM evaluation failed: %w", err)
	}
	
	log.Printf("[SearchEval] LLM evaluation completed (%d tokens)", tokens)
	
	// Parse S-expression response
	evaluation, err := e.parseSearchEvaluation(response.RawResponse)
	if err != nil {
		log.Printf("[SearchEval] Failed to parse evaluation, using fallback: %v", err)
		// Fallback: use first URL
		return &SearchEvaluation{
			BestURL:       urls[0],
			Reasoning:     "Failed to parse LLM evaluation, using first result",
			FallbackURLs:  urls[1:],
			Confidence:    0.5,
			ShouldProceed: true,
		}, nil
	}
	
	log.Printf("[SearchEval] Selected: %s (confidence: %.2f)", 
		truncate(evaluation.BestURL, 60), evaluation.Confidence)
	log.Printf("[SearchEval] Reasoning: %s", truncate(evaluation.Reasoning, 100))
	
	return evaluation, nil
}

// buildSearchEvaluationPrompt creates the LLM prompt for search evaluation
func (e *Engine) buildSearchEvaluationPrompt(searchOutput string, goalDescription string, urls []string) string {
	var prompt strings.Builder
	
	prompt.WriteString("Evaluate these search results and select the best URL to parse.\n\n")
	
	prompt.WriteString(fmt.Sprintf("GOAL: %s\n\n", goalDescription))
	
	prompt.WriteString("SEARCH RESULTS:\n")
	prompt.WriteString(searchOutput)
	prompt.WriteString("\n\n")
	
	prompt.WriteString("EVALUATION CRITERIA:\n")
	prompt.WriteString("1. Relevance to goal\n")
	prompt.WriteString("2. Source quality and authority\n")
	prompt.WriteString("3. Content accessibility (no PDFs, login pages, or paywalls)\n")
	prompt.WriteString("4. Likely to contain actionable information\n\n")
	
	prompt.WriteString("CRITICAL: Respond ONLY with this S-expression format:\n\n")
	prompt.WriteString("(search_evaluation\n")
	prompt.WriteString("  (best_url \"URL of top choice\")\n")
	prompt.WriteString("  (reasoning \"Why this URL is best - be specific\")\n")
	prompt.WriteString("  (fallback_urls \"URL2\" \"URL3\")  ; 2-3 backup options\n")
	prompt.WriteString("  (skipped_urls \"BadURL1\" \"BadURL2\")  ; PDFs, logins, etc\n")
	prompt.WriteString("  (confidence 0.85)  ; 0.0-1.0 confidence in recommendation\n")
	prompt.WriteString("  (should_proceed true))  ; false if NO good URLs found\n\n")
	
	prompt.WriteString("RULES:\n")
	prompt.WriteString("- Skip PDFs, login pages, paywalls, social media\n")
	prompt.WriteString("- Prefer .edu, .gov, .org, research journals, technical docs\n")
	prompt.WriteString("- If ALL URLs are bad, set should_proceed to false\n")
	prompt.WriteString("- Output ONLY the S-expression, no explanations\n")
	
	return prompt.String()
}

// parseSearchEvaluation extracts evaluation from S-expression response
func (e *Engine) parseSearchEvaluation(rawResponse string) (*SearchEvaluation, error) {
	// Clean up response
	content := strings.TrimSpace(rawResponse)
	content = strings.TrimPrefix(content, "```lisp")
	content = strings.TrimPrefix(content, "```")
	content = strings.TrimSuffix(content, "```")
	content = strings.TrimSpace(content)
	
	// Find search_evaluation block
	evalBlocks := findBlocks(content, "search_evaluation")
	if len(evalBlocks) == 0 {
		// Try without underscore (search-evaluation)
		evalBlocks = findBlocks(content, "search-evaluation")
	}
	
	if len(evalBlocks) == 0 {
		return nil, fmt.Errorf("no search_evaluation block found")
	}
	
	block := evalBlocks[0]
	
	evaluation := &SearchEvaluation{
		FallbackURLs: []string{},
		SkippedURLs:  []string{},
		Confidence:   0.7, // Default
		ShouldProceed: true,
	}
	
	// Extract best_url
	if url := extractFieldContent(block, "best_url"); url != "" {
		evaluation.BestURL = url
	} else if url := extractFieldContent(block, "best-url"); url != "" {
		evaluation.BestURL = url
	}
	
	// Extract reasoning
	if reasoning := extractFieldContent(block, "reasoning"); reasoning != "" {
		evaluation.Reasoning = reasoning
	}
	
	// Extract fallback_urls (list)
	fallbacks := extractListField(block, "fallback_urls")
	if len(fallbacks) == 0 {
		fallbacks = extractListField(block, "fallback-urls")
	}
	evaluation.FallbackURLs = fallbacks
	
	// Extract skipped_urls (list)
	skipped := extractListField(block, "skipped_urls")
	if len(skipped) == 0 {
		skipped = extractListField(block, "skipped-urls")
	}
	evaluation.SkippedURLs = skipped
	
	// Extract confidence
	if confStr := extractFieldContent(block, "confidence"); confStr != "" {
		if conf, err := parseFloat(confStr); err == nil {
			evaluation.Confidence = conf
		}
	}
	
	// Extract should_proceed
	if proceedStr := extractFieldContent(block, "should_proceed"); proceedStr != "" {
		evaluation.ShouldProceed = (proceedStr == "true" || proceedStr == "t")
	} else if proceedStr := extractFieldContent(block, "should-proceed"); proceedStr != "" {
		evaluation.ShouldProceed = (proceedStr == "true" || proceedStr == "t")
	}
	
	// Validate
	if evaluation.BestURL == "" && evaluation.ShouldProceed {
		return nil, fmt.Errorf("no best_url specified but should_proceed is true")
	}
	
	return evaluation, nil
}

// extractListField extracts a list of strings from an S-expression field
// Example: (fallback_urls "url1" "url2" "url3") -> ["url1", "url2", "url3"]
func extractListField(input, fieldName string) []string {
	pattern := "(" + fieldName + " "
	start := strings.Index(input, pattern)
	if start == -1 {
		return []string{}
	}
	
	start += len(pattern)
	
	// Find the closing paren for this field
	depth := 1
	end := -1
	for i := start; i < len(input); i++ {
		if input[i] == '(' {
			depth++
		} else if input[i] == ')' {
			depth--
			if depth == 0 {
				end = i
				break
			}
		}
	}
	
	if end == -1 {
		return []string{}
	}
	
	listContent := input[start:end]
	
	// Extract quoted strings
	var items []string
	pos := 0
	for pos < len(listContent) {
		// Skip whitespace
		for pos < len(listContent) && (listContent[pos] == ' ' || listContent[pos] == '\n' || listContent[pos] == '\t') {
			pos++
		}
		
		if pos >= len(listContent) {
			break
		}
		
		// Check for quoted string
		if listContent[pos] == '"' {
			pos++ // Skip opening quote
			start := pos
			
			// Find closing quote (handle escapes)
			escaped := false
			for pos < len(listContent) {
				if escaped {
					escaped = false
					pos++
					continue
				}
				if listContent[pos] == '\\' {
					escaped = true
					pos++
					continue
				}
				if listContent[pos] == '"' {
					break
				}
				pos++
			}
			
			if pos < len(listContent) {
				item := listContent[start:pos]
				items = append(items, item)
				pos++ // Skip closing quote
			}
		} else {
			// Unquoted item (read until space or close paren)
			start := pos
			for pos < len(listContent) && listContent[pos] != ' ' && listContent[pos] != ')' {
				pos++
			}
			item := listContent[start:pos]
			if item != "" {
				items = append(items, item)
			}
		}
	}
	
	return items
}

// parseFloat safely parses a float from string
func parseFloat(s string) (float64, error) {
	var f float64
	_, err := fmt.Sscanf(s, "%f", &f)
	return f, err
}

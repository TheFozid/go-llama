package dialogue

import (
	"context"
	"fmt"
	"log"
	"strings"
)

// ParseEvaluation represents the LLM's evaluation of parsed content quality
type ParseEvaluation struct {
	Quality        string   `json:"quality"`         // "sufficient" | "try_fallback" | "parse_deeper" | "completely_failed"
	Reasoning      string   `json:"reasoning"`       // Why this quality rating
	Confidence     float64  `json:"confidence"`      // 0.0-1.0 confidence in evaluation
	MissingInfo    []string `json:"missing_info"`    // What information is still needed
	NextAction     string   `json:"next_action"`     // Recommended next step
	ShouldContinue bool     `json:"should_continue"` // Continue pursuing this goal?
	UsefulContent  string   `json:"useful_content"`  // Brief summary of what WAS useful
}

// evaluateParseResults uses LLM to determine if parsed content helps achieve the goal
func (e *Engine) evaluateParseResults(
	ctx context.Context,
	parseOutput string,
	goalDescription string,
	parsedURL string,
	fallbackURLs []string,
) (*ParseEvaluation, error) {
	
	// Quick validation checks before LLM call
	if len(parseOutput) < 50 {
		return &ParseEvaluation{
			Quality:        "completely_failed",
			Reasoning:      "Parse output too short (< 50 chars) - likely failed to extract content",
			Confidence:     0.95,
			MissingInfo:    []string{"substantial content from the page"},
			NextAction:     "try_fallback",
			ShouldContinue: len(fallbackURLs) > 0,
			UsefulContent:  "",
		}, nil
	}
	
	// Check for common error patterns
	outputLower := strings.ToLower(parseOutput)
	if strings.Contains(outputLower, "403 forbidden") ||
	   strings.Contains(outputLower, "404 not found") ||
	   strings.Contains(outputLower, "access denied") ||
	   strings.Contains(outputLower, "authentication required") {
		return &ParseEvaluation{
			Quality:        "completely_failed",
			Reasoning:      "Parse failed due to access restrictions (403/404/auth required)",
			Confidence:     0.98,
			MissingInfo:    []string{"accessible content from a valid source"},
			NextAction:     "try_fallback",
			ShouldContinue: len(fallbackURLs) > 0,
			UsefulContent:  "",
		}, nil
	}
	
	// Build evaluation prompt
	prompt := e.buildParseEvaluationPrompt(parseOutput, goalDescription, parsedURL, fallbackURLs)
	
	// Call LLM with structured response
	log.Printf("[ParseEval] Requesting LLM evaluation of parse results (goal: %s)", 
		truncate(goalDescription, 60))
	
	response, tokens, err := e.callLLMWithStructuredReasoning(ctx, prompt, false, "")
	if err != nil {
		log.Printf("[ParseEval] LLM evaluation failed: %v", err)
		// Fallback to conservative evaluation
		return &ParseEvaluation{
			Quality:        "sufficient",
			Reasoning:      "LLM evaluation failed, assuming parse is sufficient",
			Confidence:     0.5,
			MissingInfo:    []string{},
			NextAction:     "continue",
			ShouldContinue: true,
			UsefulContent:  truncate(parseOutput, 200),
		}, nil
	}
	
	log.Printf("[ParseEval] LLM evaluation completed (%d tokens)", tokens)
	
	// Parse S-expression response
	evaluation, err := e.parseParseEvaluation(response.RawResponse)
	if err != nil {
		log.Printf("[ParseEval] Failed to parse evaluation, using fallback: %v", err)
		// Fallback: assume content is sufficient if it's reasonably long
		quality := "sufficient"
		if len(parseOutput) < 200 {
			quality = "try_fallback"
		}
		
		return &ParseEvaluation{
			Quality:        quality,
			Reasoning:      "Failed to parse LLM evaluation, using heuristic",
			Confidence:     0.5,
			MissingInfo:    []string{},
			NextAction:     "continue",
			ShouldContinue: true,
			UsefulContent:  truncate(parseOutput, 200),
		}, nil
	}
	
	log.Printf("[ParseEval] Quality: %s (confidence: %.2f)", evaluation.Quality, evaluation.Confidence)
	log.Printf("[ParseEval] Reasoning: %s", truncate(evaluation.Reasoning, 100))
	if len(evaluation.MissingInfo) > 0 {
		log.Printf("[ParseEval] Missing info: %v", evaluation.MissingInfo)
	}
	
	return evaluation, nil
}

// buildParseEvaluationPrompt creates the LLM prompt for parse evaluation
func (e *Engine) buildParseEvaluationPrompt(
    parseOutput string,
    goalDescription string,
    parsedURL string,
    fallbackURLs []string,
) string {
    var prompt strings.Builder
    
    // 1. FORMAT INSTRUCTIONS & PRIMING (Moved to top for better compliance)
    prompt.WriteString("You are an evaluation engine. You must assess if provided content achieves a specific goal.\n\n")
    
    prompt.WriteString("OUTPUT REQUIREMENT:\n")
    prompt.WriteString("You MUST respond with ONLY a valid S-expression. Do not include markdown, conversational text, or explanations.\n\n")
    
    prompt.WriteString("EXAMPLE OUTPUT 1 (Good content):\n")
    prompt.WriteString("(parse_evaluation\n")
    prompt.WriteString("  (quality \"sufficient\")\n")
    prompt.WriteString("  (reasoning \"Content contains specific data points requested.\")\n")
    prompt.WriteString("  (confidence 0.9)\n")
    prompt.WriteString("  (missing_info)\n") // Explicitly showing empty list
    prompt.WriteString("  (next_action \"continue\")\n")
    prompt.WriteString("  (should_continue true)\n")
    prompt.WriteString("  (useful_content \"Found the required API endpoints.\"))\n\n")
    
    prompt.WriteString("EXAMPLE OUTPUT 2 (Missing info):\n")
    prompt.WriteString("(parse_evaluation\n")
    prompt.WriteString("  (quality \"try_fallback\")\n")
    prompt.WriteString("  (reasoning \"Page is a login gate, no data accessible.\")\n")
    prompt.WriteString("  (confidence 0.95)\n")
    prompt.WriteString("  (missing_info \"accessible data\" \"public statistics\")\n")
    prompt.WriteString("  (next_action \"try_fallback\")\n")
    prompt.WriteString("  (should_continue true)\n")
    prompt.WriteString("  (useful_content \"\"))\n\n")
    
    // 2. TASK CONTEXT
    prompt.WriteString(fmt.Sprintf("GOAL: %s\n", goalDescription))
    prompt.WriteString(fmt.Sprintf("SOURCE URL: %s\n", parsedURL))
    
    if len(fallbackURLs) > 0 {
        prompt.WriteString(fmt.Sprintf("FALLBACK AVAILABLE: Yes (%d URLs)\n", len(fallbackURLs)))
    } else {
        prompt.WriteString("FALLBACK AVAILABLE: No\n")
    }
    prompt.WriteString("\n")
    
    // 3. INPUT DATA
    prompt.WriteString("PARSED CONTENT TO EVALUATE:\n")
    // Limit content to avoid token overflow (keep first 2000 chars)
    content := parseOutput
    if len(content) > 2000 {
        content = content[:2000] + "... [truncated]"
    }
    prompt.WriteString(content)
    prompt.WriteString("\n\n")
    
    // 4. CRITERIA
    prompt.WriteString("EVALUATION CRITERIA:\n")
    prompt.WriteString("- quality: \"sufficient\" (helpful), \"try_fallback\" (weak/blocked), \"parse_deeper\" (needs chunking), \"completely_failed\" (error).\n")
    prompt.WriteString("- missing_info: Use empty list () if nothing is missing. Otherwise list gaps.\n")
    prompt.WriteString("- next_action: Choose based on quality. Use \"try_fallback\" only if fallback is available.\n")
    prompt.WriteString("- useful_content: Brief summary if quality > 0, otherwise empty string.\n")
    
    return prompt.String()
}

// parseParseEvaluation extracts evaluation from S-expression response
func (e *Engine) parseParseEvaluation(rawResponse string) (*ParseEvaluation, error) {
	// Clean up response
	content := strings.TrimSpace(rawResponse)
	content = strings.TrimPrefix(content, "```lisp")
	content = strings.TrimPrefix(content, "```")
	content = strings.TrimSuffix(content, "```")
	content = strings.TrimSpace(content)
	
	// Find parse_evaluation block using recursive search (handles nested structures)
	evalBlocks := findBlocksRecursive(content, "parse_evaluation")
	if len(evalBlocks) == 0 {
		// Try with hyphen
		evalBlocks = findBlocksRecursive(content, "parse-evaluation")
	}
	
	if len(evalBlocks) == 0 {
		return nil, fmt.Errorf("no parse_evaluation block found")
	}
	
	block := evalBlocks[0]
	
	evaluation := &ParseEvaluation{
		MissingInfo:    []string{},
		Confidence:     0.7, // Default
		ShouldContinue: true,
	}
	
	// Extract quality
	if quality := extractFieldContent(block, "quality"); quality != "" {
		evaluation.Quality = quality
	} else {
		return nil, fmt.Errorf("quality field missing")
	}
	
	// Extract reasoning
	if reasoning := extractFieldContent(block, "reasoning"); reasoning != "" {
		evaluation.Reasoning = reasoning
	}
	
	// Extract confidence
	if confStr := extractFieldContent(block, "confidence"); confStr != "" {
		if conf, err := parseFloat(confStr); err == nil {
			evaluation.Confidence = conf
		}
	}
	
	// Extract missing_info (list)
	missingInfo := extractListField(block, "missing_info")
	if len(missingInfo) == 0 {
		missingInfo = extractListField(block, "missing-info")
	}
	evaluation.MissingInfo = missingInfo
	
	// Extract next_action
	if nextAction := extractFieldContent(block, "next_action"); nextAction != "" {
		evaluation.NextAction = nextAction
	} else if nextAction := extractFieldContent(block, "next-action"); nextAction != "" {
		evaluation.NextAction = nextAction
	}
	
	// Extract should_continue
	if proceedStr := extractFieldContent(block, "should_continue"); proceedStr != "" {
		evaluation.ShouldContinue = (proceedStr == "true" || proceedStr == "t")
	} else if proceedStr := extractFieldContent(block, "should-continue"); proceedStr != "" {
		evaluation.ShouldContinue = (proceedStr == "true" || proceedStr == "t")
	}
	
	// Extract useful_content
	if usefulContent := extractFieldContent(block, "useful_content"); usefulContent != "" {
		evaluation.UsefulContent = usefulContent
	} else if usefulContent := extractFieldContent(block, "useful-content"); usefulContent != "" {
		evaluation.UsefulContent = usefulContent
	}
	
	// Validate
	validQualities := map[string]bool{
		"sufficient":        true,
		"try_fallback":      true,
		"parse_deeper":      true,
		"completely_failed": true,
	}
	
	if !validQualities[evaluation.Quality] {
		return nil, fmt.Errorf("invalid quality value: %s", evaluation.Quality)
	}
	
	return evaluation, nil
}

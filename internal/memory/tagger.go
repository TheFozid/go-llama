package memory

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"strings"
	"time"
)

// OutcomeAnalysis represents the LLM's analysis of a conversation outcome
type OutcomeAnalysis struct {
	Outcome    string  `json:"outcome"`    // "good", "bad", or "neutral"
	Confidence float64 `json:"confidence"` // 0.0-1.0
	Reason     string  `json:"reason"`     // Brief explanation
}

// Timeout configuration for tagger operations
const (
	taggerBaseTimeout = 90 * time.Second
	taggerMaxRetries  = 3
	taggerRetryDelay  = 5 * time.Second
)

// Tagger handles background tagging of memories
type Tagger struct {
	llmURL    string
	llmModel  string
	batchSize int
	embedder  *Embedder
}

// NewTagger creates a new tagger instance
func NewTagger(llmURL, llmModel string, batchSize int, embedder *Embedder) *Tagger {
	return &Tagger{
		llmURL:    llmURL,
		llmModel:  llmModel,
		batchSize: batchSize,
		embedder:  embedder,
	}
}

// TagMemories processes untagged memories and updates them with outcome tags and concepts
func (t *Tagger) TagMemories(ctx context.Context, storage *Storage) error {
	log.Println("[Tagger] Starting tagging cycle...")

	// Find untagged memories (OutcomeTag is empty string)
	untagged, err := storage.FindUntaggedMemories(ctx, t.batchSize)
	if err != nil {
		return fmt.Errorf("failed to find untagged memories: %w", err)
	}

	if len(untagged) == 0 {
		log.Println("[Tagger] No untagged memories found")
		return nil
	}

	log.Printf("[Tagger] Found %d untagged memories to process", len(untagged))

	// Process each memory
	for i, mem := range untagged {
		log.Printf("[Tagger] Processing memory %d/%d (id=%s)", i+1, len(untagged), mem.ID)

// Analyze outcome with retry logic
		outcome, err := t.analyzeOutcome(ctx, mem.Content)
		if err != nil {
			log.Printf("[Tagger] WARNING: Failed to analyze outcome for memory %s after retries: %v", mem.ID, err)
			// Skip this memory but continue processing others
			continue
		}

		// Extract concepts with retry logic
		concepts, err := t.extractConcepts(ctx, mem.Content)
		if err != nil {
			log.Printf("[Tagger] WARNING: Failed to extract concepts for memory %s after retries: %v", mem.ID, err)
			// Continue with empty concepts - this is non-critical
			concepts = []string{}
		}

		// Update memory fields (but preserve embedding!)
		mem.OutcomeTag = outcome.Outcome
		mem.TrustScore = 0.5 // Initial neutral trust
		mem.ConceptTags = concepts
		mem.ValidationCount = 1 // First validation
		
		// CRITICAL: If embedding is missing, regenerate it
		if len(mem.Embedding) == 0 {
			log.Printf("[Tagger] WARNING: Memory %s has no embedding, regenerating...", mem.ID)
			newEmbedding, err := t.embedder.Embed(ctx, mem.Content)
			if err != nil {
				log.Printf("[Tagger] ERROR: Failed to regenerate embedding for memory %s: %v", mem.ID, err)
				continue // Skip this memory, cannot update without embedding
			}
			mem.Embedding = newEmbedding
			log.Printf("[Tagger] ✓ Regenerated embedding for memory %s (%d dimensions)", mem.ID, len(newEmbedding))
		}
		
		if err := storage.UpdateMemory(ctx, mem); err != nil {
			log.Printf("[Tagger] ERROR: Failed to update memory %s: %v", mem.ID, err)
			continue
		}

		log.Printf("[Tagger] ✓ Tagged memory %s: outcome=%s (%.2f confidence), concepts=%v",
			mem.ID, outcome.Outcome, outcome.Confidence, concepts)
	}

	log.Printf("[Tagger] ✓ Tagging cycle complete: processed %d memories", len(untagged))
	return nil
}

// analyzeOutcome uses LLM to determine if a conversation was good/bad/neutral
// Includes retry logic with exponential backoff for timeout resilience
func (t *Tagger) analyzeOutcome(ctx context.Context, content string) (*OutcomeAnalysis, error) {
	var lastErr error
	
	for attempt := 1; attempt <= taggerMaxRetries; attempt++ {
		// Create timeout context for this attempt
		timeoutCtx, cancel := context.WithTimeout(ctx, taggerBaseTimeout)
		
		result, err := t.analyzeOutcomeWithTimeout(timeoutCtx, content)
		cancel() // Clean up immediately
		
		if err == nil {
			if attempt > 1 {
				log.Printf("[Tagger] ✓ Succeeded on attempt %d/%d", attempt, taggerMaxRetries)
			}
			return result, nil
		}
		
		lastErr = err
		
		// Check if it's a timeout error
		if timeoutCtx.Err() == context.DeadlineExceeded {
			if attempt < taggerMaxRetries {
				backoffDelay := taggerRetryDelay * time.Duration(attempt)
				log.Printf("[Tagger] Attempt %d/%d timed out after %s, retrying in %s...", 
					attempt, taggerMaxRetries, taggerBaseTimeout, backoffDelay)
				
				// Wait before retry (with exponential backoff)
				select {
				case <-time.After(backoffDelay):
					continue
				case <-ctx.Done():
					return nil, fmt.Errorf("operation cancelled during retry backoff: %w", ctx.Err())
				}
			}
		} else {
			// Non-timeout error (e.g., network, JSON parse) - don't retry
			log.Printf("[Tagger] Non-timeout error on attempt %d, not retrying: %v", attempt, err)
			return nil, err
		}
	}
	
	return nil, fmt.Errorf("failed after %d attempts: %w", taggerMaxRetries, lastErr)
}

// analyzeOutcomeWithTimeout is the actual implementation (renamed from analyzeOutcome)
func (t *Tagger) analyzeOutcomeWithTimeout(ctx context.Context, content string) (*OutcomeAnalysis, error) {
	prompt := fmt.Sprintf(`Analyze this conversation and determine the outcome.

Conversation:
%s

Was this interaction:
- GOOD: User satisfied, task succeeded, information helpful
- BAD: User corrected, task failed, information wrong/unhelpful
- NEUTRAL: Unclear, conversation ongoing, or insufficient information

Respond with JSON only (no markdown, no explanation):
{
  "outcome": "good|bad|neutral",
  "confidence": 0.8,
  "reason": "brief explanation"
}`, content)

	payload := map[string]interface{}{
		"model": t.llmModel,
		"messages": []map[string]string{
			{
				"role":    "system",
				"content": "You are an objective conversation analyzer. Respond only with valid JSON.",
			},
			{
				"role":    "user",
				"content": prompt,
			},
		},
		"temperature": 0.3,
		"stream":      false,
	}

	body, _ := json.Marshal(payload)
	req, err := http.NewRequestWithContext(ctx, "POST", t.llmURL, bytes.NewBuffer(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")

// Use context timeout instead of client timeout
	// Add transport configuration for better timeout handling
	transport := &http.Transport{
		ResponseHeaderTimeout: 30 * time.Second, // Fail fast if LLM doesn't start responding
		IdleConnTimeout:       90 * time.Second,
		MaxIdleConns:          10,
	}
	
	client := &http.Client{
		Transport: transport,
		Timeout:   taggerBaseTimeout, // Use our configured timeout
	}
	
	log.Printf("[Tagger] Calling LLM for outcome analysis (timeout: %s)", taggerBaseTimeout)
	startTime := time.Now()
	
	resp, err := client.Do(req)
	if err != nil {
		elapsed := time.Since(startTime)
		if ctx.Err() == context.DeadlineExceeded {
			return nil, fmt.Errorf("LLM timeout after %s: %w", elapsed, err)
		}
		return nil, fmt.Errorf("LLM request failed after %s: %w", elapsed, err)
	}
	
	log.Printf("[Tagger] LLM response received in %s", time.Since(startTime))
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	var llmResp struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&llmResp); err != nil {
		return nil, err
	}

	if len(llmResp.Choices) == 0 {
		return nil, fmt.Errorf("no response from LLM")
	}

	// Parse JSON response
	content = llmResp.Choices[0].Message.Content
	content = strings.TrimSpace(content)
	// Remove markdown code fences if present
	content = strings.TrimPrefix(content, "```json")
	content = strings.TrimPrefix(content, "```")
	content = strings.TrimSuffix(content, "```")
	content = strings.TrimSpace(content)

	var analysis OutcomeAnalysis
	if err := json.Unmarshal([]byte(content), &analysis); err != nil {
		return nil, fmt.Errorf("failed to parse outcome JSON: %w (content: %s)", err, content)
	}
	
	// Validate outcome tag, fallback to neutral if invalid
	if err := ValidateOutcomeTag(analysis.Outcome); err != nil {
		log.Printf("[Tagger] WARNING: LLM returned invalid outcome '%s', defaulting to neutral (reason: %s)", 
			analysis.Outcome, analysis.Reason)
		analysis.Outcome = "neutral"
		analysis.Confidence = 0.0
	}
	
	return &analysis, nil
}

// extractConcepts uses LLM to extract key semantic concepts from a memory
// Includes retry logic with exponential backoff for timeout resilience
func (t *Tagger) extractConcepts(ctx context.Context, content string) ([]string, error) {
	var lastErr error
	
	for attempt := 1; attempt <= taggerMaxRetries; attempt++ {
		// Create timeout context for this attempt
		timeoutCtx, cancel := context.WithTimeout(ctx, taggerBaseTimeout)
		
		result, err := t.extractConceptsWithTimeout(timeoutCtx, content)
		cancel() // Clean up immediately
		
		if err == nil {
			if attempt > 1 {
				log.Printf("[Tagger] ✓ Concept extraction succeeded on attempt %d/%d", attempt, taggerMaxRetries)
			}
			return result, nil
		}
		
		lastErr = err
		
		// Check if it's a timeout error
		if timeoutCtx.Err() == context.DeadlineExceeded {
			if attempt < taggerMaxRetries {
				backoffDelay := taggerRetryDelay * time.Duration(attempt)
				log.Printf("[Tagger] Concept extraction attempt %d/%d timed out, retrying in %s...", 
					attempt, taggerMaxRetries, backoffDelay)
				
				// Wait before retry (with exponential backoff)
				select {
				case <-time.After(backoffDelay):
					continue
				case <-ctx.Done():
					return nil, fmt.Errorf("operation cancelled during retry backoff: %w", ctx.Err())
				}
			}
		} else {
			// Non-timeout error - don't retry
			log.Printf("[Tagger] Non-timeout error in concept extraction, not retrying: %v", err)
			return nil, err
		}
	}
	
	return nil, fmt.Errorf("concept extraction failed after %d attempts: %w", taggerMaxRetries, lastErr)
}

// extractConceptsWithTimeout is the actual implementation (renamed from extractConcepts)
func (t *Tagger) extractConceptsWithTimeout(ctx context.Context, content string) ([]string, error) {
	// First, try domain-specific pattern matching for conversational AI concepts
	domainConcepts := []string{}
	contentLower := strings.ToLower(content)
	
	domainPatterns := map[string][]string{
		"personality": {"personality", "character", "persona", "traits", "backstory", "identity"},
		"human-like-interaction": {"human-like", "natural conversation", "conversational flow", "human-to-human", "authentic"},
		"emotional-intelligence": {"empathy", "emotional", "feelings", "sentiment", "compassion"},
		"memory-context": {"remember", "recall", "context", "history", "past conversation", "continuity"},
		"learning-growth": {"learning", "growth", "improvement", "development", "evolution", "adaptive"},
		"storytelling": {"story", "narrative", "backstory", "biography", "experience", "anecdote"},
		"conversational-skills": {"small talk", "clarification", "ambiguity", "turn-taking", "dialogue"},
		"response-quality": {"helpfulness", "relevance", "coherence", "consistency", "naturalness"},
	}
	
	for tag, patterns := range domainPatterns {
		for _, pattern := range patterns {
			if strings.Contains(contentLower, pattern) {
				domainConcepts = append(domainConcepts, tag)
				break
			}
		}
	}
	
	// If we found domain concepts, use them
	if len(domainConcepts) > 0 {
		log.Printf("[Tagger] Extracted %d domain-specific concepts: %v", len(domainConcepts), domainConcepts)
		if len(domainConcepts) > 5 {
			domainConcepts = domainConcepts[:5]
		}
		return domainConcepts, nil
	}
	
	// Otherwise, fall back to LLM extraction
	prompt := fmt.Sprintf(`Extract 3-5 key concepts from this conversation.

Conversation:
%s

Return a JSON array of semantic tags (nouns, topics, technologies):
["concept1", "concept2", "concept3"]

Rules:
- Use lowercase
- Be specific (e.g., "python debugging" not just "programming")
- Focus on concrete topics, not abstract ideas
- No duplicates

Respond with JSON array only (no markdown, no explanation).`, content)

	payload := map[string]interface{}{
		"model": t.llmModel,
		"messages": []map[string]string{
			{
				"role":    "system",
				"content": "You are a concept extraction system. Respond only with a valid JSON array.",
			},
			{
				"role":    "user",
				"content": prompt,
			},
		},
		"temperature": 0.3,
		"stream":      false,
	}

	body, _ := json.Marshal(payload)
	req, err := http.NewRequestWithContext(ctx, "POST", t.llmURL, bytes.NewBuffer(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")

// Use context timeout with transport configuration
	transport := &http.Transport{
		ResponseHeaderTimeout: 30 * time.Second,
		IdleConnTimeout:       90 * time.Second,
		MaxIdleConns:          10,
	}
	
	client := &http.Client{
		Transport: transport,
		Timeout:   taggerBaseTimeout,
	}
	
	log.Printf("[Tagger] Calling LLM for concept extraction (timeout: %s)", taggerBaseTimeout)
	startTime := time.Now()
	
	resp, err := client.Do(req)
	if err != nil {
		elapsed := time.Since(startTime)
		if ctx.Err() == context.DeadlineExceeded {
			return nil, fmt.Errorf("LLM timeout after %s: %w", elapsed, err)
		}
		return nil, fmt.Errorf("LLM request failed after %s: %w", elapsed, err)
	}
	
	log.Printf("[Tagger] Concept extraction response received in %s", time.Since(startTime))
	defer resp.Body.Close()

	var llmResp struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&llmResp); err != nil {
		return nil, err
	}

	if len(llmResp.Choices) == 0 {
		return nil, fmt.Errorf("no response from LLM")
	}

	// Parse JSON array
	content = llmResp.Choices[0].Message.Content
	content = strings.TrimSpace(content)
	// Remove markdown code fences if present
	content = strings.TrimPrefix(content, "```json")
	content = strings.TrimPrefix(content, "```")
	content = strings.TrimSuffix(content, "```")
	content = strings.TrimSpace(content)
	
	var concepts []string
	
	// Try parsing as JSON first
	if err := json.Unmarshal([]byte(content), &concepts); err != nil {
		// Fallback: Try comma-separated values if JSON parsing fails
		log.Printf("[Tagger] JSON parse failed for concepts, trying comma-split fallback: %v", err)
		
		// Remove brackets if present
		content = strings.TrimPrefix(content, "[")
		content = strings.TrimSuffix(content, "]")
		
		// Split by comma
		rawConcepts := strings.Split(content, ",")
		concepts = make([]string, 0, len(rawConcepts))
		
		for _, raw := range rawConcepts {
			// Clean up each concept
			cleaned := strings.TrimSpace(raw)
			cleaned = strings.Trim(cleaned, `"'`) // Remove quotes
			cleaned = strings.ToLower(cleaned)
			
			if cleaned != "" && len(cleaned) < 50 { // Sanity check
				concepts = append(concepts, cleaned)
			}
		}
		
		if len(concepts) == 0 {
			return nil, fmt.Errorf("failed to extract concepts from: %s", content)
		}
		
		log.Printf("[Tagger] ✓ Extracted %d concepts via fallback parser", len(concepts))
	}

	// Limit to 5 concepts max
	if len(concepts) > 5 {
		concepts = concepts[:5]
	}

	return concepts, nil
}

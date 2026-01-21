// internal/memory/compressor.go
package memory

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
)

// Compressor handles LLM-based memory compression
type Compressor struct {
	modelURL  string
	modelName string
	client    *http.Client
	embedder  *Embedder
	linker    *Linker
	llmClient interface{} // Queue client
}

// NewCompressor creates a new compressor instance
func NewCompressor(modelURL, modelName string, embedder *Embedder, linker *Linker, llmClient interface{}) *Compressor {
	return &Compressor{
		modelURL:  modelURL,
		modelName: modelName,
		client:    &http.Client{Timeout: 60 * time.Second},
		embedder:  embedder,
		linker:    linker,
		llmClient: llmClient,
	}
}

// Compress reduces memory content using LLM based on target tier
func (c *Compressor) Compress(ctx context.Context, memory *Memory, targetTier MemoryTier) (*Memory, error) {
	// Determine compression prompt based on target tier
	var prompt string
	switch targetTier {
	case TierMedium:
		prompt = fmt.Sprintf("Summarize the following memory in exactly 100 words, preserving key information:\n\n%s", memory.Content)
	case TierLong:
		prompt = fmt.Sprintf("Extract the 20 most important words or short phrases from this memory:\n\n%s", memory.Content)
	case TierAncient:
		prompt = fmt.Sprintf("Extract only the 3 most critical keywords from this memory:\n\n%s", memory.Content)
	default:
		return memory, fmt.Errorf("invalid target tier: %s", targetTier)
	}

	// Call LLM
	compressed, err := c.callLLM(ctx, prompt)
	if err != nil {
		return nil, fmt.Errorf("LLM compression failed: %w", err)
	}

	// Store original content before compression
	if memory.CompressedFrom == "" {
		memory.CompressedFrom = memory.Content
	}

	// Update memory
	memory.Content = strings.TrimSpace(compressed)
	memory.Tier = targetTier
	
	log.Printf("[Compressor] Compressed memory %s: %s -> %s (%d -> %d chars, created: %s)",
		memory.ID, memory.Tier, targetTier, len(memory.CompressedFrom), len(memory.Content), 
		memory.CreatedAt.Format("2006-01-02 15:04"))

	return memory, nil
}

// CompressCluster compresses a group of related memories together
func (c *Compressor) CompressCluster(ctx context.Context, cluster []Memory, targetTier MemoryTier) (*Memory, error) {
	if len(cluster) == 0 {
		return nil, fmt.Errorf("empty cluster")
	}
	
	if len(cluster) == 1 {
		// Single memory, use regular compression
		return c.Compress(ctx, &cluster[0], targetTier)
	}
	
	log.Printf("[Compressor] Compressing cluster of %d memories to %s", len(cluster), targetTier)
	
	// Build combined content from all memories in cluster
	var contentBuilder strings.Builder
	contentBuilder.WriteString("=== RELATED MEMORIES ===\n\n")
	
	for i, mem := range cluster {
		contentBuilder.WriteString(fmt.Sprintf("Memory %d (created: %s, importance: %.2f, outcome: %s):\n%s\n\n",
			i+1, mem.CreatedAt.Format("2006-01-02"), mem.ImportanceScore, mem.OutcomeTag, mem.Content))
	}
	
	// Determine compression prompt based on target tier
	var prompt string
	switch targetTier {
	case TierMedium:
		prompt = fmt.Sprintf("These related memories are being compressed together. Summarize them in exactly 150 words, preserving key shared themes and important unique details:\n\n%s", contentBuilder.String())
	case TierLong:
		prompt = fmt.Sprintf("These related memories are being compressed together. Extract the 30 most important words or short phrases that capture the shared themes:\n\n%s", contentBuilder.String())
	case TierAncient:
		prompt = fmt.Sprintf("These related memories are being compressed together. Extract only the 5 most critical keywords that represent the core pattern:\n\n%s", contentBuilder.String())
	default:
		return nil, fmt.Errorf("invalid target tier: %s", targetTier)
	}
	
	// Call LLM for cluster compression
	compressed, err := c.callLLM(ctx, prompt)
	if err != nil {
		return nil, fmt.Errorf("cluster compression failed: %w", err)
	}
	
	// Extract concept tags from compressed content
	conceptTags, err := c.extractConceptTags(ctx, compressed)
	if err != nil {
		log.Printf("[Compressor] WARNING: Failed to extract concept tags: %v", err)
		conceptTags = []string{}
	}
	
	// Create merged memory from the first memory in cluster
	merged := cluster[0]
	
	// Store original content
	if merged.CompressedFrom == "" {
		merged.CompressedFrom = merged.Content
	}
	
	// Update with compressed content
	merged.Content = strings.TrimSpace(compressed)
	merged.Tier = targetTier
	merged.ConceptTags = conceptTags
	
	// Use the earliest creation time from the cluster
	earliestTime := cluster[0].CreatedAt
	for _, mem := range cluster {
		if mem.CreatedAt.Before(earliestTime) {
			earliestTime = mem.CreatedAt
		}
	}
	merged.CreatedAt = earliestTime
	// Note: Preserving full CreatedAt precision for all tiers
	
	// Aggregate importance scores (average of cluster)
	totalImportance := 0.0
	for _, mem := range cluster {
		totalImportance += mem.ImportanceScore
	}
	merged.ImportanceScore = totalImportance / float64(len(cluster))
	
	// Aggregate outcome tags (most common wins, or highest rated)
	merged.OutcomeTag = c.aggregateOutcomeTags(cluster)
	
	// Link all memories in the cluster together
	if err := c.linker.CreateLinks(ctx, cluster); err != nil {
		log.Printf("[Compressor] WARNING: Failed to create links for cluster: %v", err)
	}
	
	// Collect all related memories from cluster
	relatedSet := make(map[string]bool)
	for _, mem := range cluster {
		for _, relID := range mem.RelatedMemories {
			relatedSet[relID] = true
		}
		// Don't include cluster members themselves as related
		delete(relatedSet, mem.ID)
	}
	
	// Convert set to slice
	merged.RelatedMemories = make([]string, 0, len(relatedSet))
	for id := range relatedSet {
		merged.RelatedMemories = append(merged.RelatedMemories, id)
	}
	
	log.Printf("[Compressor] Cluster compressed: %d memories -> 1 (created: %s, concepts: %v)",
		len(cluster), merged.CreatedAt.Format("2006-01-02 15:04"), merged.ConceptTags)
	
	return &merged, nil
}

// extractConceptTags uses pattern matching and LLM to extract semantic tags from content
func (c *Compressor) extractConceptTags(ctx context.Context, content string) ([]string, error) {
	// First, try domain-specific pattern matching for conversational AI concepts
	domainConcepts := []string{}
	contentLower := strings.ToLower(content)
	
	domainPatterns := map[string][]string{
		"personality":            {"personality", "character", "persona", "traits", "backstory", "identity"},
		"human-like-interaction": {"human-like", "natural conversation", "conversational flow", "human-to-human", "authentic"},
		"emotional-intelligence": {"empathy", "emotional", "feelings", "sentiment", "compassion"},
		"memory-context":         {"remember", "recall", "context", "history", "past conversation", "continuity"},
		"learning-growth":        {"learning", "growth", "improvement", "development", "evolution", "adaptive"},
		"storytelling":           {"story", "narrative", "backstory", "biography", "experience", "anecdote"},
		"conversational-skills":  {"small talk", "clarification", "ambiguity", "turn-taking", "dialogue"},
		"response-quality":       {"helpfulness", "relevance", "coherence", "consistency", "naturalness"},
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
		log.Printf("[Compressor] Extracted %d domain-specific concepts: %v", len(domainConcepts), domainConcepts)
		if len(domainConcepts) > 5 {
			domainConcepts = domainConcepts[:5]
		}
		return domainConcepts, nil
	}
	
	// Otherwise, fall back to LLM extraction for generic content
	prompt := fmt.Sprintf("Extract 3-5 single-word concept tags that best describe this content. Return ONLY the tags separated by commas, nothing else:\n\n%s", content)
	
	response, err := c.callLLM(ctx, prompt)
	if err != nil {
		return nil, err
	}
	
	// Parse comma-separated tags
	tags := strings.Split(response, ",")
	result := make([]string, 0, len(tags))
	for _, tag := range tags {
		cleaned := strings.TrimSpace(strings.ToLower(tag))
		if cleaned != "" && len(cleaned) < 50 { // Sanity check
			result = append(result, cleaned)
		}
	}
	
	return result, nil
}


// aggregateOutcomeTags determines the outcome tag for a merged memory
func (c *Compressor) aggregateOutcomeTags(cluster []Memory) string {
	counts := map[string]int{
		"good":    0,
		"bad":     0,
		"neutral": 0,
	}
	
	for _, mem := range cluster {
		tag := mem.OutcomeTag
		if tag == "" {
			tag = "neutral"
		}
		counts[tag]++
	}
	
	// Return most common tag
	maxCount := 0
	result := "neutral"
	for tag, count := range counts {
		if count > maxCount {
			maxCount = count
			result = tag
		}
	}
	
	return result
}

// callLLM sends a request to the compression LLM via queue
func (c *Compressor) callLLM(ctx context.Context, prompt string) (string, error) {
	// Use queue if available, otherwise fall back to direct HTTP
	if c.llmClient != nil {
		type LLMCaller interface {
			Call(ctx context.Context, url string, payload map[string]interface{}) ([]byte, error)
		}
		
		if client, ok := c.llmClient.(LLMCaller); ok {
			reqBody := map[string]interface{}{
				"model": c.modelName,
				"messages": []map[string]string{
					{
						"role":    "system",
						"content": "You are a memory compression assistant. Follow instructions exactly.",
					},
					{
						"role":    "user",
						"content": prompt,
					},
				},
				"stream":      false,
				"temperature": 0.3,
			}
			
            log.Printf("[Compressor] Calling LLM via queue (prompt length: %d)", len(prompt))

            body, err := client.Call(ctx, c.modelName, reqBody)
			if err != nil {
				return "", fmt.Errorf("LLM queue call failed: %w", err)
			}
			
			var result struct {
				Choices []struct {
					Message struct {
						Content string `json:"content"`
					} `json:"message"`
				} `json:"choices"`
			}
			
			if err := json.Unmarshal(body, &result); err != nil {
				return "", fmt.Errorf("failed to decode response: %w", err)
			}
			
			if len(result.Choices) == 0 {
				return "", fmt.Errorf("no choices returned from LLM")
			}
			
			return result.Choices[0].Message.Content, nil
		}
	}
	
	// Fallback to direct HTTP (legacy compatibility)
	log.Printf("[Compressor] WARNING: Using legacy direct HTTP call")
	
	reqBody := map[string]interface{}{
		"model": c.modelName,
		"messages": []map[string]string{
			{
				"role":    "system",
				"content": "You are a memory compression assistant. Follow instructions exactly.",
			},
			{
				"role":    "user",
				"content": prompt,
			},
		},
		"stream":      false,
		"temperature": 0.3,
	}

	jsonData, err := json.Marshal(reqBody)
	if err != nil {
		return "", fmt.Errorf("failed to marshal request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, "POST", c.modelURL, bytes.NewBuffer(jsonData))
	if err != nil {
		return "", fmt.Errorf("failed to create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.client.Do(req)
	if err != nil {
		return "", fmt.Errorf("failed to send request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return "", fmt.Errorf("LLM returned status %d: %s", resp.StatusCode, string(body))
	}

	var result struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return "", fmt.Errorf("failed to decode response: %w", err)
	}

	if len(result.Choices) == 0 {
		return "", fmt.Errorf("no choices returned from LLM")
	}

	return result.Choices[0].Message.Content, nil
}

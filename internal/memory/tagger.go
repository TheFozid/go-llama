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

// Tagger handles background tagging of memories
type Tagger struct {
	llmURL    string
	llmModel  string
	batchSize int
}

// NewTagger creates a new tagger instance
func NewTagger(llmURL, llmModel string, batchSize int) *Tagger {
	return &Tagger{
		llmURL:    llmURL,
		llmModel:  llmModel,
		batchSize: batchSize,
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

		// Analyze outcome
		outcome, err := t.analyzeOutcome(ctx, mem.Content)
		if err != nil {
			log.Printf("[Tagger] WARNING: Failed to analyze outcome for memory %s: %v", mem.ID, err)
			continue
		}

		// Extract concepts
		concepts, err := t.extractConcepts(ctx, mem.Content)
		if err != nil {
			log.Printf("[Tagger] WARNING: Failed to extract concepts for memory %s: %v", mem.ID, err)
			concepts = []string{} // Continue with empty concepts
		}

		// Update memory
		mem.OutcomeTag = outcome.Outcome
		mem.TrustScore = 0.5 // Initial neutral trust
		mem.ConceptTags = concepts
		mem.ValidationCount = 1 // First validation

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
func (t *Tagger) analyzeOutcome(ctx context.Context, content string) (*OutcomeAnalysis, error) {
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

	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(req)
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

	// Validate outcome tag
	if err := ValidateOutcomeTag(analysis.Outcome); err != nil {
		return nil, fmt.Errorf("invalid outcome tag from LLM: %w", err)
	}

	return &analysis, nil
}

// extractConcepts uses LLM to extract key semantic concepts from a memory
func (t *Tagger) extractConcepts(ctx context.Context, content string) ([]string, error) {
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

	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(req)
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

	// Parse JSON array
	content = llmResp.Choices[0].Message.Content
	content = strings.TrimSpace(content)
	// Remove markdown code fences if present
	content = strings.TrimPrefix(content, "```json")
	content = strings.TrimPrefix(content, "```")
	content = strings.TrimSuffix(content, "```")
	content = strings.TrimSpace(content)

	var concepts []string
	if err := json.Unmarshal([]byte(content), &concepts); err != nil {
		return nil, fmt.Errorf("failed to parse concepts JSON: %w (content: %s)", err, content)
	}

	// Limit to 5 concepts max
	if len(concepts) > 5 {
		concepts = concepts[:5]
	}

	return concepts, nil
}

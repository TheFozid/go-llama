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
	modelURL string
	modelName string
	client   *http.Client
}

// NewCompressor creates a new compressor instance
func NewCompressor(modelURL, modelName string) *Compressor {
	return &Compressor{
		modelURL:  modelURL,
		modelName: modelName,
		client:    &http.Client{Timeout: 60 * time.Second},
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

	log.Printf("[Compressor] Compressed memory %s: %s -> %s (%d -> %d chars)",
		memory.ID, memory.Tier, targetTier, len(memory.CompressedFrom), len(memory.Content))

	return memory, nil
}

// callLLM sends a request to the compression LLM
func (c *Compressor) callLLM(ctx context.Context, prompt string) (string, error) {
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
		"temperature": 0.3, // Low temperature for consistent compression
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

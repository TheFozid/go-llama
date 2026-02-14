// internal/goal/llm.go
package goal

import (
    "context"
    "encoding/json"
    "fmt"
)

// LLMService defines the interface required by the Goal System to interact with LLMs.
// This decouples the goal logic from the specific LLM client implementation (dialogue queue).
type LLMService interface {
    // GenerateJSON sends a prompt to the LLM and expects a JSON response that unmarshals into 'target'.
    GenerateJSON(ctx context.Context, prompt string, target interface{}) error
    // GenerateText sends a prompt and returns the raw text response.
    GenerateText(ctx context.Context, prompt string) (string, error)
}

// LLMCaller defines the interface for the LLM Queue Client (subset of llm.Client)
type LLMCaller interface {
    Call(ctx context.Context, url string, payload map[string]interface{}) ([]byte, error)
}

// QueueLLMAdapter connects the Goal System to the LLM Queue.
type QueueLLMAdapter struct {
    Client   LLMCaller
    LLMURL   string
    LLMModel string
}

// NewQueueLLMAdapter creates a new adapter.
func NewQueueLLMAdapter(client LLMCaller, llmURL, llmModel string) *QueueLLMAdapter {
    return &QueueLLMAdapter{
        Client:   client,
        LLMURL:   llmURL,
        LLMModel: llmModel,
    }
}

// GenerateJSON implements LLMService.
func (q *QueueLLMAdapter) GenerateJSON(ctx context.Context, prompt string, target interface{}) error {
    if q.Client == nil {
        return fmt.Errorf("LLM client not configured")
    }

    // Construct OpenAI-compatible payload
    payload := map[string]interface{}{
        "model": q.LLMModel,
        "messages": []map[string]string{
            {"role": "system", "content": "You are a strategic AI assistant. Respond only with valid JSON."},
            {"role": "user", "content": prompt},
        },
        "temperature": 0.7,
    }

    respBytes, err := q.Client.Call(ctx, q.LLMURL, payload)
    if err != nil {
        return fmt.Errorf("LLM call failed: %w", err)
    }

    // Parse OpenAI response structure
    var llmResp struct {
        Choices []struct {
            Message struct {
                Content string `json:"content"`
            } `json:"message"`
        } `json:"choices"`
    }

    if err := json.Unmarshal(respBytes, &llmResp); err != nil {
        return fmt.Errorf("failed to parse LLM response: %w", err)
    }

    if len(llmResp.Choices) == 0 {
        return fmt.Errorf("no choices returned from LLM")
    }

    content := llmResp.Choices[0].Message.Content
    return parseStructuredResponse(content, target)
}

// GenerateText implements LLMService.
func (q *QueueLLMAdapter) GenerateText(ctx context.Context, prompt string) (string, error) {
    if q.Client == nil {
        return "", fmt.Errorf("LLM client not configured")
    }

    payload := map[string]interface{}{
        "model": q.LLMModel,
        "messages": []map[string]string{
            {"role": "user", "content": prompt},
        },
    }

    respBytes, err := q.Client.Call(ctx, q.LLMURL, payload)
    if err != nil {
        return "", err
    }

    var llmResp struct {
        Choices []struct {
            Message struct {
                Content string `json:"content"`
            } `json:"message"`
        } `json:"choices"`
    }

    if err := json.Unmarshal(respBytes, &llmResp); err != nil {
        return "", err
    }

    if len(llmResp.Choices) == 0 {
        return "", fmt.Errorf("no choices returned")
    }

    return llmResp.Choices[0].Message.Content, nil
}

// DefaultLLMAdapter is a placeholder implementation if none is provided.
// In production, this will be an adapter connecting to dialogue.LLMClient.
type DefaultLLMAdapter struct {
    JSONFunc func(ctx context.Context, prompt string, target interface{}) error
    TextFunc func(ctx context.Context, prompt string) (string, error)
}

func (d *DefaultLLMAdapter) GenerateJSON(ctx context.Context, prompt string, target interface{}) error {
    if d.JSONFunc != nil {
        return d.JSONFunc(ctx, prompt, target)
    }
    return fmt.Errorf("LLMService not configured: JSON generation unavailable")
}

func (d *DefaultLLMAdapter) GenerateText(ctx context.Context, prompt string) (string, error) {
    if d.TextFunc != nil {
        return d.TextFunc(ctx, prompt)
    }
    return "", fmt.Errorf("LLMService not configured: Text generation unavailable")
}

// parseStructuredResponse helps extract JSON from potentially messy LLM output.
func parseStructuredResponse(response string, target interface{}) error {
    // Simple extraction: look for JSON block if wrapped in markdown
    start := 0
    end := len(response)

    if idx := findSubstring(response, "```json"); idx != -1 {
        start = idx + 7
    } else if idx := findSubstring(response, "```"); idx != -1 {
        start = idx + 3
    }

    if idx := findSubstring(response[start:], "```"); idx != -1 {
        end = start + idx
    } else {
        end = len(response)
    }

    jsonStr := response[start:end]
    return json.Unmarshal([]byte(jsonStr), target)
}

func findSubstring(s, substr string) int {
    for i := 0; i <= len(s)-len(substr); i++ {
        if s[i:i+len(substr)] == substr {
            return i
        }
    }
    return -1
}

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

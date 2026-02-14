// internal/goal/llm_adapter.go
package goal

import (
    "context"
    "encoding/json"
    "fmt"

    llm "go-llama/internal/llm"
)

// LLMAdapter implements the LLMService interface using the existing llm.Client.
type LLMAdapter struct {
    Client   *llm.Client
    LLMURL   string
    LLMModel string
}

// NewLLMAdapter creates a new adapter for the goal system.
func NewLLMAdapter(client *llm.Client, llmURL, llmModel string) *LLMAdapter {
    return &LLMAdapter{
        Client:   client,
        LLMURL:   llmURL,
        LLMModel: llmModel,
    }
}

// GenerateJSON sends a prompt and parses the JSON response into the target.
func (a *LLMAdapter) GenerateJSON(ctx context.Context, prompt string, target interface{}) error {
    payload := map[string]interface{}{
        "model": a.LLMModel,
        "messages": []map[string]string{
            {
                "role":    "system",
                "content": "You are a precise JSON generator for an autonomous goal system. Output only valid JSON.",
            },
            {
                "role":    "user",
                "content": prompt,
            },
        },
        "temperature": 0.3, // Lower temp for structured logic
    }

    respBody, err := a.Client.Call(ctx, a.LLMURL, payload)
    if err != nil {
        return fmt.Errorf("llm call failed: %w", err)
    }

    // Parse standard OpenAI-style response
    var llmResp struct {
        Choices []struct {
            Message struct {
                Content string `json:"content"`
            } `json:"message"`
        } `json:"choices"`
    }

    if err := json.Unmarshal(respBody, &llmResp); err != nil {
        return fmt.Errorf("failed to unmarshal llm response: %w", err)
    }

    if len(llmResp.Choices) == 0 {
        return fmt.Errorf("no choices returned from llm")
    }

    content := llmResp.Choices[0].Message.Content
    return parseStructuredResponse(content, target)
}

// GenerateText sends a prompt and returns the raw text response.
func (a *LLMAdapter) GenerateText(ctx context.Context, prompt string) (string, error) {
    payload := map[string]interface{}{
        "model": a.LLMModel,
        "messages": []map[string]string{
            {
                "role":    "user",
                "content": prompt,
            },
        },
        "temperature": 0.7,
    }

    respBody, err := a.Client.Call(ctx, a.LLMURL, payload)
    if err != nil {
        return "", fmt.Errorf("llm call failed: %w", err)
    }

    var llmResp struct {
        Choices []struct {
            Message struct {
                Content string `json:"content"`
            } `json:"message"`
        } `json:"choices"`
    }

    if err := json.Unmarshal(respBody, &llmResp); err != nil {
        return "", fmt.Errorf("failed to unmarshal llm response: %w", err)
    }

    if len(llmResp.Choices) == 0 {
        return "", fmt.Errorf("no choices returned from llm")
    }

    return llmResp.Choices[0].Message.Content, nil
}

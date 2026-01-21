// internal/memory/embedder.go
package memory

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"time"
	"io"
	"net/http"
)

// Embedder generates vector embeddings from text
type Embedder struct {
    apiURL    string
    modelName string
    client    *http.Client
}

// NewEmbedder creates a new embedder client
func NewEmbedder(apiURL, modelName string) *Embedder {
    return &Embedder{
        apiURL:    apiURL,
        modelName: modelName,
        client: &http.Client{
            Timeout: 15 * time.Second, // Reasonable timeout for embedding generation
        },
    }
}

// Embed converts text to a vector embedding
func (e *Embedder) Embed(ctx context.Context, text string) ([]float32, error) {
    reqBody := map[string]interface{}{
        "input": text,
        "model": e.modelName, // Use the dynamically discovered model name
    }

	jsonData, err := json.Marshal(reqBody)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, "POST", e.apiURL, bytes.NewBuffer(jsonData))
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("Content-Type", "application/json")

	resp, err := e.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to send request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("API returned status %d: %s", resp.StatusCode, string(body))
	}

	var result struct {
		Data []struct {
			Embedding []float32 `json:"embedding"`
		} `json:"data"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("failed to decode response: %w", err)
	}

	if len(result.Data) == 0 {
		return nil, fmt.Errorf("no embeddings returned")
	}

	return result.Data[0].Embedding, nil
}

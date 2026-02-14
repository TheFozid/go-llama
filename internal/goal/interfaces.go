// internal/goal/interfaces.go
package goal

import "context"

// MemorySearcher defines the ability to search for relevant context.
// This decouples the goal logic from the memory implementation.
type MemorySearcher interface {
    SearchRelevant(ctx context.Context, queryText string, limit int) ([]string, error)
}

// Embedder defines the ability to generate embeddings.
type Embedder interface {
    Embed(ctx context.Context, text string) ([]float32, error)
}

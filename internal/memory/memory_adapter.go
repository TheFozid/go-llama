// internal/memory/memory_adapter.go
package memory

import (
    "context"
)

// MemoryAdapter implements goal interfaces by combining Storage and Embedder.
type MemoryAdapter struct {
    Storage  *Storage
    Embedder *Embedder
}

// NewMemoryAdapter creates a new adapter.
func NewMemoryAdapter(storage *Storage, embedder *Embedder) *MemoryAdapter {
    return &MemoryAdapter{Storage: storage, Embedder: embedder}
}

// SearchRelevant performs a semantic search and returns content strings.
// This implements the goal.MemorySearcher interface.
func (m *MemoryAdapter) SearchRelevant(ctx context.Context, queryText string, limit int) ([]string, error) {
    // 1. Embed the query
    embedding, err := m.Embedder.Embed(ctx, queryText)
    if err != nil {
        return nil, err
    }

    // 2. Perform Search
    query := RetrievalQuery{
        Limit:             limit,
        MinScore:          0.4,
        IncludeCollective: true,
        IncludePersonal:   false,
    }

    results, err := m.Storage.Search(ctx, query, embedding)
    if err != nil {
        return nil, err
    }

    // 3. Extract content strings
    contents := make([]string, 0, len(results))
    for _, r := range results {
        contents = append(contents, r.Memory.Content)
    }

    return contents, nil
}

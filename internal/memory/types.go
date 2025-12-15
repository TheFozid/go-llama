// internal/memory/types.go
package memory

import (
	"time"
)

// MemoryTier represents the compression/detail level of a memory
type MemoryTier string

const (
	TierRecent  MemoryTier = "recent"  // Hours-days: high detail
	TierMedium  MemoryTier = "medium"  // Weeks-months: summarized
	TierLong    MemoryTier = "long"    // Months-years: abstracted
	TierAncient MemoryTier = "ancient" // Years+: patterns only
)

// Memory represents a stored memory chunk with metadata
type Memory struct {
	ID              string                 `json:"id"`
	Content         string                 `json:"content"`
	CompressedFrom  string                 `json:"compressed_from,omitempty"` // Original content if compressed
	Tier            MemoryTier             `json:"tier"`
	UserID          *string                `json:"user_id,omitempty"`     // Null = collective memory
	IsCollective    bool                   `json:"is_collective"`
	CreatedAt       time.Time              `json:"created_at"`
	LastAccessedAt  time.Time              `json:"last_accessed_at"`
	AccessCount     int                    `json:"access_count"`
	ImportanceScore float64                `json:"importance_score"`
	Metadata        map[string]interface{} `json:"metadata"`
	Embedding       []float32              `json:"-"` // Not serialized
}

// EvaluationResult determines if content should be stored
type EvaluationResult struct {
	ShouldStore      bool
	ImportanceScore  float64
	IsPersonal       bool
	IsCollective     bool
	Reasons          []string
	SuggestedMetadata map[string]interface{}
}

// RetrievalQuery represents a search query for memories
type RetrievalQuery struct {
	Query          string
	UserID         *string
	IncludePersonal bool
	IncludeCollective bool
	Tier           *MemoryTier
	Limit          int
	MinScore       float64
}

// RetrievalResult represents a retrieved memory with relevance score
type RetrievalResult struct {
	Memory Memory
	Score  float64
}

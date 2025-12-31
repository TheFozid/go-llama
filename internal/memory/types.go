// internal/memory/types.go
package memory

import (
	"fmt"
	"time"
)

// MemoryTier represents the compression/detail level of a memory
type MemoryTier string

const (
	TierPrinciples MemoryTier = "principles" // Timeless: learned rules & values (10 Commandments)
	TierRecent     MemoryTier = "recent"     // Hours-days: high detail
	TierMedium     MemoryTier = "medium"     // Weeks-months: summarized
	TierLong       MemoryTier = "long"       // Months-years: abstracted
	TierAncient    MemoryTier = "ancient"    // Years+: patterns only
)

// OutcomeTag represents the evaluation of a memory's outcome
type OutcomeTag string

const (
	OutcomeGood    OutcomeTag = "good"
	OutcomeBad     OutcomeTag = "bad"
	OutcomeNeutral OutcomeTag = "neutral"
)

// ValidateOutcomeTag checks if an outcome tag is valid
func ValidateOutcomeTag(tag string) error {
	switch OutcomeTag(tag) {
	case OutcomeGood, OutcomeBad, OutcomeNeutral:
		return nil
	case "": // Empty is allowed (not yet evaluated)
		return nil
	default:
		return fmt.Errorf("invalid outcome tag: %s (must be 'good', 'bad', or 'neutral')", tag)
	}
}

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

	// Phase 4 enhancements: Good/Bad Tagging System
	OutcomeTag      string  `json:"outcome_tag"`       // "good", "bad", "neutral", or ""
	TrustScore      float64 `json:"trust_score"`       // 0.0-1.0 reliability rating
	ValidationCount int     `json:"validation_count"`  // Number of times outcome was validated

	// Phase 4 enhancements: Neural Network (Memory Linking)
	RelatedMemories []string `json:"related_memories"`  // IDs of linked memories
	ConceptTags     []string `json:"concept_tags"`      // Semantic tags for clustering

	// Phase 4 enhancements: Temporal Resolution
	TemporalResolution string `json:"temporal_resolution"` // ISO 8601 with degrading precision

	// Phase 4 enhancements: Principles System (10 Commandments)
	PrincipleRating float64 `json:"principle_rating"` // For Principles tier only (0.0-1.0)
}

// SetOutcomeTag validates and sets the outcome tag
func (m *Memory) SetOutcomeTag(tag string) error {
	if err := ValidateOutcomeTag(tag); err != nil {
		return err
	}
	m.OutcomeTag = tag
	return nil
}

// GetOutcomeTag returns the outcome tag as a typed value
func (m *Memory) GetOutcomeTag() OutcomeTag {
	if m.OutcomeTag == "" {
		return OutcomeNeutral
	}
	return OutcomeTag(m.OutcomeTag)
}

// EvaluationResult determines if content should be stored
type EvaluationResult struct {
	ShouldStore       bool
	ImportanceScore   float64
	IsPersonal        bool
	IsCollective      bool
	Reasons           []string
	SuggestedMetadata map[string]interface{}
}

// RetrievalQuery represents a search query for memories
type RetrievalQuery struct {
	Query             string
	UserID            *string
	IncludePersonal   bool
	IncludeCollective bool
	Tier              *MemoryTier
	Limit             int
	MinScore          float64
	
	// Phase 4 enhancements: filtering by outcome and concepts
	OutcomeFilter *OutcomeTag  // Filter by good/bad/neutral
	ConceptTags   []string     // Filter by semantic tags
}

// RetrievalResult represents a retrieved memory with relevance score
type RetrievalResult struct {
	Memory Memory
	Score  float64
}

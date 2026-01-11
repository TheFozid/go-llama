package memory

import (
	"context"
	"fmt"
	"log"
	"math"
)

// Consolidator finds and merges semantically duplicate memories
type Consolidator struct {
	storage  *Storage
	embedder *Embedder
}

// NewConsolidator creates a new consolidator
func NewConsolidator(storage *Storage, embedder *Embedder) *Consolidator {
	return &Consolidator{
		storage:  storage,
		embedder: embedder,
	}
}

// ConsolidateDuplicates finds memories expressing the same idea and creates authoritative version
// Returns: (consolidated count, errors)
func (c *Consolidator) ConsolidateDuplicates(ctx context.Context, tier MemoryTier) (int, error) {
	log.Printf("[Consolidator] Finding semantic duplicates in tier %s", tier)
	
	// Get all memories in tier
	memories, err := c.storage.FindMemoriesForCompression(ctx, tier, 999999, 1000)
	if err != nil {
		return 0, fmt.Errorf("failed to fetch memories: %w", err)
	}
	
	if len(memories) < 2 {
		return 0, nil // Nothing to consolidate
	}
	
	log.Printf("[Consolidator] Analyzing %d memories for duplicates", len(memories))
	
	consolidatedCount := 0
	processedIDs := make(map[string]bool)
	
	for i := range memories {
		if processedIDs[memories[i].ID] {
			continue
		}
		
		// Find semantically similar memories (very high threshold = duplicates)
		duplicates := []Memory{}
		
		for j := i; j < len(memories); j++ {
			if processedIDs[memories[j].ID] {
				continue
			}
			
			// Calculate semantic similarity
			similarity := cosineSimilarity(memories[i].Embedding, memories[j].Embedding)
			
			// Very high threshold = expressing same idea
			if similarity > 0.95 {
				duplicates = append(duplicates, memories[j])
				processedIDs[memories[j].ID] = true
			}
		}
		
		// If we found 3+ duplicates, consolidate them
		if len(duplicates) >= 3 {
			consolidated, err := c.consolidateDuplicateSet(ctx, duplicates)
			if err != nil {
				log.Printf("[Consolidator] WARNING: Failed to consolidate %d duplicates: %v",
					len(duplicates), err)
				continue
			}
			
			consolidatedCount++
			log.Printf("[Consolidator] ✓ Consolidated %d duplicates → 1 authoritative memory (validation_count=%d)",
				len(duplicates), consolidated.ValidationCount)
		}
	}
	
	return consolidatedCount, nil
}

// consolidateDuplicateSet creates one authoritative memory from duplicates
func (c *Consolidator) consolidateDuplicateSet(ctx context.Context, duplicates []Memory) (*Memory, error) {
	if len(duplicates) == 0 {
		return nil, fmt.Errorf("empty duplicate set")
	}
	
	// Use the memory with highest importance as base
	baseMem := duplicates[0]
	for _, dup := range duplicates {
		if dup.ImportanceScore > baseMem.ImportanceScore {
			baseMem = dup
		}
	}
	
	// Create consolidated version
	consolidated := baseMem
	
	// Aggregate validation counts (this is evidence of pattern repetition)
	totalValidations := 0
	for _, dup := range duplicates {
		totalValidations += dup.ValidationCount
	}
	consolidated.ValidationCount = totalValidations
	
	// Recalculate trust score with higher validation
	// Bayesian: (good_validations + 2) / (total_validations + 4)
	goodValidations := float64(totalValidations) // Assume all are good if duplicated
	if consolidated.OutcomeTag == "bad" {
		goodValidations = 0
	} else if consolidated.OutcomeTag == "neutral" {
		goodValidations = float64(totalValidations) * 0.5
	}
	
	consolidated.TrustScore = (goodValidations + 2) / (float64(totalValidations) + 4)
	
	// Link all duplicates as related
	consolidated.RelatedMemories = make([]string, 0, len(duplicates)-1)
	for _, dup := range duplicates {
		if dup.ID != consolidated.ID {
			consolidated.RelatedMemories = append(consolidated.RelatedMemories, dup.ID)
		}
	}
	
	// Update storage
	if err := c.storage.UpdateMemory(ctx, &consolidated); err != nil {
		return nil, err
	}
	
	// Delete duplicate memories (keep only the consolidated one)
	for _, dup := range duplicates {
		if dup.ID != consolidated.ID {
			if err := c.storage.DeleteMemory(ctx, dup.ID); err != nil {
				log.Printf("[Consolidator] WARNING: Failed to delete duplicate %s: %v", dup.ID, err)
			}
		}
	}
	
	return &consolidated, nil
}

// cosineSimilarity calculates similarity between two embeddings
func cosineSimilarity(a, b []float32) float64 {
	if len(a) != len(b) || len(a) == 0 {
		return 0.0
	}
	
	var dotProduct, normA, normB float64
	for i := 0; i < len(a); i++ {
		dotProduct += float64(a[i]) * float64(b[i])
		normA += float64(a[i]) * float64(a[i])
		normB += float64(b[i]) * float64(b[i])
	}
	
	if normA == 0 || normB == 0 {
		return 0.0
	}
	
	return dotProduct / (math.Sqrt(normA) * math.Sqrt(normB))
}

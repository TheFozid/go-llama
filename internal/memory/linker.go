// internal/memory/linker.go
package memory

import (
	"context"
	"log"
	"time"
)

// Linker handles memory linking and co-occurrence tracking
type Linker struct {
	storage             *Storage
	similarityThreshold float64
	maxLinksPerMemory   int
}

// NewLinker creates a new linker instance
func NewLinker(storage *Storage, similarityThreshold float64, maxLinksPerMemory int) *Linker {
	return &Linker{
		storage:             storage,
		similarityThreshold: similarityThreshold,
		maxLinksPerMemory:   maxLinksPerMemory,
	}
}

// CreateLinks finds and establishes links between similar memories
// Called during compression to link memories in a cluster
func (l *Linker) CreateLinks(ctx context.Context, memories []Memory) error {
	if len(memories) <= 1 {
		return nil // Nothing to link
	}

	log.Printf("[Linker] Creating links for %d memories in cluster", len(memories))
	
	// For each memory in the cluster, link it to all others
	for i := range memories {
		for j := range memories {
			if i == j {
				continue // Don't link to self
			}
			
			// Add bidirectional link
			if err := l.addLink(&memories[i], memories[j].ID); err != nil {
				return err
			}
		}
	}
	
	// Update all memories in storage
	for i := range memories {
		if err := l.storage.UpdateMemory(ctx, &memories[i]); err != nil {
			log.Printf("[Linker] ERROR: Failed to update memory %s: %v", memories[i].ID, err)
			return err
		}
	}
	
	log.Printf("[Linker] Successfully linked %d memories", len(memories))
	return nil
}

// addLink adds a link from source memory to target memory ID
func (l *Linker) addLink(source *Memory, targetID string) error {
	// Check if link already exists
	for _, existingID := range source.RelatedMemories {
		if existingID == targetID {
			return nil // Link already exists
		}
	}
	
	// Enforce max links limit
	if len(source.RelatedMemories) >= l.maxLinksPerMemory {
		log.Printf("[Linker] Memory %s at max links (%d), skipping link to %s",
			source.ID, l.maxLinksPerMemory, targetID)
		return nil
	}
	
	// Add the link
	source.RelatedMemories = append(source.RelatedMemories, targetID)
	
	return nil
}

// TrackCoOccurrence increments co-retrieval count when memories are retrieved together
// Uses timestamp-based deduplication to prevent artificial inflation from rapid successive retrievals
func (l *Linker) TrackCoOccurrence(ctx context.Context, retrievedMemories []Memory) error {
	if len(retrievedMemories) <= 1 {
		return nil // Nothing to track
	}
	
	now := time.Now()
	throttleWindow := 1 * time.Hour // Only count co-occurrence if last occurrence was >1 hour ago
	
	// For each memory, track which other memories it was retrieved with
	for i := range retrievedMemories {
		updated := false
		
		// Initialize co_retrieval_counts if not exists
		if retrievedMemories[i].Metadata == nil {
			retrievedMemories[i].Metadata = make(map[string]interface{})
		}
		
		var coRetrievalCounts map[string]int
		var lastCoOccurrence map[string]int64 // Track last occurrence timestamp for each memory
		
		// Extract existing counts
		if existingCounts, ok := retrievedMemories[i].Metadata["co_retrieval_counts"]; ok {
			// Type assert existing counts
			if counts, ok := existingCounts.(map[string]int); ok {
				coRetrievalCounts = counts
			} else if counts, ok := existingCounts.(map[string]interface{}); ok {
				// Handle case where it's stored as map[string]interface{} from JSON
				coRetrievalCounts = make(map[string]int)
				for k, v := range counts {
					if intVal, ok := v.(int); ok {
						coRetrievalCounts[k] = intVal
					} else if floatVal, ok := v.(float64); ok {
						coRetrievalCounts[k] = int(floatVal)
					}
				}
			} else {
				coRetrievalCounts = make(map[string]int)
			}
		} else {
			coRetrievalCounts = make(map[string]int)
		}
		
		// Extract last occurrence timestamps
		if existingTimes, ok := retrievedMemories[i].Metadata["co_retrieval_last"]; ok {
			if times, ok := existingTimes.(map[string]int64); ok {
				lastCoOccurrence = times
			} else if times, ok := existingTimes.(map[string]interface{}); ok {
				lastCoOccurrence = make(map[string]int64)
				for k, v := range times {
					if intVal, ok := v.(int64); ok {
						lastCoOccurrence[k] = intVal
					} else if floatVal, ok := v.(float64); ok {
						lastCoOccurrence[k] = int64(floatVal)
					}
				}
			} else {
				lastCoOccurrence = make(map[string]int64)
			}
		} else {
			lastCoOccurrence = make(map[string]int64)
		}
		
		// Increment count for each co-retrieved memory (with throttling)
		for j := range retrievedMemories {
			if i == j {
				continue // Don't count self
			}
			
			linkedID := retrievedMemories[j].ID
			
			// Check if we should throttle this update
			if lastTime, exists := lastCoOccurrence[linkedID]; exists {
				timeSinceLastOccurrence := now.Sub(time.Unix(lastTime, 0))
				if timeSinceLastOccurrence < throttleWindow {
					// Too recent, skip increment
					log.Printf("[Linker] Throttling co-occurrence update for %s <-> %s (last: %s ago)",
						retrievedMemories[i].ID[:8], linkedID[:8], timeSinceLastOccurrence.Round(time.Minute))
					continue
				}
			}
			
			// Update count and timestamp
			coRetrievalCounts[linkedID]++
			lastCoOccurrence[linkedID] = now.Unix()
			updated = true
		}
		
		// Update metadata
		if updated {
			// Update in storage using lightweight co-occurrence update
			if err := l.storage.UpdateCoOccurrence(ctx, retrievedMemories[i].ID, coRetrievalCounts, lastCoOccurrence); err != nil {
				log.Printf("[Linker] WARNING: Failed to update co-occurrence for memory %s: %v",
					retrievedMemories[i].ID, err)
				// Continue with other memories even if one fails
			}
		}
	}
	
	return nil
}

// GetLinkStrength calculates the strength of a link between two memories
// Returns a value between 0.0 and 1.0 based on co-retrieval frequency
func (l *Linker) GetLinkStrength(memory *Memory, linkedMemoryID string) float64 {
	// Get co-retrieval counts from metadata
	if memory.Metadata == nil {
		return 0.0
	}
	
	coRetrievalCounts, ok := memory.Metadata["co_retrieval_counts"]
	if !ok {
		return 0.0
	}

	// Type assert to map
	var counts map[string]int
	switch v := coRetrievalCounts.(type) {
	case map[string]int:
		counts = v
	case map[string]interface{}:
		counts = make(map[string]int)
		for k, val := range v {
			if intVal, ok := val.(int); ok {
				counts[k] = intVal
			} else if floatVal, ok := val.(float64); ok {
				counts[k] = int(floatVal)
			}
		}
	default:
		return 0.0
	}
	
	coCount, exists := counts[linkedMemoryID]
	if !exists {
		return 0.0
	}
	
	// For new memories (AccessCount = 0), use co-retrieval count directly
	// Each co-retrieval contributes 0.2 strength, capped at 1.0
	if memory.AccessCount == 0 {
		strength := float64(coCount) * 0.2
		if strength > 1.0 {
			strength = 1.0
		}
		return strength
	}
	
	// For established memories, use ratio of co-retrieval to total accesses
	// Strength = co-retrieval count / total access count
	// Capped at 1.0
	strength := float64(coCount) / float64(memory.AccessCount)
	if strength > 1.0 {
		strength = 1.0
	}
	
	return strength
}

// FindClusters finds semantically similar memories for cluster-based compression
// Uses cosine similarity threshold from config
func (l *Linker) FindClusters(ctx context.Context, memory *Memory, tier MemoryTier, limit int) ([]Memory, error) {
	// Use the memory's embedding to find similar memories in the same tier
	cluster, err := l.storage.FindMemoryClusters(ctx, tier, memory.Embedding, l.similarityThreshold, limit)
	if err != nil {
		return nil, err
	}
	
	log.Printf("[Linker] Found %d similar memories for memory %s (tier=%s, threshold=%.2f)",
		len(cluster), memory.ID, tier, l.similarityThreshold)
	
	return cluster, nil
}

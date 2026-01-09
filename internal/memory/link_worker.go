package memory

import (
	"context"
	"log"
	"time"
)

// LinkWorker manages background memory linking
type LinkWorker struct {
	storage       *Storage
	linker        *Linker
	scheduleHours int
	stopChan      chan struct{}
}

// NewLinkWorker creates a new link worker
func NewLinkWorker(storage *Storage, linker *Linker, scheduleHours int) *LinkWorker {
	return &LinkWorker{
		storage:       storage,
		linker:        linker,
		scheduleHours: scheduleHours,
		stopChan:      make(chan struct{}),
	}
}

// Start begins the linking loop
func (w *LinkWorker) Start() {
	log.Printf("[LinkWorker] Starting link worker (runs every %d hours)", w.scheduleHours)

	ticker := time.NewTicker(time.Duration(w.scheduleHours) * time.Hour)
	defer ticker.Stop()

	// Run immediately on start
	w.runLinkingCycle()

	for {
		select {
		case <-ticker.C:
			w.runLinkingCycle()
		case <-w.stopChan:
			log.Printf("[LinkWorker] Stopping link worker")
			return
		}
	}
}

// Stop gracefully stops the worker
func (w *LinkWorker) Stop() {
	close(w.stopChan)
}

// runLinkingCycle performs one full linking cycle
func (w *LinkWorker) runLinkingCycle() {
	log.Printf("[LinkWorker] Starting linking cycle at %s", time.Now().Format(time.RFC3339))
	startTime := time.Now()
	ctx := context.Background()

	// Process each tier
	tiers := []MemoryTier{TierRecent, TierMedium, TierLong, TierAncient}
	totalLinksCreated := 0

	for _, tier := range tiers {
		linksCreated, err := w.linkMemoriesInTier(ctx, tier)
		if err != nil {
			log.Printf("[LinkWorker] ERROR processing tier %s: %v", tier, err)
			continue
		}
		totalLinksCreated += linksCreated
	}

	duration := time.Since(startTime)
	log.Printf("[LinkWorker] Linking cycle complete: %d links created (took %s)",
		totalLinksCreated, duration.Round(time.Second))
}

// linkMemoriesInTier finds and links similar memories within a tier
func (w *LinkWorker) linkMemoriesInTier(ctx context.Context, tier MemoryTier) (int, error) {
	log.Printf("[LinkWorker] Processing tier %s...", tier)

	// Get a sample of memories from this tier to analyze
	batchSize := 50 // Process 50 memories per tier per cycle
	memories, err := w.storage.FindMemoriesForCompression(ctx, tier, 0, batchSize)
	if err != nil {
		return 0, err
	}

	if len(memories) == 0 {
		log.Printf("[LinkWorker] No memories found in tier %s", tier)
		return 0, nil
	}

	log.Printf("[LinkWorker] Found %d memories in tier %s to analyze", len(memories), tier)

	linksCreated := 0

	// For each memory, find similar ones and create links
	for i := range memories {
		mem := &memories[i]

		// Skip if already at max links
		if len(mem.RelatedMemories) >= w.linker.maxLinksPerMemory {
			continue
		}

		// Find similar memories
		similar, err := w.linker.FindClusters(ctx, mem, tier, 10)
		if err != nil {
			log.Printf("[LinkWorker] WARNING: Failed to find clusters for memory %s: %v", mem.ID, err)
			continue
		}

		// Link to similar memories (excluding self)
		initialLinkCount := len(mem.RelatedMemories)
		for _, simMem := range similar {
			if simMem.ID == mem.ID {
				continue // Skip self
			}

			// Check if link already exists
			exists := false
			for _, existingID := range mem.RelatedMemories {
				if existingID == simMem.ID {
					exists = true
					break
				}
			}

			if !exists && len(mem.RelatedMemories) < w.linker.maxLinksPerMemory {
				mem.RelatedMemories = append(mem.RelatedMemories, simMem.ID)
			}
		}

		// Update memory if new links were added
		if len(mem.RelatedMemories) > initialLinkCount {
			newLinks := len(mem.RelatedMemories) - initialLinkCount
			if err := w.storage.UpdateLinks(ctx, mem.ID, mem.RelatedMemories); err != nil {
				log.Printf("[LinkWorker] WARNING: Failed to update links for memory %s: %v", mem.ID, err)
				continue
			}
			linksCreated += newLinks
			log.Printf("[LinkWorker] Added %d links to memory %s", newLinks, mem.ID[:8])
		}
	}

	log.Printf("[LinkWorker] Tier %s complete: %d links created", tier, linksCreated)
	return linksCreated, nil
}

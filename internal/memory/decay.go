// internal/memory/decay.go
package memory

import (
	"context"
	"log"
	"math"
	"time"

	"gorm.io/gorm"
)

// DecayWorker manages the background compression and principle evolution process
type DecayWorker struct {
	storage                *Storage
	compressor             *Compressor
	embedder               *Embedder
	tagger                 *Tagger
	db                     *gorm.DB
	scheduleHours          int
	principleScheduleHours int
	minRatingThreshold     float64
	tierRules              TierRules
	importanceMod          float64
	accessMod              float64
	stopChan               chan struct{}
	lastPrincipleEvolution time.Time
}

// TierRules defines age thresholds for tier transitions
type TierRules struct {
	RecentToMediumDays int
	MediumToLongDays   int
	LongToAncientDays  int
}

// NewDecayWorker creates a new background compression worker
func NewDecayWorker(
	storage *Storage,
	compressor *Compressor,
	embedder *Embedder,
	tagger *Tagger,
	db *gorm.DB,
	scheduleHours int,
	principleScheduleHours int,
	minRatingThreshold float64,
	tierRules TierRules,
	importanceMod float64,
	accessMod float64,
) *DecayWorker {
	return &DecayWorker{
		storage:                storage,
		compressor:             compressor,
		embedder:               embedder,
		tagger:                 tagger,
		db:                     db,
		scheduleHours:          scheduleHours,
		principleScheduleHours: principleScheduleHours,
		minRatingThreshold:     minRatingThreshold,
		tierRules:              tierRules,
		importanceMod:          importanceMod,
		accessMod:              accessMod,
		stopChan:               make(chan struct{}),
		lastPrincipleEvolution: time.Now(), // Initialize to now
	}
}

// Start begins the background compression loop
func (w *DecayWorker) Start() {
	log.Printf("[DecayWorker] Starting compression worker (runs every %d hours)", w.scheduleHours)
	log.Printf("[DecayWorker] Principle evolution runs every %d hours", w.principleScheduleHours)

	ticker := time.NewTicker(time.Duration(w.scheduleHours) * time.Hour)
	defer ticker.Stop()

	// Run immediately on start
	w.runCompressionCycle()

	for {
		select {
		case <-ticker.C:
			w.runCompressionCycle()
		case <-w.stopChan:
			log.Printf("[DecayWorker] Stopping compression worker")
			return
		}
	}
}

// Stop gracefully stops the worker
func (w *DecayWorker) Stop() {
	close(w.stopChan)
}

// runCompressionCycle performs one full compression cycle
func (w *DecayWorker) runCompressionCycle() {
	log.Printf("[DecayWorker] Starting compression cycle at %s", time.Now().Format(time.RFC3339))
	startTime := time.Now()

	ctx := context.Background()

	// PHASE 1: Tag untagged memories
	log.Println("[DecayWorker] PHASE 1: Tagging untagged memories...")
	if err := w.tagger.TagMemories(ctx, w.storage); err != nil {
		log.Printf("[DecayWorker] ERROR in tagging phase: %v", err)
	}

	// PHASE 2: Compress old memories
	log.Println("[DecayWorker] PHASE 2: Compressing old memories...")

	// Compress Recent -> Medium
	w.compressTier(ctx, TierRecent, TierMedium, w.tierRules.RecentToMediumDays)

	// Compress Medium -> Long
	w.compressTier(ctx, TierMedium, TierLong, w.tierRules.MediumToLongDays)

	// Compress Long -> Ancient
	w.compressTier(ctx, TierLong, TierAncient, w.tierRules.LongToAncientDays)

	// PHASE 3: Evolve principles (only if schedule interval has passed)
	timeSinceLastEvolution := time.Since(w.lastPrincipleEvolution)
	principleInterval := time.Duration(w.principleScheduleHours) * time.Hour

	if timeSinceLastEvolution >= principleInterval {
		log.Printf("[DecayWorker] PHASE 3: Evolving principles (last evolution: %s ago)...",
			timeSinceLastEvolution.Round(time.Hour))

		if err := w.evolvePrinciplesPhase(ctx); err != nil {
			log.Printf("[DecayWorker] ERROR in principle evolution phase: %v", err)
		} else {
			w.lastPrincipleEvolution = time.Now()
		}
	} else {
		timeUntilNext := principleInterval - timeSinceLastEvolution
		log.Printf("[DecayWorker] PHASE 3: Skipping principle evolution (next in %s)",
			timeUntilNext.Round(time.Hour))
	}

	duration := time.Since(startTime)
	log.Printf("[DecayWorker] Compression cycle complete (took %s)", duration.Round(time.Second))
}

// evolvePrinciplesPhase runs the principle evolution process
func (w *DecayWorker) evolvePrinciplesPhase(ctx context.Context) error {
	// Extract principle candidates from memory patterns
	candidates, err := ExtractPrinciples(w.db, w.storage, w.minRatingThreshold)
	if err != nil {
		return err
	}

	if len(candidates) == 0 {
		log.Printf("[DecayWorker] No principle candidates found")
		return nil
	}

	log.Printf("[DecayWorker] Found %d principle candidates", len(candidates))

	// Evolve principles (update slots 4-10 with best candidates)
	return EvolvePrinciples(w.db, candidates, w.minRatingThreshold)
}

// compressTier finds and compresses memories from one tier to another
func (w *DecayWorker) compressTier(ctx context.Context, fromTier, toTier MemoryTier, baseAgeDays int) {
	log.Printf("[DecayWorker] Processing %s -> %s (base age: %d days)", fromTier, toTier, baseAgeDays)

	// Find memories eligible for compression (limit to 100 per tier per run)
	memories, err := w.storage.FindMemoriesForCompression(ctx, fromTier, baseAgeDays, 100)
	if err != nil {
		log.Printf("[DecayWorker] ERROR: Failed to find memories for compression: %v", err)
		return
	}

	if len(memories) == 0 {
		log.Printf("[DecayWorker] No memories found for %s -> %s", fromTier, toTier)
		return
	}

	log.Printf("[DecayWorker] Found %d memories eligible for %s -> %s", len(memories), fromTier, toTier)

	compressed := 0
	skipped := 0

	for _, memory := range memories {
		// Calculate adjusted age based on importance and access count
		adjustedAgeDays := w.calculateAdjustedAge(&memory, baseAgeDays)

		// Skip if adjusted age doesn't meet threshold
		if adjustedAgeDays < float64(baseAgeDays) {
			skipped++
			continue
		}

		// Compress the memory
		compressedMemory, err := w.compressor.Compress(ctx, &memory, toTier)
		if err != nil {
			log.Printf("[DecayWorker] ERROR: Failed to compress memory %s: %v", memory.ID, err)
			continue
		}

		// Regenerate embedding for compressed content
		newEmbedding, err := w.embedder.Embed(ctx, compressedMemory.Content)
		if err != nil {
			log.Printf("[DecayWorker] ERROR: Failed to generate embedding for compressed memory %s: %v", memory.ID, err)
			continue
		}
		compressedMemory.Embedding = newEmbedding

		// Update in storage
		if err := w.storage.UpdateMemory(ctx, compressedMemory); err != nil {
			log.Printf("[DecayWorker] ERROR: Failed to update memory %s: %v", memory.ID, err)
			continue
		}

		compressed++

		// Log progress every 10 memories
		if compressed%10 == 0 {
			log.Printf("[DecayWorker] Progress: %d/%d compressed", compressed, len(memories))
		}
	}

	log.Printf("[DecayWorker] %s -> %s complete: %d compressed, %d skipped (protected by importance/access)",
		fromTier, toTier, compressed, skipped)
}

// calculateAdjustedAge applies importance and access modifiers to memory age
func (w *DecayWorker) calculateAdjustedAge(memory *Memory, baseAgeDays int) float64 {
	realAgeDays := time.Since(memory.CreatedAt).Hours() / 24.0

	// Importance modifier: higher importance = slower aging
	// importanceMod of 2.0 means importance of 1.0 doubles the effective age threshold
	importanceFactor := 1.0 + (memory.ImportanceScore * w.importanceMod)

	// Access modifier: more accesses = slower aging
	// accessMod of 1.5 means each access adds 1.5x protection
	accessFactor := 1.0 + (math.Log1p(float64(memory.AccessCount)) * w.accessMod)

	// Combined modifier
	protectionFactor := importanceFactor * accessFactor

	// Adjusted age = real age / protection factor
	// Higher protection = lower adjusted age = less likely to compress
	adjustedAge := realAgeDays / protectionFactor

	return adjustedAge
}

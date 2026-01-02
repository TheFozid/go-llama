// internal/memory/decay.go
package memory

import (
	"context"
	"log"
	"math"
	"time"
	"sync"

	"gorm.io/gorm"
)

// DecayWorker manages the background compression and principle evolution process
type DecayWorker struct {
	storage                *Storage
	compressor             *Compressor
	embedder               *Embedder
	tagger                 *Tagger
	linker                 *Linker
	db                     *gorm.DB
	scheduleHours          int
	principleScheduleHours int
	minRatingThreshold     float64
	extractionLimit        int        // Max memories to analyze for principles
	tierRules              TierRules
	mergeWindows           MergeWindows
	importanceMod          float64
	accessMod              float64
	stopChan               chan struct{}
	lastPrincipleEvolution time.Time
	evolutionMutex         sync.Mutex // Protects lastPrincipleEvolution
	migrationComplete      bool       // One-time memory_id migration flag
}

// TierRules defines age thresholds for tier transitions
type TierRules struct {
	RecentToMediumDays int
	MediumToLongDays   int
	LongToAncientDays  int
}

// MergeWindows defines time windows for cluster-based compression
type MergeWindows struct {
	RecentDays int // Merge memories within N days for Recent tier
	MediumDays int // Merge memories within N days for Medium tier
	LongDays   int // Merge memories within N days for Long tier
}

// NewDecayWorker creates a new background compression worker
func NewDecayWorker(
	storage *Storage,
	compressor *Compressor,
	embedder *Embedder,
	tagger *Tagger,
	linker *Linker,
	db *gorm.DB,
	scheduleHours int,
	principleScheduleHours int,
	minRatingThreshold float64,
	extractionLimit int,
	tierRules TierRules,
	mergeWindows MergeWindows,
	importanceMod float64,
	accessMod float64,
) *DecayWorker {
	return &DecayWorker{
		storage:                storage,
		compressor:             compressor,
		embedder:               embedder,
		tagger:                 tagger,
		linker:                 linker,
		db:                     db,
		scheduleHours:          scheduleHours,
		principleScheduleHours: principleScheduleHours,
		minRatingThreshold:     minRatingThreshold,
		extractionLimit:        extractionLimit,
		tierRules:              tierRules,
		mergeWindows:           mergeWindows,
		importanceMod:          importanceMod,
		accessMod:              accessMod,
		stopChan:               make(chan struct{}),
		lastPrincipleEvolution: time.Now(), // Initialize to now
		migrationComplete:      false, // Will run on first cycle
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
	
	// PHASE 0: One-time migration (runs only on first cycle)
	if !w.migrationComplete {
		log.Println("[DecayWorker] PHASE 0: Running one-time memory_id migration...")
		if err := w.storage.MigrateMemoryIDs(ctx); err != nil {
			log.Printf("[DecayWorker] ERROR in migration phase: %v", err)
		} else {
			w.migrationComplete = true
			log.Println("[DecayWorker] ✓ Migration complete, will not run again")
		}
	}
	
	// PHASE 1: Tag untagged memories
	log.Println("[DecayWorker] PHASE 1: Tagging untagged memories...")
	if err := w.tagger.TagMemories(ctx, w.storage); err != nil {
		log.Printf("[DecayWorker] ERROR in tagging phase: %v", err)
	}

	// PHASE 2: Cluster-based compression of old memories
	log.Println("[DecayWorker] PHASE 2: Cluster-based compression...")

	// Compress Recent -> Medium
	w.compressTierWithClusters(ctx, TierRecent, TierMedium, w.tierRules.RecentToMediumDays, w.mergeWindows.RecentDays)

	// Compress Medium -> Long
	w.compressTierWithClusters(ctx, TierMedium, TierLong, w.tierRules.MediumToLongDays, w.mergeWindows.MediumDays)

	// Compress Long -> Ancient
	w.compressTierWithClusters(ctx, TierLong, TierAncient, w.tierRules.LongToAncientDays, w.mergeWindows.LongDays)

	// PHASE 3: Evolve principles (only if schedule interval has passed)
	w.evolutionMutex.Lock()
	timeSinceLastEvolution := time.Since(w.lastPrincipleEvolution)
	principleInterval := time.Duration(w.principleScheduleHours) * time.Hour
	shouldEvolve := timeSinceLastEvolution >= principleInterval
	w.evolutionMutex.Unlock()
	
	if shouldEvolve {
		log.Printf("[DecayWorker] PHASE 3: Evolving principles (last evolution: %s ago)...",
			timeSinceLastEvolution.Round(time.Hour))
		
		if err := w.evolvePrinciplesPhase(ctx); err != nil {
			log.Printf("[DecayWorker] ERROR in principle evolution phase: %v", err)
		} else {
			// Update timestamp with mutex protection
			w.evolutionMutex.Lock()
			w.lastPrincipleEvolution = time.Now()
			w.evolutionMutex.Unlock()
			log.Printf("[DecayWorker] ✓ Principle evolution timestamp updated")
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
	// Note: extractionLimit comes from config (passed during initialization)
	candidates, err := ExtractPrinciples(w.db, w.storage, w.minRatingThreshold, w.extractionLimit)
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

// compressTierWithClusters finds and compresses memories using cluster-based approach
func (w *DecayWorker) compressTierWithClusters(ctx context.Context, fromTier, toTier MemoryTier, baseAgeDays int, mergeWindowDays int) {
	log.Printf("[DecayWorker] Processing %s -> %s (base age: %d days, merge window: %d days)",
		fromTier, toTier, baseAgeDays, mergeWindowDays)

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
	clustered := 0
	processedIDs := make(map[string]bool) // Track which memories we've already processed

	for _, memory := range memories {
		// Skip if already processed as part of a cluster
		if processedIDs[memory.ID] {
			continue
		}

		// Calculate adjusted age based on importance and access count
		adjustedAgeDays := w.calculateAdjustedAge(&memory, baseAgeDays)

		// Skip if adjusted age doesn't meet threshold
		if adjustedAgeDays < float64(baseAgeDays) {
			skipped++
			processedIDs[memory.ID] = true
			continue
		}

		// Find similar memories for clustering
		cluster, err := w.linker.FindClusters(ctx, &memory, fromTier, 10)
		if err != nil {
			log.Printf("[DecayWorker] WARNING: Failed to find cluster for memory %s: %v", memory.ID, err)
			// Fall back to individual compression
			cluster = []Memory{memory}
		}

		// Filter cluster to only include memories within merge window
		validCluster := []Memory{}
		for _, clusterMem := range cluster {
			// Skip if already processed
			if processedIDs[clusterMem.ID] {
				continue
			}

			// Check if within temporal merge window
			timeDiff := math.Abs(float64(memory.CreatedAt.Sub(clusterMem.CreatedAt).Hours() / 24))
			if timeDiff <= float64(mergeWindowDays) {
				// Also check adjusted age
				clusterAdjustedAge := w.calculateAdjustedAge(&clusterMem, baseAgeDays)
				if clusterAdjustedAge >= float64(baseAgeDays) {
					validCluster = append(validCluster, clusterMem)
				}
			}
		}

		if len(validCluster) == 0 {
			// No valid cluster members, skip
			skipped++
			processedIDs[memory.ID] = true
			continue
		}

		// Compress the cluster
		var compressedMemory *Memory
		if len(validCluster) > 1 {
			log.Printf("[DecayWorker] Compressing cluster of %d memories", len(validCluster))
			compressedMemory, err = w.compressor.CompressCluster(ctx, validCluster, toTier)
			clustered += len(validCluster)
		} else {
			// Single memory, use regular compression
			compressedMemory, err = w.compressor.Compress(ctx, &validCluster[0], toTier)
		}

		if err != nil {
			log.Printf("[DecayWorker] ERROR: Failed to compress cluster: %v", err)
			for _, mem := range validCluster {
				processedIDs[mem.ID] = true
			}
			continue
		}

		// Regenerate embedding for compressed content
		newEmbedding, err := w.embedder.Embed(ctx, compressedMemory.Content)
		if err != nil {
			log.Printf("[DecayWorker] ERROR: Failed to generate embedding for compressed memory %s: %v",
				compressedMemory.ID, err)
			for _, mem := range validCluster {
				processedIDs[mem.ID] = true
			}
			continue
		}
		compressedMemory.Embedding = newEmbedding
		
		// Update in storage
		if err := w.storage.UpdateMemory(ctx, compressedMemory); err != nil {
			log.Printf("[DecayWorker] ERROR: Failed to update memory %s: %v", compressedMemory.ID, err)
			for _, mem := range validCluster {
				processedIDs[mem.ID] = true
			}
			continue
		}
		
		// Delete the other cluster members (all except the first one which was merged into)
		deletedCount := 0
		for i := 1; i < len(validCluster); i++ {
			if err := w.storage.DeleteMemory(ctx, validCluster[i].ID); err != nil {
				log.Printf("[DecayWorker] WARNING: Failed to delete merged memory %s: %v",
					validCluster[i].ID, err)
			} else {
				deletedCount++
			}
		}
		
		if deletedCount > 0 {
			log.Printf("[DecayWorker] ✓ Deleted %d merged memories from cluster", deletedCount)
		}
		
		// Mark all cluster members as processed
		for _, mem := range validCluster {
			processedIDs[mem.ID] = true
		}
		
		compressed++
		
		// Log progress every 10 compressions
		if compressed%10 == 0 {
			log.Printf("[DecayWorker] Progress: %d compressions (%d memories clustered, %d deleted)", 
				compressed, clustered, deletedCount)
		}
	}

	log.Printf("[DecayWorker] %s -> %s complete: %d compressions (%d memories in clusters), %d skipped",
		fromTier, toTier, compressed, clustered, skipped)
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

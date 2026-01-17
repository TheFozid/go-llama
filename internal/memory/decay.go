// internal/memory/decay.go
package memory

import (
	"context"
	"fmt"
	"log"
	"math"
	"sort"
	"time"
	"sync"

	"gorm.io/gorm"
)

// TaggerQueueInterface defines the interface for async tagging
type TaggerQueueInterface interface {
	EnqueueBatch(memoryIDs []string)
	GetStats() TaggerStats
}

// StorageLimits defines space-based compression configuration
type StorageLimits struct {
	MaxTotalMemories   int
	TierAllocation     TierAllocation
	CompressionTrigger float64
	AllowTierOverflow  bool
}

// TierAllocation defines percentage allocation for each tier
type TierAllocation struct {
	Recent  float64
	Medium  float64
	Long    float64
	Ancient float64
}

// CompressionWeights defines scoring weights for compression candidate selection
type CompressionWeights struct {
	Age        float64
	Importance float64
	Access     float64
}

// DecayWorker manages the background compression and principle evolution process
type DecayWorker struct {
	storage                *Storage
	compressor             *Compressor
	embedder               *Embedder
	taggerQueue            TaggerQueueInterface
	linker                 *Linker
	db                     *gorm.DB
	llmURL                 string     // LLM URL for principle generation
	llmModel               string     // LLM model name for principle generation
	llmClient              interface{} // LLM queue client for principle generation
	scheduleHours          int
	principleScheduleHours int
	minRatingThreshold     float64
	extractionLimit        int        // Max memories to analyze for principles
	tierRules              TierRules  // DEPRECATED: kept for backwards compatibility
	mergeWindows           MergeWindows
	importanceMod          float64    // DEPRECATED: kept for backwards compatibility
	accessMod              float64    // DEPRECATED: kept for backwards compatibility
	
	// Space-based compression configuration
	storageLimits          StorageLimits
	compressionWeights     CompressionWeights
	
	stopChan               chan struct{}
	lastPrincipleEvolution time.Time
	evolutionMutex         sync.Mutex // Protects lastPrincipleEvolution
	migrationComplete      bool       // One-time memory_id migration flag (in-memory only, check DB on start)
	db                     *gorm.DB   // Database handle for migration status
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
	taggerQueue TaggerQueueInterface,
	linker *Linker,
	db *gorm.DB,
	llmURL string,
	llmModel string,
	llmClient interface{}, // NEW: LLM queue client
	scheduleHours int,
	principleScheduleHours int,
	minRatingThreshold float64,
	extractionLimit int,
	tierRules TierRules,           // DEPRECATED: kept for backwards compatibility
	mergeWindows MergeWindows,
	importanceMod float64,          // DEPRECATED: kept for backwards compatibility
	accessMod float64,              // DEPRECATED: kept for backwards compatibility
	storageLimits StorageLimits,   // NEW: space-based compression config
	compressionWeights CompressionWeights, // NEW: compression scoring weights
) *DecayWorker {
	return &DecayWorker{
		storage:                storage,
		compressor:             compressor,
		embedder:               embedder,
		taggerQueue:            taggerQueue,
		linker:                 linker,
		db:                     db,
		llmURL:                 llmURL,
		llmModel:               llmModel,
		llmClient:              llmClient, // NEW: Store LLM client
		scheduleHours:          scheduleHours,
		principleScheduleHours: principleScheduleHours,
		minRatingThreshold:     minRatingThreshold,
		extractionLimit:        extractionLimit,
		tierRules:              tierRules,        // DEPRECATED
		mergeWindows:           mergeWindows,
		importanceMod:          importanceMod,    // DEPRECATED
		accessMod:              accessMod,        // DEPRECATED
		storageLimits:          storageLimits,
		compressionWeights:     compressionWeights,
		stopChan:               make(chan struct{}),
		lastPrincipleEvolution: time.Now(), // Initialize to now
		migrationComplete:      false, // Will check DB on first cycle
		db:                     db,
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

// runCompressionCycle performs one full compression cycle (space-based)
func (w *DecayWorker) runCompressionCycle() {
	log.Printf("[DecayWorker] Starting compression cycle at %s", time.Now().Format(time.RFC3339))
	startTime := time.Now()
	ctx := context.Background()
	
	// PHASE 0: One-time migration (check DB status, run if needed)
	if !w.migrationComplete {
		// Check database to see if migration already ran
		var state struct {
			MigrationMemoryIDComplete bool
		}
		err := w.db.Table("growerai_dialogue_state").
			Select("migration_memory_id_complete").
			Where("id = ?", 1).
			First(&state).Error
		
		if err != nil {
			log.Printf("[DecayWorker] WARNING: Could not check migration status: %v", err)
		} else if state.MigrationMemoryIDComplete {
			w.migrationComplete = true
			log.Println("[DecayWorker] ✓ Migration already completed (from DB), skipping")
		} else {
			log.Println("[DecayWorker] PHASE 0: Running one-time memory_id migration...")
			if err := w.storage.MigrateMemoryIDs(ctx); err != nil {
				log.Printf("[DecayWorker] ERROR in migration phase: %v", err)
			} else {
				w.migrationComplete = true
				
				// Mark migration as complete in database
				err := w.db.Table("growerai_dialogue_state").
					Where("id = ?", 1).
					Update("migration_memory_id_complete", true).Error
				
				if err != nil {
					log.Printf("[DecayWorker] WARNING: Failed to persist migration status: %v", err)
				} else {
					log.Println("[DecayWorker] ✓ Migration complete and persisted to DB")
				}
			}
		}
	}
	
	// PHASE 1: Enqueue untagged memories for async tagging
	log.Println("[DecayWorker] PHASE 1: Tagging untagged memories...")
	
	// Find untagged memories
	untagged, err := w.storage.FindUntaggedMemories(ctx, 1000) // Get up to 1000
	if err != nil {
		log.Printf("[DecayWorker] WARNING: Failed to find untagged memories: %v", err)
	} else if len(untagged) > 0 {
		// Extract memory IDs
		memoryIDs := make([]string, len(untagged))
		for i, mem := range untagged {
			memoryIDs[i] = mem.ID
		}
		
		// Enqueue for async processing (non-blocking)
		w.taggerQueue.EnqueueBatch(memoryIDs)
		log.Printf("[DecayWorker] ✓ Enqueued %d memories for tagging", len(memoryIDs))
		
		// Log current queue stats
		stats := w.taggerQueue.GetStats()
		log.Printf("[DecayWorker] Tagger queue stats: pending=%d, processed=%d, failed=%d",
			stats.CurrentQueue, stats.Processed, stats.Failed)
	} else {
		log.Println("[DecayWorker] No untagged memories found")
	}
	
	// PHASE 2: Space-based compression
	log.Println("[DecayWorker] PHASE 2: Space-based compression check...")
	if err := w.runSpaceBasedCompression(ctx); err != nil {
		log.Printf("[DecayWorker] ERROR in compression phase: %v", err)
	}
	
	// PHASE 3: Prune weak links
	log.Println("[DecayWorker] PHASE 3: Pruning weak links...")
	if err := w.pruneWeakLinksPhase(ctx); err != nil {
		log.Printf("[DecayWorker] ERROR in link pruning phase: %v", err)
	}
	
	// PHASE 4: Recalculate trust scores
	log.Println("[DecayWorker] PHASE 4: Recalculating trust scores...")
	if err := w.recalculateTrustScores(ctx); err != nil {
		log.Printf("[DecayWorker] ERROR in trust recalculation phase: %v", err)
	}
	// PHASE 4.5: Semantic deduplication (consolidate duplicate memories)
	log.Println("[DecayWorker] PHASE 4.5: Consolidating duplicate memories...")
	if err := w.consolidateDuplicatesPhase(ctx); err != nil {
		log.Printf("[DecayWorker] ERROR in consolidation phase: %v", err)
	}

	
	// PHASE 5: Evolve principles (only if schedule interval has passed)
	w.evolutionMutex.Lock()
	timeSinceLastEvolution := time.Since(w.lastPrincipleEvolution)
	principleInterval := time.Duration(w.principleScheduleHours) * time.Hour
	shouldEvolve := timeSinceLastEvolution >= principleInterval
	
	// Log detailed timing info for debugging
	log.Printf("[DecayWorker] Principle evolution check: last=%s, elapsed=%s, interval=%s, shouldEvolve=%v",
		w.lastPrincipleEvolution.Format("2006-01-02 15:04:05"),
		timeSinceLastEvolution.Round(time.Minute),
		principleInterval,
		shouldEvolve)
	w.evolutionMutex.Unlock()
	
	if shouldEvolve {
		log.Printf("[DecayWorker] PHASE 5: Evolving principles (last evolution: %s ago)...",
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
		log.Printf("[DecayWorker] PHASE 5: Skipping principle evolution (next in %s)",
			timeUntilNext.Round(time.Hour))
	}
	
	duration := time.Since(startTime)
	log.Printf("[DecayWorker] Compression cycle complete (took %s)", duration.Round(time.Second))
}

// runSpaceBasedCompression checks each tier's space usage and compresses if needed
func (w *DecayWorker) runSpaceBasedCompression(ctx context.Context) error {
	// Get current memory counts per tier
	tierCounts, err := w.storage.GetTierCounts(ctx)
	if err != nil {
		return fmt.Errorf("failed to get tier counts: %w", err)
	}
	
	totalCount, err := w.storage.GetTotalMemoryCount(ctx)
	if err != nil {
		return fmt.Errorf("failed to get total count: %w", err)
	}
	
	log.Printf("[DecayWorker] Current memory distribution: Total=%d, Recent=%d, Medium=%d, Long=%d, Ancient=%d",
		totalCount,
		tierCounts[TierRecent],
		tierCounts[TierMedium],
		tierCounts[TierLong],
		tierCounts[TierAncient])
	
	// Calculate tier limits
	tierLimits := map[MemoryTier]int{
		TierRecent:  int(float64(w.storageLimits.MaxTotalMemories) * w.storageLimits.TierAllocation.Recent),
		TierMedium:  int(float64(w.storageLimits.MaxTotalMemories) * w.storageLimits.TierAllocation.Medium),
		TierLong:    int(float64(w.storageLimits.MaxTotalMemories) * w.storageLimits.TierAllocation.Long),
		TierAncient: int(float64(w.storageLimits.MaxTotalMemories) * w.storageLimits.TierAllocation.Ancient),
	}
	
	log.Printf("[DecayWorker] Tier limits: Recent=%d, Medium=%d, Long=%d, Ancient=%d",
		tierLimits[TierRecent], tierLimits[TierMedium], tierLimits[TierLong], tierLimits[TierAncient])
	
	// Check each tier and compress if needed
	tiers := []struct {
		current MemoryTier
		target  MemoryTier
	}{
		{TierRecent, TierMedium},
		{TierMedium, TierLong},
		{TierLong, TierAncient},
	}
	
	for _, tierPair := range tiers {
		currentTier := tierPair.current
		targetTier := tierPair.target
		
		currentCount := tierCounts[currentTier]
		tierLimit := tierLimits[currentTier]
		triggerThreshold := int(float64(tierLimit) * w.storageLimits.CompressionTrigger)
		
		// Check if compression is needed
		if currentCount < triggerThreshold {
			log.Printf("[DecayWorker] Tier %s: %d/%d (%.1f%%) - below trigger threshold (%d), skipping",
				currentTier, currentCount, tierLimit,
				float64(currentCount)/float64(tierLimit)*100, triggerThreshold)
			continue
		}
		
		log.Printf("[DecayWorker] Tier %s: %d/%d (%.1f%%) - EXCEEDS trigger threshold (%d), compressing to %s",
			currentTier, currentCount, tierLimit,
			float64(currentCount)/float64(tierLimit)*100, triggerThreshold, targetTier)
		
		// Calculate target count (compress down to 80% of limit for breathing room)
		targetCount := int(float64(tierLimit) * 0.80)
		
		// Select memories for compression based on scoring
		candidates, err := w.selectMemoriesForCompression(ctx, currentTier, currentCount, tierLimit, targetCount)
		if err != nil {
			log.Printf("[DecayWorker] ERROR selecting memories for compression: %v", err)
			continue
		}
		
		if len(candidates) == 0 {
			log.Printf("[DecayWorker] No candidates selected for compression in tier %s", currentTier)
			continue
		}
		
		// Compress candidates using cluster-based approach
		compressed, clustered := w.compressMemoriesWithClusters(ctx, candidates, targetTier)
		
		log.Printf("[DecayWorker] %s -> %s complete: %d compressions (%d memories in clusters)",
			currentTier, targetTier, compressed, clustered)
	}
	
	// Final status report
	finalCounts, _ := w.storage.GetTierCounts(ctx)
	finalTotal, _ := w.storage.GetTotalMemoryCount(ctx)
	
	log.Printf("[DecayWorker] Final memory distribution: Total=%d, Recent=%d, Medium=%d, Long=%d, Ancient=%d",
		finalTotal,
		finalCounts[TierRecent],
		finalCounts[TierMedium],
		finalCounts[TierLong],
		finalCounts[TierAncient])
	
	return nil
}

// compressMemoriesWithClusters compresses a list of candidate memories using cluster-based approach
// Returns: (number of compressions, total memories in clusters)
func (w *DecayWorker) compressMemoriesWithClusters(ctx context.Context, candidates []Memory, targetTier MemoryTier) (int, int) {
	compressed := 0
	clustered := 0
	processedIDs := make(map[string]bool)
	
	for _, memory := range candidates {
		// Skip if already processed as part of a cluster
		if processedIDs[memory.ID] {
			continue
		}
		
		// Find similar memories for clustering
		cluster, err := w.linker.FindClusters(ctx, &memory, memory.Tier, 10)
		if err != nil {
			log.Printf("[DecayWorker] WARNING: Failed to find cluster for memory %s: %v", memory.ID, err)
			cluster = []Memory{memory}
		}
		
		// Filter cluster to only include memories from candidates list
		candidateIDs := make(map[string]bool)
		for _, c := range candidates {
			candidateIDs[c.ID] = true
		}
		
		validCluster := []Memory{}
		for _, clusterMem := range cluster {
			if processedIDs[clusterMem.ID] {
				continue
			}
			if candidateIDs[clusterMem.ID] {
				validCluster = append(validCluster, clusterMem)
			}
		}
		
		if len(validCluster) == 0 {
			continue
		}
		
		// Compress the cluster
		var compressedMemory *Memory
		if len(validCluster) > 1 {
			log.Printf("[DecayWorker] Compressing cluster of %d memories", len(validCluster))
			compressedMemory, err = w.compressor.CompressCluster(ctx, validCluster, targetTier)
			clustered += len(validCluster)
		} else {
			// Single memory, use regular compression
			compressedMemory, err = w.compressor.Compress(ctx, &validCluster[0], targetTier)
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
			log.Printf("[DecayWorker] Progress: %d compressions (%d memories in clusters)", compressed, clustered)
		}
	}
	
	return compressed, clustered
}

// evolvePrinciplesPhase runs the principle and identity evolution process
func (w *DecayWorker) evolvePrinciplesPhase(ctx context.Context) error {
	// Sub-phase A: Evolve system identity (slot 0)
	log.Printf("[DecayWorker] Sub-phase A: Identity evolution...")
		if err := EvolveIdentity(w.db, w.storage, w.embedder, w.llmURL, w.llmModel, w.llmClient); err != nil {
		log.Printf("[DecayWorker] ERROR evolving identity: %v", err)
		// Non-fatal, continue to principle evolution
	}
	
	// Sub-phase B: Extract principle candidates from memory patterns
	log.Printf("[DecayWorker] Sub-phase B: Principle extraction...")
	candidates, err := ExtractPrinciples(w.db, w.storage, w.embedder, w.minRatingThreshold, w.extractionLimit, w.llmURL, w.llmModel, w.llmClient)
	if err != nil {
		return err
	}

	if len(candidates) == 0 {
		log.Printf("[DecayWorker] No principle candidates found")
		return nil
	}

	log.Printf("[DecayWorker] Found %d principle candidates", len(candidates))

	// Sub-phase C: Evolve principles (update slots 4-10 with best candidates)
	log.Printf("[DecayWorker] Sub-phase C: Principle evolution...")
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

// calculateCompressionScore computes a score for prioritizing memories for compression
// Higher score = more likely to be compressed
// Factors: age (older = higher), importance (lower = higher), access (less = higher)
func (w *DecayWorker) calculateCompressionScore(memory *Memory, weights struct{ Age, Importance, Access float64 }) float64 {
	now := time.Now()
	
	// 1. Age component (0.0 to 1.0, normalized to max 365 days)
	ageDays := now.Sub(memory.CreatedAt).Hours() / 24.0
	normalizedAge := math.Min(ageDays/365.0, 1.0) // Cap at 1 year
	
	// 2. Importance component (inverted: low importance = high score)
	// ImportanceScore is 0.0-1.0, we want (1 - importance)
	importanceComponent := 1.0 - memory.ImportanceScore
	
	// 3. Access component (inverted: low access = high score)
	// Use logarithmic scale to handle high access counts
	// Formula: 1 / (1 + log(1 + access_count))
	accessComponent := 1.0 / (1.0 + math.Log1p(float64(memory.AccessCount)))
	
	// Weighted sum
	score := (weights.Age * normalizedAge) +
		(weights.Importance * importanceComponent) +
		(weights.Access * accessComponent)
	
	return score
}

// selectMemoriesForCompression chooses which memories to compress based on space limits
// Returns memories sorted by compression score (highest first)
func (w *DecayWorker) selectMemoriesForCompression(
	ctx context.Context,
	tier MemoryTier,
	currentCount int,
	tierLimit int,
	targetCount int,
) ([]Memory, error) {
	// Calculate how many memories need to be compressed
	excessCount := currentCount - targetCount
	
	if excessCount <= 0 {
		return []Memory{}, nil // No compression needed
	}
	
	log.Printf("[DecayWorker] Tier %s: %d/%d memories (%.1f%% full), need to compress %d",
		tier, currentCount, tierLimit, float64(currentCount)/float64(tierLimit)*100, excessCount)
	
	// Fetch more memories than needed to allow for clustering
	// Fetch 2x excess to have options for cluster formation
	fetchLimit := excessCount * 2
	if fetchLimit > 1000 {
		fetchLimit = 1000 // Cap at 1000 to avoid huge queries
	}
	
	// Get all memories in this tier (we need to score them)
	// Use FindMemoriesForCompression with age=0 to get all memories in tier
	memories, err := w.storage.FindMemoriesForCompression(ctx, tier, 0, fetchLimit)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch memories for scoring: %w", err)
	}
	
	if len(memories) == 0 {
		return []Memory{}, nil
	}
	
	log.Printf("[DecayWorker] Fetched %d memories from tier %s for scoring", len(memories), tier)
	
	// Calculate compression score for each memory
	type scoredMemory struct {
		memory Memory
		score  float64
	}
	
	scored := make([]scoredMemory, len(memories))
	for i, mem := range memories {
		score := w.calculateCompressionScore(&mem, struct{ Age, Importance, Access float64 }{
			Age:        w.compressionWeights.Age,
			Importance: w.compressionWeights.Importance,
			Access:     w.compressionWeights.Access,
		})
		scored[i] = scoredMemory{memory: mem, score: score}
	}
	
	// Sort by score (highest first = most compressible)
	sort.Slice(scored, func(i, j int) bool {
		return scored[i].score > scored[j].score
	})
	
	// Select top N candidates for compression
	selectedCount := excessCount
	if selectedCount > len(scored) {
		selectedCount = len(scored)
	}
	
	selected := make([]Memory, selectedCount)
	for i := 0; i < selectedCount; i++ {
		selected[i] = scored[i].memory
	}
	
	log.Printf("[DecayWorker] Selected %d memories for compression (scores: %.3f to %.3f)",
		len(selected), scored[0].score, scored[selectedCount-1].score)
	
	return selected, nil
}

// pruneWeakLinksPhase scans all memories with links and removes weak ones
func (w *DecayWorker) pruneWeakLinksPhase(ctx context.Context) error {
	// Configuration
	minLinkStrength := 0.1 // Remove links with strength below 10%
	
	totalMemoriesScanned := 0
	totalLinksRemoved := 0
	memoriesUpdated := 0
	
	// We need to scan all memories across all tiers
	tiers := []MemoryTier{TierRecent, TierMedium, TierLong, TierAncient}
	
	for _, tier := range tiers {
		log.Printf("[LinkPruning] Scanning tier %s for weak links...", tier)
		
		// Get all memories in this tier with links
		// Use age=999999 to get ALL memories (sorted by age)
		batchSize := 100
		processedIDs := make(map[string]bool) // Track what we've seen to avoid reprocessing
		
		for {
			// Fetch batch
			memories, err := w.storage.FindMemoriesForCompression(ctx, tier, 999999, batchSize)
			if err != nil {
				return fmt.Errorf("failed to fetch memories for link pruning: %w", err)
			}
			
			if len(memories) == 0 {
				break // No more memories in this tier
			}
			
			// Filter out already-processed memories (since we can't offset)
			newMemories := []Memory{}
			for _, mem := range memories {
				if !processedIDs[mem.ID] {
					newMemories = append(newMemories, mem)
					processedIDs[mem.ID] = true
				}
			}
			
			if len(newMemories) == 0 {
				// All memories in this batch were already processed
				break
			}
			
			// Process each memory
			for i := range newMemories {
				mem := &newMemories[i]
				
				// Skip if no links
				if len(mem.RelatedMemories) == 0 {
					continue
				}
				
				totalMemoriesScanned++
				
				// Calculate strength for each link
				strongLinks := []string{}
				removedCount := 0
				
				for _, linkedID := range mem.RelatedMemories {
					strength := w.linker.GetLinkStrength(mem, linkedID)
					
					if strength >= minLinkStrength {
						strongLinks = append(strongLinks, linkedID)
					} else {
						removedCount++
						log.Printf("[LinkPruning]   Removing weak link %s -> %s (strength: %.3f)",
							mem.ID[:8], linkedID[:8], strength)
					}
				}
				
				// Update memory if any links were removed
				if removedCount > 0 {
					if err := w.storage.UpdateLinks(ctx, mem.ID, strongLinks); err != nil {
						log.Printf("[LinkPruning] WARNING: Failed to update memory %s: %v",
							mem.ID, err)
						continue
					}
					
					totalLinksRemoved += removedCount
					memoriesUpdated++
				}
				
			}
			
			// If we got fewer than batchSize NEW memories, we've reached the end
			if len(newMemories) < batchSize {
				break
			}
		}
		
		log.Printf("[LinkPruning] Tier %s complete (%d memories scanned)", tier, len(processedIDs))
	}
	
	if totalLinksRemoved > 0 {
		log.Printf("[LinkPruning] ✓ Removed %d weak links from %d memories (scanned %d memories total)",
			totalLinksRemoved, memoriesUpdated, totalMemoriesScanned)
	} else {
		log.Printf("[LinkPruning] No weak links found (scanned %d memories)", totalMemoriesScanned)
	}
	
	return nil
}

// consolidateDuplicatesPhase finds and merges semantically duplicate memories
func (w *DecayWorker) consolidateDuplicatesPhase(ctx context.Context) error {
	consolidator := NewConsolidator(w.storage, w.embedder)
	
	// Run consolidation on each tier
	tiers := []MemoryTier{TierRecent, TierMedium, TierLong}
	totalConsolidated := 0
	
	for _, tier := range tiers {
		count, err := consolidator.ConsolidateDuplicates(ctx, tier)
		if err != nil {
			log.Printf("[Consolidation] WARNING: Failed to consolidate tier %s: %v", tier, err)
			continue
		}
		totalConsolidated += count
		
		if count > 0 {
			log.Printf("[Consolidation] Tier %s: consolidated %d duplicate sets", tier, count)
		}
	}
	
	if totalConsolidated > 0 {
		log.Printf("[Consolidation] ✓ Total: %d duplicate sets consolidated", totalConsolidated)
	} else {
		log.Printf("[Consolidation] No duplicates found")
	}
	
	return nil
}

func (w *DecayWorker) recalculateTrustScores(ctx context.Context) error {
	// Bayesian trust formula:
	// trust_score = (good_validations + prior) / (total_validations + 2*prior)
	// Where prior = 2 (equivalent to 2 good + 2 bad observations)
	
	const prior = 2.0
	batchSize := 100
	totalUpdated := 0
	
	// Process all tiers
	tiers := []MemoryTier{TierRecent, TierMedium, TierLong, TierAncient}
	
	for _, tier := range tiers {
		processedIDs := make(map[string]bool) // Track processed memories
		
		for {
			// Fetch memories batch
			memories, err := w.storage.FindMemoriesForCompression(ctx, tier, 999999, batchSize)
			if err != nil {
				return fmt.Errorf("failed to fetch memories for trust calculation: %w", err)
			}
			
			if len(memories) == 0 {
				break
			}
			
			// Filter out already-processed memories
			newMemories := []Memory{}
			for _, mem := range memories {
				if !processedIDs[mem.ID] {
					newMemories = append(newMemories, mem)
					processedIDs[mem.ID] = true
				}
			}
			
			if len(newMemories) == 0 {
				// All memories in this batch were already processed
				break
			}
			
			// Process each memory
			for i := range newMemories {
				mem := &newMemories[i]
				
				// Skip if no validations yet
				if mem.ValidationCount == 0 {
					continue
				}
				
				// Count good validations based on outcome tag
				var goodValidations float64
				if mem.OutcomeTag == "good" {
					// All validations are good
					goodValidations = float64(mem.ValidationCount)
				} else if mem.OutcomeTag == "bad" {
					// All validations are bad
					goodValidations = 0
				} else {
					// Neutral: assume 50/50
					goodValidations = float64(mem.ValidationCount) * 0.5
				}
				
				// Apply Bayesian formula
				totalValidations := float64(mem.ValidationCount)
				newTrustScore := (goodValidations + prior) / (totalValidations + 2*prior)
				
				// Only update if trust score changed significantly (avoid unnecessary writes)
				if abs(newTrustScore-mem.TrustScore) > 0.01 {
					oldTrust := mem.TrustScore
					
					// Update in storage (minimal update - just trust_score)
					if err := w.storage.UpdateTrustScore(ctx, mem.ID, newTrustScore); err != nil {
						log.Printf("[TrustCalc] WARNING: Failed to update trust for memory %s: %v",
							mem.ID, err)
						continue
					}
					
					totalUpdated++
					
					log.Printf("[TrustCalc]   Memory %s: trust %.2f -> %.2f (validations=%d, outcome=%s)",
						mem.ID[:8], oldTrust, newTrustScore, mem.ValidationCount, mem.OutcomeTag)
				}
			}
			
			// If we got fewer than batchSize NEW memories, we've reached the end
			if len(newMemories) < batchSize {
				break
			}
		}
		
		log.Printf("[TrustCalc] Tier %s complete (%d memories processed)", tier, len(processedIDs))
	}
	
	if totalUpdated > 0 {
		log.Printf("[TrustCalc] ✓ Updated trust scores for %d memories", totalUpdated)
	} else {
		log.Printf("[TrustCalc] No trust score updates needed")
	}
	
	return nil
}

// abs returns absolute value of a float64
func abs(x float64) float64 {
	if x < 0 {
		return -x
	}
	return x
}

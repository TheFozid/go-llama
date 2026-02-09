package memory

import (
	"context"
	"log"
	"sync"
	"time"
)

// TaggerQueue handles asynchronous memory tagging with parallel workers
type TaggerQueue struct {
	tagger    *Tagger
	storage   *Storage
	queue     chan string // Memory IDs to tag
	workers   int
	wg        sync.WaitGroup
	ctx       context.Context
	cancel    context.CancelFunc
	mu        sync.Mutex
	stats     TaggerStats
}

// TaggerStats tracks queue performance
type TaggerStats struct {
	Enqueued       int64
	Processed      int64
	Failed         int64
	Dropped        int64
	CurrentQueue   int
	WorkersActive  int
}

// NewTaggerQueue creates an async tagger with configurable workers
func NewTaggerQueue(tagger *Tagger, storage *Storage, workers int, queueSize int) *TaggerQueue {
	if workers < 1 {
		workers = 3 // Default to 3 workers
	}
	if queueSize < 100 {
		queueSize = 1000 // Default to 1000 buffer
	}
	
	ctx, cancel := context.WithCancel(context.Background())
	
	tq := &TaggerQueue{
		tagger:  tagger,
		storage: storage,
		queue:   make(chan string, queueSize),
		workers: workers,
		ctx:     ctx,
		cancel:  cancel,
		stats:   TaggerStats{},
	}
	
	// Start background workers
	for i := 0; i < workers; i++ {
		tq.wg.Add(1)
		go tq.worker(i)
	}
	
	log.Printf("[TaggerQueue] Started with %d workers and queue size %d", workers, queueSize)
	
	return tq
}

// Enqueue adds a memory ID to the tagging queue (non-blocking)
func (tq *TaggerQueue) Enqueue(memoryID string) {
	select {
	case tq.queue <- memoryID:
		// Successfully queued
		tq.mu.Lock()
		tq.stats.Enqueued++
		tq.stats.CurrentQueue = len(tq.queue)
		tq.mu.Unlock()
		
	default:
		// Queue is full, drop this memory
		tq.mu.Lock()
		tq.stats.Dropped++
		tq.mu.Unlock()
		log.Printf("[TaggerQueue] WARNING: Queue full, dropping memory %s (total dropped: %d)", 
			memoryID, tq.stats.Dropped)
	}
}

// EnqueueBatch adds multiple memory IDs efficiently
func (tq *TaggerQueue) EnqueueBatch(memoryIDs []string) {
	for _, id := range memoryIDs {
		tq.Enqueue(id)
	}
	log.Printf("[TaggerQueue] Enqueued batch of %d memories (queue size: %d/%d)", 
		len(memoryIDs), len(tq.queue), cap(tq.queue))
}

// worker processes memories from the queue
func (tq *TaggerQueue) worker(id int) {
	defer tq.wg.Done()
	
	log.Printf("[TaggerQueue] Worker %d started", id)
	
	for {
		select {
		case <-tq.ctx.Done():
			log.Printf("[TaggerQueue] Worker %d stopping (context cancelled)", id)
			return
			
		case memID := <-tq.queue:
			tq.mu.Lock()
			tq.stats.WorkersActive++
			tq.stats.CurrentQueue = len(tq.queue)
			tq.mu.Unlock()
			
			log.Printf("[TaggerQueue] Worker %d processing memory %s (queue: %d remaining)", 
				id, memID, len(tq.queue))
			
        // Process with queue context (no outer timeout, relies on internal LLM timeouts)
        err := tq.processMemory(tq.ctx, memID)
        
        tq.mu.Lock()
        tq.stats.WorkersActive--
        if err != nil {
            tq.stats.Failed++
            log.Printf("[TaggerQueue] Worker %d failed to tag %s: %v (total failures: %d)", 
                id, memID, err, tq.stats.Failed)
        } else {
            tq.stats.Processed++
        }
        tq.mu.Unlock()
        
        // No cancel() needed here as we are using the parent queue context
		}
	}
}

// processMemory tags a single memory
func (tq *TaggerQueue) processMemory(ctx context.Context, memID string) error {
	// Fetch memory
	mem, err := tq.storage.GetMemoryByID(ctx, memID)
	if err != nil {
		return err
	}
	
	// Already tagged? Skip
	if mem.OutcomeTag != "" {
		log.Printf("[TaggerQueue] Memory %s already tagged, skipping", memID)
		return nil
	}
	
	// Analyze outcome with retry logic (from tagger.go)
	outcome, err := tq.tagger.analyzeOutcome(ctx, mem.Content)
	if err != nil {
		return err
	}
	
	// Extract concepts with retry logic
	concepts, err := tq.tagger.extractConcepts(ctx, mem.Content)
	if err != nil {
		log.Printf("[TaggerQueue] WARNING: Concept extraction failed for %s, using empty: %v", memID, err)
		concepts = []string{} // Non-critical, continue
	}
	
	// Update memory
	mem.OutcomeTag = outcome.Outcome
	mem.TrustScore = 0.5
	mem.ConceptTags = concepts
	mem.ValidationCount = 1
	
	// Regenerate embedding if missing
	if len(mem.Embedding) == 0 {
		log.Printf("[TaggerQueue] Memory %s missing embedding, regenerating...", memID)
		embedding, err := tq.tagger.embedder.Embed(ctx, mem.Content)
		if err != nil {
			return err
		}
		mem.Embedding = embedding
	}
	
	return tq.storage.UpdateMemory(ctx, mem)
}

// GetStats returns current queue statistics
func (tq *TaggerQueue) GetStats() TaggerStats {
	tq.mu.Lock()
	defer tq.mu.Unlock()
	
	stats := tq.stats
	stats.CurrentQueue = len(tq.queue)
	return stats
}

// LogStats logs current statistics
func (tq *TaggerQueue) LogStats() {
	stats := tq.GetStats()
	log.Printf("[TaggerQueue] Stats: enqueued=%d, processed=%d, failed=%d, dropped=%d, queue=%d, active_workers=%d",
		stats.Enqueued, stats.Processed, stats.Failed, stats.Dropped, stats.CurrentQueue, stats.WorkersActive)
}

// Stop gracefully shuts down the queue and waits for workers to finish
func (tq *TaggerQueue) Stop() {
	log.Printf("[TaggerQueue] Stopping... (queue: %d pending)", len(tq.queue))
	
	// Signal workers to stop
	tq.cancel()
	
	// Wait for workers to finish current tasks
	tq.wg.Wait()
	
	// Log final stats
	tq.LogStats()
	
	log.Printf("[TaggerQueue] Stopped")
}

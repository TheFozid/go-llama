// internal/dialogue/worker.go
package dialogue

import (
	"context"
	"log"
	"math/rand"
	"time"
)

// Worker manages the background dialogue scheduling
type Worker struct {
	engine              *Engine
	baseIntervalMinutes int
	jitterWindowMinutes int
	stopChan            chan struct{}
}

// NewWorker creates a new dialogue worker
func NewWorker(
	engine *Engine,
	baseIntervalMinutes int,
	jitterWindowMinutes int,
) *Worker {
	return &Worker{
		engine:              engine,
		baseIntervalMinutes: baseIntervalMinutes,
		jitterWindowMinutes: jitterWindowMinutes,
		stopChan:            make(chan struct{}),
	}
}

// Start begins the dialogue scheduling loop
func (w *Worker) Start() {
	log.Printf("[DialogueWorker] Starting dialogue worker (base interval: %d minutes, jitter: ±%d minutes)",
		w.baseIntervalMinutes, w.jitterWindowMinutes)
	
	// Seed random number generator for jitter
	rand.Seed(time.Now().UnixNano())
	
	// Run first cycle immediately
	w.runCycleSafely()
	
	// Schedule subsequent cycles with jitter
	go w.scheduleLoop()
}

// Stop gracefully stops the worker
func (w *Worker) Stop() {
	log.Printf("[DialogueWorker] Stopping dialogue worker")
	close(w.stopChan)
}

// scheduleLoop runs the dialogue cycle at intervals with jitter
func (w *Worker) scheduleLoop() {
	for {
		// Calculate next run time with jitter
		baseInterval := time.Duration(w.baseIntervalMinutes) * time.Minute
		jitter := generateJitter(w.jitterWindowMinutes)
		nextInterval := baseInterval + jitter
		
		log.Printf("[DialogueWorker] Next cycle in %s (base: %s, jitter: %s)",
			nextInterval.Round(time.Second),
			baseInterval.Round(time.Second),
			jitter.Round(time.Second))
		
		// Wait for next cycle or stop signal
		select {
		case <-time.After(nextInterval):
			w.runCycleSafely()
		case <-w.stopChan:
			log.Printf("[DialogueWorker] Stopped")
			return
		}
	}
}

// runCycleSafely runs a dialogue cycle with panic recovery
func (w *Worker) runCycleSafely() {
	defer func() {
		if r := recover(); r != nil {
			log.Printf("[DialogueWorker] PANIC recovered: %v", r)
		}
	}()
	
	ctx := context.Background()
	
	if err := w.engine.RunDialogueCycle(ctx); err != nil {
		log.Printf("[DialogueWorker] ERROR in dialogue cycle: %v", err)
	}
}

// generateJitter returns a random duration within the jitter window
func generateJitter(windowMinutes int) time.Duration {
	if windowMinutes <= 0 {
		return 0
	}
	
	// Random value between -windowMinutes and +windowMinutes
	jitterMinutes := rand.Intn(windowMinutes*2+1) - windowMinutes
	return time.Duration(jitterMinutes) * time.Minute
}

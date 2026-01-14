package llm

import "time"

// Config controls queue behavior
type Config struct {
	// Concurrency control
	MaxConcurrent int // Total concurrent LLM requests

	// Queue sizes
	CriticalQueueSize   int // User requests (small, rarely queues)
	BackgroundQueueSize int // Background tasks (larger buffer)

	// Timeouts
	CriticalTimeout   time.Duration // Shorter timeout for user requests
	BackgroundTimeout time.Duration // Longer timeout for background
}

// DefaultConfig returns sensible defaults
func DefaultConfig() *Config {
	return &Config{
		MaxConcurrent:       2,                  // Start conservative
		CriticalQueueSize:   20,                 // Small buffer
		BackgroundQueueSize: 100,                // Large buffer
		CriticalTimeout:     360 * time.Second,
		BackgroundTimeout:   360 * time.Second,
	}
}

package llm

import (
	"context"
	"net/http"
	"sync"
	"time"
)

// Priority levels (just 2)
type Priority int

const (
	PriorityCritical   Priority = 0 // User conversations
	PriorityBackground Priority = 1 // Everything else
)

// Request encapsulates an LLM call
type Request struct {
	ID          string
	Priority    Priority
	Context     context.Context

	// For standard requests
	URL         string
	Payload     map[string]interface{}
	IsStreaming bool

	// Response handling
	ResponseCh chan<- *Response
	ErrorCh    chan<- error

	SubmitTime time.Time
	Timeout    time.Duration
}

// Response encapsulates LLM output
type Response struct {
	StatusCode int
	Body       []byte
	HTTPResp   *http.Response // For streaming
	CancelFunc context.CancelFunc // For streaming: allows caller to clean up context
}

// Metrics tracks queue performance
type Metrics struct {
    CriticalEnqueued    int64
    CriticalProcessed   int64
    CriticalDropped     int64
    BackgroundEnqueued  int64
    BackgroundProcessed int64
    BackgroundDropped   int64
    CurrentQueueDepth   map[Priority]int
}

// ModelInfo represents information about a discovered model
type ModelInfo struct {
    Name        string    `json:"id"`
    Object      string    `json:"object"`
    Created     int64     `json:"created"`
    OwnedBy     string    `json:"owned_by"`
    LastFetched time.Time `json:"-"`
    IsChat      bool      `json:"-"` // Determined by testing endpoint
    IsEmbedding bool      `json:"-"` // Determined by testing endpoint
}

// LLMEndpoint represents a discovered LLM server endpoint
type LLMEndpoint struct {
    BaseURL     string      `json:"url"`
    Models      []ModelInfo `json:"models"`
    LastUpdated time.Time   `json:"last_updated"`
    IsOnline    bool        `json:"is_online"`
    ErrorCount  int         `json:"error_count"`
    mutex       sync.RWMutex `json:"-"`
}

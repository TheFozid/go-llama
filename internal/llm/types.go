package llm

import (
	"context"
	"net/http"
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

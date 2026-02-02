package llm

import (
    "bytes"
    "context"
    "encoding/json"
    "fmt"
    "io"
    "log"
    "net/http"
    "sync"
    "time"

    "go-llama/internal/tools"
)

// Manager coordinates all LLM requests
type Manager struct {
    criticalQueue   chan *Request
    backgroundQueue chan *Request

    maxConcurrent int
    semaphore     chan struct{} // Limit concurrent requests

    circuitBreaker *tools.CircuitBreaker

    mu      sync.RWMutex
    metrics Metrics

    stopCh chan struct{}
    wg     sync.WaitGroup

    config *Config
}

// NewManager creates a new queue manager
func NewManager(config *Config, circuitBreaker *tools.CircuitBreaker) *Manager {
    m := &Manager{
        criticalQueue:   make(chan *Request, config.CriticalQueueSize),
        backgroundQueue: make(chan *Request, config.BackgroundQueueSize),
        maxConcurrent:   config.MaxConcurrent,
        semaphore:       make(chan struct{}, config.MaxConcurrent),
        circuitBreaker:  circuitBreaker,
        metrics: Metrics{
            CurrentQueueDepth: map[Priority]int{
                PriorityCritical:   0,
                PriorityBackground: 0,
            },
        },
        stopCh: make(chan struct{}),
        config: config,
    }

    // Start dispatcher
    m.wg.Add(1)
    go m.dispatcher()

    log.Printf("[LLM Queue] Started with %d concurrent slots", config.MaxConcurrent)
    return m
}

// Submit adds a request to the queue (non-blocking with drop behavior)
func (m *Manager) Submit(req *Request) error {
    var queue chan *Request
    var priorityName string

    if req.Priority == PriorityCritical {
        queue = m.criticalQueue
        priorityName = "critical"
        m.mu.Lock()
        m.metrics.CriticalEnqueued++
        m.mu.Unlock()
    } else {
        queue = m.backgroundQueue
        priorityName = "background"
        m.mu.Lock()
        m.metrics.BackgroundEnqueued++
        m.mu.Unlock()
    }

    select {
    case queue <- req:
        m.mu.Lock()
        m.metrics.CurrentQueueDepth[req.Priority] = len(queue)
        m.mu.Unlock()
        return nil

    default:
        // Queue full - drop request
        m.mu.Lock()
        if req.Priority == PriorityCritical {
            m.metrics.CriticalDropped++
        } else {
            m.metrics.BackgroundDropped++
        }
        m.mu.Unlock()

        log.Printf("[LLM Queue] WARNING: %s queue full, dropping request %s",
            priorityName, req.ID)
        return fmt.Errorf("queue full")
    }
}

// dispatcher selects next request (critical first, then background)
// OPTIMIZATION: Fixed StopCh handling and Priority Blocking
func (m *Manager) dispatcher() {
    defer m.wg.Done()

    for {
        // Step 1: Wait for a request to appear in either queue.
        // We use a select to ensure we immediately service Critical if both are available.
        var req *Request
        var isCritical bool

        select {
        case <-m.stopCh:
            return
        case req = <-m.criticalQueue:
            isCritical = true
        case req = <-m.backgroundQueue:
            isCritical = false
            // PREEMPTION CHECK:
            // Even though we pulled a background request, we must double-check if a Critical
            // request arrived in the nanoseconds between the select above picking background
            // and now. If so, we swap them.
            select {
            case critReq := <-m.criticalQueue:
                // Put background back
                m.backgroundQueue <- req
                req = critReq
                isCritical = true
            default:
                // No critical request, proceed with background
            }
        }

        // Step 2: Wait for a processing slot (semaphore).
        // CRITICAL FIX: This select allows us to shut down even if we are blocked waiting for a slot.
        select {
        case <-m.stopCh:
            // If we are stopping, we put the request back so it's not lost (optional, or just drop it).
            // Given we are shutting down, dropping is usually acceptable, but putting back is cleaner.
            if isCritical {
                m.criticalQueue <- req
            } else {
                m.backgroundQueue <- req
            }
            return
        case m.semaphore <- struct{}{}:
            // Slot acquired, proceed
        }

        // Step 3: Process the request
        m.wg.Add(1)
        go m.processRequest(req)
    }
}

// processRequest executes the actual LLM call
func (m *Manager) processRequest(req *Request) {
    defer func() {
        <-m.semaphore // Release slot
        m.wg.Done()

        m.mu.Lock()
        if req.Priority == PriorityCritical {
            m.metrics.CriticalProcessed++
        } else {
            m.metrics.BackgroundProcessed++
        }
        m.mu.Unlock()
    }()

    startTime := time.Now()

    // Check if context already cancelled
    if req.Context.Err() != nil {
        req.ErrorCh <- req.Context.Err()
        return
    }

    // Apply timeout
    ctx, cancel := context.WithTimeout(req.Context, req.Timeout)
    
    // CRITICAL FIX: For streaming, don't cancel context until after response is sent
    if !req.IsStreaming {
        defer cancel()
    }

    // Execute request
    resp, err := m.executeHTTPRequest(ctx, req)
    if err != nil {
        if req.IsStreaming {
            cancel() // Clean up on error
        }
        log.Printf("[LLM Queue] Request %s failed after %s: %v",
            req.ID, time.Since(startTime), err)
        req.ErrorCh <- err
        return
    }

    // For streaming: attach cancel function to response so caller can clean up
    if req.IsStreaming {
        resp.CancelFunc = cancel
    }

    // Send response
    select {
    case req.ResponseCh <- resp:
        log.Printf("[LLM Queue] Request %s completed in %s",
            req.ID, time.Since(startTime))
    case <-ctx.Done():
        if req.IsStreaming {
            cancel()
        }
        log.Printf("[LLM Queue] Request %s timeout after %s",
            req.ID, time.Since(startTime))
        req.ErrorCh <- ctx.Err()
    }
}

// executeHTTPRequest performs the actual HTTP call
func (m *Manager) executeHTTPRequest(ctx context.Context, req *Request) (*Response, error) {
    // Check circuit breaker first
    if m.circuitBreaker != nil && m.circuitBreaker.IsOpen() {
        return nil, fmt.Errorf("circuit breaker open")
    }

    // Marshal payload
    jsonData, err := json.Marshal(req.Payload)
    if err != nil {
        return nil, fmt.Errorf("failed to marshal payload: %w", err)
    }

    // Create HTTP request
    httpReq, err := http.NewRequestWithContext(ctx, "POST", req.URL, bytes.NewBuffer(jsonData))
    if err != nil {
        return nil, fmt.Errorf("failed to create request: %w", err)
    }
    httpReq.Header.Set("Content-Type", "application/json")

    // Execute with timeout
    client := &http.Client{
        Timeout: req.Timeout,
        Transport: &http.Transport{
            ResponseHeaderTimeout: req.Timeout,
            IdleConnTimeout:       req.Timeout,
            MaxIdleConns:          10,
            DisableKeepAlives:     false,
        },
    }

    httpResp, err := client.Do(httpReq)
    if err != nil {
        // Record failure in circuit breaker
        if m.circuitBreaker != nil {
            m.circuitBreaker.Call(func() error { return err })
        }
        return nil, fmt.Errorf("http request failed: %w", err)
    }

    // Record success in circuit breaker
    if m.circuitBreaker != nil {
        m.circuitBreaker.Call(func() error { return nil })
    }

    // For streaming, return response immediately
    // Context lifecycle will be managed by caller via CancelFunc
    if req.IsStreaming {
        return &Response{
            StatusCode: httpResp.StatusCode,
            HTTPResp:   httpResp,
        }, nil
    }

    // For non-streaming, read body
    defer httpResp.Body.Close()
    body, err := io.ReadAll(httpResp.Body)
    if err != nil {
        return nil, fmt.Errorf("failed to read response: %w", err)
    }

    return &Response{
        StatusCode: httpResp.StatusCode,
        Body:       body,
    }, nil
}

// GetMetrics returns current queue statistics
func (m *Manager) GetMetrics() Metrics {
    m.mu.RLock()
    defer m.mu.RUnlock()

    metrics := m.metrics
    metrics.CurrentQueueDepth[PriorityCritical] = len(m.criticalQueue)
    metrics.CurrentQueueDepth[PriorityBackground] = len(m.backgroundQueue)
    return metrics
}

// Stop gracefully shuts down the queue
func (m *Manager) Stop() {
    close(m.stopCh)
    m.wg.Wait()
    log.Printf("[LLM Queue] Stopped")
}

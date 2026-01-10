package tools

import (
	"errors"
	"fmt"
	"log"
	"sync"
	"time"
)

// Circuit breaker errors
var (
	ErrCircuitOpen     = errors.New("circuit breaker open")
	ErrTooManyRequests = errors.New("too many requests in half-open state")
)

// CircuitState represents the state of the circuit breaker
type CircuitState string

const (
	StateClosed   CircuitState = "closed"   // Normal operation
	StateOpen     CircuitState = "open"     // Failing, reject requests
	StateHalfOpen CircuitState = "half-open" // Testing if service recovered
)

// CircuitBreaker prevents cascading failures by stopping requests to failing services
type CircuitBreaker struct {
	mu              sync.RWMutex
	state           CircuitState
	failureCount    int
	successCount    int
	consecutiveSuccesses int
	lastFailureTime time.Time
	lastStateChange time.Time
	
	// Configuration
	failureThreshold int           // Failures before opening
	successThreshold int           // Successes to close from half-open
	timeout          time.Duration // How long to stay open
	halfOpenMax      int           // Max concurrent requests in half-open
	
	// Stats
	totalRequests      int64
	totalSuccesses     int64
	totalFailures      int64
	totalRejections    int64
}

// NewCircuitBreaker creates a circuit breaker with the given configuration
func NewCircuitBreaker(failureThreshold int, timeout time.Duration) *CircuitBreaker {
	if failureThreshold < 1 {
		failureThreshold = 3
	}
	if timeout < 1*time.Second {
		timeout = 5 * time.Minute
	}
	
	cb := &CircuitBreaker{
		state:            StateClosed,
		failureThreshold: failureThreshold,
		successThreshold: 3, // Default: 3 successes to close
		timeout:          timeout,
		halfOpenMax:      3, // Default: max 3 concurrent tests
		lastStateChange:  time.Now(),
	}
	
	log.Printf("[CircuitBreaker] Initialized: threshold=%d failures, timeout=%s, half_open_max=%d",
		failureThreshold, timeout, cb.halfOpenMax)
	
	return cb
}

// Call attempts to execute a function through the circuit breaker
func (cb *CircuitBreaker) Call(fn func() error) error {
	// Check if we should allow this request
	if err := cb.beforeRequest(); err != nil {
		return err
	}
	
	// Execute function
	err := fn()
	
	// Record result
	cb.afterRequest(err)
	
	return err
}

// beforeRequest checks if the request should be allowed
func (cb *CircuitBreaker) beforeRequest() error {
	cb.mu.Lock()
	defer cb.mu.Unlock()
	
	cb.totalRequests++
	
	switch cb.state {
	case StateClosed:
		// Normal operation, allow request
		return nil
		
	case StateOpen:
		// Check if timeout has elapsed
		if time.Since(cb.lastFailureTime) > cb.timeout {
			// Transition to half-open
			cb.setState(StateHalfOpen)
			cb.successCount = 0
			cb.consecutiveSuccesses = 0
			log.Printf("[CircuitBreaker] State: OPEN → HALF-OPEN (timeout elapsed, testing service)")
			return nil
		}
		
		// Still in timeout period, reject
		cb.totalRejections++
		return ErrCircuitOpen
		
	case StateHalfOpen:
		// Allow limited concurrent requests for testing
		if cb.successCount >= cb.halfOpenMax {
			cb.totalRejections++
			return ErrTooManyRequests
		}
		return nil
		
	default:
		return nil
	}
}

// afterRequest records the result and updates state
func (cb *CircuitBreaker) afterRequest(err error) {
	cb.mu.Lock()
	defer cb.mu.Unlock()
	
	if err != nil {
		// Request failed
		cb.totalFailures++
		cb.failureCount++
		cb.consecutiveSuccesses = 0
		cb.lastFailureTime = time.Now()
		
		switch cb.state {
		case StateClosed:
			// Check if we should open
			if cb.failureCount >= cb.failureThreshold {
				cb.setState(StateOpen)
				log.Printf("[CircuitBreaker] State: CLOSED → OPEN (%d consecutive failures, threshold=%d)",
					cb.failureCount, cb.failureThreshold)
			}
			
		case StateHalfOpen:
			// Failed during testing, go back to open
			cb.setState(StateOpen)
			log.Printf("[CircuitBreaker] State: HALF-OPEN → OPEN (test request failed)")
		}
		
	} else {
		// Request succeeded
		cb.totalSuccesses++
		cb.successCount++
		cb.consecutiveSuccesses++
		
		switch cb.state {
		case StateClosed:
			// Reset failure count on success
			if cb.failureCount > 0 {
				log.Printf("[CircuitBreaker] Success after %d failures, resetting counter", cb.failureCount)
				cb.failureCount = 0
			}
			
		case StateHalfOpen:
			// Check if we should close
			if cb.consecutiveSuccesses >= cb.successThreshold {
				cb.setState(StateClosed)
				cb.failureCount = 0
				log.Printf("[CircuitBreaker] State: HALF-OPEN → CLOSED (%d consecutive successes, service recovered)",
					cb.consecutiveSuccesses)
			} else {
				log.Printf("[CircuitBreaker] Half-open test succeeded (%d/%d)", 
					cb.consecutiveSuccesses, cb.successThreshold)
			}
		}
	}
}

// setState changes the circuit breaker state
func (cb *CircuitBreaker) setState(newState CircuitState) {
	oldState := cb.state
	cb.state = newState
	cb.lastStateChange = time.Now()
	
	if oldState != newState {
		log.Printf("[CircuitBreaker] State transition: %s → %s", oldState, newState)
	}
}

// State returns the current state
func (cb *CircuitBreaker) State() CircuitState {
	cb.mu.RLock()
	defer cb.mu.RUnlock()
	return cb.state
}

// IsOpen returns true if the circuit is open
func (cb *CircuitBreaker) IsOpen() bool {
	return cb.State() == StateOpen
}

// Stats returns current statistics
func (cb *CircuitBreaker) Stats() map[string]interface{} {
	cb.mu.RLock()
	defer cb.mu.RUnlock()
	
	successRate := 0.0
	if cb.totalRequests > 0 {
		successRate = float64(cb.totalSuccesses) / float64(cb.totalRequests)
	}
	
	return map[string]interface{}{
		"state":                string(cb.state),
		"total_requests":       cb.totalRequests,
		"total_successes":      cb.totalSuccesses,
		"total_failures":       cb.totalFailures,
		"total_rejections":     cb.totalRejections,
		"success_rate":         successRate,
		"failure_count":        cb.failureCount,
		"consecutive_successes": cb.consecutiveSuccesses,
		"time_in_state":        time.Since(cb.lastStateChange).String(),
	}
}

// LogStats logs current statistics
func (cb *CircuitBreaker) LogStats() {
	stats := cb.Stats()
	log.Printf("[CircuitBreaker] Stats: state=%s, requests=%d, successes=%d, failures=%d, rejections=%d, success_rate=%.2f",
		stats["state"], stats["total_requests"], stats["total_successes"], 
		stats["total_failures"], stats["total_rejections"], stats["success_rate"])
}

// Reset manually resets the circuit breaker to closed state
func (cb *CircuitBreaker) Reset() {
	cb.mu.Lock()
	defer cb.mu.Unlock()
	
	log.Printf("[CircuitBreaker] Manual reset: %s → CLOSED", cb.state)
	cb.setState(StateClosed)
	cb.failureCount = 0
	cb.successCount = 0
	cb.consecutiveSuccesses = 0
}

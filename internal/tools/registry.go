// internal/tools/registry.go
package tools

import (
	"context"
	"fmt"
	"log"
	"sync"
	"time"
)

// Registry manages all available tools
type Registry struct {
	tools map[string]Tool
	mu    sync.RWMutex
}

// NewRegistry creates a new tool registry
func NewRegistry() *Registry {
	return &Registry{
		tools: make(map[string]Tool),
	}
}

// Register adds a tool to the registry
func (r *Registry) Register(tool Tool) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	name := tool.Name()
	if _, exists := r.tools[name]; exists {
		return fmt.Errorf("tool already registered: %s", name)
	}

	r.tools[name] = tool
	log.Printf("[ToolRegistry] Registered tool: %s - %s", name, tool.Description())
	return nil
}

// Get retrieves a tool by name
func (r *Registry) Get(name string) (Tool, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	tool, exists := r.tools[name]
	if !exists {
		return nil, fmt.Errorf("tool not found: %s", name)
	}

	return tool, nil
}

// Execute runs a tool with the given parameters and context
// Includes retry logic for timeout failures in idle mode
func (r *Registry) Execute(ctx context.Context, toolName string, params map[string]interface{}, execCtx ExecutionContext) (*ToolResult, error) {
	tool, err := r.Get(toolName)
	if err != nil {
		return nil, err
	}

	// Create context with timeout
	execTimeout := execCtx.Timeout
	if execTimeout == 0 {
		// Default timeout based on context
		if execCtx.IsInteractive {
			execTimeout = 5 * time.Second
		} else {
			execTimeout = 60 * time.Second
		}
	}

	// Log execution
	contextStr := ContextIdleExploration
	if execCtx.IsInteractive {
		contextStr = ContextUserInteraction
	}
	
	// Retry logic for idle context only (not user-facing)
	maxRetries := 1 // Default: no retries
	if !execCtx.IsInteractive {
		maxRetries = 2 // Idle mode: allow 1 retry on timeout
	}
	
	var lastErr error
	var lastResult *ToolResult
	
	for attempt := 1; attempt <= maxRetries; attempt++ {
		if attempt > 1 {
			log.Printf("[ToolRegistry] Retry attempt %d/%d for tool '%s'", attempt, maxRetries, toolName)
		}
		
		timeoutCtx, cancel := context.WithTimeout(ctx, execTimeout)
		
		log.Printf("[ToolRegistry] Executing tool '%s' (context: %s, timeout: %s, attempt: %d/%d)", 
			toolName, contextStr, execTimeout, attempt, maxRetries)

		// Execute tool
		startTime := time.Now()
		result, err := tool.Execute(timeoutCtx, params)
		duration := time.Since(startTime)
		cancel() // Clean up context immediately

		if err != nil {
			lastErr = err
			lastResult = &ToolResult{
				Success:  false,
				Error:    err.Error(),
				Duration: duration,
			}
			
			// Check if this was a timeout
			isTimeout := timeoutCtx.Err() == context.DeadlineExceeded ||
			            strings.Contains(strings.ToLower(err.Error()), "timeout") ||
			            strings.Contains(strings.ToLower(err.Error()), "deadline exceeded")
			
			if isTimeout && attempt < maxRetries {
				log.Printf("[ToolRegistry] Tool '%s' timed out after %s, will retry with extended timeout", 
					toolName, duration)
				// Increase timeout by 50% for retry
				execTimeout = execTimeout * 3 / 2
				time.Sleep(2 * time.Second) // Brief pause before retry
				continue
			}
			
			log.Printf("[ToolRegistry] Tool '%s' failed after %s (attempt %d/%d): %v", 
				toolName, duration, attempt, maxRetries, err)
			return lastResult, err
		}

		// Success
		result.Duration = duration
		if attempt > 1 {
			log.Printf("[ToolRegistry] Tool '%s' succeeded on retry attempt %d in %s", 
				toolName, attempt, duration)
		} else {
			log.Printf("[ToolRegistry] Tool '%s' completed in %s (success: %v)", 
				toolName, duration, result.Success)
		}

		return result, nil
	}
	
	// All retries exhausted
	return lastResult, lastErr
}

// List returns all registered tool names and descriptions
func (r *Registry) List() map[string]string {
	r.mu.RLock()
	defer r.mu.RUnlock()

	list := make(map[string]string)
	for name, tool := range r.tools {
		list[name] = tool.Description()
	}
	return list
}

// RecordUsage logs tool usage for learning (could be extended to store in DB)
func (r *Registry) RecordUsage(usage *ToolUsage) {
	log.Printf("[ToolRegistry] Usage: %s in %s context → %s (outcome: %s)", 
		usage.ToolName, usage.Context, usage.Result.Output[:min(50, len(usage.Result.Output))], usage.Outcome)
	
	if usage.Learning != "" {
		log.Printf("[ToolRegistry] Learning: %s", usage.Learning)
	}
	
	// TODO Phase 3.2+: Store usage patterns in database for meta-learning
}

// Helper function
func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

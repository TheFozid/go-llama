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

	timeoutCtx, cancel := context.WithTimeout(ctx, execTimeout)
	defer cancel()

	// Log execution
	contextStr := ContextIdleExploration
	if execCtx.IsInteractive {
		contextStr = ContextUserInteraction
	}
	log.Printf("[ToolRegistry] Executing tool '%s' (context: %s, timeout: %s)", 
		toolName, contextStr, execTimeout)

	// Execute tool
	startTime := time.Now()
	result, err := tool.Execute(timeoutCtx, params)
	duration := time.Since(startTime)

	if err != nil {
		log.Printf("[ToolRegistry] Tool '%s' failed after %s: %v", toolName, duration, err)
		return &ToolResult{
			Success:  false,
			Error:    err.Error(),
			Duration: duration,
		}, err
	}

	result.Duration = duration
	log.Printf("[ToolRegistry] Tool '%s' completed in %s (success: %v)", 
		toolName, duration, result.Success)

	return result, nil
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

// internal/tools/context.go
package tools

import (
	"context"
	"log"
	"time"
)

// ContextualRegistry wraps Registry with context-aware execution
type ContextualRegistry struct {
	registry *Registry
	configs  map[string]ToolConfig
}

// NewContextualRegistry creates a context-aware tool registry
func NewContextualRegistry(registry *Registry, configs map[string]ToolConfig) *ContextualRegistry {
	return &ContextualRegistry{
		registry: registry,
		configs:  configs,
	}
}

// ExecuteInteractive runs a tool in interactive (user-facing) mode
// - Short timeouts (5s default)
// - Limited results (3-5)
// - Fast failure is better than slow success
func (cr *ContextualRegistry) ExecuteInteractive(ctx context.Context, toolName string, params map[string]interface{}) (*ToolResult, error) {
	config, exists := cr.configs[toolName]
	if !exists {
		config = ToolConfig{
			TimeoutInteractive:    5 * time.Second,
			MaxResultsInteractive: 3,
		}
	}

	// Add context hint to params
	params["is_interactive"] = true

	execCtx := ExecutionContext{
		IsInteractive: true,
		Timeout:       config.TimeoutInteractive,
		MaxResults:    config.MaxResultsInteractive,
	}

	log.Printf("[ContextualRegistry] ExecuteInteractive: tool=%s, timeout=%s, context=user_interaction", 
		toolName, config.TimeoutInteractive)

	return cr.registry.Execute(ctx, toolName, params, execCtx)
}

// ExecuteIdle runs a tool in idle exploration mode
// - Longer timeouts (240s default)
// - More results (10-20)
// - Thoroughness over speed
func (cr *ContextualRegistry) ExecuteIdle(ctx context.Context, toolName string, params map[string]interface{}) (*ToolResult, error) {
	config, exists := cr.configs[toolName]
	if !exists {
		config = ToolConfig{
			TimeoutIdle:    240 * time.Second,
			MaxResultsIdle: 20,
		}
	}

	// Add context hint to params
	params["is_interactive"] = false

	execCtx := ExecutionContext{
		IsInteractive: false,
		Timeout:       config.TimeoutIdle,
		MaxResults:    config.MaxResultsIdle,
	}

	log.Printf("[ContextualRegistry] ExecuteIdle: tool=%s, timeout=%s, context=idle_exploration", 
		toolName, config.TimeoutIdle)

	return cr.registry.Execute(ctx, toolName, params, execCtx)
}

// GetRegistry returns the underlying registry
func (cr *ContextualRegistry) GetRegistry() *Registry {
	return cr.registry
}

// RecordUsage wraps registry's RecordUsage
func (cr *ContextualRegistry) RecordUsage(usage *ToolUsage) {
	cr.registry.RecordUsage(usage)
}

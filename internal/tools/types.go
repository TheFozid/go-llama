// internal/tools/types.go
package tools

import (
	"context"
	"time"
)

// Tool defines the interface that all tools must implement
type Tool interface {
	// Name returns the unique identifier for this tool
	Name() string
	
	// Description returns a human-readable description of what the tool does
	Description() string
	
	// Execute runs the tool with the given parameters
	Execute(ctx context.Context, params map[string]interface{}) (*ToolResult, error)
	
	// RequiresAuth returns true if this tool needs authentication
	RequiresAuth() bool
}

// ToolResult contains the outcome of a tool execution
type ToolResult struct {
	Success    bool                   `json:"success"`
	Output     string                 `json:"output"`
	Error      string                 `json:"error,omitempty"`
	Duration   time.Duration          `json:"duration"`
	TokensUsed int                    `json:"tokens_used,omitempty"`
	Metadata   map[string]interface{} `json:"metadata,omitempty"`
}

// ToolUsage tracks tool execution for learning
type ToolUsage struct {
	ToolName  string                 `json:"tool_name"`
	Timestamp time.Time              `json:"timestamp"`
	Context   string                 `json:"context"` // "user_interaction" | "idle_exploration"
	Params    map[string]interface{} `json:"params"`
	Result    *ToolResult            `json:"result"`
	Outcome   string                 `json:"outcome"` // "good" | "bad" | "neutral"
	Learning  string                 `json:"learning,omitempty"`
}

// ExecutionContext provides context about how the tool is being used
type ExecutionContext struct {
	IsInteractive bool          // true = during user interaction, false = idle exploration
	Timeout       time.Duration // max time allowed for execution
	MaxResults    int           // limit on results (for search tools)
	UserID        *string       // optional user context
}

// ToolConfig represents configuration for a specific tool
type ToolConfig struct {
	Enabled               bool          `json:"enabled"`
	TimeoutInteractive    time.Duration `json:"timeout_interactive"`
	TimeoutIdle           time.Duration `json:"timeout_idle"`
	MaxResultsInteractive int           `json:"max_results_interactive"`
	MaxResultsIdle        int           `json:"max_results_idle"`
}

// Constants for tool names (must match dialogue.Action.Tool constants)
const (
	ToolNameSearch              = "search"
	ToolNameWebParse            = "web_parse"
	ToolNameSandbox             = "sandbox"
	ToolNameMemoryConsolidation = "memory_consolidation"
)

// Constants for execution context
const (
	ContextUserInteraction = "user_interaction"
	ContextIdleExploration = "idle_exploration"
)

// Constants for outcomes
const (
	OutcomeGood    = "good"
	OutcomeBad     = "bad"
	OutcomeNeutral = "neutral"
)

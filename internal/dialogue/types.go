// internal/dialogue/types.go
package dialogue

import (
	"encoding/json"
	"fmt"
	"time"
)

// Goal represents a self-directed learning objective
type Goal struct {
	ID          string    `json:"id"`
	Description string    `json:"description"`
	Source      string    `json:"source"` // "user_failure", "knowledge_gap", "curiosity", "principle"
	Priority    int       `json:"priority"` // 1-10
	Created     time.Time `json:"created"`
	Progress    float64   `json:"progress"` // 0.0 to 1.0
	Actions     []Action  `json:"actions"`
	Status      string    `json:"status"` // "active", "completed", "abandoned"
	Outcome     string    `json:"outcome,omitempty"` // "good", "bad", "neutral" (when completed)
}

// Action represents a step taken toward completing a goal
type Action struct {
	Description string    `json:"description"`
	Tool        string    `json:"tool"` // "search", "web_parse", "sandbox", "memory_consolidation"
	Status      string    `json:"status"` // "pending", "in_progress", "completed"
	Result      string    `json:"result,omitempty"`
	Timestamp   time.Time `json:"timestamp"`
}

// InternalState represents the system's working memory between dialogue cycles
type InternalState struct {
	ActiveGoals     []Goal   `json:"active_goals"`
	CompletedGoals  []Goal   `json:"completed_goals"`
	KnowledgeGaps   []string `json:"knowledge_gaps"`
	RecentFailures  []string `json:"recent_failures"`
	Patterns        []string `json:"patterns"`
	LastCycleTime   time.Time `json:"last_cycle_time"`
	CycleCount      int      `json:"cycle_count"`
}

// ThoughtRecord logs an internal thought during a dialogue cycle
type ThoughtRecord struct {
	CycleID     int       `json:"cycle_id"`
	ThoughtNum  int       `json:"thought_num"`
	Content     string    `json:"content"`
	TokensUsed  int       `json:"tokens_used"`
	Timestamp   time.Time `json:"timestamp"`
	ActionTaken bool      `json:"action_taken"` // Did this thought result in external action?
}

// CycleMetrics tracks performance of a dialogue cycle
type CycleMetrics struct {
	CycleID          int           `json:"cycle_id"`
	StartTime        time.Time     `json:"start_time"`
	EndTime          time.Time     `json:"end_time"`
	Duration         time.Duration `json:"duration"`
	ThoughtCount     int           `json:"thought_count"`
	ActionCount      int           `json:"action_count"`
	TokensUsed       int           `json:"tokens_used"`
	GoalsCreated     int           `json:"goals_created"`
	GoalsCompleted   int           `json:"goals_completed"`
	MemoriesStored   int           `json:"memories_stored"`
	StopReason       string        `json:"stop_reason"` // "max_thoughts", "max_time", "action_requirement", "natural_stop"
}

// GoalSource constants
const (
	GoalSourceUserFailure  = "user_failure"
	GoalSourceKnowledgeGap = "knowledge_gap"
	GoalSourceCuriosity    = "curiosity"
	GoalSourcePrinciple    = "principle"
)

// GoalStatus constants
const (
	GoalStatusActive    = "active"
	GoalStatusCompleted = "completed"
	GoalStatusAbandoned = "abandoned"
)

// ActionStatus constants
const (
	ActionStatusPending    = "pending"
	ActionStatusInProgress = "in_progress"
	ActionStatusCompleted  = "completed"
)

// ActionTool constants
const (
	ActionToolSearch              = "search"
	ActionToolWebParse            = "web_parse"
	ActionToolSandbox             = "sandbox"
	ActionToolMemoryConsolidation = "memory_consolidation"
)

// StopReason constants
const (
	StopReasonMaxThoughts       = "max_thoughts"
	StopReasonMaxTime           = "max_time"
	StopReasonActionRequirement = "action_requirement"
	StopReasonNaturalStop       = "natural_stop"
)

// ReasoningResponse represents structured LLM reasoning output
type ReasoningResponse struct {
	Reflection      string              `json:"reflection"`
	Insights        StringOrArray       `json:"insights"`
	Strengths       StringOrArray       `json:"strengths"`
	Weaknesses      StringOrArray       `json:"weaknesses"`
	KnowledgeGaps   StringOrArray       `json:"knowledge_gaps"`
	Patterns        StringOrArray       `json:"patterns"`
	GoalsToCreate   []GoalProposal      `json:"goals_to_create"`
	Learnings       []Learning          `json:"learnings"`
	SelfAssessment  *SelfAssessment     `json:"self_assessment,omitempty"`
}

// StringOrArray handles JSON that can be either a string or array of strings
type StringOrArray []string

// UnmarshalJSON handles both string and array formats
func (s *StringOrArray) UnmarshalJSON(data []byte) error {
	// Try array first
	var arr []string
	if err := json.Unmarshal(data, &arr); err == nil {
		*s = StringOrArray(arr)
		return nil
	}
	
	// Try string
	var str string
	if err := json.Unmarshal(data, &str); err == nil {
		if str == "" {
			*s = StringOrArray{}
		} else {
			*s = StringOrArray{str}
		}
		return nil
	}
	
	return fmt.Errorf("field must be string or array of strings")
}

// ToSlice converts to []string
func (s StringOrArray) ToSlice() []string {
	return []string(s)
}

// GoalProposal represents a goal the LLM wants to create
type GoalProposal struct {
	Description  string   `json:"description"`
	Priority     int      `json:"priority"`
	Reasoning    string   `json:"reasoning"`
	ActionPlan   []string `json:"action_plan"`
	ExpectedTime string   `json:"expected_time"` // e.g., "2 cycles", "1 week"
}

// Learning represents something learned from experience
type Learning struct {
	What       string `json:"what"`        // What was learned
	Context    string `json:"context"`     // When/where it was learned
	Confidence float64 `json:"confidence"` // 0.0-1.0
	Category   string `json:"category"`    // "strategy", "user_preference", "tool_effectiveness", "self_knowledge"
}

// SelfAssessment represents self-knowledge evaluation
type SelfAssessment struct {
	RecentSuccesses []string `json:"recent_successes"`
	RecentFailures  []string `json:"recent_failures"`
	SkillGaps       []string `json:"skill_gaps"`
	Confidence      float64  `json:"confidence"` // Overall confidence 0.0-1.0
	FocusAreas      []string `json:"focus_areas"` // What to prioritize
}

// ActionPlanStep represents a step in a dynamic action plan
type ActionPlanStep struct {
	Description string `json:"description"`
	Tool        string `json:"tool"`
	Query       string `json:"query,omitempty"`
	ExpectedOutcome string `json:"expected_outcome"`
}

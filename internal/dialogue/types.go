// internal/dialogue/types.go
package dialogue

import (
	"encoding/json"
	"fmt"
	"time"
)

// Goal represents a self-directed learning objective
type Goal struct {
    ID              string         `json:"id"`
    Description     string         `json:"description"`
    Source          string         `json:"source"` // "user_failure", "knowledge_gap", "curiosity", "principle"
    Priority        int            `json:"priority"` // 1-10
    Created         time.Time      `json:"created"`
    Progress        float64        `json:"progress"` // 0.0 to 1.0
    Actions         []Action       `json:"actions"`
    Status          string         `json:"status"` // "active", "completed", "abandoned"
    Outcome         string         `json:"outcome,omitempty"` // "good", "bad", "neutral" (when completed)
    ResearchPlan    *ResearchPlan  `json:"research_plan,omitempty"` // Multi-step investigation plan
    Metadata        map[string]interface{} `json:"metadata,omitempty"` // Additional metadata for the goal
    FailureCount    int            `json:"failure_count"` // Track consecutive failures before abandoning
    Tier            string         `json:"tier"` // "primary", "secondary", "tactical"
    SupportsGoals   []string       `json:"supports_goals,omitempty"` // IDs of primary goals this supports
    DependencyScore float64        `json:"dependency_score"` // 0.0-1.0 confidence in dependency link
    HasPendingWork     bool                    `json:"has_pending_work"` // True if goal has pending actions
	LastPursued        time.Time               `json:"last_pursued"` // When this goal was last worked on
	LastAssessment     *PlanAssessment         `json:"last_assessment,omitempty"` // Result of last progress check
	ReplanCount        int                     `json:"replan_count"` // Number of times this goal has been replanned
	SelfModGoal        *SelfModificationGoal   `json:"self_mod_goal,omitempty"` // Self-modification details if applicable
}

// SelfModificationGoal represents a deliberate attempt to modify thinking patterns
type SelfModificationGoal struct {
	TargetSlot         int      `json:"target_slot"`          // Which principle to modify (4-10 only)
	CurrentPrinciple   string   `json:"current_principle"`    // What it says now
	ProposedPrinciple  string   `json:"proposed_principle"`   // What it should say
	Justification      string   `json:"justification"`        // Why this change is needed
	TestActions        []Action `json:"test_actions"`         // Actions to validate the change
	BaselineComparison string   `json:"baseline_comparison"`  // What to compare against
	ValidationStatus   string   `json:"validation_status"`    // "pending", "testing", "validated", "failed"
}

// PlanAssessment represents evaluation of progress after completing an action
type PlanAssessment struct {
	Timestamp       time.Time `json:"timestamp"`
	ProgressQuality string    `json:"progress_quality"` // "good", "partial", "poor"
	PlanValidity    string    `json:"plan_validity"`    // "valid", "needs_adjustment", "needs_replan"
	Reasoning       string    `json:"reasoning"`
	Recommendation  string    `json:"recommendation"`   // "continue", "adjust", "replan"
}

// Action represents a step taken toward completing a goal
type Action struct {
	Description string                 `json:"description"`
	Tool        string                 `json:"tool"` // "search", "web_parse", "sandbox", "memory_consolidation"
	Status      string                 `json:"status"` // "pending", "in_progress", "completed"
	Result      string                 `json:"result,omitempty"`
	Timestamp   time.Time              `json:"timestamp"`
	Metadata    map[string]interface{} `json:"metadata,omitempty"` // For passing extra params like purpose
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
	GoalSourceUserFailure     = "user_failure"
	GoalSourceKnowledgeGap    = "knowledge_gap"
	GoalSourceCuriosity       = "curiosity"
	GoalSourcePrinciple       = "principle"
	GoalSourceUserInterest    = "user_interest"
	GoalSourceSelfModification = "self_modification"
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
	ActionToolWebParse            = "web_parse"              // Generic (deprecated)
	ActionToolWebParseMetadata    = "web_parse_metadata"    // Phase 3.4: Lightweight metadata
	ActionToolWebParseGeneral     = "web_parse_general"     // Phase 3.4: Auto-summary
	ActionToolWebParseContextual  = "web_parse_contextual"  // Phase 3.4: Purpose-driven
	ActionToolWebParseChunked     = "web_parse_chunked"     // Phase 3.4: Chunk access
	ActionToolSandbox             = "sandbox"
	ActionToolMemoryConsolidation = "memory_consolidation"
	ActionToolSynthesis           = "synthesis"              // Phase 4: Research synthesis
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
    RawResponse     string              `json:"-"` // Holds unparsed LLM output for specialized parsing
    Insights        StringOrArray       `json:"insights"`
    Strengths       StringOrArray       `json:"strengths"`
    Weaknesses      StringOrArray       `json:"weaknesses"`
    KnowledgeGaps   StringOrArray       `json:"knowledge_gaps"`
    Patterns        StringOrArray       `json:"patterns"`
    GoalsToCreate   GoalsOrString       `json:"goals_to_create"`
    Learnings       LearningsOrString   `json:"learnings"`
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

// GoalsOrString handles JSON that can be string, empty array, array of strings, or array of goals
type GoalsOrString []GoalProposal

func (g *GoalsOrString) UnmarshalJSON(data []byte) error {
	// Try array of GoalProposal objects first
	var goals []GoalProposal
	if err := json.Unmarshal(data, &goals); err == nil {
		*g = GoalsOrString(goals)
		return nil
	}
	
	// Try array of strings (LLM sometimes does this)
	var stringArray []string
	if err := json.Unmarshal(data, &stringArray); err == nil {
		// Convert strings to GoalProposal objects
		proposals := make([]GoalProposal, 0, len(stringArray))
		for _, s := range stringArray {
			if s != "" {
				proposals = append(proposals, GoalProposal{
					Description:  s,
					Priority:     7, // Default priority
					Reasoning:    "Generated from simplified goal description",
					ActionPlan:   []string{},
					ExpectedTime: "unknown",
				})
			}
		}
		*g = GoalsOrString(proposals)
		return nil
	}
	
	// Try single string (ignore it)
	var str string
	if err := json.Unmarshal(data, &str); err == nil {
		*g = GoalsOrString{}
		return nil
	}
	
	return fmt.Errorf("goals_to_create must be array or string")
}

func (g GoalsOrString) ToSlice() []GoalProposal {
	return []GoalProposal(g)
}

// LearningsOrString handles JSON that can be string, empty array, array of strings, or array of learnings
type LearningsOrString []Learning

func (l *LearningsOrString) UnmarshalJSON(data []byte) error {
	// Try array of Learning objects first
	var learnings []Learning
	if err := json.Unmarshal(data, &learnings); err == nil {
		*l = LearningsOrString(learnings)
		return nil
	}
	
	// Try array of strings (LLM sometimes does this)
	var stringArray []string
	if err := json.Unmarshal(data, &stringArray); err == nil {
		// Convert strings to Learning objects
		learningObjs := make([]Learning, 0, len(stringArray))
		for _, s := range stringArray {
			if s != "" {
				learningObjs = append(learningObjs, Learning{
					What:       s,
					Context:    "Generated during dialogue cycle",
					Confidence: 0.7, // Default confidence
					Category:   "general",
				})
			}
		}
		*l = LearningsOrString(learningObjs)
		return nil
	}
	
	// Try single string (ignore it)
	var str string
	if err := json.Unmarshal(data, &str); err == nil {
		*l = LearningsOrString{}
		return nil
	}
	
	return fmt.Errorf("learnings must be array or string")
}

func (l LearningsOrString) ToSlice() []Learning {
	return []Learning(l)
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

// ResearchPlan represents a structured multi-step investigation
type ResearchPlan struct {
	RootQuestion    string             `json:"root_question"`     // Main question being investigated
	SubQuestions    []ResearchQuestion `json:"sub_questions"`     // Ordered list of investigation steps
	CurrentStep     int                `json:"current_step"`      // Which sub-question (0-indexed)
	SynthesisNeeded bool               `json:"synthesis_needed"`  // All questions answered, ready to synthesize
	CreatedAt       time.Time          `json:"created_at"`
	UpdatedAt       time.Time          `json:"updated_at"`
}

// ResearchQuestion represents one investigation step in the research plan
type ResearchQuestion struct {
	ID              string   `json:"id"`                 // e.g., "q1", "q2"
	Question        string   `json:"question"`           // What to investigate
	SearchQuery     string   `json:"search_query"`       // Suggested search terms
	ParentID        string   `json:"parent_id"`          // For nested questions (future use)
	Status          string   `json:"status"`             // "pending", "in_progress", "completed", "skipped"
	Priority        int      `json:"priority"`           // 1-10 importance
	Dependencies    []string `json:"dependencies"`       // Question IDs that must complete first
	SourcesFound    []string `json:"sources_found"`      // URLs discovered
	KeyFindings     string   `json:"key_findings"`       // Summary of findings
	ConfidenceLevel float64  `json:"confidence_level"`   // 0.0-1.0 confidence in answer
}

// ResearchQuestion status constants
const (
	ResearchStatusPending    = "pending"
	ResearchStatusInProgress = "in_progress"
	ResearchStatusCompleted  = "completed"
	ResearchStatusSkipped    = "skipped"
)

// GoalSupportValidation represents LLM's assessment of goal linkage
type GoalSupportValidation struct {
	SupportsGoalID string  `json:"supports_goal_id"` // ID of primary goal being supported
	Confidence     float64 `json:"confidence"`       // 0.0-1.0 confidence in linkage
	Reasoning      string  `json:"reasoning"`        // Why this secondary supports the primary
	IsValid        bool    `json:"is_valid"`         // True if linkage is meaningful
}

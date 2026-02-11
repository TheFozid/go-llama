package dialogue

import (
    "time"
)

// Goal represents a self-directed learning objective
type Goal struct {
    ID              string                  `json:"id"`
    Description     string                  `json:"description"`
    Source          string                  `json:"source"` // "user_failure", "knowledge_gap", "curiosity", "principle"
    Priority        int                     `json:"priority"` // 1-10
    Created         time.Time               `json:"created"`
    Progress        float64                 `json:"progress"` // 0.0 to 1.0
    Actions         []Action                `json:"actions"`
    Status          string                  `json:"status"` // "active", "completed", "abandoned"
    Outcome         string                  `json:"outcome,omitempty"` // "good", "bad", "neutral" (when completed)
    ResearchPlan    *ResearchPlan           `json:"research_plan,omitempty"` // Multi-step investigation plan
    Metadata        map[string]interface{}  `json:"metadata,omitempty"` // Additional metadata for the goal
    FailureCount    int                     `json:"failure_count"` // Track consecutive failures before abandoning
    Tier            string                  `json:"tier"` // "primary", "secondary", "tactical"
    SupportsGoals   []string                `json:"supports_goals,omitempty"` // IDs of primary goals this supports
    DependencyScore float64                 `json:"dependency_score"` // 0.0-1.0 confidence in dependency link
    HasPendingWork  bool                    `json:"has_pending_work"` // True if goal has pending actions
    LastPursued     time.Time               `json:"last_pursued"` // When this goal was last worked on
    LastAssessment  *PlanAssessment         `json:"last_assessment,omitempty"` // Result of last progress check
    ReplanCount     int                     `json:"replan_count"` // Number of times this goal has been replanned
    SelfModGoal     *SelfModificationGoal   `json:"self_mod_goal,omitempty"` // Self-modification details if applicable
}

// SelfModificationGoal represents a deliberate attempt to modify thinking patterns
type SelfModificationGoal struct {
    TargetSlot        int      `json:"target_slot"`          // Which principle to modify (4-10 only)
    CurrentPrinciple  string   `json:"current_principle"`    // What it says now
    ProposedPrinciple string   `json:"proposed_principle"`   // What it should say
    Justification     string   `json:"justification"`        // Why this change is needed
    TestActions       []Action `json:"test_actions"`         // Actions to validate the change
    BaselineComparison string  `json:"baseline_comparison"`  // What to compare against
    ValidationStatus  string   `json:"validation_status"`    // "pending", "testing", "validated", "failed"
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
    ActiveGoals        []Goal                 `json:"active_goals"`
    CurrentMissionMap  map[string]interface{} `json:"current_mission_map"` // Helper to store mission details
    CapabilityMatrix   []Capability           `json:"capability_matrix"`
    CompletedGoals     []Goal                 `json:"completed_goals"`
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
    CycleID        int           `json:"cycle_id"`
    StartTime      time.Time     `json:"start_time"`
    EndTime        time.Time     `json:"end_time"`
    Duration       time.Duration `json:"duration"`
    ThoughtCount   int           `json:"thought_count"`
    ActionCount    int           `json:"action_count"`
    TokensUsed     int           `json:"tokens_used"`
    GoalsCreated   int           `json:"goals_created"`
    GoalsCompleted int           `json:"goals_completed"`
    MemoriesStored int           `json:"memories_stored"`
    StopReason     string        `json:"stop_reason"` // "max_thoughts", "max_time", "action_requirement", "natural_stop"
}

// ActionPlanStep represents a step in a dynamic action plan
type ActionPlanStep struct {
    Description      string `json:"description"`
    Tool             string `json:"tool"`
    Query            string `json:"query,omitempty"`
    ExpectedOutcome  string `json:"expected_outcome"`
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

// Mission represents the high-level objective given to the AI
type Mission struct {
    ID          string    `json:"id"`
    Description string    `json:"description"`
    Status      string    `json:"status"` // "active", "completed", "abandoned"
    CreatedAt   time.Time `json:"created_at"`
}

// Capability represents a dynamic skill or attribute required for a mission
type Capability struct {
    Name  string  `json:"name"`
    Score float64 `json:"score"` // 0.0 to 1.0
}

// SimulationAction represents a practice scenario execution
type SimulationAction struct {
    Scenario          string  `json:"scenario"`
    Difficulty        string  `json:"difficulty"`
    ActionTaken       string  `json:"action_taken"`
    Score             float64 `json:"score"`
    Feedback          string  `json:"feedback"`
    CapabilityUpdated string  `json:"capability_updated"`
    ScoreDelta        float64 `json:"score_delta"`
}

// GoalSource constants
const (
    GoalSourceUserFailure      = "user_failure"
    GoalSourceKnowledgeGap     = "knowledge_gap"
    GoalSourceCuriosity        = "curiosity"
    GoalSourcePrinciple        = "principle"
    GoalSourceUserInterest     = "user_interest"
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
    ActionToolSearch       = "search"
    ActionToolSimulate     = "simulate"
    ActionToolWebParseUnified    = "web_parse_unified"
    ActionToolSandbox             = "sandbox"
    ActionToolMemoryConsolidation = "memory_consolidation"
    ActionToolSynthesis           = "synthesis"
)

// StopReason constants
const (
    StopReasonMaxThoughts       = "max_thoughts"
    StopReasonMaxTime           = "max_time"
    StopReasonActionRequirement = "action_requirement"
    StopReasonNaturalStop       = "natural_stop"
)

// ResearchQuestion status constants
const (
    ResearchStatusPending    = "pending"
    ResearchStatusInProgress = "in_progress"
    ResearchStatusCompleted  = "completed"
    ResearchStatusSkipped    = "skipped"
)

package goal

import (
    "time"
)

// GoalState defines the lifecycle state of a goal
type GoalState string

const (
    StateProposed    GoalState = "PROPOSED"
    StateValidating  GoalState = "VALIDATING"
    StateQueued      GoalState = "QUEUED"
    StateActive      GoalState = "ACTIVE"
    StateReviewing   GoalState = "REVIEWING"
    StatePaused      GoalState = "PAUSED"
    StateCompleted   GoalState = "COMPLETED"
    StateArchived    GoalState = "ARCHIVED"
)

// GoalOrigin defines who created the goal
type GoalOrigin string

const (
    OriginUser GoalOrigin = "USER"
    OriginAI   GoalOrigin = "AI"
)

// GoalType categorizes the nature of the goal
type GoalType string

const (
    TypeAchievable         GoalType = "ACHIEVABLE"
    TypeOngoing            GoalType = "ONGOING"
    TypeCapabilityBuilding GoalType = "CAPABILITY_BUILDING"
)

// ArchiveReason defines why a goal was archived
type ArchiveReason string

const (
    ArchiveMissingTools  ArchiveReason = "MISSING_TOOLS"
    ArchiveImpossible    ArchiveReason = "IMPOSSIBLE"
    ArchiveUserCancelled ArchiveReason = "USER_CANCELLED"
    ArchivePriorityDecay ArchiveReason = "PRIORITY_DECAY"
    ArchiveDuplicate     ArchiveReason = "DUPLICATE"
    ArchiveValidationFailed ArchiveReason = "VALIDATION_FAILED"
)

// SkillProficiency defines the level of a skill
type SkillProficiency string

const (
    ProficiencyDeveloping SkillProficiency = "DEVELOPING"
    ProficiencyCompetent  SkillProficiency = "COMPETENT"
    ProficiencyProficient SkillProficiency = "PROFICIENT"
    ProficiencyExpert     SkillProficiency = "EXPERT"
)

// SubGoalStatus defines the state of a sub-goal
type SubGoalStatus string

const (
    SubGoalPending   SubGoalStatus = "PENDING"
    SubGoalActive    SubGoalStatus = "ACTIVE"
    SubGoalCompleted SubGoalStatus = "COMPLETED"
    SubGoalFailed    SubGoalStatus = "FAILED"
    SubGoalSkipped   SubGoalStatus = "SKIPPED"
)

// Goal represents a self-directed objective with full lifecycle tracking
type Goal struct {
    // Identity
    ID              string      `json:"id"`
    Title           string      `json:"title"`
    Description     string      `json:"description"`
    Origin          GoalOrigin  `json:"origin"`
    CreationTime    time.Time   `json:"creation_time"`
    SourceContextID string      `json:"source_context_id"` // chat_id or reflection_id

    // Classification
    Type                   GoalType   `json:"type"`
    ComplexityScore        int        `json:"complexity_score"` // 1-100
    TimeScore              int        `json:"time_score"`       // Estimated effort units
    InitialTimeScore       int        `json:"initial_time_score"`
    RequiredCapabilities   []string   `json:"required_capabilities"`

    // Priority
    BasePriority           int        `json:"base_priority"`      // USER: 80-100, AI: 40-60
    CurrentPriority        int        `json:"current_priority"`
    PriorityCap            int        `json:"priority_cap"`       // Max 100
    LastPriorityCalculation time.Time `json:"last_priority_calculation"`
    ProposalCount          int        `json:"proposal_count"`
    LastProposedTimestamp  time.Time  `json:"last_proposed_timestamp"`

    // Progress
    State                  GoalState  `json:"state"`
    ProgressPercentage     float64    `json:"progress_percentage"`
    TimeInvestedScore      int        `json:"time_invested_score"`
    TimeRemainingEstimate  int        `json:"time_remaining_estimate"`
    LastProgressTimestamp  time.Time  `json:"last_progress_timestamp"`
    StagnationCounter      int        `json:"stagnation_counter"`
    CyclesWithoutProgress  int        `json:"cycles_without_progress"`

    // Metrics (Self-Derived)
    SuccessCriteria        string                 `json:"success_criteria"`
    MeasurementMethod      string                 `json:"measurement_method"`
    CurrentMetricValues    map[string]interface{} `json:"current_metric_values"`
    CompletionThreshold    float64                `json:"completion_threshold"`
    MetricHistory          []MetricDataPoint      `json:"metric_history"`

    // Strategy
    CurrentPlan            []string   `json:"current_plan"` // Ordered sub-goal IDs
    PlanVersion            int        `json:"plan_version"`
    PlanHistory            []PlanRecord `json:"plan_history"`
    AttemptedApproaches    []string   `json:"attempted_approaches"`
    FailedApproaches       []string   `json:"failed_approaches"`
    LessonsLearned         []string   `json:"lessons_learned"`

    // Sub-Goal Tree
    SubGoals               []SubGoal  `json:"sub_goals"`
    ActiveSubGoalID        string     `json:"active_sub_goal_id"`
    TreeDepth              int        `json:"tree_depth"`

    // Skills & Knowledge
    SkillsAcquired         []string   `json:"skills_acquired"` // Skill IDs
    KnowledgeGained        []string   `json:"knowledge_gained"` // Memory IDs
    PracticeSessionsCount  int        `json:"practice_sessions_count"`
    SimulationsRunCount    int        `json:"simulations_run_count"`

    // Archive Data
    ArchiveReason          ArchiveReason `json:"archive_reason,omitempty"`
    MissingCapabilities    []string      `json:"missing_capabilities,omitempty"`
    RevivalConditions      map[string]interface{} `json:"revival_conditions,omitempty"`
    ArchiveTimestamp       time.Time     `json:"archive_timestamp,omitempty"`
}

// SubGoal represents a component of a larger goal
type SubGoal struct {
    ID                string        `json:"id"` // Hierarchical e.g., "1.1"
    Title             string        `json:"title"`
    Description       string        `json:"description"`
    Status            SubGoalStatus `json:"status"`
    Dependencies      []string      `json:"dependencies"` // IDs of prerequisite sub-goals
    EstimatedEffort   string        `json:"estimated_effort"` // SIMPLE, MEDIUM, COMPLEX
    Outcome           string        `json:"outcome"`
    FailureReason     string        `json:"failure_reason"`
    LLMCallsEstimate  int           `json:"llm_calls_estimate"`
    ToolCallsEstimate int           `json:"tool_calls_estimate"`
    TimeScoreEstimate int           `json:"time_score_estimate"`
    
    // Intelligence-driven execution fields
    ActionType        ActionType    `json:"action_type"` // RESEARCH, PRACTICE, EXECUTE_TOOL, etc.
    ToolName          string        `json:"tool_name"`   // Specific tool to use, e.g., "search", "browser"
}

// Skill represents an acquired capability
type Skill struct {
    ID                  string            `json:"id"`
    Name                string            `json:"name"`
    Description         string            `json:"description"`
    AcquisitionContext  string            `json:"acquisition_context"` // Goal ID
    ProficiencyLevel    SkillProficiency  `json:"proficiency_level"`
    DomainApplicability string            `json:"domain_applicability"` // GENERAL, DOMAIN_SPECIFIC
    TransferabilityScore int              `json:"transferability_score"` // 0-100
    CreatedAt           time.Time         `json:"created_at"`
    LastUsedAt          time.Time         `json:"last_used_at"`
    FreshnessScore      int               `json:"freshness_score"` // Decays over time
    UseCount            int               `json:"use_count"`
    RelatedSkills       []string          `json:"related_skills"`
}

// MetricDataPoint tracks historical metric values
type MetricDataPoint struct {
    Timestamp time.Time         `json:"timestamp"`
    Values    map[string]interface{} `json:"values"`
}

// ActionType defines the category of action required for a sub-goal
type ActionType string

const (
    ActionResearch    ActionType = "RESEARCH"
    ActionPractice    ActionType = "PRACTICE"
    ActionLearn       ActionType = "LEARN"
    ActionCreate      ActionType = "CREATE"
    ActionReflect     ActionType = "REFLECT"
    ActionPlan        ActionType = "PLAN"
    ActionMeasure     ActionType = "MEASURE"
    ActionExecuteTool ActionType = "EXECUTE_TOOL"
)

// ActionResult captures the outcome of a sub-goal execution
type ActionResult struct {
    SubGoalID   string    `json:"sub_goal_id"`
    Success     bool      `json:"success"`
    Output      string    `json:"output"`
    Error       string    `json:"error,omitempty"`
    Duration    time.Duration `json:"duration"`
    SkillsUsed  []string  `json:"skills_used"`
    ToolUsed    string    `json:"tool_used"`
    Timestamp   time.Time `json:"timestamp"`
}

// Simulation represents a practice environment scenario (MDD 14.2)
type Simulation struct {
    ID                 string                 `json:"id"`
    ScenarioDescription string                `json:"scenario_description"`
    Objective          string                 `json:"objective"`
    SuccessCriteria    string                 `json:"success_criteria"`
    Participants       []SimulationParticipant `json:"participants"`
    ConversationLog    []string               `json:"conversation_log"`
    ObserverNotes      []string               `json:"observer_notes"`
    OutcomeAssessment  *SimulationOutcome     `json:"outcome_assessment,omitempty"`
}

// SimulationParticipant defines a role in a simulation
type SimulationParticipant struct {
    PersonalityID string   `json:"personality_id"`
    Role          string   `json:"role"`
    SystemPrompt  string   `json:"system_prompt"`
    Traits        []string `json:"traits"`
}

// SimulationOutcome captures the result of a practice run
type SimulationOutcome struct {
    Success      bool    `json:"success"`
    Score        float64 `json:"score"`
    Feedback     string  `json:"feedback"`
    Learnings    []string `json:"learnings"`
}

// PlanRecord stores history of plan changes
type PlanRecord struct {
    Version   int        `json:"version"`
    Timestamp time.Time  `json:"timestamp"`
    Plan      []string   `json:"plan"` // Sub-goal IDs
    Reason    string     `json:"reason"`
}

// PriorityConfig holds configuration for priority calculations
type PriorityConfig struct {
    UserBaseMin         int     `json:"user_base_min"`
    UserBaseMax         int     `json:"user_base_max"`
    AIBaseMin           int     `json:"ai_base_min"`
    AIBaseMax           int     `json:"ai_base_max"`
    DecayRateActive     float64 `json:"decay_rate_active"`
    DecayRateQueued     float64 `json:"decay_rate_queued"`
    StrengtheningMin    int     `json:"strengthening_min"`
    StrengtheningMax    int     `json:"strengthening_max"`
    SelectionExponent   float64 `json:"selection_exponent"`
    ProgressBonusFactor float64 `json:"progress_bonus_factor"`
}

// DefaultPriorityConfig returns the default priority configuration
func DefaultPriorityConfig() *PriorityConfig {
    return &PriorityConfig{
        UserBaseMin:         80,
        UserBaseMax:         100,
        AIBaseMin:           40,
        AIBaseMax:           60,
        DecayRateActive:     1.0, // -1 per cycle
        DecayRateQueued:     5.0, // -5 per cycle
        StrengtheningMin:    5,
        StrengtheningMax:    15,
        SelectionExponent:   0.7,
        ProgressBonusFactor: 0.5,
    }
}

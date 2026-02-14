package goal

import (
    "math/rand"
    "time"

    "github.com/google/uuid"
)

// Factory handles the creation of valid Goal objects
type Factory struct {
    priorityConfig *PriorityConfig
}

// NewFactory creates a new Goal factory
func NewFactory(config *PriorityConfig) *Factory {
    if config == nil {
        config = DefaultPriorityConfig()
    }
    return &Factory{priorityConfig: config}
}

// CreateUserGoal creates a new goal originated by the user
func (f *Factory) CreateUserGoal(description, contextID string) *Goal {
    now := time.Now()
    basePriority := f.generateBasePriority(OriginUser)

    return &Goal{
        // Identity
        ID:              uuid.New().String(),
        Title:           extractTitle(description), // Simple extraction for now
        Description:     description,
        Origin:          OriginUser,
        CreationTime:    now,
        SourceContextID: contextID,

        // State
        State:           StateProposed,
        
        // Priority
        BasePriority:    basePriority,
        CurrentPriority: basePriority,
        PriorityCap:     100,
        ProposalCount:   1,
        LastProposedTimestamp: now,

        // Classification
        Type: TypeAchievable, // Default, overridden by LLM in Phase 3
        
        // Initialization
        SubGoals:        []SubGoal{},
        CurrentPlan:     []string{},
        SkillsAcquired:  []string{},
        KnowledgeGained: []string{},
        RequiredCapabilities: []string{},
        AttemptedApproaches: []string{},
        FailedApproaches:    []string{},
        LessonsLearned:      []string{},
        MetricHistory:       []MetricDataPoint{},
    }
}

// CreateAIGoal creates a new goal originated by the AI
func (f *Factory) CreateAIGoal(description, contextID string) *Goal {
    now := time.Now()
    basePriority := f.generateBasePriority(OriginAI)

    return &Goal{
        // Identity
        ID:              uuid.New().String(),
        Title:           extractTitle(description),
        Description:     description,
        Origin:          OriginAI,
        CreationTime:    now,
        SourceContextID: contextID,

        // State
        State:           StateProposed,
        
        // Priority
        BasePriority:    basePriority,
        CurrentPriority: basePriority,
        PriorityCap:     100,
        ProposalCount:   1,
        LastProposedTimestamp: now,

        // Classification
        Type: TypeCapabilityBuilding, // AI goals usually skill-oriented
        
        // Initialization
        SubGoals:        []SubGoal{},
        CurrentPlan:     []string{},
        SkillsAcquired:  []string{},
        KnowledgeGained: []string{},
        RequiredCapabilities: []string{},
        AttemptedApproaches: []string{},
        FailedApproaches:    []string{},
        LessonsLearned:      []string{},
        MetricHistory:       []MetricDataPoint{},
    }
}

// CreateSubGoal creates a sub-goal attached to a parent goal
func (f *Factory) CreateSubGoal(parent *Goal, description string, hierarchyID string) *SubGoal {
    return &SubGoal{
        ID:          hierarchyID, // e.g., "1.1"
        Title:       extractTitle(description),
        Description: description,
        Status:      SubGoalPending,
        Dependencies: []string{},
    }
}

// generateBasePriority returns a random priority within the allowed range for the origin
func (f *Factory) generateBasePriority(origin GoalOrigin) int {
    var min, max int
    if origin == OriginUser {
        min = f.priorityConfig.UserBaseMin
        max = f.priorityConfig.UserBaseMax
    } else {
        min = f.priorityConfig.AIBaseMin
        max = f.priorityConfig.AIBaseMax
    }

    // rand.Intn is exclusive on the max, so we add 1
    return rand.Intn(max-min+1) + min
}

// extractTitle creates a brief title from a description
// Note: In Phase 3, this will be enhanced by LLM summarization
func extractTitle(description string) string {
    if len(description) > 60 {
        return description[:57] + "..."
    }
    return description
}

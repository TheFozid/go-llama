package goal

// TimeScoreCalculator estimates the effort units (time score) for a goal.
// Phase 2 Implementation: Uses heuristic calculations.
type TimeScoreCalculator struct {
    BaseUnit          int
    SubGoalMultiplier int
    DepthMultiplier   int
}

// NewTimeScoreCalculator creates a new calculator with default heuristics.
func NewTimeScoreCalculator() *TimeScoreCalculator {
    return &TimeScoreCalculator{
        BaseUnit:          10, // Base effort for any goal
        SubGoalMultiplier: 5,  // Additional effort per sub-goal
        DepthMultiplier:   10, // Additional effort for tree complexity
    }
}

// CalculateTimeScore estimates effort based on goal properties.
// Formula: Base + (SubGoalCount * Multiplier) + (Depth * Multiplier)
func (t *TimeScoreCalculator) CalculateTimeScore(description string, subGoalCount int, treeDepth int) int {
    score := t.BaseUnit
    score += subGoalCount * t.SubGoalMultiplier
    
    if treeDepth > 0 {
        score += treeDepth * t.DepthMultiplier
    }
    
    return score
}

// EstimateLLMCalls provides a rough estimate of LLM calls based on complexity.
func (t *TimeScoreCalculator) EstimateLLMCalls(complexityScore int) int {
    // Simple heuristic: 1 call per 10 complexity points, min 1
    if complexityScore <= 0 {
        return 1
    }
    return complexityScore / 10
}

// RecalculateRemainingScore adjusts the time score based on remaining sub-goals.
func (t *TimeScoreCalculator) RecalculateRemainingScore(g *Goal) int {
    completedCount := 0
    for _, sg := range g.SubGoals {
        if sg.Status == SubGoalCompleted {
            completedCount++
        }
    }
    
    remainingCount := len(g.SubGoals) - completedCount
    // Re-use the calculation logic but for remaining items
    return t.CalculateTimeScore(g.Description, remainingCount, g.TreeDepth)
}

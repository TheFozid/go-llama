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

// LLMEnhancedCalculator wraps the heuristic calculator with LLM estimation capabilities.
type LLMEnhancedCalculator struct {
    heuristic *TimeScoreCalculator
    llm       LLMService
}

// NewLLMEnhancedCalculator creates a new calculator with LLM fallback.
func NewLLMEnhancedCalculator(heuristic *TimeScoreCalculator, llm LLMService) *LLMEnhancedCalculator {
    return &LLMEnhancedCalculator{
        heuristic: heuristic,
        llm:       llm,
    }
}

// EstimateTimeScore uses LLM to estimate effort if heuristics are insufficient.
func (l *LLMEnhancedCalculator) EstimateTimeScore(ctx context.Context, g *Goal) (int, error) {
    // 1. Try heuristic first if we have sub-goal counts
    if len(g.SubGoals) > 0 {
        return l.heuristic.CalculateTimeScore(g.Description, len(g.SubGoals), g.TreeDepth), nil
    }

    // 2. Use LLM for estimation
    log.Printf("[TimeScore] Using LLM to estimate effort for: %s", g.Description)
    
    prompt := fmt.Sprintf(`Estimate the computational effort (Time Score) required to complete the following goal.
    
Goal: %s
Type: %s

Output JSON:
{
  "time_score": 50, // Integer representing effort units (10-500 range)
  "complexity": "MEDIUM", // SIMPLE, MEDIUM, COMPLEX, EXTREME
  "reasoning": "Brief explanation"
}`, g.Description, g.Type)

    var response struct {
        TimeScore  int    `json:"time_score"`
        Complexity string `json:"complexity"`
    }

    if err := l.llm.GenerateJSON(ctx, prompt, &response); err != nil {
        log.Printf("[TimeScore] LLM estimation failed, falling back to base heuristic: %v", err)
        return l.heuristic.BaseUnit, nil // Return base unit as fallback
    }

    if response.TimeScore <= 0 {
        return l.heuristic.BaseUnit, nil
    }
    return response.TimeScore, nil
}

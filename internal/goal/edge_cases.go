// internal/goal/edge_cases.go
package goal

import (
    "context"
    "log"
)

// EdgeCaseHandler manages exceptional scenarios in goal pursuit.
type EdgeCaseHandler struct {
    Repo GoalRepository
}

// NewEdgeCaseHandler creates a new handler for edge cases.
func NewEdgeCaseHandler(repo GoalRepository) *EdgeCaseHandler {
    return &EdgeCaseHandler{Repo: repo}
}

// HandleContradictoryGoal manages goals that might conflict with existing ones.
// Design Decision (17.1): We permit contradictions. Both goals remain, and learnings are retained.
func (e *EdgeCaseHandler) HandleContradictoryGoal(ctx context.Context, newGoal *Goal, existingGoals []*Goal) {
    // Check for semantic contradiction (simplified heuristic)
    // In a full implementation, we would use embeddings to detect semantic opposites.
    
    log.Printf("[EdgeCase] Checking for contradiction for goal: %s", newGoal.Description)
    
    // We do not block the goal. We just log it.
    // Contradictions are allowed to exist in the queue.
}

// HandlePerpetualGoal manages goals with no clear end state (e.g., "Become better at X").
// Design Decision (17.2): Define sufficient achievement thresholds.
func (e *EdgeCaseHandler) HandlePerpetualGoal(ctx context.Context, g *Goal) {
    if g.Type == TypeOngoing {
        // Ensure it doesn't get stuck in 100% progress loops
        if g.ProgressPercentage >= 100.0 {
            log.Printf("[EdgeCase] Perpetual goal %s reached 100%% progress. Resetting metrics for ongoing improvement.", g.ID)
            // Reset or adjust metrics to allow continued pursuit if desired,
            // or mark as "Completed" for this iteration.
            // For now, we prevent it from being "done" permanently by capping visibility.
            g.ProgressPercentage = 99.0 
        }
    }
}

// HandleSubGoalFailure decides the impact of a failed sub-goal on the parent.
// Design Decision (17.3): Evaluate criticality before failing parent.
func (e *EdgeCaseHandler) HandleSubGoalFailure(ctx context.Context, subGoal *SubGoal, parentGoal *Goal) string {
    log.Printf("[EdgeCase] Handling failure of sub-goal %s for parent %s", subGoal.ID, parentGoal.ID)

    // Heuristic: If a sub-goal with "COMPLEX" effort fails, it might be critical.
    // If it was "SIMPLE", we might find an alternative.
    
    if subGoal.EstimatedEffort == "COMPLEX" {
        // Critical failure?
        // For now, we just log. The ReviewProcessor will handle the state change.
        return "CRITICAL_FAILURE"
    }

    // Try to find alternative or mark as skip
    return "REPLAN_BRANCH"
}

// HandleStrategyLoop prevents cycling through the same failed approaches.
// Design Decision (17.4): Maintain records of attempted approaches.
func (e *EdgeCaseHandler) HandleStrategyLoop(ctx context.Context, g *Goal, proposedApproach string) bool {
    for _, attempted := range g.AttemptedApproaches {
        if attempted == proposedApproach {
            log.Printf("[EdgeCase] Strategy loop detected for goal %s: Approach '%s' already tried.", g.ID, proposedApproach)
            return true // Loop detected
        }
    }
    return false
}

// HandleUnknownUnknowns detects gaps in knowledge during execution.
// Design Decision (17.5): Dynamically add knowledge acquisition sub-goals.
func (e *EdgeCaseHandler) HandleUnknownUnknowns(ctx context.Context, g *Goal, gapDescription string) {
    log.Printf("[EdgeCase] Knowledge gap detected for goal %s: %s", g.ID, gapDescription)
    
    // Create a new discovery sub-goal
    newSubGoal := SubGoal{
        ID:          generateUniqueSubGoalID(g),
        Title:       "Gap Discovery: " + truncate(gapDescription, 20),
        Description: gapDescription,
        Status:      SubGoalPending,
        // Insert at the beginning of the plan? Or end?
        // Usually, dependencies mean we must pause and learn this first.
    }

    // Prepend to active plan
    g.SubGoals = append([]SubGoal{newSubGoal}, g.SubGoals...)
}

// HandleContextChange validates if a paused goal is still relevant.
// Design Decision (17.6): Validate context before reactivation.
func (e *EdgeCaseHandler) HandleContextChange(ctx context.Context, g *Goal) bool {
    log.Printf("[EdgeCase] Checking context validity for paused goal %s", g.ID)

    // If the source chat was deleted, or context is gone, we might archive.
    // For now, we assume context is valid unless explicitly flagged.
    // Real implementation would check if SourceContextID exists in DB.
    
    return true // Context is valid
}

// Helper to generate a unique ID for dynamically added sub-goals
func generateUniqueSubGoalID(g *Goal) string {
    return fmt.Sprintf("dyn-%d", time.Now().UnixNano())
}

func truncate(s string, maxLen int) string {
    if len(s) <= maxLen {
        return s
    }
    return s[:maxLen] + "..."
}

package goal

// ReviewOutcome defines the result of a review process.
type ReviewOutcome struct {
    Decision   string // "CONTINUE", "REPLAN", "DEMOTE", "COMPLETE", "ARCHIVE"
    Reason     string
    NextGoalID string // Populated if decision is DEMOTE
}

// ReviewProcessor handles the decision-making process for active goals.
type ReviewProcessor struct {
    Selector   *GoalSelector
    Calculator *Calculator
    Monitor    *ProgressMonitor
}

// NewReviewProcessor creates a new review processor.
func NewReviewProcessor(selector *GoalSelector, calc *Calculator, monitor *ProgressMonitor) *ReviewProcessor {
    return &ReviewProcessor{
        Selector:   selector,
        Calculator: calc,
        Monitor:    monitor,
    }
}

// ExecuteReview runs the full review process for an active goal.
func (r *ReviewProcessor) ExecuteReview(activeGoal *Goal, queuedGoals []*Goal) ReviewOutcome {
    // 1. Assess Progress
    r.Monitor.CalculateProgressPercentage(activeGoal)

    // 2. Check Stagnation
    isStagnant := r.Monitor.DetectStagnation(activeGoal)

    // 3. Check Completion
    if activeGoal.ProgressPercentage >= 100.0 {
        return ReviewOutcome{
            Decision: "COMPLETE",
            Reason:   "Success criteria met.",
        }
    }

    // 4. Compare against Queue
    activeScore, bestQueued := r.Selector.CompareForReview(activeGoal, queuedGoals)

    // 5. Determine Outcome
    
    // Case: There is a higher priority goal waiting
    if bestQueued != nil {
        queuedScore := r.Calculator.CalculateSelectionScore(bestQueued)
        
        // If queued score significantly beats active score (considering progress bonus)
        // We use the ShouldSwitchGoal logic inverted: should we demote current?
        // Note: ShouldSwitchGoal checks if Proposed > Active. 
        // Here we check if BestQueued > ActiveScore.
        if queuedScore > activeScore {
            return ReviewOutcome{
                Decision:   "DEMOTE",
                Reason:     "Higher priority goal found: " + bestQueued.ID,
                NextGoalID: bestQueued.ID,
            }
        }
    }

    // Case: Stagnation detected
    if isStagnant {
        return ReviewOutcome{
            Decision: "REPLAN",
            Reason:   "Stagnation detected; strategy needs revision.",
        }
    }

    // Case: Time threshold reached but no better goal (Review triggered by time)
    // If we are here, the goal is still the best option.
    return ReviewOutcome{
        Decision: "CONTINUE",
        Reason:   "Goal remains highest priority.",
    }
}

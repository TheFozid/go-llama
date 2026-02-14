package goal

import "sort"

// GoalSelector ranks and selects goals based on priority and effort.
type GoalSelector struct {
    Calculator *Calculator
}

// NewGoalSelector creates a new selector with a priority calculator.
func NewGoalSelector(calc *Calculator) *GoalSelector {
    return &GoalSelector{Calculator: calc}
}

// SelectNextGoal returns the goal with the highest selection score.
// Returns nil if the queue is empty.
func (s *GoalSelector) SelectNextGoal(queuedGoals []*Goal) *Goal {
    if len(queuedGoals) == 0 {
        return nil
    }

    // Rank goals
    ranked := s.RankGoals(queuedGoals)
    
    // Return top ranked
    return ranked[0]
}

// RankGoals sorts goals by SelectionScore in descending order.
func (s *GoalSelector) RankGoals(goals []*Goal) []*Goal {
    // Create a slice for sorting to avoid mutating order unexpectedly
    sorted := make([]*Goal, len(goals))
    copy(sorted, goals)

    sort.Slice(sorted, func(i, j int) bool {
        scoreI := s.Calculator.CalculateSelectionScore(sorted[i])
        scoreJ := s.Calculator.CalculateSelectionScore(sorted[j])
        return scoreI > scoreJ // Descending order
    })

    return sorted
}

// CompareForReview compares an active goal against queued goals during a review.
// Returns the active goal score (with bonus) and the best queued goal.
func (s *GoalSelector) CompareForReview(activeGoal *Goal, queuedGoals []*Goal) (activeScore float64, bestQueuedGoal *Goal) {
    activeScore = s.Calculator.CalculateProgressBonus(activeGoal)
    
    if len(queuedGoals) == 0 {
        return activeScore, nil
    }

    // Find best queued goal
    bestQueuedGoal = s.SelectNextGoal(queuedGoals)
    return activeScore, bestQueuedGoal
}

// ShouldActivateGoal determines if a proposed goal should interrupt the active goal.
func (s *GoalSelector) ShouldActivateGoal(proposedGoal *Goal, activeGoal *Goal) bool {
    if activeGoal == nil {
        return true // No active goal, safe to activate
    }
    return s.Calculator.ShouldSwitchGoal(activeGoal, proposedGoal)
}

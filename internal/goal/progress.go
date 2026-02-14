package goal

// ProgressMonitor tracks goal advancement and detects stagnation.
type ProgressMonitor struct {
    StagnationThreshold int // Max cycles without progress before flagging
}

// NewProgressMonitor creates a new monitor with default settings.
func NewProgressMonitor() *ProgressMonitor {
    return &ProgressMonitor{
        StagnationThreshold: 5, // Default: 5 cycles
    }
}

// CalculateProgressPercentage updates the progress percentage based on sub-goal completion.
func (p *ProgressMonitor) CalculateProgressPercentage(g *Goal) float64 {
    if len(g.SubGoals) == 0 {
        // If no sub-goals, progress is binary (0 or 100) or based on other metrics.
        // For Phase 2, we assume 0 until completion logic is added.
        return g.ProgressPercentage 
    }

    completedCount := 0
    for _, sg := range g.SubGoals {
        if sg.Status == SubGoalCompleted {
            completedCount++
        }
    }

    percentage := (float64(completedCount) / float64(len(g.SubGoals))) * 100.0
    
    // Update the goal directly (as per design "UpdateProgress")
    g.ProgressPercentage = percentage
    
    return percentage
}

// DetectStagnation checks if the goal has stalled.
func (p *ProgressMonitor) DetectStagnation(g *Goal) bool {
    return g.CyclesWithoutProgress >= p.StagnationThreshold
}

// IncrementStagnation increments the stagnation counter.
func (p *ProgressMonitor) IncrementStagnation(g *Goal) {
    g.CyclesWithoutProgress++
}

// ResetStagnation clears the stagnation counter (usually after progress).
func (p *ProgressMonitor) ResetStagnation(g *Goal) {
    g.CyclesWithoutProgress = 0
}

// CheckForLoop compares the current approach against attempted approaches.
func (p *ProgressMonitor) CheckForLoop(g *Goal, currentApproach string) bool {
    for _, attempted := range g.AttemptedApproaches {
        if attempted == currentApproach {
            return true
        }
    }
    return false
}

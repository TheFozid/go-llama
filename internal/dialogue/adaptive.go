package dialogue

import (
	"context"
	"log"
)

// AdaptiveConfig manages dynamic threshold adjustments
type AdaptiveConfig struct {
	// Base values from config
	baseSearchThreshold    float64
	baseGoalSimilarity     float64
	baseToolTimeout        int
	
	// Current adaptive values
	searchThreshold        float64
	goalSimilarityThreshold float64
	toolTimeout            int
	
	// Historical metrics for adaptation
	recentSearchSuccessRate float64
	recentGoalSuccessRate   float64
	averageMemoryCount      int
}

// NewAdaptiveConfig creates a new adaptive configuration manager
func NewAdaptiveConfig(baseSearchThreshold, baseGoalSimilarity float64, baseToolTimeout int) *AdaptiveConfig {
	return &AdaptiveConfig{
		baseSearchThreshold:     baseSearchThreshold,
		baseGoalSimilarity:      baseGoalSimilarity,
		baseToolTimeout:         baseToolTimeout,
		searchThreshold:         baseSearchThreshold,
		goalSimilarityThreshold: baseGoalSimilarity,
		toolTimeout:             baseToolTimeout,
		recentSearchSuccessRate: 0.5, // Start neutral
		recentGoalSuccessRate:   0.5,
		averageMemoryCount:      0,
	}
}

// UpdateMetrics recalculates adaptive thresholds based on current performance
func (ac *AdaptiveConfig) UpdateMetrics(ctx context.Context, state *InternalState, totalMemories int) {
	// Update memory count
	ac.averageMemoryCount = totalMemories
	
	// Calculate goal success rate from recent goals
	timeoutCount := 0
	successCount := 0
	totalGoals := 0
	
	if len(state.CompletedGoals) > 0 {
		recentGoals := state.CompletedGoals
		if len(recentGoals) > 10 {
			recentGoals = recentGoals[len(recentGoals)-10:]
		}
		
		for _, goal := range recentGoals {
			totalGoals++
			if goal.Outcome == "good" {
				successCount++
			}
			
			// Check if goal failed due to timeout
			for _, action := range goal.Actions {
				if action.Status == ActionStatusCompleted && 
				   (strings.Contains(strings.ToLower(action.Result), "timeout") ||
				    strings.Contains(strings.ToLower(action.Result), "deadline exceeded")) {
					timeoutCount++
					break // Count goal once even if multiple actions timed out
				}
			}
		}
		
		if totalGoals > 0 {
			ac.recentGoalSuccessRate = float64(successCount) / float64(totalGoals)
		}
	}
	
	// Adapt search threshold based on memory count
	// More memories = higher threshold (be more selective)
	if totalMemories > 100000 {
		ac.searchThreshold = ac.baseSearchThreshold + 0.15 // 0.45
	} else if totalMemories > 10000 {
		ac.searchThreshold = ac.baseSearchThreshold + 0.10 // 0.40
	} else if totalMemories > 1000 {
		ac.searchThreshold = ac.baseSearchThreshold + 0.05 // 0.35
	} else {
		ac.searchThreshold = ac.baseSearchThreshold // 0.30
	}
	
	// Adapt goal similarity based on success rate
	// Low success = be more aggressive about duplicates (higher threshold)
	// High success = allow more variation (lower threshold)
	if ac.recentGoalSuccessRate < 0.3 {
		// Struggling - be more strict about duplicates
		ac.goalSimilarityThreshold = ac.baseGoalSimilarity + 0.10 // 0.95
	} else if ac.recentGoalSuccessRate > 0.7 {
		// Doing well - allow more diversity
		ac.goalSimilarityThreshold = ac.baseGoalSimilarity - 0.10 // 0.75
	} else {
		ac.goalSimilarityThreshold = ac.baseGoalSimilarity // 0.85
	}
	
	// Adapt tool timeout based on success rate AND timeout frequency
	// Low success = give tools more time
	// High success = maintain efficiency
	// High timeout rate = significantly increase timeout
	
	timeoutRate := 0.0
	if totalGoals > 0 {
		timeoutRate = float64(timeoutCount) / float64(totalGoals)
	}
	
	// Base adjustment on success rate
	timeoutMultiplier := 1.0
	if ac.recentGoalSuccessRate < 0.3 {
		timeoutMultiplier = 2.0 // Double when struggling
	} else if ac.recentGoalSuccessRate > 0.7 {
		timeoutMultiplier = 1.0 // Base when doing well
	} else {
		timeoutMultiplier = 1.5 // 1.5x when neutral
	}
	
	// Additional adjustment if timeouts are frequent
	if timeoutRate > 0.3 { // More than 30% goals have timeouts
		timeoutMultiplier *= 1.5 // Add 50% more time
		log.Printf("[AdaptiveConfig] High timeout rate detected (%.0f%%), increasing timeout multiplier to %.1fx",
			timeoutRate*100, timeoutMultiplier)
	}
	
	ac.toolTimeout = int(float64(ac.baseToolTimeout) * timeoutMultiplier)
	
	// Cap at reasonable limits
	if ac.toolTimeout > 600 { // Max 10 minutes
		ac.toolTimeout = 600
	}
	if ac.toolTimeout < 30 { // Min 30 seconds
		ac.toolTimeout = 30
	}
	
	log.Printf("[AdaptiveConfig] Updated thresholds: search=%.2f (base=%.2f), goal_sim=%.2f (base=%.2f), timeout=%ds (base=%ds)",
		ac.searchThreshold, ac.baseSearchThreshold,
		ac.goalSimilarityThreshold, ac.baseGoalSimilarity,
		ac.toolTimeout, ac.baseToolTimeout)
	log.Printf("[AdaptiveConfig] Metrics: memories=%d, goal_success=%.2f, timeout_rate=%.2f (%d/%d goals)",
		ac.averageMemoryCount, ac.recentGoalSuccessRate, timeoutRate, timeoutCount, totalGoals)
}

// GetSearchThreshold returns the current adaptive search threshold
func (ac *AdaptiveConfig) GetSearchThreshold() float64 {
	return ac.searchThreshold
}

// GetGoalSimilarityThreshold returns the current adaptive goal similarity threshold
func (ac *AdaptiveConfig) GetGoalSimilarityThreshold() float64 {
	return ac.goalSimilarityThreshold
}

// GetToolTimeout returns the current adaptive tool timeout in seconds
func (ac *AdaptiveConfig) GetToolTimeout() int {
	return ac.toolTimeout
}

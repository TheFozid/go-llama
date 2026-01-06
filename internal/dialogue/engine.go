// internal/dialogue/engine.go
package dialogue

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"math/rand"
	"net/http"
	"strings"
	"time"

	"go-llama/internal/memory"
	"go-llama/internal/tools"
)

// Engine manages the internal dialogue process
type Engine struct {
	storage                   *memory.Storage
	embedder                  *memory.Embedder
	stateManager              *StateManager
	toolRegistry              *tools.ContextualRegistry
	llmURL                    string
	llmModel                  string
    contextSize               int
	maxTokensPerCycle         int
	maxDurationMinutes        int
	maxThoughtsPerCycle       int
	actionRequirementInterval int
	noveltyWindowHours        int
	// Enhanced reasoning config
	reasoningDepth            string
	enableSelfAssessment      bool
	enableMetaLearning        bool
	enableStrategyTracking    bool
	storeInsights             bool
	dynamicActionPlanning     bool
}

// NewEngine creates a new dialogue engine
func NewEngine(
	storage *memory.Storage,
	embedder *memory.Embedder,
	stateManager *StateManager,
	toolRegistry *tools.ContextualRegistry,
	llmURL string,
	llmModel string,
	maxTokensPerCycle int,
	maxDurationMinutes int,
	maxThoughtsPerCycle int,
	actionRequirementInterval int,
	noveltyWindowHours int,
	reasoningDepth string,
	enableSelfAssessment bool,
	enableMetaLearning bool,
	enableStrategyTracking bool,
	storeInsights bool,
	dynamicActionPlanning bool,
) *Engine {
	return &Engine{
		storage:                   storage,
		embedder:                  embedder,
		stateManager:              stateManager,
		toolRegistry:              toolRegistry,
		llmURL:                    llmURL,
		llmModel:                  llmModel,
		contextSize:               contextSize,
		maxTokensPerCycle:         maxTokensPerCycle,
		maxDurationMinutes:        maxDurationMinutes,
		maxThoughtsPerCycle:       maxThoughtsPerCycle,
		actionRequirementInterval: actionRequirementInterval,
		noveltyWindowHours:        noveltyWindowHours,
		reasoningDepth:            reasoningDepth,
		enableSelfAssessment:      enableSelfAssessment,
		enableMetaLearning:        enableMetaLearning,
		enableStrategyTracking:    enableStrategyTracking,
		storeInsights:             storeInsights,
		dynamicActionPlanning:     dynamicActionPlanning,
	}
}

// RunDialogueCycle executes one full dialogue cycle
func (e *Engine) RunDialogueCycle(ctx context.Context) error {
	startTime := time.Now()
	
	// Load current state
	state, err := e.stateManager.LoadState(ctx)
	if err != nil {
		return fmt.Errorf("failed to load state: %w", err)
	}
	
	state.CycleCount++
	cycleID := state.CycleCount
	
	log.Printf("[Dialogue] Starting cycle #%d at %s", cycleID, startTime.Format(time.RFC3339))
	
	// Initialize metrics
	metrics := &CycleMetrics{
		CycleID:   cycleID,
		StartTime: startTime,
	}
	
	// Create context with timeout
	cycleCtx, cancel := context.WithTimeout(ctx, time.Duration(e.maxDurationMinutes)*time.Minute)
	defer cancel()
	
	// Run dialogue phases with safety checks
	stopReason, err := e.runDialoguePhases(cycleCtx, state, metrics)
	if err != nil {
		log.Printf("[Dialogue] ERROR in cycle #%d: %v", cycleID, err)
		return err
	}
	
	// Finalize metrics
	metrics.EndTime = time.Now()
	metrics.Duration = metrics.EndTime.Sub(metrics.StartTime)
	metrics.StopReason = stopReason
	
	// Update state
	state.LastCycleTime = time.Now()
	
	// Save state and metrics
	if err := e.stateManager.SaveState(ctx, state); err != nil {
		log.Printf("[Dialogue] ERROR saving state: %v", err)
	}
	if err := e.stateManager.SaveMetrics(ctx, metrics); err != nil {
		log.Printf("[Dialogue] ERROR saving metrics: %v", err)
	}
	
	log.Printf("[Dialogue] Cycle #%d complete: %d thoughts, %d actions, %d tokens, took %s (reason: %s)",
		cycleID, metrics.ThoughtCount, metrics.ActionCount, metrics.TokensUsed,
		metrics.Duration.Round(time.Second), stopReason)
	
	return nil
}

// runDialoguePhases executes the dialogue phases with safety mechanisms
func (e *Engine) runDialoguePhases(ctx context.Context, state *InternalState, metrics *CycleMetrics) (string, error) {
	thoughtCount := 0
	actionCount := 0
	totalTokens := 0
	recentThoughts := []string{} // For novelty filtering
	

	// PHASE 1: Enhanced Reflection with Structured Reasoning
	log.Printf("[Dialogue] PHASE 1: Enhanced Reflection")
	
	reasoning, tokens, err := e.performEnhancedReflection(ctx, state)
	if err != nil {
		return StopReasonNaturalStop, fmt.Errorf("reflection failed: %w", err)
	}
	
	thoughtCount++
	totalTokens += tokens
	recentThoughts = append(recentThoughts, reasoning.Reflection)
	
	// Save thought record
	e.stateManager.SaveThought(ctx, &ThoughtRecord{
		CycleID:     state.CycleCount,
		ThoughtNum:  thoughtCount,
		Content:     reasoning.Reflection,
		TokensUsed:  tokens,
		ActionTaken: false,
		Timestamp:   time.Now(),
	})
	
	log.Printf("[Dialogue] Reflection: %s", truncate(reasoning.Reflection, 80))
	
	// Log insights
	insights := reasoning.Insights.ToSlice()
	if len(insights) > 0 {
		log.Printf("[Dialogue] Generated %d insights", len(insights))
		for i, insight := range insights {
			log.Printf("[Dialogue]   Insight %d: %s", i+1, truncate(insight, 80))
		}
	}
	
	// Log self-assessment if enabled
	if e.enableSelfAssessment && reasoning.SelfAssessment != nil {
		log.Printf("[Dialogue] Self-Assessment:")
		log.Printf("[Dialogue]   Confidence: %.2f", reasoning.SelfAssessment.Confidence)
		if len(reasoning.SelfAssessment.RecentSuccesses) > 0 {
			log.Printf("[Dialogue]   Successes: %d", len(reasoning.SelfAssessment.RecentSuccesses))
		}
		if len(reasoning.SelfAssessment.RecentFailures) > 0 {
			log.Printf("[Dialogue]   Failures: %d", len(reasoning.SelfAssessment.RecentFailures))
		}
		if len(reasoning.SelfAssessment.FocusAreas) > 0 {
			log.Printf("[Dialogue]   Focus Areas: %v", reasoning.SelfAssessment.FocusAreas)
		}
	}
	
	// Store learnings as memories if enabled
	if e.storeInsights && len(reasoning.Learnings.ToSlice()) > 0 {
	for _, learning := range reasoning.Learnings.ToSlice() {
			e.storeLearning(ctx, learning)
		}
		log.Printf("[Dialogue] Stored %d learnings in memory", len(reasoning.Learnings))
	}
	
	// Check token budget
	if totalTokens >= e.maxTokensPerCycle {
		metrics.ThoughtCount = thoughtCount
		metrics.ActionCount = actionCount
		metrics.TokensUsed = totalTokens
		return StopReasonMaxThoughts, nil
	}

	
	// PHASE 2: Reasoning-Driven Goal Management
	log.Printf("[Dialogue] PHASE 2: Reasoning-Driven Goal Management")
	
	// Use insights from reflection to identify gaps
	gaps := reasoning.KnowledgeGaps.ToSlice()
	if len(gaps) == 0 {
		// Fallback to old method if reasoning didn't find any
		gaps, err = e.identifyKnowledgeGaps(ctx)
		if err != nil {
			log.Printf("[Dialogue] WARNING: Failed to identify gaps: %v", err)
			gaps = []string{}
		}
	}
	
	failures, err := e.identifyRecentFailures(ctx)
	if err != nil {
		log.Printf("[Dialogue] WARNING: Failed to identify failures: %v", err)
		failures = []string{}
	}
	
	// Update state with findings
	state.KnowledgeGaps = gaps
	state.RecentFailures = failures
	state.Patterns = reasoning.Patterns.ToSlice()
	
	if len(gaps) > 0 {
		log.Printf("[Dialogue] Identified %d knowledge gaps from reasoning", len(gaps))
	}
	if len(failures) > 0 {
		log.Printf("[Dialogue] Identified %d recent failures", len(failures))
	}
	if len(reasoning.Patterns) > 0 {
		log.Printf("[Dialogue] Detected %d patterns", len(reasoning.Patterns))
	}
	
	// Create goals from LLM proposals if available (but not if we have too many already)
	newGoals := []Goal{}
	if len(reasoning.GoalsToCreate.ToSlice()) > 0 && len(state.ActiveGoals) < 5 {
		log.Printf("[Dialogue] LLM proposed %d new goals", len(reasoning.GoalsToCreate))
		for _, proposal := range reasoning.GoalsToCreate.ToSlice() {
			// Check for duplicates with more aggressive matching
			isDuplicate := false
			proposalLower := strings.ToLower(proposal.Description)
			
			for _, existingGoal := range state.ActiveGoals {
				existingLower := strings.ToLower(existingGoal.Description)
				
				// Check if either contains the other (more aggressive)
				if strings.Contains(existingLower, proposalLower[:min(len(proposalLower), 30)]) ||
				   strings.Contains(proposalLower, existingLower[:min(len(existingLower), 30)]) {
					isDuplicate = true
					log.Printf("[Dialogue] Skipping duplicate goal: %s", truncate(proposal.Description, 40))
					break
				}
			}
			
			if isDuplicate {
				continue
			}
			
			goal := e.createGoalFromProposal(proposal)
			newGoals = append(newGoals, goal)
			log.Printf("[Dialogue] Created goal from LLM proposal: %s (priority: %d)", truncate(goal.Description, 60), goal.Priority)
			log.Printf("[Dialogue]   Reasoning: %s", truncate(proposal.Reasoning, 80))
		}
	} else {
		// Fallback to old goal formation
		newGoals = e.formGoals(state)
	}
	
	if len(newGoals) > 0 {
		state.ActiveGoals = append(state.ActiveGoals, newGoals...)
		metrics.GoalsCreated = len(newGoals)
		log.Printf("[Dialogue] Created %d new goals total", len(newGoals))
	}
	

	// PHASE 3: Goal Pursuit (if we have active goals)
	if len(state.ActiveGoals) > 0 {
		log.Printf("[Dialogue] PHASE 3: Goal Pursuit (%d active goals)", len(state.ActiveGoals))
		
		// Sort goals by priority
		sortedGoals := sortGoalsByPriority(state.ActiveGoals)
		topGoal := sortedGoals[0]
		
		log.Printf("[Dialogue] Pursuing goal: %s", topGoal.Description)
		
		// Phase 3.2: Execute actions with tools
		actionExecuted := false
		for i := range topGoal.Actions {
			action := &topGoal.Actions[i]
			
			// Skip completed actions
			if action.Status == ActionStatusCompleted {
				continue
			}
			
			// Execute pending action
			if action.Status == ActionStatusPending {
				action.Status = ActionStatusInProgress
				
				log.Printf("[Dialogue] Executing action: %s using tool '%s'", action.Description, action.Tool)
				
				// Execute tool
				result, err := e.executeAction(ctx, action)
				if err != nil {
					log.Printf("[Dialogue] Action failed: %v", err)
					action.Result = fmt.Sprintf("ERROR: %v", err)
					action.Status = ActionStatusCompleted // Mark as completed even on failure
				} else {
					action.Result = result
					action.Status = ActionStatusCompleted
					actionCount++
					actionExecuted = true
					log.Printf("[Dialogue] Action completed: %s", truncate(result, 80))
				}
				action.Timestamp = time.Now()
				
				// Only execute one action per cycle
				break
			}
		}
		
		// If no actions were executed, check if we should create new actions
		if !actionExecuted {
			// Only create new actions if goal has NO actions at all
			// If it has actions, they should be executed next cycle
			if len(topGoal.Actions) == 0 {
				goalThought, tokens, err := e.thinkAboutGoal(ctx, &topGoal)
				if err != nil {
					log.Printf("[Dialogue] WARNING: Failed to think about goal: %v", err)
				} else {
					thoughtCount++
					totalTokens += tokens
					
					e.stateManager.SaveThought(ctx, &ThoughtRecord{
						CycleID:     state.CycleCount,
						ThoughtNum:  thoughtCount,
						Content:     goalThought,
						TokensUsed:  tokens,
						ActionTaken: false,
						Timestamp:   time.Now(),
					})
					
					log.Printf("[Dialogue] Goal thought: %s", truncate(goalThought, 80))
					
					// Create initial search action based on goal type
					var searchQuery string
					
					// Extract key terms from goal description for search
					desc := strings.ToLower(topGoal.Description)
					
					if strings.Contains(desc, "research") {
						// Extract what to research
						searchQuery = strings.TrimPrefix(desc, "research ")
						searchQuery = strings.TrimPrefix(searchQuery, "other ")
						searchQuery = strings.TrimPrefix(searchQuery, "human like ")
					} else if strings.Contains(desc, "learn about:") {
						searchQuery = strings.TrimPrefix(desc, "learn about: ")
					} else if strings.Contains(desc, "choose") || strings.Contains(desc, "select") {
						// For choice/selection goals, extract the subject
						searchQuery = desc
					} else {
						searchQuery = topGoal.Description
					}
					
					// Create initial search action
					newAction := Action{
						Description: searchQuery,
						Tool:        ActionToolSearch,
						Status:      ActionStatusPending,
						Timestamp:   time.Now(),
					}
					topGoal.Actions = append(topGoal.Actions, newAction)
					log.Printf("[Dialogue] Created search action: %s", truncate(searchQuery, 60))
				}
			} else {
				// Goal has pending actions - log and wait for next cycle
				pendingCount := 0
				for _, action := range topGoal.Actions {
					if action.Status == ActionStatusPending {
						pendingCount++
					}
				}
				log.Printf("[Dialogue] Goal has %d pending actions, will execute next cycle", pendingCount)
			}
		}
		
		// After executing actions, check if we should create follow-up actions
		// (e.g., parse action after search completes)
		if len(topGoal.Actions) > 0 {
			lastAction := topGoal.Actions[len(topGoal.Actions)-1]
			
			// Check if last action was a completed search
			if lastAction.Tool == ActionToolSearch && lastAction.Status == ActionStatusCompleted {
				// Extract URLs from the search result
				urls := e.extractURLsFromSearchResults(lastAction.Result)
				
				if len(urls) > 0 {
					// Create parse action for the first (most relevant) URL
					parseAction := Action{
						Description: urls[0],
						Tool:        ActionToolWebParseGeneral,
						Status:      ActionStatusPending,
						Timestamp:   time.Now(),
					}
					topGoal.Actions = append(topGoal.Actions, parseAction)
					log.Printf("[Dialogue] Created web_parse_general action for: %s", truncate(urls[0], 60))
				}
			}
		}

		
// Update goal progress based on completed actions
completedActions := 0
totalActions := len(topGoal.Actions)

for _, action := range topGoal.Actions {
	if action.Status == ActionStatusCompleted {
		completedActions++
	}
}

// Only calculate progress if we have actions
if totalActions > 0 {
	topGoal.Progress = float64(completedActions) / float64(totalActions)
} else {
	// No actions yet, no progress
	topGoal.Progress = 0.0
}

// NEW: Don't mark complete if we just did a search and could parse
// Check if the last completed action was a search
if completedActions > 0 && totalActions == completedActions {
	lastAction := topGoal.Actions[totalActions-1]
	
	// If last action was search, wait for parse action to be created
	if lastAction.Tool == ActionToolSearch {
		log.Printf("[Dialogue] Search completed, waiting for parse action creation")
		topGoal.Progress = 0.99 // Almost complete, not fully complete
	}
}
		
if topGoal.Progress >= 1.0 {
	// Validate that the goal actually achieved something useful
	hasUsefulOutcome := false
	hasFailures := false
	
	for _, action := range topGoal.Actions {
		if action.Status == ActionStatusCompleted {
			// Check if action result contains error
			if strings.Contains(action.Result, "ERROR") || 
			   strings.Contains(action.Result, "Failed") ||
			   strings.Contains(action.Result, "failed") {
				hasFailures = true
			}
			
			// Check if action produced meaningful output (>100 chars)
			if len(action.Result) > 100 && !hasFailures {
				hasUsefulOutcome = true
			}
		}
	}
	
	// Mark goal based on outcome quality
	if hasUsefulOutcome {
		topGoal.Status = GoalStatusCompleted
		topGoal.Outcome = "good"
		log.Printf("[Dialogue] ✓ Goal completed successfully: %s", topGoal.Description)
		metrics.GoalsCompleted++
	} else if hasFailures {
		topGoal.Status = GoalStatusAbandoned
		topGoal.Outcome = "bad"
		log.Printf("[Dialogue] ⚠ Goal abandoned (actions failed): %s", truncate(topGoal.Description, 60))
	} else {
		// Actions completed but results too short/empty
		topGoal.Status = GoalStatusAbandoned
		topGoal.Outcome = "neutral"
		log.Printf("[Dialogue] ⚠ Goal abandoned (no useful output): %s", truncate(topGoal.Description, 60))
	}
}
		
		// Update goal in state
		for i := range state.ActiveGoals {
			if state.ActiveGoals[i].ID == topGoal.ID {
				state.ActiveGoals[i] = topGoal
				break
			}
		}
	}
	
	// PHASE 4: Pattern Detection
	log.Printf("[Dialogue] PHASE 4: Pattern Detection")
	
	patterns, err := e.detectPatterns(ctx)
	if err != nil {
		log.Printf("[Dialogue] WARNING: Failed to detect patterns: %v", err)
		patterns = []string{}
	}
	
	if len(patterns) > 0 {
		state.Patterns = patterns
		log.Printf("[Dialogue] Detected %d patterns", len(patterns))
		for _, pattern := range patterns {
			log.Printf("[Dialogue]   - %s", pattern)
		}
	}
	
	// PHASE 5: Cleanup (move completed/abandoned goals and limit active goals)
	completedCount := 0
	abandonedCount := 0
	activeGoals := []Goal{}
	
	for _, goal := range state.ActiveGoals {
		if goal.Status == GoalStatusCompleted {
			state.CompletedGoals = append(state.CompletedGoals, goal)
			completedCount++
		} else if goal.Status == GoalStatusAbandoned {
			state.CompletedGoals = append(state.CompletedGoals, goal)
			abandonedCount++
		} else if goal.Progress == 0.0 && time.Since(goal.Created) > 24*time.Hour {
			// Abandon goals with no progress after 24 hours
			goal.Status = GoalStatusAbandoned
			goal.Outcome = "neutral"
			state.CompletedGoals = append(state.CompletedGoals, goal)
			abandonedCount++
			log.Printf("[Dialogue] Auto-abandoned stale goal: %s", truncate(goal.Description, 60))
		} else {
			activeGoals = append(activeGoals, goal)
		}
	}
	
	// Limit active goals to top 5 by priority
	if len(activeGoals) > 5 {
		sortedGoals := sortGoalsByPriority(activeGoals)
		activeGoals = sortedGoals[:5]
		
		// Abandon the rest
		for i := 5; i < len(sortedGoals); i++ {
			sortedGoals[i].Status = GoalStatusAbandoned
			sortedGoals[i].Outcome = "neutral"
			state.CompletedGoals = append(state.CompletedGoals, sortedGoals[i])
			abandonedCount++
		}
		log.Printf("[Dialogue] Pruned active goals from %d to 5 (abandoned %d low-priority)", len(sortedGoals), len(sortedGoals)-5)
	}
	
	state.ActiveGoals = activeGoals
	
	if completedCount > 0 || abandonedCount > 0 {
		log.Printf("[Dialogue] Moved %d completed and %d abandoned goals to history", completedCount, abandonedCount)
	}
	
	// Update metrics
	metrics.ThoughtCount = thoughtCount
	metrics.ActionCount = actionCount
	metrics.TokensUsed = totalTokens
	
	// Check stop conditions
	if thoughtCount >= e.maxThoughtsPerCycle {
		return StopReasonMaxThoughts, nil
	}
	if totalTokens >= e.maxTokensPerCycle {
		return StopReasonMaxThoughts, nil
	}
	
	return StopReasonNaturalStop, nil
}

// reflectOnRecentActivity analyzes recent memory patterns
func (e *Engine) reflectOnRecentActivity(ctx context.Context) (string, int, error) {
	// Find recent memories (last 24 hours) - search ALL memories (no filters)
	embedding, err := e.embedder.Embed(ctx, "recent activity and patterns")
	if err != nil {
		return "", 0, fmt.Errorf("failed to generate embedding: %w", err)
	}
	
	// Search without user/collective filters to see ALL recent activity
	query := memory.RetrievalQuery{
		// Don't set UserID - we want to see all activity
		// Don't filter by collective - we want everything
		Limit:             10,
		MinScore:          0.3,
	}
	
	results, err := e.storage.Search(ctx, query, embedding)
	if err != nil {
		return "", 0, fmt.Errorf("failed to search memories: %w", err)
	}
	
	if len(results) == 0 {
		return "No recent activity to reflect on.", 0, nil
	}
	
	// Build reflection prompt
	prompt := "Analyze these recent memories and identify patterns, successes, and failures:\n\n"
	for i, result := range results {
		prompt += fmt.Sprintf("%d. [%s] %s\n", i+1, result.Memory.OutcomeTag, result.Memory.Content)
	}
	prompt += "\nProvide a brief 2-3 sentence reflection."
	
	// Call LLM
	reflection, tokens, err := e.callLLM(ctx, prompt)
	if err != nil {
		return "", 0, fmt.Errorf("LLM call failed: %w", err)
	}
	
	return reflection, tokens, nil
}

// identifyKnowledgeGaps finds topics the system doesn't know about
func (e *Engine) identifyKnowledgeGaps(ctx context.Context) ([]string, error) {
	// Search for recent user messages that mention goals or requests
	embedding, err := e.embedder.Embed(ctx, "user requests goals learning tasks research")
	if err != nil {
		return []string{}, err
	}
	
	query := memory.RetrievalQuery{
		Limit:    10,
		MinScore: 0.4,
	}
	
	results, err := e.storage.Search(ctx, query, embedding)
	if err != nil {
		return []string{}, err
	}
	
	gaps := []string{}
	
	// Look for phrases that indicate user-requested goals
	goalPhrases := []string{
		"set yourself the goal",
		"you should try to",
		"i want you to learn",
		"research",
		"think about",
		"have a think about",
		"explore",
		"study",
		"investigate",
	}
	
	for _, result := range results {
		content := strings.ToLower(result.Memory.Content)
		
		// Check if this memory contains a goal-related phrase
		for _, phrase := range goalPhrases {
			if strings.Contains(content, phrase) {
				// Extract the topic after the phrase
				// Simple extraction: take the content and add as knowledge gap
				gap := extractGoalTopic(content, phrase)
				if gap != "" && len(gap) > 10 {
					gaps = append(gaps, gap)
					log.Printf("[Dialogue] Detected knowledge gap from user request: %s", truncate(gap, 60))
				}
				break
			}
		}
	}
	
	return gaps, nil
}

// extractGoalTopic attempts to extract the main topic from a goal-related message
func extractGoalTopic(content string, triggerPhrase string) string {
	// Find the trigger phrase
	idx := strings.Index(content, triggerPhrase)
	if idx == -1 {
		return ""
	}
	
	// Get text after the trigger phrase
	afterPhrase := content[idx+len(triggerPhrase):]
	
	// Take up to the next period, newline, or 200 characters
	var topic string
	for i, char := range afterPhrase {
		if char == '.' || char == '\n' || i > 200 {
			topic = afterPhrase[:i]
			break
		}
	}
	
	if topic == "" {
		topic = afterPhrase
	}
	
	// Clean up
	topic = strings.TrimSpace(topic)
	topic = strings.Trim(topic, ".,!?")
	
	// If it starts with "to ", remove it
	topic = strings.TrimPrefix(topic, "to ")
	
	return topic
}

// identifyRecentFailures finds memories tagged as "bad"
func (e *Engine) identifyRecentFailures(ctx context.Context) ([]string, error) {
	badOutcome := memory.OutcomeBad
	query := memory.RetrievalQuery{
		IncludeCollective: true,
		OutcomeFilter:     &badOutcome,
		Limit:             5,
		MinScore:          0.0,
	}
	
	embedding, err := e.embedder.Embed(ctx, "recent mistakes and failures")
	if err != nil {
		return []string{}, err
	}
	
	results, err := e.storage.Search(ctx, query, embedding)
	if err != nil {
		return []string{}, err
	}
	
	failures := []string{}
	for _, result := range results {
		failures = append(failures, result.Memory.Content)
	}
	
	return failures, nil
}

// formGoals creates new goals based on state
func (e *Engine) formGoals(state *InternalState) []Goal {
	goals := []Goal{}
	
	// Create goals from knowledge gaps (user requests)
	for _, gap := range state.KnowledgeGaps {
		// Check if we already have a similar active goal
		isDuplicate := false
		for _, existingGoal := range state.ActiveGoals {
			if strings.Contains(existingGoal.Description, gap[:min(len(gap), 30)]) {
				isDuplicate = true
				log.Printf("[Dialogue] Skipping duplicate goal: %s", truncate(gap, 40))
				break
			}
		}
		
		if isDuplicate {
			continue
		}
		
		// Determine if this is a research goal or learning goal
		description := ""
		priority := 7
		
		if strings.Contains(strings.ToLower(gap), "research") ||
		   strings.Contains(strings.ToLower(gap), "think about") ||
		   strings.Contains(strings.ToLower(gap), "choose") ||
		   strings.Contains(strings.ToLower(gap), "select") {
			description = gap // Use the gap as-is for research goals
			priority = 8 // Higher priority for explicit research requests
		} else {
			description = fmt.Sprintf("Learn about: %s", gap)
			priority = 7
		}
		
		goal := Goal{
			ID:          fmt.Sprintf("goal_%d", time.Now().UnixNano()),
			Description: description,
			Source:      GoalSourceKnowledgeGap,
			Priority:    priority,
			Created:     time.Now(),
			Progress:    0.0,
			Status:      GoalStatusActive,
			Actions:     []Action{},
		}
		goals = append(goals, goal)
		log.Printf("[Dialogue] Formed new goal from user request: %s (priority: %d)", truncate(description, 60), priority)
	}
	
	// Create goals from failures
	for _, failure := range state.RecentFailures {
		goal := Goal{
			ID:          fmt.Sprintf("goal_%d", time.Now().UnixNano()),
			Description: fmt.Sprintf("Improve understanding of: %s", truncate(failure, 50)),
			Source:      GoalSourceUserFailure,
			Priority:    9, // Higher priority for failures
			Created:     time.Now(),
			Progress:    0.0,
			Status:      GoalStatusActive,
			Actions:     []Action{},
		}
		goals = append(goals, goal)
	}
	
	// Limit number of new goals per cycle
	if len(goals) > 3 {
		goals = goals[:3]
	}
	
	return goals
}

// min returns the minimum of two integers
func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

// thinkAboutGoal generates thoughts about pursuing a goal
func (e *Engine) thinkAboutGoal(ctx context.Context, goal *Goal) (string, int, error) {
	prompt := fmt.Sprintf("You are pursuing this goal: %s\n\nThink about how to approach this. What should you do next? Keep it brief (2-3 sentences).", goal.Description)
	
	thought, tokens, err := e.callLLM(ctx, prompt)
	if err != nil {
		return "", 0, err
	}
	
	return thought, tokens, nil
}

// detectPatterns identifies recurring patterns in memories
func (e *Engine) detectPatterns(ctx context.Context) ([]string, error) {
	// For Phase 3.1, return empty
	// In Phase 3.2+, this would analyze concept tags, co-occurrences, etc.
	return []string{}, nil
}

// callLLM makes a request to the reasoning model
func (e *Engine) callLLM(ctx context.Context, prompt string) (string, int, error) {
	reqBody := map[string]interface{}{
		"model": e.llmModel,
		"max_tokens": e.contextSize,
		"messages": []map[string]string{
			{
				"role":    "system",
				"content": "You are GrowerAI's internal dialogue system. Think briefly and clearly.",
			},
			{
				"role":    "user",
				"content": prompt,
			},
		},
		"temperature": 0.3,
		"stream":      false,
	}
	
	jsonData, err := json.Marshal(reqBody)
	if err != nil {
		return "", 0, fmt.Errorf("failed to marshal request: %w", err)
	}
	
	req, err := http.NewRequestWithContext(ctx, "POST", e.llmURL, bytes.NewBuffer(jsonData))
	if err != nil {
		return "", 0, fmt.Errorf("failed to create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	
	client := &http.Client{Timeout: 120 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return "", 0, fmt.Errorf("failed to send request: %w", err)
	}
	defer resp.Body.Close()
	
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return "", 0, fmt.Errorf("LLM returned status %d: %s", resp.StatusCode, string(body))
	}
	
	var result struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
		Usage struct {
			TotalTokens int `json:"total_tokens"`
		} `json:"usage"`
	}
	
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return "", 0, fmt.Errorf("failed to decode response: %w", err)
	}
	
	if len(result.Choices) == 0 {
		return "", 0, fmt.Errorf("no choices returned from LLM")
	}
	
	content := strings.TrimSpace(result.Choices[0].Message.Content)
	tokens := result.Usage.TotalTokens
	
	return content, tokens, nil
}

// callLLMWithStructuredReasoning requests structured JSON reasoning from the LLM
func (e *Engine) callLLMWithStructuredReasoning(ctx context.Context, prompt string, expectJSON bool) (*ReasoningResponse, int, error) {
systemPrompt := `You are GrowerAI's internal reasoning system. Output ONLY valid JSON.

CRITICAL: Check each array has BOTH [ and ] brackets.

VALID EXAMPLES:
{
  "reflection": "text here",
  "insights": ["item1", "item2"],
  "strengths": [],
  "weaknesses": ["weakness1"],
  "knowledge_gaps": [],
  "patterns": [],
  "goals_to_create": [],
  "learnings": [],
  "self_assessment": {
    "recent_successes": [],
    "recent_failures": [],
    "skill_gaps": [],
    "confidence": 0.7,
    "focus_areas": []
  }
}

RULES:
1. Every array needs BOTH [ and ]
2. Put comma after ] if more fields follow
3. No comma after last field
4. Empty arrays are fine: []

OUTPUT ONLY JSON. NO MARKDOWN. NO EXPLANATIONS.`

	reqBody := map[string]interface{}{
		"model": e.llmModel,
		"max_tokens": e.contextSize,
		"messages": []map[string]string{
			{
				"role":    "system",
				"content": systemPrompt,
			},
			{
				"role":    "user",
				"content": prompt,
			},
		},
		"temperature": 0.7,
		"stream":      false,
	}
	
	jsonData, err := json.Marshal(reqBody)
	if err != nil {
		return nil, 0, fmt.Errorf("failed to marshal request: %w", err)
	}
	
	req, err := http.NewRequestWithContext(ctx, "POST", e.llmURL, bytes.NewBuffer(jsonData))
	if err != nil {
		return nil, 0, fmt.Errorf("failed to create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	
	client := &http.Client{Timeout: 120 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, 0, fmt.Errorf("failed to send request: %w", err)
	}
	defer resp.Body.Close()
	
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, 0, fmt.Errorf("LLM returned status %d: %s", resp.StatusCode, string(body))
	}
	
	var result struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
		Usage struct {
			TotalTokens int `json:"total_tokens"`
		} `json:"usage"`
	}
	
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, 0, fmt.Errorf("failed to decode response: %w", err)
	}
	
	if len(result.Choices) == 0 {
		return nil, 0, fmt.Errorf("no choices returned from LLM")
	}
	
	content := strings.TrimSpace(result.Choices[0].Message.Content)
	tokens := result.Usage.TotalTokens
	
	// Parse JSON response
	// Remove markdown code fences if present
	content = strings.TrimPrefix(content, "```json")
	content = strings.TrimPrefix(content, "```")
	content = strings.TrimSuffix(content, "```")
	content = strings.TrimSpace(content)
	
	var reasoning ReasoningResponse
	if err := json.Unmarshal([]byte(content), &reasoning); err != nil {
		log.Printf("[Dialogue] WARNING: Failed to parse JSON reasoning: %v", err)
		log.Printf("[Dialogue] Raw response (first 500 chars): %s", truncateResponse(content, 500))
		
		// Try to fix common JSON errors
		fixedContent := fixCommonJSONErrors(content)
		
		// Log what we fixed (first 500 chars)
		if fixedContent != content {
			log.Printf("[Dialogue] Applied JSON fixes (first 500 chars): %s", truncateResponse(fixedContent, 500))
		}
		
		if err := json.Unmarshal([]byte(fixedContent), &reasoning); err != nil {
			log.Printf("[Dialogue] WARNING: Failed to parse even after JSON fixes: %v", err)
			
			// Last attempt: try to extract just the reflection field
			var partialParse map[string]interface{}
			if err := json.Unmarshal([]byte(fixedContent), &partialParse); err == nil {
				if refl, ok := partialParse["reflection"].(string); ok {
					log.Printf("[Dialogue] Extracted reflection field only, using degraded mode")
					return &ReasoningResponse{
						Reflection: refl,
						Insights:   []string{},
					}, tokens, nil
				}
			}
			
			// Complete fallback
			log.Printf("[Dialogue] Complete JSON parse failure, using fallback mode")
			return &ReasoningResponse{
				Reflection: "Failed to parse structured reasoning. Using fallback mode.",
				Insights:   []string{},
			}, tokens, nil
		}
		log.Printf("[Dialogue] ✓ Successfully parsed JSON after fixes")
	}
	
	return &reasoning, tokens, nil
}


// executeAction executes a tool-based action
func (e *Engine) executeAction(ctx context.Context, action *Action) (string, error) {
	// Map action tool to actual tool execution
	switch action.Tool {
case ActionToolSearch:
	// Extract search query from action description
	// For now, use the full description as the query
	params := map[string]interface{}{
		"query": action.Description,
	}
	
	result, err := e.toolRegistry.ExecuteIdle(ctx, tools.ToolNameSearch, params)
	if err != nil {
		return "", fmt.Errorf("search tool failed: %w", err)
	}
	
	if !result.Success {
		return "", fmt.Errorf("search failed: %s", result.Error)
	}
	
	// NEW: Extract URLs from search results and create parse actions
	// This connects search → web parsing automatically
	urls := e.extractURLsFromSearchResults(result.Output)
	if len(urls) > 0 {
		log.Printf("[Dialogue] Extracted %d URLs from search results", len(urls))
		
		// Add parse action for the first URL (most relevant)
		// Note: This happens in the calling code, so we'll just log for now
		// The actual action creation happens in the goal pursuit phase
	}
	
	return result.Output, nil
		
case ActionToolWebParse,
     ActionToolWebParseMetadata,
     ActionToolWebParseGeneral,
     ActionToolWebParseContextual,
     ActionToolWebParseChunked:
	
	// Extract URL from action description
	// Formats handled: 
	//   - "https://example.com"
	//   - "Parse URL: https://example.com"
	//   - "Search result: https://example.com - title"
	url := strings.TrimSpace(action.Description)
	
	// Clean up common prefixes
	if idx := strings.Index(url, "http"); idx != -1 {
		url = url[idx:] // Start from http
	}
	
	// Remove everything after first space (titles, descriptions)
	if idx := strings.Index(url, " "); idx != -1 {
		url = url[:idx]
	}
	
	// Basic validation
	if !strings.HasPrefix(url, "http://") && !strings.HasPrefix(url, "https://") {
		return "", fmt.Errorf("invalid URL in action description: %s", action.Description)
	}
	
	params := map[string]interface{}{
		"url": url,
	}
	
	// For contextual parsing, try to extract purpose from goal context
	if action.Tool == ActionToolWebParseContextual {
		// The dialogue engine should have set this up, but provide fallback
		params["purpose"] = "Extract relevant information for research goal"
	}
	
	// For chunked parsing, look for chunk index
	if action.Tool == ActionToolWebParseChunked {
		// Default to first chunk - LLM should specify in future iterations
		params["chunk_index"] = 0
		
		// Try to parse chunk index from description
		// Format: "Read chunk 3 from URL" or "chunk_index: 3"
		desc := strings.ToLower(action.Description)
		if strings.Contains(desc, "chunk") {
			// Simple extraction - matches "chunk 3", "chunk 0", etc.
			parts := strings.Fields(desc)
			for i, part := range parts {
				if part == "chunk" && i+1 < len(parts) {
					if chunkIdx, err := fmt.Sscanf(parts[i+1], "%d", new(int)); err == nil && chunkIdx >= 0 {
						var idx int
						fmt.Sscanf(parts[i+1], "%d", &idx)
						params["chunk_index"] = idx
						break
					}
				}
			}
		}
	}
	
	// Execute the appropriate web parse tool
	result, err := e.toolRegistry.ExecuteIdle(ctx, action.Tool, params)
	if err != nil {
		return "", fmt.Errorf("web parse tool failed: %w", err)
	}
	
	if !result.Success {
		return "", fmt.Errorf("web parse failed: %s", result.Error)
	}
	
	return result.Output, nil
		
	case ActionToolSandbox:
		// Phase 3.5: Sandbox not yet implemented
		return "", fmt.Errorf("sandbox tool not yet implemented")
		
	case ActionToolMemoryConsolidation:
		// This is internal, not a real tool
		return "Memory consolidation completed", nil
		
	default:
		return "", fmt.Errorf("unknown tool: %s", action.Tool)
	}
}

// Helper functions

func sortGoalsByPriority(goals []Goal) []Goal {
	// Simple bubble sort by priority (descending)
	sorted := make([]Goal, len(goals))
	copy(sorted, goals)
	
	for i := 0; i < len(sorted); i++ {
		for j := i + 1; j < len(sorted); j++ {
			if sorted[j].Priority > sorted[i].Priority {
				sorted[i], sorted[j] = sorted[j], sorted[i]
			}
		}
	}
	
	return sorted
}

func truncate(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "..."
}

// performEnhancedReflection performs structured reasoning about recent activity
func (e *Engine) performEnhancedReflection(ctx context.Context, state *InternalState) (*ReasoningResponse, int, error) {
	// Find recent memories for context
	embedding, err := e.embedder.Embed(ctx, "recent activity patterns successes failures")
	if err != nil {
		return nil, 0, fmt.Errorf("failed to generate embedding: %w", err)
	}
	
	query := memory.RetrievalQuery{
		Limit:    10,
		MinScore: 0.3,
	}
	
	results, err := e.storage.Search(ctx, query, embedding)
	if err != nil {
		return nil, 0, fmt.Errorf("failed to search memories: %w", err)
	}
	
	// Build context for reasoning
	memoryContext := "Recent memories:\n"
	if len(results) == 0 {
		memoryContext += "No recent memories found.\n"
	} else {
		for i, result := range results {
			outcome := result.Memory.OutcomeTag
			if outcome == "" {
				outcome = "unrated"
			}
			memoryContext += fmt.Sprintf("%d. [%s] %s\n", i+1, outcome, truncate(result.Memory.Content, 100))
		}
	}
	
	// Add current goals context
	goalsContext := fmt.Sprintf("\nCurrent active goals: %d\n", len(state.ActiveGoals))
	if len(state.ActiveGoals) > 0 {
		for i, goal := range state.ActiveGoals {
			goalsContext += fmt.Sprintf("%d. %s (progress: %.0f%%, priority: %d)\n", 
				i+1, truncate(goal.Description, 60), goal.Progress*100, goal.Priority)
		}
	}
	
	// Build prompt based on reasoning depth
	var prompt string
	switch e.reasoningDepth {
	case "deep":
		prompt = fmt.Sprintf(`%s%s

Perform deep analysis:
1. Reflect on what these memories reveal about recent interactions
2. Identify at least 3 insights or patterns
3. Assess your strengths and weaknesses honestly
4. Identify knowledge gaps that need addressing
5. Propose 1-3 specific goals with detailed action plans
6. Extract learnings about what strategies work
7. Provide comprehensive self-assessment

Be thorough and analytical. Focus on actionable insights.`, memoryContext, goalsContext)
		
	case "moderate":
		prompt = fmt.Sprintf(`%s%s

Analyze recent activity:
1. What patterns do you see in these memories?
2. What are you doing well? What needs improvement?
3. What knowledge gaps should you address?
4. Propose 1-2 goals with action plans
5. What have you learned about effective strategies?

Be analytical but concise.`, memoryContext, goalsContext)
		
	default: // conservative
		prompt = fmt.Sprintf(`%s%s

Brief analysis:
1. Key takeaway from recent memories?
2. One strength, one weakness
3. Most important knowledge gap to address?
4. Propose one goal if needed

Keep it focused and actionable.`, memoryContext, goalsContext)
	}
	
	// Call LLM with structured reasoning
	return e.callLLMWithStructuredReasoning(ctx, prompt, true)
}

// createGoalFromProposal creates a Goal from an LLM proposal
func (e *Engine) createGoalFromProposal(proposal GoalProposal) Goal {
	goal := Goal{
		ID:          fmt.Sprintf("goal_%d", time.Now().UnixNano()),
		Description: proposal.Description,
		Source:      GoalSourceKnowledgeGap, // Could be smarter based on reasoning
		Priority:    proposal.Priority,
		Created:     time.Now(),
		Progress:    0.0,
		Status:      GoalStatusActive,
		Actions:     []Action{},
	}
	
	// Create actions from LLM's action plan if dynamic planning enabled
	if e.dynamicActionPlanning && len(proposal.ActionPlan) > 0 {
		for _, planStep := range proposal.ActionPlan {
			action := e.parseActionFromPlan(planStep)
			goal.Actions = append(goal.Actions, action)
		}
		log.Printf("[Dialogue] Created %d actions from LLM action plan", len(goal.Actions))
	}
	
	return goal
}

// parseActionFromPlan converts an LLM action plan step into an Action
func (e *Engine) parseActionFromPlan(planStep string) Action {
	// Simple parsing: look for tool keywords
	tool := ActionToolSearch // Default to search
	planLower := strings.ToLower(planStep)
	
	if strings.Contains(planLower, "parse") || strings.Contains(planLower, "read") || strings.Contains(planLower, "fetch") {
		tool = ActionToolWebParse
	} else if strings.Contains(planLower, "search") || strings.Contains(planLower, "find") || strings.Contains(planLower, "look up") {
		tool = ActionToolSearch
	} else if strings.Contains(planLower, "test") || strings.Contains(planLower, "experiment") || strings.Contains(planLower, "try") {
		tool = ActionToolSandbox
	}
	
	return Action{
		Description: planStep,
		Tool:        tool,
		Status:      ActionStatusPending,
		Timestamp:   time.Now(),
	}
}

// storeLearning stores a learning as a collective memory
func (e *Engine) storeLearning(ctx context.Context, learning Learning) error {
	content := fmt.Sprintf("LEARNING [%s]: %s (Context: %s, Confidence: %.2f)",
		learning.Category, learning.What, learning.Context, learning.Confidence)
	
	embedding, err := e.embedder.Embed(ctx, content)
	if err != nil {
		log.Printf("[Dialogue] WARNING: Failed to embed learning: %v", err)
		return err
	}
	
	mem := &memory.Memory{
		Content:         content,
		ImportanceScore: learning.Confidence, // Use confidence as importance
		IsCollective:    true,                // Learnings are collective knowledge
		ConceptTags:     []string{"learning", learning.Category},
		OutcomeTag:      "good",              // Learnings are positive
		ValidationCount: 1,                   // Pre-validated
		TrustScore:      learning.Confidence,
		Tier:            memory.TierRecent,   // Start in recent tier
		CreatedAt:       time.Now(),
		LastAccessedAt:  time.Now(),
		AccessCount:     0,
		Embedding:       embedding,
	}
	
	err = e.storage.Store(ctx, mem)
	if err != nil {
		log.Printf("[Dialogue] WARNING: Failed to store learning: %v", err)
		return err
	}
	
	log.Printf("[Dialogue] Stored learning: %s", truncate(learning.What, 60))
	return nil
}

// generateJitter returns a random duration within the jitter window
func generateJitter(windowMinutes int) time.Duration {
	if windowMinutes <= 0 {
		return 0
	}
	
	// Random value between -windowMinutes and +windowMinutes
	jitterMinutes := rand.Intn(windowMinutes*2+1) - windowMinutes
	return time.Duration(jitterMinutes) * time.Minute

}

// truncateResponse truncates a string for logging
func truncateResponse(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "... (truncated)"
}

// fixCommonJSONErrors attempts to fix common JSON generation errors from LLMs
func fixCommonJSONErrors(jsonStr string) string {
	// Remove markdown fences
	fixed := strings.ReplaceAll(jsonStr, "```json", "")
	fixed = strings.ReplaceAll(fixed, "```", "")
	fixed = strings.TrimSpace(fixed)
	
	// Fix missing comma after array with closing bracket on new line
	fixed = strings.ReplaceAll(fixed, "]\n  \"", "],\n  \"")
	fixed = strings.ReplaceAll(fixed, "]\n\n  \"", "],\n\n  \"")
	
	// Parse line by line to fix structural issues
	lines := strings.Split(fixed, "\n")
	var fixedLines []string
	
	for i := 0; i < len(lines); i++ {
		line := strings.TrimSpace(lines[i])
		
		// Skip empty lines
		if line == "" {
			fixedLines = append(fixedLines, lines[i])
			continue
		}
		
		// Check if this line starts an array but never closes it
		if strings.Contains(line, "\":") && strings.Contains(line, "[") && !strings.Contains(line, "]") {
			// This is an array field like: "insights": ["item1", "item2"
			// Need to find where it closes
			
			insideArray := true
			arrayLines := []string{lines[i]}
			
			// Look ahead for array items and closing
			for j := i + 1; j < len(lines) && insideArray; j++ {
				nextLine := strings.TrimSpace(lines[j])
				arrayLines = append(arrayLines, lines[j])
				
				// Check if this is a new field (ends array)
				if strings.Contains(nextLine, "\":") && !strings.HasPrefix(nextLine, "\"") {
					// New field started, array was never closed
					// Add closing bracket to previous line
					if len(arrayLines) >= 2 {
						prevIdx := len(arrayLines) - 2
						prevLine := strings.TrimSpace(arrayLines[prevIdx])
						
						// Add ] before comma if needed
						if strings.HasSuffix(prevLine, ",") {
							arrayLines[prevIdx] = strings.TrimSuffix(arrayLines[prevIdx], ",") + "],"
						} else if strings.HasSuffix(prevLine, "\"") {
							arrayLines[prevIdx] = arrayLines[prevIdx] + "],"
						}
					}
					insideArray = false
					
					// Don't re-add the new field line yet
					j--
					arrayLines = arrayLines[:len(arrayLines)-1]
				} else if strings.Contains(nextLine, "]") {
					// Array properly closed
					insideArray = false
				}
				
				i = j
			}
			
			// Add all fixed array lines
			fixedLines = append(fixedLines, arrayLines...)
			continue
		}
		
		// Fix trailing comma before closing brace
		if line == "}," || line == "}" {
			// Check if previous line has comma
			if len(fixedLines) > 0 {
				prevIdx := len(fixedLines) - 1
				prevLine := strings.TrimSpace(fixedLines[prevIdx])
				
				// If it's the last field before }, remove comma
				if line == "}" && strings.HasSuffix(prevLine, ",") && !strings.HasSuffix(prevLine, "],") {
					fixedLines[prevIdx] = strings.TrimSuffix(fixedLines[prevIdx], ",")
				}
			}
		}
		
		fixedLines = append(fixedLines, lines[i])
	}
	
	fixed = strings.Join(fixedLines, "\n")
	
	// Final cleanup: remove trailing commas before }
	fixed = strings.ReplaceAll(fixed, ",\n}", "\n}")
	fixed = strings.ReplaceAll(fixed, ", }", " }")
	
	// Fix double commas
	fixed = strings.ReplaceAll(fixed, ",,", ",")
	
	// Fix missing commas between array items (common error)
	fixed = strings.ReplaceAll(fixed, "\" \"", "\", \"")
	
	return fixed
}

// extractURLsFromSearchResults extracts URLs from SearXNG search output
func (e *Engine) extractURLsFromSearchResults(searchOutput string) []string {
	urls := []string{}
	lines := strings.Split(searchOutput, "\n")
	
	for _, line := range lines {
		// SearXNG format: "    URL: https://example.com"
		if strings.Contains(line, "URL: ") {
			parts := strings.Split(line, "URL: ")
			if len(parts) > 1 {
				url := strings.TrimSpace(parts[1])
				// Validate it's a proper URL
				if strings.HasPrefix(url, "http://") || strings.HasPrefix(url, "https://") {
					urls = append(urls, url)
				}
			}
		}
	}
	
	return urls
}

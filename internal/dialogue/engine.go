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
	adaptiveConfig            *AdaptiveConfig
	circuitBreaker            *tools.CircuitBreaker
}

// NewEngine creates a new dialogue engine
func NewEngine(
	storage *memory.Storage,
	embedder *memory.Embedder,
	stateManager *StateManager,
	toolRegistry *tools.ContextualRegistry,
	llmURL string,
	llmModel string,
	contextSize int,
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
	circuitBreaker *tools.CircuitBreaker,
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
		adaptiveConfig:            NewAdaptiveConfig(0.30, 0.85, 60),
		circuitBreaker:            circuitBreaker,
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
	
	// PHASE 0: Update adaptive thresholds based on current state
	totalMemories, err := e.storage.GetTotalMemoryCount(ctx)
	if err != nil {
		log.Printf("[Dialogue] WARNING: Failed to get memory count: %v", err)
		totalMemories = 0
	}
	e.adaptiveConfig.UpdateMetrics(ctx, state, totalMemories)
	
	// PHASE 1: Enhanced Reflection with Structured Reasoning
	log.Printf("[Dialogue] PHASE 1: Enhanced Reflection")
	
	// Check context before expensive operation
	if ctx.Err() != nil {
		return StopReasonNaturalStop, fmt.Errorf("cycle cancelled before reflection: %w", ctx.Err())
	}
	
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
		storedCount := 0
		storedIDs := []string{}
		for _, learning := range reasoning.Learnings.ToSlice() {
			memID, err := e.storeLearning(ctx, learning)
			if err != nil {
				log.Printf("[Dialogue] ERROR: Failed to store learning: %v", err)
			} else {
				storedCount++
				storedIDs = append(storedIDs, memID)
			}
		}
		log.Printf("[Dialogue] Stored %d/%d learnings in memory (collective=true)", storedCount, len(reasoning.Learnings))
		
		// Give Qdrant time to index the new embeddings
		if storedCount > 0 {
			log.Printf("[Dialogue] Waiting 2s for Qdrant to index %d new learnings...", storedCount)
			time.Sleep(2 * time.Second)
			
			// Verify learnings are searchable
			for _, memID := range storedIDs {
				mem, err := e.storage.GetMemoryByID(ctx, memID)
				if err != nil {
					log.Printf("[Dialogue] WARNING: Stored learning %s not immediately retrievable: %v", memID, err)
				} else {
					log.Printf("[Dialogue] ✓ Verified learning %s is retrievable", truncate(mem.Content, 60))
				}
			}
		}
	}
	
	// Check token budget
	if totalTokens >= e.maxTokensPerCycle {
		metrics.ThoughtCount = thoughtCount
		metrics.ActionCount = actionCount
		metrics.TokensUsed = totalTokens
		return StopReasonMaxThoughts, nil
	}

	// Check for extended idle periods and trigger exploration
	if len(state.ActiveGoals) == 0 {
		timeSinceLastCycle := time.Since(state.LastCycleTime)
		
		// If idle for 1+ hours with no goals, explore proactively
		if timeSinceLastCycle > 1*time.Hour {
			log.Printf("[Dialogue] Extended idle period detected (%s), generating exploratory goal",
				timeSinceLastCycle.Round(time.Minute))
			
			userInterests, err := e.analyzeUserInterests(ctx)
			if err != nil {
				log.Printf("[Dialogue] WARNING: Failed to analyze user interests: %v", err)
				userInterests = []string{}
			}
			
			exploratoryGoal := e.generateExploratoryGoal(ctx, userInterests, "", []string{})
			state.ActiveGoals = append(state.ActiveGoals, exploratoryGoal)
			metrics.GoalsCreated++
			
			log.Printf("[Dialogue] ✓ Created idle-period exploratory goal: %s",
				truncate(exploratoryGoal.Description, 60))
		}
	}

	
	// PHASE 2: Reasoning-Driven Goal Management
	log.Printf("[Dialogue] PHASE 2: Reasoning-Driven Goal Management")
	
	// Check context before continuing
	if ctx.Err() != nil {
		metrics.ThoughtCount = thoughtCount
		metrics.ActionCount = actionCount
		metrics.TokensUsed = totalTokens
		return StopReasonNaturalStop, fmt.Errorf("cycle cancelled before goal management: %w", ctx.Err())
	}
	
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

	// Check for meta-loops and trigger exploration if needed
	inMetaLoop, loopTopic := e.detectMetaLoop(state)
	
if inMetaLoop {
		log.Printf("[Dialogue] Meta-loop detected, switching to exploratory mode")
		
		// Get user interests for context
		userInterests, err := e.analyzeUserInterests(ctx)
		if err != nil {
			log.Printf("[Dialogue] WARNING: Failed to analyze user interests: %v", err)
			userInterests = []string{}
		}
		
		if len(userInterests) > 0 {
			log.Printf("[Dialogue] User interests identified: %v", userInterests)
		}
		
		// Extract recent goal descriptions to avoid repetition
		recentGoalDescriptions := []string{}
		recentGoals := state.CompletedGoals
		if len(recentGoals) > 5 {
			recentGoals = recentGoals[len(recentGoals)-5:]
		}
		for _, goal := range recentGoals {
			recentGoalDescriptions = append(recentGoalDescriptions, goal.Description)
		}
		
		// Create exploratory goal
		exploratoryGoal := e.generateExploratoryGoal(ctx, userInterests, loopTopic, recentGoalDescriptions)
		
		// Add to state immediately
		state.ActiveGoals = append(state.ActiveGoals, exploratoryGoal)
		metrics.GoalsCreated++
		
		log.Printf("[Dialogue] ✓ Created exploratory goal to break meta-loop: %s", 
			truncate(exploratoryGoal.Description, 60))
	}
	
// Try to create user-aligned goal if we have capacity and no recent user-aligned goals
	if len(state.ActiveGoals) < 3 {
		// Check if we recently created a user-aligned goal
		hasRecentUserGoal := false
		for _, goal := range state.ActiveGoals {
			if goal.Source == "user_interest" {
				hasRecentUserGoal = true
				break
			}
		}
		
		if !hasRecentUserGoal {
			// Build user profile
			userProfile, err := e.BuildUserProfile(ctx)
			if err != nil {
				log.Printf("[Dialogue] WARNING: Failed to build user profile: %v", err)
			} else if len(userProfile.TopTopics) > 0 {
				// Get recent goal descriptions to avoid duplication
				recentTopics := []string{}
				for _, goal := range state.ActiveGoals {
					recentTopics = append(recentTopics, goal.Description)
				}
				for _, goal := range state.CompletedGoals {
					if len(recentTopics) < 10 {
						recentTopics = append(recentTopics, goal.Description)
					}
				}
				
				// Generate user-aligned goal
				userGoal, err := e.GenerateUserAlignedGoal(ctx, userProfile, recentTopics)
				if err != nil {
					log.Printf("[Dialogue] WARNING: Failed to generate user-aligned goal: %v", err)
				} else {
					state.ActiveGoals = append(state.ActiveGoals, userGoal)
					metrics.GoalsCreated++
					log.Printf("[Dialogue] ✓ Created user-aligned goal: %s", 
						truncate(userGoal.Description, 60))
				}
			}
		}
	}
	
	// Create goals from LLM proposals if available (but not if we have too many already)
	newGoals := []Goal{}
	if len(reasoning.GoalsToCreate.ToSlice()) > 0 && len(state.ActiveGoals) < 5 {
		log.Printf("[Dialogue] LLM proposed %d new goals", len(reasoning.GoalsToCreate))
		
		// Get recently abandoned goals (last 10)
		recentlyAbandoned := []Goal{}
		if len(state.CompletedGoals) > 0 {
			startIdx := len(state.CompletedGoals) - 10
			if startIdx < 0 {
				startIdx = 0
			}
			for i := startIdx; i < len(state.CompletedGoals); i++ {
				if state.CompletedGoals[i].Status == GoalStatusAbandoned {
					recentlyAbandoned = append(recentlyAbandoned, state.CompletedGoals[i])
				}
			}
		}
		
		for _, proposal := range reasoning.GoalsToCreate.ToSlice() {
			// Check for duplicates against active goals
			if e.isGoalDuplicate(ctx, proposal.Description, state.ActiveGoals) {
				log.Printf("[Dialogue] Skipping duplicate goal (matches active): %s", truncate(proposal.Description, 40))
				continue
			}
			
			// Check for duplicates against recently abandoned goals
			if len(recentlyAbandoned) > 0 && e.isGoalDuplicate(ctx, proposal.Description, recentlyAbandoned) {
				log.Printf("[Dialogue] Skipping duplicate goal (matches recently abandoned): %s", truncate(proposal.Description, 40))
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
		
		// Check context before executing actions
		if ctx.Err() != nil {
			metrics.ThoughtCount = thoughtCount
			metrics.ActionCount = actionCount
			metrics.TokensUsed = totalTokens
			log.Printf("[Dialogue] Cycle timeout reached during goal pursuit")
			return StopReasonNaturalStop, nil
		}
		
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
				
				// If this was a search, pass URLs to the next parse action
				if err == nil && action.Tool == ActionToolSearch {
					if action.Metadata != nil {
						if urls, ok := action.Metadata["extracted_urls"].([]string); ok && len(urls) > 0 {
							// Find next pending parse action and give it the URLs
							for j := i + 1; j < len(topGoal.Actions); j++ {
								nextAction := &topGoal.Actions[j]
								if (nextAction.Tool == ActionToolWebParseGeneral || 
								    nextAction.Tool == ActionToolWebParseContextual) &&
								   nextAction.Status == ActionStatusPending {
									if nextAction.Metadata == nil {
										nextAction.Metadata = make(map[string]interface{})
									}
									nextAction.Metadata["previous_search_urls"] = urls
									// Update description with actual URL
									nextAction.Description = urls[0]
									log.Printf("[Dialogue] Updated next parse action with URL: %s", truncate(urls[0], 60))
									break
								}
							}
						}
					}
				}
				if err != nil {
					log.Printf("[Dialogue] Action failed: %v", err)
					action.Result = fmt.Sprintf("ERROR: %v", err)
					action.Status = ActionStatusCompleted
					
					// Mark goal as having failures
					topGoal.Outcome = "bad"
					
					// Don't increment actionCount - this was a failure
					actionExecuted = true // Still counts as execution attempt
					
					log.Printf("[Dialogue] ⚠ Action failed, goal marked as bad outcome")
				} else {
					// Check if result indicates success
					resultLower := strings.ToLower(result)
					if strings.Contains(resultLower, "error") || 
					   strings.Contains(resultLower, "failed") ||
					   strings.Contains(resultLower, "403") ||
					   strings.Contains(resultLower, "404") ||
					   strings.Contains(resultLower, "timeout") {
						log.Printf("[Dialogue] Action completed but result indicates failure")
						action.Result = result
						action.Status = ActionStatusCompleted
						topGoal.Outcome = "bad"
						actionExecuted = true
					} else {
						action.Result = result
						action.Status = ActionStatusCompleted
						actionCount++
						actionExecuted = true
						log.Printf("[Dialogue] Action completed successfully: %s", truncate(result, 80))
					}
				}
				action.Timestamp = time.Now()
				
				// Only execute one action per cycle
				break
			}
		}
		
		// If no actions were executed, check if we should create new actions
		if !actionExecuted {
			// Check if all actions are completed (need to create more)
			allActionsCompleted := len(topGoal.Actions) > 0
			for _, action := range topGoal.Actions {
				if action.Status == ActionStatusPending || action.Status == ActionStatusInProgress {
					allActionsCompleted = false
					break
				}
			}
			
			// Create new actions if: (1) no actions at all, OR (2) all actions completed
			if len(topGoal.Actions) == 0 || allActionsCompleted {
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
					searchAction := Action{
						Description: searchQuery,
						Tool:        ActionToolSearch,
						Status:      ActionStatusPending,
						Timestamp:   time.Now(),
					}
					topGoal.Actions = append(topGoal.Actions, searchAction)
					log.Printf("[Dialogue] Created search action: %s", truncate(searchQuery, 60))
					
					// Pre-plan parse action (will execute after search completes)
					// Determine which parser to use based on goal type
					goalLower := strings.ToLower(topGoal.Description)
					var parseAction Action
					
					if strings.Contains(goalLower, "research") ||
					   strings.Contains(goalLower, "analyze") ||
					   strings.Contains(goalLower, "understand") ||
					   strings.Contains(goalLower, "learn about") {
						// Create contextual parse with specific purpose
						purpose := fmt.Sprintf("Extract information relevant to: %s", topGoal.Description)
						parseAction = Action{
							Description: "URL from search results",
							Tool:        ActionToolWebParseContextual,
							Status:      ActionStatusPending,
							Timestamp:   time.Now(),
							Metadata:    map[string]interface{}{"purpose": purpose},
						}
						log.Printf("[Dialogue] Pre-planned contextual parse action")
					} else {
						// Use general parser for simpler goals
						parseAction = Action{
							Description: "URL from search results",
							Tool:        ActionToolWebParseGeneral,
							Status:      ActionStatusPending,
							Timestamp:   time.Now(),
						}
						log.Printf("[Dialogue] Pre-planned general parse action")
					}
					
					topGoal.Actions = append(topGoal.Actions, parseAction)
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
	currentTime := time.Now()
	
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
			log.Printf("[Dialogue] Auto-abandoned stale goal (24h+ with no progress): %s", truncate(goal.Description, 60))
		} else {
			activeGoals = append(activeGoals, goal)
		}
	}
	
	// Limit active goals to top 5 by priority
	// BUT: Don't prune goals created in the last 30 minutes (give them a chance)
	if len(activeGoals) > 5 {
		sortedGoals := sortGoalsByPriority(activeGoals)
		
		// Separate new vs old goals
		newGoals := []Goal{}
		oldGoals := []Goal{}
		for _, goal := range sortedGoals {
			if currentTime.Sub(goal.Created) < 30*time.Minute {
				newGoals = append(newGoals, goal)
			} else {
				oldGoals = append(oldGoals, goal)
			}
		}
		
		log.Printf("[Dialogue] Goal counts: %d total (%d new, %d old)", len(sortedGoals), len(newGoals), len(oldGoals))
		
		// Keep all new goals + top old goals up to limit of 5
		activeGoals = newGoals
		remainingSlots := 5 - len(newGoals)
		
		if remainingSlots > 0 && len(oldGoals) > 0 {
			// Keep top N old goals
			if len(oldGoals) > remainingSlots {
				activeGoals = append(activeGoals, oldGoals[:remainingSlots]...)
				// Abandon the rest
				for i := remainingSlots; i < len(oldGoals); i++ {
					oldGoals[i].Status = GoalStatusAbandoned
					oldGoals[i].Outcome = "neutral"
					state.CompletedGoals = append(state.CompletedGoals, oldGoals[i])
					abandonedCount++
				}
				log.Printf("[Dialogue] Pruned %d low-priority old goals (kept %d new + %d old)", 
					len(oldGoals)-remainingSlots, len(newGoals), remainingSlots)
			} else {
				activeGoals = append(activeGoals, oldGoals...)
			}
		} else if remainingSlots <= 0 {
			// All 5 slots taken by new goals, abandon all old goals
			for i := range oldGoals {
				oldGoals[i].Status = GoalStatusAbandoned
				oldGoals[i].Outcome = "neutral"
				state.CompletedGoals = append(state.CompletedGoals, oldGoals[i])
				abandonedCount++
			}
			if len(oldGoals) > 0 {
				log.Printf("[Dialogue] Abandoned %d old goals (all slots taken by new goals)", len(oldGoals))
			}
		}
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
		IncludeCollective: true,
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
		IncludeCollective: true,
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
	ctx := context.Background()
	
	// Get recently abandoned goals for duplicate checking
	recentlyAbandoned := []Goal{}
	if len(state.CompletedGoals) > 0 {
		startIdx := len(state.CompletedGoals) - 10
		if startIdx < 0 {
			startIdx = 0
		}
		for i := startIdx; i < len(state.CompletedGoals); i++ {
			if state.CompletedGoals[i].Status == GoalStatusAbandoned {
				recentlyAbandoned = append(recentlyAbandoned, state.CompletedGoals[i])
			}
		}
	}
	
	// Create goals from knowledge gaps (user requests)
	for _, gap := range state.KnowledgeGaps {
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
		
		// Check for duplicates against active goals
		if e.isGoalDuplicate(ctx, description, state.ActiveGoals) {
			log.Printf("[Dialogue] Skipping duplicate goal (matches active): %s", truncate(description, 40))
			continue
		}
		
		// Check for duplicates against recently abandoned goals
		if len(recentlyAbandoned) > 0 && e.isGoalDuplicate(ctx, description, recentlyAbandoned) {
			log.Printf("[Dialogue] Skipping duplicate goal (matches recently abandoned): %s", truncate(description, 40))
			continue
		}
		
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

// isGoalDuplicate checks if a proposed goal is too similar to existing goals
// Uses both string matching and semantic similarity
func (e *Engine) isGoalDuplicate(ctx context.Context, proposalDesc string, existingGoals []Goal) bool {
	proposalLower := strings.ToLower(proposalDesc)
	
	// Quick check: exact match or very similar strings
	for _, existingGoal := range existingGoals {
		existingLower := strings.ToLower(existingGoal.Description)
		
		// Exact match
		if proposalLower == existingLower {
			log.Printf("[Dialogue] Duplicate detected (exact match): '%s'", truncate(proposalDesc, 50))
			return true
		}
		
		// Check if first 50 chars match (increased from 30 for better detection)
		minLen := min(len(proposalLower), len(existingLower))
		if minLen > 50 {
			minLen = 50
		}
		if minLen >= 20 { // Only check if we have enough characters
			if strings.Contains(existingLower, proposalLower[:minLen]) ||
			   strings.Contains(proposalLower, existingLower[:minLen]) {
				log.Printf("[Dialogue] Duplicate detected (prefix match): '%s' ~= '%s'", 
					truncate(proposalDesc, 40), truncate(existingGoal.Description, 40))
				return true
			}
		}
	}
	
	// Semantic similarity check using embeddings
	// Only check if we have at least 3 existing goals (avoid overhead for small lists)
	if len(existingGoals) >= 3 {
		proposalEmbedding, err := e.embedder.Embed(ctx, proposalDesc)
		if err != nil {
			log.Printf("[Dialogue] WARNING: Failed to generate embedding for duplicate check: %v", err)
			return false // Don't block on embedding failure
		}
		
		// Compare with each existing goal
		for _, existingGoal := range existingGoals {
			existingEmbedding, err := e.embedder.Embed(ctx, existingGoal.Description)
			if err != nil {
				continue // Skip this comparison
			}
			
			// Calculate cosine similarity
			similarity := cosineSimilarity(proposalEmbedding, existingEmbedding)
			
			// Use adaptive threshold for duplicate detection
			threshold := e.adaptiveConfig.GetGoalSimilarityThreshold()
			if similarity > threshold {
				log.Printf("[Dialogue] Detected semantic duplicate (%.2f > %.2f threshold): '%s' ~= '%s'",
					similarity, threshold, truncate(proposalDesc, 40), truncate(existingGoal.Description, 40))
				return true
			}
		}
	}
	
	return false
}

// analyzeUserInterests extracts topics the user has shown interest in
func (e *Engine) analyzeUserInterests(ctx context.Context) ([]string, error) {
	// Search for user interactions (non-collective memories)
	embedding, err := e.embedder.Embed(ctx, "user questions topics interests discussion")
	if err != nil {
		return []string{}, err
	}
	
	query := memory.RetrievalQuery{
		Limit:             20,
		MinScore:          0.3,
		IncludePersonal:   true,
		IncludeCollective: false, // Only user interactions
	}
	
	results, err := e.storage.Search(ctx, query, embedding)
	if err != nil {
		return []string{}, err
	}
	
	if len(results) == 0 {
		return []string{}, nil
	}
	
	// Extract concept tags from user memories
	topicFrequency := make(map[string]int)
	for _, result := range results {
		for _, tag := range result.Memory.ConceptTags {
			topicFrequency[tag]++
		}
	}
	
	// Sort by frequency
	type topicCount struct {
		topic string
		count int
	}
	var topics []topicCount
	for topic, count := range topicFrequency {
		topics = append(topics, topicCount{topic, count})
	}
	
	// Sort descending by count
	for i := 0; i < len(topics); i++ {
		for j := i + 1; j < len(topics); j++ {
			if topics[j].count > topics[i].count {
				topics[i], topics[j] = topics[j], topics[i]
			}
		}
	}
	
	// Return top topics
	result := []string{}
	for i := 0; i < len(topics) && i < 5; i++ {
		result = append(result, topics[i].topic)
	}
	
	return result, nil
}

// detectMetaLoop checks if system is stuck researching the same topic
func (e *Engine) detectMetaLoop(state *InternalState) (bool, string) {
	if len(state.CompletedGoals) < 3 {
		return false, ""
	}
	
	// Check last 5 completed goals
	recentGoals := state.CompletedGoals
	if len(recentGoals) > 5 {
		recentGoals = recentGoals[len(recentGoals)-5:]
	}
	
	// Count topic similarities
	topicCounts := make(map[string]int)
	for _, goal := range recentGoals {
		// Extract key terms from goal description
		desc := strings.ToLower(goal.Description)
		
		// Common meta-loop topics
		if strings.Contains(desc, "memory") || strings.Contains(desc, "data") || 
		   strings.Contains(desc, "missing") || strings.Contains(desc, "experiential") {
			topicCounts["meta-memory"]++
		}
		if strings.Contains(desc, "learn about") && strings.Contains(desc, "knowledge") {
			topicCounts["meta-learning"]++
		}
	}
	
	// If 3+ of last 5 goals are about the same meta topic, it's a loop
	for topic, count := range topicCounts {
		if count >= 3 {
			log.Printf("[Dialogue] Meta-loop detected: %d/%d recent goals about '%s'", 
				count, len(recentGoals), topic)
			return true, topic
		}
	}
	
	return false, ""
}

// generateExploratoryGoal creates a curiosity-driven goal based on context
func (e *Engine) generateExploratoryGoal(ctx context.Context, userInterests []string, avoidTopic string, recentGoalDescriptions []string) Goal {
	var description string
	var priority int
	
	if len(userInterests) > 0 {
		// Filter out generic terms
		genericTerms := []string{"general", "context", "learning", "user", "curiosity", 
		                        "personal", "interests", "data", "memory", "conversation"}
		
		specificInterests := []string{}
		for _, interest := range userInterests {
			isGeneric := false
			for _, generic := range genericTerms {
				if interest == generic {
					isGeneric = true
					break
				}
			}
			if !isGeneric {
				specificInterests = append(specificInterests, interest)
			}
		}
		
		// Use specific interests if we have them
		candidates := specificInterests
		if len(candidates) == 0 {
			candidates = userInterests
		}
		
		// Pick a topic that hasn't been explored in recent goals
		selectedTopic := ""
		for _, candidate := range candidates {
			alreadyExplored := false
			for _, recentGoal := range recentGoalDescriptions {
				if strings.Contains(strings.ToLower(recentGoal), strings.ToLower(candidate)) {
					alreadyExplored = true
					break
				}
			}
			if !alreadyExplored {
				selectedTopic = candidate
				break
			}
		}
		
		// Fallback if all explored
		if selectedTopic == "" && len(candidates) > 0 {
			selectedTopic = candidates[0]
		}
		
		if selectedTopic != "" {
			// Generate SPECIFIC variations
			variations := []string{
				fmt.Sprintf("Research how %s relates to human-AI interaction", selectedTopic),
				fmt.Sprintf("Explore practical examples of %s in conversational AI", selectedTopic),
				fmt.Sprintf("Investigate current approaches to %s in chatbot development", selectedTopic),
				fmt.Sprintf("Analyze how %s contributes to natural dialogue", selectedTopic),
			}
			
			description = variations[rand.Intn(len(variations))]
			priority = 6
			
			log.Printf("[Dialogue] Generated user-interest exploratory goal: %s", description)
		}
	}
	
	// Fallback: conversation-focused topics
	if description == "" {
		exploratoryTopics := []string{
			"Research how chatbots develop consistent personalities",
			"Explore techniques for natural conversation flow in AI",
			"Investigate how AI can express empathy and emotional intelligence",
			"Research methods for AI to maintain conversational context",
			"Explore how dialogue systems handle ambiguity",
			"Investigate storytelling techniques in conversational AI",
			"Research how AI can develop and maintain a backstory",
		}
		
		description = exploratoryTopics[rand.Intn(len(exploratoryTopics))]
		priority = 5
		
		log.Printf("[Dialogue] Generated conversation-focused exploratory goal: %s", description)
	}
	
	return Goal{
		ID:          fmt.Sprintf("goal_%d", time.Now().UnixNano()),
		Description: description,
		Source:      GoalSourceCuriosity,
		Priority:    priority,
		Created:     time.Now(),
		Progress:    0.0,
		Status:      GoalStatusActive,
		Actions:     []Action{},
	}
}

// cosineSimilarity calculates cosine similarity between two vectors
func cosineSimilarity(a, b []float32) float64 {
	if len(a) != len(b) {
		return 0.0
	}
	
	var dotProduct, normA, normB float64
	
	for i := 0; i < len(a); i++ {
		dotProduct += float64(a[i]) * float64(b[i])
		normA += float64(a[i]) * float64(a[i])
		normB += float64(b[i]) * float64(b[i])
	}
	
	if normA == 0 || normB == 0 {
		return 0.0
	}
	
	return dotProduct / (sqrt(normA) * sqrt(normB))
}

// sqrt is a simple square root helper
func sqrt(x float64) float64 {
	if x < 0 {
		return 0
	}
	// Use Newton's method for square root
	if x == 0 {
		return 0
	}
	z := x
	for i := 0; i < 10; i++ {
		z = (z + x/z) / 2
	}
	return z
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
	// Check circuit breaker first
	if e.circuitBreaker != nil && e.circuitBreaker.IsOpen() {
		return "", 0, fmt.Errorf("LLM circuit breaker is open, service unavailable")
	}
	
	// Get adaptive timeout
	timeout := time.Duration(e.adaptiveConfig.GetToolTimeout()) * time.Second
	if timeout < 60*time.Second {
		timeout = 60 * time.Second // Minimum 60s
	}
	
	// Create timeout context
	timeoutCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	
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
	
	req, err := http.NewRequestWithContext(timeoutCtx, "POST", e.llmURL, bytes.NewBuffer(jsonData))
	if err != nil {
		return "", 0, fmt.Errorf("failed to create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	
	// Configure transport with response header timeout
	transport := &http.Transport{
		ResponseHeaderTimeout: 90 * time.Second, // Fail fast if LLM doesn't start
		IdleConnTimeout:       90 * time.Second,
		MaxIdleConns:          10,
		DisableKeepAlives:     false,
	}
	
	client := &http.Client{
		Timeout:   timeout,
		Transport: transport,
	}
	
	log.Printf("[Dialogue] LLM call started (timeout: %s, prompt length: %d chars)", timeout, len(prompt))
	startTime := time.Now()
	
	resp, err := client.Do(req)
	if err != nil {
		elapsed := time.Since(startTime)
		
		// Record failure in circuit breaker
		if e.circuitBreaker != nil {
			e.circuitBreaker.Call(func() error { return err })
		}
		
		if timeoutCtx.Err() == context.DeadlineExceeded {
			log.Printf("[Dialogue] LLM timeout after %s", elapsed)
			return "", 0, fmt.Errorf("LLM timeout after %s: %w", elapsed, err)
		}
		log.Printf("[Dialogue] LLM request failed after %s: %v", elapsed, err)
		return "", 0, fmt.Errorf("failed to send request: %w", err)
	}
	defer resp.Body.Close()
	
	log.Printf("[Dialogue] LLM response received in %s", time.Since(startTime))
	
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
	
	// Record success in circuit breaker
	if e.circuitBreaker != nil {
		e.circuitBreaker.Call(func() error { return nil })
	}
	
	return content, tokens, nil
}

// callLLMWithStructuredReasoning requests structured JSON reasoning from the LLM
func (e *Engine) callLLMWithStructuredReasoning(ctx context.Context, prompt string, expectJSON bool) (*ReasoningResponse, int, error) {
	// Check circuit breaker first
	if e.circuitBreaker != nil && e.circuitBreaker.IsOpen() {
		return nil, 0, fmt.Errorf("LLM circuit breaker is open, service unavailable")
	}
	
	// Get adaptive timeout (structured reasoning may take longer)
	timeout := time.Duration(e.adaptiveConfig.GetToolTimeout()) * time.Second
	if timeout < 120*time.Second {
		timeout = 120 * time.Second // Minimum 2 minutes for structured reasoning
	}
	
	// Create timeout context
	timeoutCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	
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
	
	req, err := http.NewRequestWithContext(timeoutCtx, "POST", e.llmURL, bytes.NewBuffer(jsonData))
	if err != nil {
		return nil, 0, fmt.Errorf("failed to create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	
	// Configure transport with response header timeout
	transport := &http.Transport{
		ResponseHeaderTimeout: 90 * time.Second,
		IdleConnTimeout:       90 * time.Second,
		MaxIdleConns:          10,
		DisableKeepAlives:     false,
	}
	
	client := &http.Client{
		Timeout:   timeout,
		Transport: transport,
	}
	
	log.Printf("[Dialogue] Structured reasoning LLM call started (timeout: %s, prompt length: %d chars)", 
		timeout, len(prompt))
	startTime := time.Now()
	
	resp, err := client.Do(req)
	if err != nil {
		elapsed := time.Since(startTime)
		
		// Record failure in circuit breaker
		if e.circuitBreaker != nil {
			e.circuitBreaker.Call(func() error { return err })
		}
		
		if timeoutCtx.Err() == context.DeadlineExceeded {
			log.Printf("[Dialogue] Structured reasoning LLM timeout after %s", elapsed)
			return nil, 0, fmt.Errorf("LLM timeout after %s: %w", elapsed, err)
		}
		log.Printf("[Dialogue] Structured reasoning LLM request failed after %s: %v", elapsed, err)
		return nil, 0, fmt.Errorf("failed to send request: %w", err)
	}
	defer resp.Body.Close()
	
	log.Printf("[Dialogue] Structured reasoning LLM response received in %s", time.Since(startTime))
	
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
	
	// Record success in circuit breaker
	if e.circuitBreaker != nil {
		e.circuitBreaker.Call(func() error { return nil })
	}
	
	return &reasoning, tokens, nil
}


// executeAction executes a tool-based action
func (e *Engine) executeAction(ctx context.Context, action *Action) (string, error) {
	log.Printf("[Dialogue] Executing action with tool '%s' (description: %s)", 
		action.Tool, truncate(action.Description, 60))
	startTime := time.Now()
	
	// Check context before starting
	if ctx.Err() != nil {
		return "", fmt.Errorf("action cancelled before execution: %w", ctx.Err())
	}
	
	// Map action tool to actual tool execution
	switch action.Tool {
case ActionToolSearch:
	// Extract search query from action description
	params := map[string]interface{}{
		"query": action.Description,
	}
	
	log.Printf("[Dialogue] Calling search tool with query: %s", truncate(action.Description, 80))
	result, err := e.toolRegistry.ExecuteIdle(ctx, tools.ToolNameSearch, params)
	
	elapsed := time.Since(startTime)
	
	if err != nil {
		log.Printf("[Dialogue] Search tool failed after %s: %v", elapsed, err)
		return "", fmt.Errorf("search tool failed: %w", err)
	}
	
	if !result.Success {
		log.Printf("[Dialogue] Search returned failure after %s: %s", elapsed, result.Error)
		return "", fmt.Errorf("search failed: %s", result.Error)
	}
	
	log.Printf("[Dialogue] Search completed successfully in %s", elapsed)
	
	// Store URLs in action metadata for the next parse action to use
	urls := e.extractURLsFromSearchResults(result.Output)
	if len(urls) > 0 {
		log.Printf("[Dialogue] Extracted %d URLs from search results, storing for parse action", len(urls))
		if action.Metadata == nil {
			action.Metadata = make(map[string]interface{})
		}
		action.Metadata["extracted_urls"] = urls
	}
	
	return result.Output, nil
		
case ActionToolWebParse,
     ActionToolWebParseMetadata,
     ActionToolWebParseGeneral,
     ActionToolWebParseContextual,
     ActionToolWebParseChunked:
	
	var url string
	
	// First, check if previous search action stored URLs in metadata
	if action.Metadata != nil {
		if urls, ok := action.Metadata["previous_search_urls"].([]string); ok && len(urls) > 0 {
			url = urls[0]
			log.Printf("[Dialogue] Using URL from previous search metadata: %s", truncate(url, 60))
		}
	}
	
	// Fallback: extract URL from action description
	if url == "" {
		// Formats handled: 
		//   - "https://example.com"
		//   - "Parse URL: https://example.com"
		//   - "Search result: https://example.com - title"
		//   - "URL from search results" (placeholder - will fail with clear error)
		url = strings.TrimSpace(action.Description)
		
		// Handle placeholder case
		if url == "URL from search results" {
			return "", fmt.Errorf("parse action has placeholder URL - previous search may have failed or returned no URLs")
		}
		
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
	}
	
	params := map[string]interface{}{
		"url": url,
	}
	
	// For contextual parsing, extract purpose from metadata if available
	if action.Tool == ActionToolWebParseContextual {
		if action.Metadata != nil {
			if purpose, ok := action.Metadata["purpose"].(string); ok && purpose != "" {
				params["purpose"] = purpose
				log.Printf("[Dialogue] Using contextual parser with purpose: %s", truncate(purpose, 60))
			} else {
				params["purpose"] = "Extract relevant information for research goal"
			}
		} else {
			params["purpose"] = "Extract relevant information for research goal"
		}
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
	log.Printf("[Dialogue] Calling web parse tool '%s' for URL: %s", action.Tool, truncate(url, 80))
	result, err := e.toolRegistry.ExecuteIdle(ctx, action.Tool, params)
	
	elapsed := time.Since(startTime)
	
	if err != nil {
		log.Printf("[Dialogue] Web parse tool failed after %s: %v", elapsed, err)
		return "", fmt.Errorf("web parse tool failed: %w", err)
	}
	
	if !result.Success {
		log.Printf("[Dialogue] Web parse returned failure after %s: %s", elapsed, result.Error)
		return "", fmt.Errorf("web parse failed: %s", result.Error)
	}
	
	log.Printf("[Dialogue] Web parse completed successfully in %s (%d chars output)", 
		elapsed, len(result.Output))
	
	return result.Output, nil
		
	case ActionToolSandbox:
		// Phase 3.5: Sandbox not yet implemented
		return "", fmt.Errorf("sandbox tool not yet implemented")
		
	case ActionToolMemoryConsolidation:
		// This is internal, not a real tool
		elapsed := time.Since(startTime)
		log.Printf("[Dialogue] Memory consolidation completed in %s", elapsed)
		return "Memory consolidation completed", nil
		
	default:
		return "", fmt.Errorf("unknown tool: %s", action.Tool)
	}
	
	// Note: Result logging happens in each case block above
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
	
	searchThreshold := e.adaptiveConfig.GetSearchThreshold()
	
	// For collective memory search (learnings), use a lower threshold
	// Recent learnings might not have perfect semantic match but should still be retrieved
	collectiveThreshold := searchThreshold
	if collectiveThreshold > 0.20 {
		collectiveThreshold = 0.20 // Lower threshold for collective memories
	}
	
	query := memory.RetrievalQuery{
		Limit:             10, // Increased from 8 to get more learnings
		MinScore:          collectiveThreshold,
		IncludeCollective: true,
		IncludePersonal:   false, // Explicitly exclude personal for collective-only search
	}
	
	log.Printf("[Dialogue] Searching collective memories (threshold: %.2f [adaptive: %.2f], limit: %d)", 
		collectiveThreshold, searchThreshold, 10)
	
	results, err := e.storage.Search(ctx, query, embedding)
	if err != nil {
		return nil, 0, fmt.Errorf("failed to search memories: %w", err)
	}
	
	log.Printf("[Dialogue] Collective memory search returned %d results", len(results))
	if len(results) > 0 {
		for i, result := range results {
			log.Printf("[Dialogue]   Result %d: score=%.2f, is_collective=%v, content=%s", 
				i+1, result.Score, result.Memory.IsCollective, truncate(result.Memory.Content, 60))
		}
	}
	
	// Additionally search specifically for learnings (by concept tag)
	learningQuery := memory.RetrievalQuery{
		Limit:             5,
		MinScore:          0.15, // Very low threshold for tagged learnings
		IncludeCollective: true,
		IncludePersonal:   false,
		ConceptTags:       []string{"learning"}, // Search for learning tag specifically
	}
	
	// Create a simple embedding for "learning" query
	learningEmbedding, err := e.embedder.Embed(ctx, "recent learnings insights knowledge")
	if err == nil {
		learningResults, err := e.storage.Search(ctx, learningQuery, learningEmbedding)
		if err == nil && len(learningResults) > 0 {
			log.Printf("[Dialogue] Found %d additional learnings by concept tag", len(learningResults))
			
			// Merge learning results with main results (avoid duplicates)
			existingIDs := make(map[string]bool)
			for _, r := range results {
				existingIDs[r.Memory.ID] = true
			}
			
			for _, lr := range learningResults {
				if !existingIDs[lr.Memory.ID] {
					results = append(results, lr)
					log.Printf("[Dialogue]   Learning: score=%.2f, content=%s", 
						lr.Score, truncate(lr.Memory.Content, 60))
				}
			}
		}
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
			goalsContext += fmt.Sprintf("%d. %s (progress: %.0f%%, priority: %d, age: %s)\n", 
				i+1, truncate(goal.Description, 60), goal.Progress*100, goal.Priority,
				time.Since(goal.Created).Round(time.Minute))
		}
	}
	
	// Add recently abandoned goals context (last 5)
	recentlyAbandoned := []Goal{}
	if len(state.CompletedGoals) > 0 {
		startIdx := len(state.CompletedGoals) - 5
		if startIdx < 0 {
			startIdx = 0
		}
		for i := startIdx; i < len(state.CompletedGoals); i++ {
			if state.CompletedGoals[i].Status == GoalStatusAbandoned {
				recentlyAbandoned = append(recentlyAbandoned, state.CompletedGoals[i])
			}
		}
	}
	
	if len(recentlyAbandoned) > 0 {
		goalsContext += "\nRecently abandoned goals (avoid recreating these):\n"
		for i, goal := range recentlyAbandoned {
			goalsContext += fmt.Sprintf("%d. %s (outcome: %s)\n", 
				i+1, truncate(goal.Description, 60), goal.Outcome)
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
	reasoning, tokens, err := e.callLLMWithStructuredReasoning(ctx, prompt, true)
	if err != nil {
		return nil, tokens, err
	}
	
	// Override LLM's confidence with calculated confidence based on actual metrics
	calculatedConfidence := e.calculateConfidence(ctx, state)
	
	// If LLM provided self-assessment, adjust the confidence
	if reasoning.SelfAssessment != nil {
		// Allow LLM to adjust ±0.2 from calculated baseline
		llmConfidence := reasoning.SelfAssessment.Confidence
		adjustment := llmConfidence - 0.5 // LLM's deviation from neutral
		
		// Apply adjustment (capped at ±0.2)
		if adjustment > 0.2 {
			adjustment = 0.2
		} else if adjustment < -0.2 {
			adjustment = -0.2
		}
		
		finalConfidence := calculatedConfidence + adjustment
		
		// Clamp to valid range
		if finalConfidence < 0.1 {
			finalConfidence = 0.1
		}
		if finalConfidence > 0.9 {
			finalConfidence = 0.9
		}
		
		log.Printf("[Dialogue] Confidence: calculated=%.2f, llm_raw=%.2f, adjustment=%.2f, final=%.2f",
			calculatedConfidence, llmConfidence, adjustment, finalConfidence)
		
		reasoning.SelfAssessment.Confidence = finalConfidence
	} else {
		// No self-assessment from LLM, use calculated confidence
		reasoning.SelfAssessment = &SelfAssessment{
			Confidence: calculatedConfidence,
		}
		log.Printf("[Dialogue] Confidence: calculated=%.2f (no LLM assessment)", calculatedConfidence)
	}
	
	return reasoning, tokens, nil
}

// calculateConfidence computes confidence score based on actual metrics
func (e *Engine) calculateConfidence(ctx context.Context, state *InternalState) float64 {
	// Start with baseline confidence
	confidence := 0.5
	
	// Factor 1: Goal completion rate (last 10 goals)
	recentGoals := state.CompletedGoals
	if len(recentGoals) > 10 {
		recentGoals = recentGoals[len(recentGoals)-10:]
	}
	
	if len(recentGoals) > 0 {
		successCount := 0
		for _, goal := range recentGoals {
			if goal.Outcome == "good" {
				successCount++
			}
		}
		goalSuccessRate := float64(successCount) / float64(len(recentGoals))
		confidence += (goalSuccessRate - 0.5) * 0.3 // ±0.15 based on goal success
	}
	
	// Factor 2: Recent memory retrieval (are we finding relevant context?)
	embedding, err := e.embedder.Embed(ctx, "recent activity patterns success")
	if err == nil {
		query := memory.RetrievalQuery{
			Limit:             5,
			MinScore:          0.5,
			IncludeCollective: true,
		}
		results, err := e.storage.Search(ctx, query, embedding)
		if err == nil && len(results) > 0 {
			// Average relevance score of retrieved memories
			avgScore := 0.0
			for _, result := range results {
				avgScore += result.Score
			}
			avgScore /= float64(len(results))
			confidence += (avgScore - 0.5) * 0.2 // ±0.1 based on retrieval quality
		}
	}
	
	// Factor 3: Active goals progress
	if len(state.ActiveGoals) > 0 {
		totalProgress := 0.0
		for _, goal := range state.ActiveGoals {
			totalProgress += goal.Progress
		}
		avgProgress := totalProgress / float64(len(state.ActiveGoals))
		confidence += (avgProgress - 0.5) * 0.2 // ±0.1 based on active progress
	}
	
	// Clamp to 0.1-0.9 range (never completely certain or uncertain)
	if confidence < 0.1 {
		confidence = 0.1
	}
	if confidence > 0.9 {
		confidence = 0.9
	}
	
	return confidence
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

// storeLearning stores a learning as a collective memory and returns the memory ID
func (e *Engine) storeLearning(ctx context.Context, learning Learning) (string, error) {
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
	
	log.Printf("[Dialogue] Storing learning as collective memory (is_collective=true): %s", truncate(learning.What, 60))
	
	err = e.storage.Store(ctx, mem)
	if err != nil {
		log.Printf("[Dialogue] ERROR: Failed to store learning in Qdrant: %v", err)
		return "", err
	}
	
	log.Printf("[Dialogue] ✓ Learning stored successfully (ID: %s, is_collective: true)", mem.ID)
	return mem.ID, nil
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

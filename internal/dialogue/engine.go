package dialogue

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"strconv"
	"math/rand"
	"strings"
	"time"

	"go-llama/internal/memory"
	"go-llama/internal/tools"
	"gorm.io/gorm"
)

// Engine manages the internal dialogue process
type Engine struct {
	storage                   *memory.Storage
	embedder                  *memory.Embedder
	stateManager              *StateManager
	toolRegistry              *tools.ContextualRegistry
	llmURL                    string
	llmModel                  string
	llmClient                 interface{} // Will be *llm.Client but avoid import cycle
	db                        *gorm.DB    // For loading principles
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
	db *gorm.DB, // Add DB for principles
	llmURL string,
	llmModel string,
	contextSize int,
	llmClient interface{}, // Accept queue client
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
		db:                        db, // Store DB
		llmURL:                    llmURL,
		llmModel:                  llmModel,
		llmClient:                 llmClient, // Store client
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
		adaptiveConfig:            NewAdaptiveConfig(0.30, 0.75, 60),
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
	
    if reasoning.Reflection == "" {
        log.Printf("[Dialogue] Reflection: (Empty - LLM did not provide reflection text)")
    } else {
        log.Printf("[Dialogue] Reflection: %s", truncate(reasoning.Reflection, 80))
    }
	
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
	if len(reasoning.GoalsToCreate.ToSlice()) > 0 && len(state.ActiveGoals) < 15 { // Increased limit for tier system
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
			
			// TIER VALIDATION: If secondary, validate linkage to primary goals
			if goal.Tier == "secondary" {
				primaryGoals := e.getPrimaryGoals(state.ActiveGoals)
				if len(primaryGoals) == 0 {
					log.Printf("[Dialogue] No primary goals exist, promoting secondary to primary: %s",
						truncate(goal.Description, 60))
					goal.Tier = "primary"
				} else {
					// Validate linkage to at least one primary
					validation, err := e.validateGoalSupport(ctx, &goal, primaryGoals)
					if err != nil {
						log.Printf("[Dialogue] WARNING: Failed to validate goal support: %v", err)
						// Allow goal but mark as unvalidated
						goal.DependencyScore = 0.5
					} else if !validation.IsValid {
						log.Printf("[Dialogue] Secondary goal does not support any primary, converting to tactical: %s",
							truncate(goal.Description, 60))
						goal.Tier = "tactical"
					} else {
						// Link to primary
						goal.SupportsGoals = []string{validation.SupportsGoalID}
						goal.DependencyScore = validation.Confidence
						
						log.Printf("[Dialogue] Secondary goal validated: supports %s (confidence: %.2f)",
							truncate(validation.SupportsGoalID, 20), validation.Confidence)
						log.Printf("[Dialogue]   Reasoning: %s", truncate(validation.Reasoning, 80))
					}
				}
			}
			
			newGoals = append(newGoals, goal)
			log.Printf("[Dialogue] Created goal [%s]: %s (priority: %d)", 
				goal.Tier, truncate(goal.Description, 60), goal.Priority)
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
		
		// GOAL CONTINUITY LOCK: Prioritize goals with pending work
		var topGoal Goal
		foundContinuation := false
		
		for i := range state.ActiveGoals {
			goal := &state.ActiveGoals[i]
			if goal.HasPendingWork && time.Since(goal.LastPursued) < 2*time.Hour {
				topGoal = *goal
				foundContinuation = true
				log.Printf("[Dialogue] Continuing goal with pending work: %s (last pursued: %s ago)",
					truncate(topGoal.Description, 60), time.Since(goal.LastPursued).Round(time.Minute))
				break
			}
		}
		
		// Fallback: Select highest priority goal
		if !foundContinuation {
			sortedGoals := sortGoalsByPriority(state.ActiveGoals)
			topGoal = sortedGoals[0]
			log.Printf("[Dialogue] Pursuing highest priority goal: %s (priority: %d)",
				truncate(topGoal.Description, 60), topGoal.Priority)
		}
		
		// Mark goal as actively pursued
		topGoal.LastPursued = time.Now()
		
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
				
// If this was a search, extract URLs and create parse action
if err == nil && action.Tool == ActionToolSearch {
    // Extract URLs from search results
    urls := e.extractURLsFromSearchResults(result)
    
    if len(urls) == 0 {
        log.Printf("[Dialogue] WARNING: No URLs found in search results")
        action.Result = "Search completed but no URLs found"
        action.Status = ActionStatusCompleted
        topGoal.Outcome = "bad"
    } else {
        // Use LLM-based evaluation to select best URL
        log.Printf("[Dialogue] Evaluating %d search results with LLM...", len(urls))
        
        evaluation, evalErr := e.evaluateSearchResults(ctx, result, topGoal.Description)
        
        if evalErr != nil || !evaluation.ShouldProceed {
            // Evaluation failed or no good URLs found
            if evalErr != nil {
                log.Printf("[Dialogue] WARNING: Search evaluation failed: %v", evalErr)
            } else {
                log.Printf("[Dialogue] Search evaluation determined no suitable URLs")
            }
            
            action.Result = "Search completed but no suitable URLs found"
            action.Status = ActionStatusCompleted
            topGoal.Outcome = "bad"
        } else {
            // Evaluation succeeded - use best URL
            bestURL := evaluation.BestURL
            log.Printf("[Dialogue] ✓ LLM selected URL (confidence: %.2f): %s", 
                evaluation.Confidence, truncate(bestURL, 60))
            log.Printf("[Dialogue] Selection reasoning: %s", truncate(evaluation.Reasoning, 100))
						// Store best URL in metadata
						if action.Metadata == nil {
							action.Metadata = make(map[string]interface{})
						}
						action.Metadata["best_url"] = bestURL
						action.Metadata["eval_confidence"] = evaluation.Confidence
						action.Metadata["eval_reasoning"] = evaluation.Reasoning
						action.Metadata["fallback_urls"] = evaluation.FallbackURLs
						
						// Check if there's already a pending parse action
						hasParseAction := false
						for j := i + 1; j < len(topGoal.Actions); j++ {
							nextAction := &topGoal.Actions[j]
							if (nextAction.Tool == ActionToolWebParseGeneral || 
							    nextAction.Tool == ActionToolWebParseContextual) &&
							   nextAction.Status == ActionStatusPending {
								if nextAction.Metadata == nil {
									nextAction.Metadata = make(map[string]interface{})
								}
								nextAction.Metadata["selected_url"] = bestURL
								nextAction.Description = bestURL
								log.Printf("[Dialogue] Updated next parse action with best URL: %s", truncate(bestURL, 60))
								hasParseAction = true
								break
							}
						}
						
						// If no parse action exists, create one NOW
						if !hasParseAction {
							var parseAction Action
							goalLower := strings.ToLower(topGoal.Description)
							
							if strings.Contains(goalLower, "research") ||
							   strings.Contains(goalLower, "analyze") ||
							   strings.Contains(goalLower, "understand") ||
							   strings.Contains(goalLower, "learn about") {
								purpose := fmt.Sprintf("Extract information relevant to: %s", topGoal.Description)
								parseAction = Action{
									Description: bestURL,
									Tool:        ActionToolWebParseContextual,
									Status:      ActionStatusPending,
									Timestamp:   time.Now(),
									Metadata:    map[string]interface{}{
										"purpose": purpose,
										"selected_url": bestURL,
									},
								}
								log.Printf("[Dialogue] Auto-created contextual parse action for best URL: %s", truncate(bestURL, 60))
							} else {
								parseAction = Action{
									Description: bestURL,
									Tool:        ActionToolWebParseGeneral,
									Status:      ActionStatusPending,
									Timestamp:   time.Now(),
									Metadata:    map[string]interface{}{
										"selected_url": bestURL,
									},
								}
								log.Printf("[Dialogue] Auto-created general parse action for best URL: %s", truncate(bestURL, 60))
							}
							
							topGoal.Actions = append(topGoal.Actions, parseAction)
							
							// CRITICAL: Update goal in state immediately
							for k := range state.ActiveGoals {
								if state.ActiveGoals[k].ID == topGoal.ID {
									state.ActiveGoals[k] = topGoal
									log.Printf("[Dialogue] ✓ Updated goal in state with new parse action")
									break
								}
							}
						}}
					}
				}
				if err != nil {
					log.Printf("[Dialogue] Action failed: %v", err)
					action.Result = fmt.Sprintf("ERROR: %v", err)
					action.Status = ActionStatusCompleted
					
					// Track failures - don't abandon on first failure
					topGoal.FailureCount++
					
					// Only mark as bad after 3+ consecutive failures
					if topGoal.FailureCount >= 3 {
						topGoal.Outcome = "bad"
						log.Printf("[Dialogue] ⚠ Goal marked as bad after %d consecutive failures", topGoal.FailureCount)
					} else {
						log.Printf("[Dialogue] ⚠ Action failed (failure %d/3), will retry", topGoal.FailureCount)
					}
					
					// Don't increment actionCount - this was a failure
					actionExecuted = true // Still counts as execution attempt

} else if action.Tool == ActionToolWebParseContextual || 
          action.Tool == ActionToolWebParseGeneral ||
          action.Tool == ActionToolWebParseChunked {
    // NEW: Parse evaluation for web parse actions
    log.Printf("[Dialogue] Parse action completed, evaluating quality...")
    
    // Get fallback URLs from metadata (set during search evaluation)
    var fallbackURLs []string
    var parsedURL string
    
    if action.Metadata != nil {
        if urls, ok := action.Metadata["fallback_urls"].([]string); ok {
            fallbackURLs = urls
        }
        if url, ok := action.Metadata["selected_url"].(string); ok {
            parsedURL = url
        }
    }
    
    if parsedURL == "" {
        parsedURL = action.Description
    }
    
    // Evaluate parse quality
    parseEval, evalErr := e.evaluateParseResults(
        ctx,
        result,
        topGoal.Description,
        parsedURL,
        fallbackURLs,
    )
    
    if evalErr != nil {
        log.Printf("[Dialogue] WARNING: Parse evaluation failed: %v", evalErr)
        // Continue with action as if it succeeded
        action.Result = result
        action.Status = ActionStatusCompleted
        actionCount++
        actionExecuted = true
    } else {
        // Store evaluation in metadata
        if action.Metadata == nil {
            action.Metadata = make(map[string]interface{})
        }
        action.Metadata["parse_quality"] = parseEval.Quality
        action.Metadata["parse_confidence"] = parseEval.Confidence
        action.Metadata["parse_reasoning"] = parseEval.Reasoning
        
        log.Printf("[Dialogue] ✓ Parse quality: %s (confidence: %.2f)", 
            parseEval.Quality, parseEval.Confidence)
        
        // Decision tree based on quality
        switch parseEval.Quality {
        case "sufficient":
            // Content is good, mark action complete
            action.Result = result
            action.Status = ActionStatusCompleted
            actionCount++
            actionExecuted = true
            log.Printf("[Dialogue] Parse result sufficient, continuing goal")
            
        case "try_fallback":
            // Content inadequate, try next fallback URL if available
            action.Result = fmt.Sprintf("Parse quality insufficient: %s", parseEval.Reasoning)
            action.Status = ActionStatusCompleted
            actionExecuted = true
            
            if len(fallbackURLs) > 0 {
                // Create new parse action with next fallback URL
                nextURL := fallbackURLs[0]
                remainingFallbacks := fallbackURLs[1:]
                
                log.Printf("[Dialogue] Trying fallback URL: %s", truncate(nextURL, 60))
                
                var fallbackAction Action
                if action.Tool == ActionToolWebParseContextual {
                    purpose := "Extract relevant information for goal"
                    if action.Metadata != nil {
                        if p, ok := action.Metadata["purpose"].(string); ok {
                            purpose = p
                        }
                    }
                    
                    fallbackAction = Action{
                        Description: nextURL,
                        Tool:        ActionToolWebParseContextual,
                        Status:      ActionStatusPending,
                        Timestamp:   time.Now(),
                        Metadata: map[string]interface{}{
                            "purpose":        purpose,
                            "selected_url":   nextURL,
                            "fallback_urls":  remainingFallbacks,
                            "is_fallback":    true,
                        },
                    }
                } else {
                    fallbackAction = Action{
                        Description: nextURL,
                        Tool:        ActionToolWebParseGeneral,
                        Status:      ActionStatusPending,
                        Timestamp:   time.Now(),
                        Metadata: map[string]interface{}{
                            "selected_url":  nextURL,
                            "fallback_urls": remainingFallbacks,
                            "is_fallback":   true,
                        },
                    }
                }
                
                topGoal.Actions = append(topGoal.Actions, fallbackAction)
                log.Printf("[Dialogue] ✓ Created fallback parse action (%d fallbacks remaining)", 
                    len(remainingFallbacks))
                
		// Update pending work status
		topGoal.HasPendingWork = hasPendingActions(&topGoal)
		
		// Update goal in state
		for i := range state.ActiveGoals {
			if state.ActiveGoals[i].ID == topGoal.ID {
				state.ActiveGoals[i] = topGoal
				log.Printf("[Dialogue] Updated goal in state: pending_work=%v", topGoal.HasPendingWork)
				break
			}
		}
            } else {
                log.Printf("[Dialogue] No fallback URLs available, marking goal as partial failure")
                topGoal.Outcome = "bad"
            }
            
        case "parse_deeper":
            // Content exists but extraction incomplete, try chunked parsing
            action.Result = fmt.Sprintf("Parse incomplete: %s", parseEval.Reasoning)
            action.Status = ActionStatusCompleted
            actionExecuted = true
            
            log.Printf("[Dialogue] Parse needs deeper extraction, creating chunked parse action")
            
            chunkedAction := Action{
                Description: parsedURL,
                Tool:        ActionToolWebParseChunked,
                Status:      ActionStatusPending,
                Timestamp:   time.Now(),
                Metadata: map[string]interface{}{
                    "selected_url": parsedURL,
                    "chunk_index":  0,
                },
            }
            
            topGoal.Actions = append(topGoal.Actions, chunkedAction)
            log.Printf("[Dialogue] ✓ Created chunked parse action for deeper extraction")
            
            // Update goal in state
            for k := range state.ActiveGoals {
                if state.ActiveGoals[k].ID == topGoal.ID {
                    state.ActiveGoals[k] = topGoal
                    break
                }
            }
            
        case "completely_failed":
            // Parse completely failed, try fallback or abandon
            action.Result = fmt.Sprintf("Parse failed: %s", parseEval.Reasoning)
            action.Status = ActionStatusCompleted
            actionExecuted = true
            
            if len(fallbackURLs) > 0 && parseEval.ShouldContinue {
                // Try fallback (same logic as try_fallback)
                nextURL := fallbackURLs[0]
                remainingFallbacks := fallbackURLs[1:]
                
                log.Printf("[Dialogue] Parse completely failed, trying fallback: %s", 
                    truncate(nextURL, 60))
                
                fallbackAction := Action{
                    Description: nextURL,
                    Tool:        action.Tool,
                    Status:      ActionStatusPending,
                    Timestamp:   time.Now(),
                    Metadata: map[string]interface{}{
                        "selected_url":  nextURL,
                        "fallback_urls": remainingFallbacks,
                        "is_fallback":   true,
                    },
                }
                
                topGoal.Actions = append(topGoal.Actions, fallbackAction)
                
                // Update goal in state
                for k := range state.ActiveGoals {
                    if state.ActiveGoals[k].ID == topGoal.ID {
                        state.ActiveGoals[k] = topGoal
                        break
                    }
                }
            } else {
                log.Printf("[Dialogue] Parse failed with no fallbacks, marking goal as bad")
                topGoal.Outcome = "bad"
            }
        }
    }
    
    action.Timestamp = time.Now()


} else {
	// Check if result ACTUALLY indicates failure (check prefix only)
	resultLower := strings.ToLower(result)
	resultPrefix := resultLower
	if len(resultPrefix) > 100 {
		resultPrefix = resultPrefix[:100]
	}
	
	if strings.HasPrefix(resultPrefix, "error:") || 
	   strings.HasPrefix(resultPrefix, "failed:") ||
	   strings.HasPrefix(resultPrefix, "timeout:") ||
	   strings.Contains(resultPrefix, "403") ||
	   strings.Contains(resultPrefix, "404") ||
	   strings.Contains(resultPrefix, "no suitable urls") {
		log.Printf("[Dialogue] Action completed but result indicates failure")
		action.Result = result
		action.Status = ActionStatusCompleted
		
		// Track failures - don't abandon on first failure
		topGoal.FailureCount++
		if topGoal.FailureCount >= 3 {
			topGoal.Outcome = "bad"
			log.Printf("[Dialogue] ⚠ Goal marked as bad after %d failures", topGoal.FailureCount)
		}
		actionExecuted = true
					} else {
						action.Result = result
						action.Status = ActionStatusCompleted
						actionCount++
						actionExecuted = true
						
						// Reset failure count on success
						if topGoal.FailureCount > 0 {
							log.Printf("[Dialogue] Action succeeded, resetting failure count (was %d)", topGoal.FailureCount)
							topGoal.FailureCount = 0
						}
						
						log.Printf("[Dialogue] Action completed successfully: %s", truncate(result, 80))
					}
					action.Timestamp = time.Now()
				}
				
				// Update research plan if this was part of a plan
				if topGoal.ResearchPlan != nil && action.Metadata != nil {
					if questionID, ok := action.Metadata["research_question_id"].(string); ok {
						if err := e.updateResearchProgress(ctx, &topGoal, questionID, result); err != nil {
							log.Printf("[Dialogue] WARNING: Failed to update research progress: %v", err)
						}
					}
				}
				
				// Only execute one action per cycle
				break
			}
		}
		
		// If no actions were executed, check if we should create new actions
if !actionExecuted {
// Check for stale in-progress actions with adaptive timeouts
now := time.Now()
for i := range topGoal.Actions {
	action := &topGoal.Actions[i]
	if action.Status == ActionStatusInProgress {
		age := now.Sub(action.Timestamp)
		
		// Adaptive timeout based on action type (accounting for slow CPU inference)
		timeout := 10 * time.Minute // Default: 10 minutes for search/general actions
		
		// Web parsing can take significantly longer due to LLM processing
		if action.Tool == ActionToolWebParseContextual || 
		   action.Tool == ActionToolWebParseGeneral ||
		   action.Tool == ActionToolWebParseChunked {
			timeout = 15 * time.Minute // 15 minutes for parse actions
		}
		
		if age > timeout {
			log.Printf("[Dialogue] Found stale in-progress action (age: %s, timeout: %s), marking as failed: %s",
				age.Round(time.Second), timeout, truncate(action.Description, 60))
			action.Status = ActionStatusCompleted
			action.Result = fmt.Sprintf("TIMEOUT: Action abandoned after %s (timeout: %s)", 
				age.Round(time.Second), timeout)
			topGoal.Outcome = "bad"
		}
	}
}
    
    // Check if all actions are completed (need to create more)
    hasPendingActions := false
    allActionsCompleted := len(topGoal.Actions) > 0
    for _, action := range topGoal.Actions {
        if action.Status == ActionStatusPending {
            hasPendingActions = true
            allActionsCompleted = false
            break
        }
        if action.Status == ActionStatusInProgress {
            allActionsCompleted = false
        }
    }
    
    // If we have pending actions, log and wait for next cycle to execute them
    if hasPendingActions {
        pendingCount := 0
        for _, action := range topGoal.Actions {
            if action.Status == ActionStatusPending {
                pendingCount++
            }
        }
        log.Printf("[Dialogue] Goal has %d pending actions, will execute in next cycle", pendingCount)
    }
    
    // Create new actions if: (1) no actions at all, OR (2) all actions completed AND no pending
    if len(topGoal.Actions) == 0 || (allActionsCompleted && !hasPendingActions) {
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
						
						// Determine if this goal needs a research plan
						desc := strings.ToLower(topGoal.Description)
						needsResearchPlan := strings.Contains(desc, "research") ||
											 strings.Contains(desc, "investigate") ||
											 strings.Contains(desc, "explore") ||
											 strings.Contains(desc, "analyze") ||
											 strings.Contains(desc, "understand")
						
if needsResearchPlan && topGoal.ResearchPlan == nil {
    // Generate multi-step research plan
    log.Printf("[Dialogue] Goal requires research plan, generating...")
    
    plan, planTokens, err := e.generateResearchPlan(ctx, &topGoal)
    if err != nil {
        log.Printf("[Dialogue] WARNING: Failed to generate research plan: %v", err)
        // Fallback to simple action
        searchQuery := topGoal.Description
        searchAction := Action{
            Description: searchQuery,
            Tool:        ActionToolSearch,
            Status:      ActionStatusPending,
            Timestamp:   time.Now(),
        }
        topGoal.Actions = append(topGoal.Actions, searchAction)
        
        // CRITICAL FIX: Update goal in state immediately
        for i := range state.ActiveGoals {
            if state.ActiveGoals[i].ID == topGoal.ID {
                state.ActiveGoals[i] = topGoal
                log.Printf("[Dialogue] ✓ Updated goal in state with %d actions (research plan fallback)", len(topGoal.Actions))
                break
            }
        }
    } else {
        topGoal.ResearchPlan = plan
        thoughtCount++
        totalTokens += planTokens
        
        log.Printf("[Dialogue] ✓ Research plan created: '%s' with %d questions",
            plan.RootQuestion, len(plan.SubQuestions))
        for i, q := range plan.SubQuestions {
            log.Printf("[Dialogue]   Q%d [%s]: %s (deps: %v)",
                i+1, q.ID, truncate(q.Question, 60), q.Dependencies)
        }
        
        // ✅ CRITICAL FIX: Create first action from research plan immediately
        firstAction := e.getNextResearchAction(ctx, &topGoal)
        if firstAction != nil {
            topGoal.Actions = append(topGoal.Actions, *firstAction)
            topGoal.HasPendingWork = true
            
            log.Printf("[Dialogue] ✓ Created first research action: %s", 
                truncate(firstAction.Description, 60))
            
            // Update goal in state immediately
            for i := range state.ActiveGoals {
                if state.ActiveGoals[i].ID == topGoal.ID {
                    state.ActiveGoals[i] = topGoal
                    log.Printf("[Dialogue] ✓ Updated goal in state with first action (pending_work=true)")
                    break
                }
            }
        } else {
            log.Printf("[Dialogue] WARNING: Research plan created but getNextResearchAction returned nil")
        }
    }
						} else if topGoal.ResearchPlan != nil {
							// Execute next step of existing research plan
							log.Printf("[Dialogue] Executing research plan step %d/%d",
								topGoal.ResearchPlan.CurrentStep+1, len(topGoal.ResearchPlan.SubQuestions))
							
							nextAction := e.getNextResearchAction(ctx, &topGoal)
							if nextAction != nil {
								topGoal.Actions = append(topGoal.Actions, *nextAction)
								log.Printf("[Dialogue] ✓ Created action: %s", nextAction.Description)
								
								// CRITICAL FIX: Update goal in state immediately
								for i := range state.ActiveGoals {
									if state.ActiveGoals[i].ID == topGoal.ID {
										state.ActiveGoals[i] = topGoal
										log.Printf("[Dialogue] ✓ Updated goal in state with %d actions", len(topGoal.Actions))
										break
									}
								}
							} else {
								// All questions complete, create synthesis action
								topGoal.ResearchPlan.SynthesisNeeded = true
								synthesisAction := Action{
									Description: "Synthesize research findings",
									Tool:        ActionToolSynthesis,
									Status:      ActionStatusPending,
									Timestamp:   time.Now(),
								}
								topGoal.Actions = append(topGoal.Actions, synthesisAction)
								log.Printf("[Dialogue] ✓ Research complete, synthesis action created")
							}
						} else {
							// Simple goal without research plan - extract keywords
							searchQuery := e.extractSearchKeywords(topGoal.Description)
							
							searchAction := Action{
								Description: searchQuery,
								Tool:        ActionToolSearch,
								Status:      ActionStatusPending,
								Timestamp:   time.Now(),
							}
							topGoal.Actions = append(topGoal.Actions, searchAction)
							log.Printf("[Dialogue] Created simple search action with keywords: %s", truncate(searchQuery, 60))
							
							// Parse action will be created automatically after search completes
							log.Printf("[Dialogue] Parse action will be created after search returns URLs")
						}
					}
					
					// CRITICAL FIX: Update goal in state immediately after creating actions
					for i := range state.ActiveGoals {
						if state.ActiveGoals[i].ID == topGoal.ID {
							state.ActiveGoals[i] = topGoal
							log.Printf("[Dialogue] ✓ Updated goal in state with %d actions", len(topGoal.Actions))
							break
						}
					}
} else {
    // Goal has pending actions - log and wait for next cycle
    pendingCount := 0
    completedCount := 0
    for _, action := range topGoal.Actions {
        if action.Status == ActionStatusPending {
            pendingCount++
        }
        if action.Status == ActionStatusCompleted {
            completedCount++
        }
    }
    log.Printf("[Dialogue] DEBUG: Goal '%s' has %d total actions (%d pending, %d completed)", 
        truncate(topGoal.Description, 40), len(topGoal.Actions), pendingCount, completedCount)
}
			}
		
// Update goal progress based on completed actions
completedActions := 0
successfulActions := 0
totalActions := len(topGoal.Actions)

for _, action := range topGoal.Actions {
	if action.Status == ActionStatusCompleted {
		completedActions++
		// Check if action actually succeeded (not just completed with error)
		if !strings.Contains(strings.ToLower(action.Result), "error") &&
		   !strings.Contains(strings.ToLower(action.Result), "failed") &&
		   !strings.Contains(strings.ToLower(action.Result), "timeout") {
			successfulActions++
		}
	}
}

// Only calculate progress if we have actions
if totalActions > 0 {
	topGoal.Progress = float64(completedActions) / float64(totalActions)
} else {
	// No actions yet, no progress
	topGoal.Progress = 0.0
}

// Don't mark complete if:
// 1. Last action was a search (parse action might be pending/upcoming)
// 2. We have pending actions that haven't executed yet
hasPendingActions := false
lastActionWasSearch := false

if totalActions > 0 {
	lastAction := topGoal.Actions[totalActions-1]
	if lastAction.Tool == ActionToolSearch && lastAction.Status == ActionStatusCompleted {
		lastActionWasSearch = true
	}
}

for _, action := range topGoal.Actions {
	if action.Status == ActionStatusPending {
		hasPendingActions = true
		break
	}
}

if lastActionWasSearch && hasPendingActions {
	log.Printf("[Dialogue] Search completed with pending parse action, not marking goal complete yet")
	topGoal.Progress = 0.99 // Almost complete
}
		
if topGoal.Progress >= 1.0 && !hasPendingActions {
	// Validate that the goal actually achieved something useful
	hasUsefulOutcome := false
	hasFailures := false
	totalOutputLength := 0
	
	for _, action := range topGoal.Actions {
		if action.Status == ActionStatusCompleted {
			resultLower := strings.ToLower(action.Result)
			
			// Check if action ACTUALLY failed (not just mentioned errors in parsed content)
			// Only check first 100 chars where real error messages appear
			// This prevents false positives from phrases like "error handling" in parsed articles
			resultPrefix := resultLower
			if len(resultPrefix) > 100 {
				resultPrefix = resultPrefix[:100]
			}
			
			// Only mark as failure if error appears at the START of the result
			if strings.HasPrefix(resultPrefix, "error:") ||
			   strings.HasPrefix(resultPrefix, "failed:") ||
			   strings.HasPrefix(resultPrefix, "timeout:") ||
			   strings.Contains(resultPrefix, "no suitable urls") ||
			   strings.Contains(resultPrefix, "parse failed:") ||
			   strings.Contains(resultPrefix, "action failed:") {
				hasFailures = true
				log.Printf("[Dialogue] Detected actual failure in action result: %s", 
					truncate(action.Result, 80))
			}
			
			// Accumulate output length
			totalOutputLength += len(action.Result)
			
			// Check if action produced meaningful output
			// - Parse actions should produce >200 chars
			// - Search actions should produce >100 chars
			minLength := 100
			if action.Tool == ActionToolWebParseContextual || 
			   action.Tool == ActionToolWebParseGeneral {
				minLength = 200
			}
			
			if len(action.Result) > minLength && !strings.HasPrefix(resultPrefix, "error:") {
				hasUsefulOutcome = true
			}
		}
	}
	
	log.Printf("[Dialogue] Goal evaluation: useful=%v, failures=%v, totalOutput=%d chars", 
		hasUsefulOutcome, hasFailures, totalOutputLength)
	
	// Mark goal based on outcome quality
	if hasUsefulOutcome && !hasFailures {
		// Synthesize research findings if this goal had a research plan
		if topGoal.ResearchPlan != nil && topGoal.ResearchPlan.SynthesisNeeded {
			log.Printf("[Dialogue] Synthesizing research findings...")
			
			synthesis, synthesisTokens, err := e.synthesizeResearchFindings(ctx, &topGoal)
			totalTokens += synthesisTokens
			
			if err != nil {
				log.Printf("[Dialogue] WARNING: Synthesis failed: %v", err)
			} else {
				// Store synthesis as collective memory
				if err := e.storeResearchSynthesis(ctx, &topGoal, synthesis); err != nil {
					log.Printf("[Dialogue] WARNING: Failed to store synthesis: %v", err)
				} else {
					log.Printf("[Dialogue] ✓ Synthesis stored (%d chars): %s",
						len(synthesis), truncate(synthesis, 100))
				}
			}
		}
		
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
		} else if goal.Progress == 0.0 && time.Since(goal.Created) > 48*time.Hour {
			// Abandon goals with no progress after 48 hours (increased from 24h)
			goal.Status = GoalStatusAbandoned
			goal.Outcome = "neutral"
			state.CompletedGoals = append(state.CompletedGoals, goal)
			abandonedCount++
			log.Printf("[Dialogue] Auto-abandoned stale goal (48h+ with no progress): %s", truncate(goal.Description, 60))
		} else if goal.Progress > 0.0 && goal.Progress < 1.0 && time.Since(goal.Created) > 7*24*time.Hour {
			// Abandon goals stuck in progress for over a week
			goal.Status = GoalStatusAbandoned
			goal.Outcome = "neutral"
			state.CompletedGoals = append(state.CompletedGoals, goal)
			abandonedCount++
			log.Printf("[Dialogue] Auto-abandoned stuck goal (7d+ with partial progress %.0f%%): %s", 
				goal.Progress*100, truncate(goal.Description, 60))
		} else if len(goal.Actions) > 10 && goal.Progress < 0.5 {
			// Abandon goals with many failed actions (>10) but low progress
			failedActions := 0
			for _, action := range goal.Actions {
				if action.Status == ActionStatusCompleted {
					resultLower := strings.ToLower(action.Result)
					if strings.Contains(resultLower, "error") || 
					   strings.Contains(resultLower, "failed") ||
					   strings.Contains(resultLower, "timeout") {
						failedActions++
					}
				}
			}
			
			if failedActions >= 5 {
				goal.Status = GoalStatusAbandoned
				goal.Outcome = "bad"
				state.CompletedGoals = append(state.CompletedGoals, goal)
				abandonedCount++
				log.Printf("[Dialogue] Auto-abandoned goal with too many failures (%d failed actions): %s", 
					failedActions, truncate(goal.Description, 60))
			} else {
				activeGoals = append(activeGoals, goal)
			}
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
		
		gapLower := strings.ToLower(gap)
		
		// Higher priority for explicit user requests
		if strings.Contains(gapLower, "research") ||
		   strings.Contains(gapLower, "think about") ||
		   strings.Contains(gapLower, "choose") ||
		   strings.Contains(gapLower, "select") {
			description = gap
			priority = 9 // Very high priority for explicit research requests
		} else if strings.Contains(gapLower, "learn") ||
		          strings.Contains(gapLower, "understand") ||
		          strings.Contains(gapLower, "explore") {
			description = gap
			priority = 8 // High priority for learning goals
		} else {
			description = fmt.Sprintf("Learn about: %s", gap)
			priority = 7 // Standard priority
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

	// Keyword overlap detection (high precision, catches semantic duplicates)
	proposalKeywords := e.extractSignificantKeywords(proposalDesc)
	for _, existingGoal := range existingGoals {
		existingKeywords := e.extractSignificantKeywords(existingGoal.Description)
		
		// Calculate keyword overlap ratio
		overlap := e.calculateKeywordOverlap(proposalKeywords, existingKeywords)
		
		// If 80%+ keywords overlap, it's likely a duplicate (raised from 60% to allow more diversity)
		if overlap >= 0.80 {
			log.Printf("[Dialogue] Duplicate detected (keyword overlap %.0f%%): '%s' ~= '%s'",
				overlap*100, truncate(proposalDesc, 40), truncate(existingGoal.Description, 40))
			log.Printf("[Dialogue]   Proposal keywords: %v", proposalKeywords)
			log.Printf("[Dialogue]   Existing keywords: %v", existingKeywords)
			return true
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
			
			// Use adaptive threshold for duplicate detection (bounded between 0.70-0.80)
			threshold := e.adaptiveConfig.GetGoalSimilarityThreshold()
			// Cap threshold at 0.80 to allow reasonable variation (was 0.75)
			if threshold > 0.80 {
				threshold = 0.80
			}
			// But never go below 0.70 to still catch obvious duplicates
			if threshold < 0.70 {
				threshold = 0.70
			}
			
			log.Printf("[Dialogue] Using semantic similarity threshold: %.2f (adaptive: %.2f)", 
				threshold, e.adaptiveConfig.GetGoalSimilarityThreshold())
			
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
		
		// Common meta-loop topics (narrowed definition)
		if strings.Contains(desc, "memory system") || 
		   strings.Contains(desc, "meta-memory") ||
		   strings.Contains(desc, "self-awareness") {
			topicCounts["meta-memory"]++
		}
		if strings.Contains(desc, "learn about learning") || 
		   strings.Contains(desc, "meta-learning") {
			topicCounts["meta-learning"]++
		}
	}
	
	// If 4+ of last 5 goals are about the same meta topic, it's a loop (increased threshold)
	for topic, count := range topicCounts {
		if count >= 4 {
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
	// If queue client is available, use it
	if e.llmClient != nil {
		// Type assertion (safe because we control initialization)
		type LLMCaller interface {
			Call(ctx context.Context, url string, payload map[string]interface{}) ([]byte, error)
		}
		
		if client, ok := e.llmClient.(LLMCaller); ok {
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
			
			log.Printf("[Dialogue] LLM call via queue (prompt length: %d chars)", len(prompt))
			startTime := time.Now()
			
			body, err := client.Call(ctx, e.llmURL, reqBody)
			if err != nil {
				log.Printf("[Dialogue] LLM queue call failed after %s: %v", time.Since(startTime), err)
				return "", 0, fmt.Errorf("LLM call failed: %w", err)
			}
			
			log.Printf("[Dialogue] LLM queue response received in %s", time.Since(startTime))
			
			// Parse response
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
			
			if err := json.Unmarshal(body, &result); err != nil {
				return "", 0, fmt.Errorf("failed to decode response: %w", err)
			}
			
			if len(result.Choices) == 0 {
				return "", 0, fmt.Errorf("no choices returned from LLM")
			}
			
			content := strings.TrimSpace(result.Choices[0].Message.Content)
			tokens := result.Usage.TotalTokens
			
			return content, tokens, nil
		}
	}
	
	// Queue client is REQUIRED for dialogue
	log.Printf("[Dialogue] ERROR: LLM queue client not available")
	return "", 0, fmt.Errorf("LLM queue client required for dialogue")
}


func (e *Engine) callLLMWithStructuredReasoning(ctx context.Context, prompt string, expectJSON bool) (*ReasoningResponse, int, error) {
    systemPrompt := `Output ONLY S-expressions (Lisp-style). No Markdown.
Format: (reasoning (reflection "...") (insights "...") (goals_to_create (goal (description "...") (priority 7))))
Example: (reasoning (reflection "Good session") (insights "Learned X") (goals_to_create (goal (description "Do Y") (priority 8))))

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
	
	// Use queue if available
	if e.llmClient != nil {
		type LLMCaller interface {
			Call(ctx context.Context, url string, payload map[string]interface{}) ([]byte, error)
		}
		
		if client, ok := e.llmClient.(LLMCaller); ok {
			log.Printf("[Dialogue] Structured reasoning LLM call via queue (prompt length: %d chars)", len(prompt))
			startTime := time.Now()
			
			body, err := client.Call(ctx, e.llmURL, reqBody)
			if err != nil {
				log.Printf("[Dialogue] Structured reasoning queue call failed after %s: %v", time.Since(startTime), err)
				return nil, 0, fmt.Errorf("LLM call failed: %w", err)
			}
			
			log.Printf("[Dialogue] Structured reasoning response received in %s", time.Since(startTime))
			
			// Parse LLM response wrapper
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
			
			if err := json.Unmarshal(body, &result); err != nil {
				return nil, 0, fmt.Errorf("failed to decode response: %w", err)
			}
			
			if len(result.Choices) == 0 {
				return nil, 0, fmt.Errorf("no choices returned from LLM")
			}
			
			content := strings.TrimSpace(result.Choices[0].Message.Content)
			tokens := result.Usage.TotalTokens
			
            // Parse S-expression with automatic repair
            reasoning, err := ParseReasoningSExpr(content)
            
            // Store raw response for custom parsing (e.g., Research Plans)
            reasoning.RawResponse = content
            
            if err != nil {
                log.Printf("[Dialogue] WARNING: Failed to parse S-expression reasoning: %v", err)
                log.Printf("[Dialogue] Raw response (first 500 chars): %s", truncateResponse(content, 500))
                
                // Fallback mode
                return &ReasoningResponse{
                    Reflection:  "Failed to parse structured reasoning. Using fallback mode.",
                    RawResponse: content, // Preserve raw content
                    Insights:    []string{},
                }, tokens, nil
            }
            
            log.Printf("[Dialogue] ✓ Successfully parsed S-expression reasoning")
            return reasoning, tokens, nil
		}
	}
	
	// Queue client is REQUIRED
	log.Printf("[Dialogue] ERROR: LLM queue client not available for structured reasoning")
	return nil, 0, fmt.Errorf("LLM queue client required for structured reasoning")
}

// generateResearchPlan creates a structured multi-step investigation plan
func (e *Engine) generateResearchPlan(ctx context.Context, goal *Goal) (*ResearchPlan, int, error) {
	prompt := fmt.Sprintf(`Create a thorough research plan to investigate this goal.

Goal: %s

Break this into 3-7 logical sub-questions that:
1. Start with foundational understanding
2. Build progressively to specific details
3. Include verification/cross-checking

REQUIRED FORMAT - Respond with S-expression in this exact structure:

(research_plan
  (root_question "Main question being answered")
  (sub_questions
    (question
      (id "q1")
      (text "First foundational question")
      (search_query "suggested search terms")
      (priority 10)
      (deps ()))
    (question
      (id "q2")
      (text "Follow-up building on q1")
      (search_query "more specific search terms")
      (priority 9)
      (deps ("q1")))))

IMPORTANT RULES:
- Output the research_plan block directly (wrapping in (reasoning ...) is acceptable)
- Use underscores: research_plan, root_question, sub_questions, search_query
- IDs must be unique: "q1", "q2", "q3", etc.
- deps is a list: use () for no deps, ("q1") for one dep, ("q1" "q2") for multiple
- Keep plan achievable (3-7 questions max)

EXAMPLE (correct format):
(research_plan
  (root_question "How do chatbots maintain context?")
  (sub_questions
    (question
      (id "q1")
      (text "What is conversational context?")
      (search_query "conversational context definition chatbots")
      (priority 10)
      (deps ()))
    (question
      (id "q2")
      (text "What techniques store conversation history?")
      (search_query "conversation history storage techniques AI")
      (priority 9)
      (deps ("q1")))
    (question
      (id "q3")
      (text "How do modern systems implement context windows?")
      (search_query "context window implementation modern chatbots")
      (priority 8)
      (deps ("q1" "q2")))))`, 
        goal.Description)

	response, tokens, err := e.callLLMWithStructuredReasoning(ctx, prompt, true)
	if err != nil {
		return nil, tokens, fmt.Errorf("LLM call failed: %w", err)
	}
	
    // Parse S-expression response
    // Use RawResponse because Reflection might be generic fallback text
    content := response.RawResponse
    
    // DEBUG LOGGING: Always log what we received
    log.Printf("[Dialogue] Research plan response length: %d chars", len(content))
    log.Printf("[Dialogue] Research plan response (first 300 chars): %s", truncateResponse(content, 300))
    
    // Clean up markdown fences
    content = strings.TrimPrefix(content, "```lisp")
    content = strings.TrimPrefix(content, "```")
    content = strings.TrimSuffix(content, "```")
    content = strings.TrimSpace(content)
    
    // IMPROVED PARSING: Use recursive search first (most robust)
    
    // Strategy 1: Recursive search (handles nested structures like (reasoning (research_plan ...)))
    planBlocks := findBlocksRecursive(content, "research_plan")
    
    // Strategy 2: Direct search as fallback
    if len(planBlocks) == 0 {
        log.Printf("[Dialogue] Recursive search failed, trying direct search...")
        planBlocks = findBlocks(content, "research_plan")
    }
    
    // Strategy 3: Regex fallback (handles malformed S-expressions)
    if len(planBlocks) == 0 {
        log.Printf("[Dialogue] Structured parsing failed, attempting regex extraction...")
        plan, err := extractResearchPlanFromMalformed(content)
        if err == nil && plan != nil {
            log.Printf("[Dialogue] ✓ Extracted research plan via regex fallback (%d questions)", len(plan.SubQuestions))
            return plan, tokens, nil
        }
        log.Printf("[Dialogue] Regex extraction also failed: %v", err)
    }
    
    // If all strategies failed, provide detailed error
    if len(planBlocks) == 0 {
        log.Printf("[Dialogue] ERROR: All parsing strategies failed")
        log.Printf("[Dialogue] Content structure analysis:")
        log.Printf("[Dialogue]   - Contains '(reasoning': %v", strings.Contains(content, "(reasoning"))
        log.Printf("[Dialogue]   - Contains '(reflection': %v", strings.Contains(content, "(reflection"))
        log.Printf("[Dialogue]   - Contains '(research_plan': %v", strings.Contains(content, "(research_plan"))
        log.Printf("[Dialogue]   - Contains '(research-plan': %v", strings.Contains(content, "(research-plan"))
        log.Printf("[Dialogue]   - Contains '(question': %v", strings.Contains(content, "(question"))
        log.Printf("[Dialogue] Full content: %s", content)
        
        return nil, tokens, fmt.Errorf("no research_plan block found in S-expression after trying all parsing strategies")
    }
    
    log.Printf("[Dialogue] ✓ Found research_plan block using structured parsing")

    // Extract Root Question
    rootQuestion := extractFieldContent(planBlocks[0], "root_question")
    
    // Extract Sub Questions
    questionBlocks := findBlocks(planBlocks[0], "question")
    if len(questionBlocks) == 0 {
        return nil, tokens, fmt.Errorf("no question blocks found in research plan")
    }

    if len(questionBlocks) > 10 {
        questionBlocks = questionBlocks[:10]
    }

    // Convert to internal ResearchPlan
    plan := &ResearchPlan{
        RootQuestion:    rootQuestion,
        SubQuestions:    make([]ResearchQuestion, len(questionBlocks)),
        CurrentStep:     0,
        SynthesisNeeded: false,
        CreatedAt:       time.Now(),
        UpdatedAt:       time.Now(),
    }

    for i, qBlock := range questionBlocks {
        // Helper to extract integer fields safely
        getInt := func(field string) int {
            val := extractFieldContent(qBlock, field)
            if val == "" {
                return 0
            }
            if p, err := strconv.Atoi(val); err == nil {
                return p
            }
            return 0
        }

        // Helper to extract dependencies list (deps ("q1" "q2"))
        getDeps := func(field string) []string {
            pattern := "(" + field + " "
            start := strings.Index(qBlock, pattern)
            if start == -1 {
                return []string{}
            }
            start += len(pattern)
            
            // Handle empty list ()
            if start < len(qBlock) && qBlock[start] == ')' {
                return []string{}
            }

            // Naive extraction of quoted strings until closing )
            var deps []string
            rest := qBlock[start:]
            for {
                qStart := strings.Index(rest, `"`)
                if qStart == -1 {
                    break
                }
                qEnd := strings.Index(rest[qStart+1:], `"`)
                if qEnd == -1 {
                    break
                }
                deps = append(deps, rest[qStart+1:qStart+1+qEnd])
                rest = rest[qStart+1+qEnd+1:]
                if strings.HasPrefix(rest, ")") {
                    break
                }
            }
            return deps
        }

        plan.SubQuestions[i] = ResearchQuestion{
            ID:              extractFieldContent(qBlock, "id"),
            Question:        extractFieldContent(qBlock, "text"),
            SearchQuery:     extractFieldContent(qBlock, "search_query"),
            Priority:        getInt("priority"),
            Dependencies:    getDeps("deps"),
            Status:          ResearchStatusPending,
            SourcesFound:    []string{},
            KeyFindings:     "",
            ConfidenceLevel: 0.0,
        }
    }
    
    return plan, tokens, nil
}

// getNextResearchAction determines next action from research plan
func (e *Engine) getNextResearchAction(ctx context.Context, goal *Goal) *Action {
	plan := goal.ResearchPlan
	if plan == nil {
		return nil
	}
	
	// Find next pending question (respecting dependencies)
	var nextQuestion *ResearchQuestion
	
	for i := range plan.SubQuestions {
		q := &plan.SubQuestions[i]
		
		if q.Status != ResearchStatusPending {
			continue
		}
		
		// Check dependencies
		dependenciesMet := true
		for _, depID := range q.Dependencies {
			for _, dq := range plan.SubQuestions {
				if dq.ID == depID && dq.Status != ResearchStatusCompleted {
					dependenciesMet = false
					break
				}
			}
			if !dependenciesMet {
				break
			}
		}
		
		if dependenciesMet {
			nextQuestion = q
			break
		}
	}
	
	if nextQuestion == nil {
		return nil // No questions available
	}
	
	// Create search action
	return &Action{
		Description: nextQuestion.SearchQuery,
		Tool:        ActionToolSearch,
		Status:      ActionStatusPending,
		Timestamp:   time.Now(),
		Metadata: map[string]interface{}{
			"research_question_id": nextQuestion.ID,
			"question_text":        nextQuestion.Question,
		},
	}
}

// updateResearchProgress records findings from completed action
func (e *Engine) updateResearchProgress(ctx context.Context, goal *Goal, questionID string, actionResult string) error {
	plan := goal.ResearchPlan
	if plan == nil {
		return fmt.Errorf("no research plan")
	}
	
	// Find question
	var question *ResearchQuestion
	for i := range plan.SubQuestions {
		if plan.SubQuestions[i].ID == questionID {
			question = &plan.SubQuestions[i]
			break
		}
	}
	
	if question == nil {
		return fmt.Errorf("question %s not found", questionID)
	}
	
	// Extract findings using simple heuristics (lightweight, no LLM)
	// Take first 200 chars as key finding
	findings := actionResult
	if len(findings) > 200 {
		findings = findings[:200] + "..."
	}
	
	question.KeyFindings = findings
	question.ConfidenceLevel = 0.7 // Default confidence
	question.Status = ResearchStatusCompleted
	plan.UpdatedAt = time.Now()
	
	log.Printf("[Dialogue] ✓ Question '%s' complete: %s", questionID, truncate(findings, 80))
	
	return nil
}

// synthesizeResearchFindings combines all findings into coherent knowledge
func (e *Engine) synthesizeResearchFindings(ctx context.Context, goal *Goal) (string, int, error) {
	plan := goal.ResearchPlan
	if plan == nil {
		return "", 0, fmt.Errorf("no research plan")
	}
	
	// Build context from completed questions
	var findingsBuilder strings.Builder
	findingsBuilder.WriteString(fmt.Sprintf("Research: %s\n\n", plan.RootQuestion))
	
	completedCount := 0
	for i, q := range plan.SubQuestions {
		if q.Status == ResearchStatusCompleted && q.KeyFindings != "" {
			completedCount++
			findingsBuilder.WriteString(fmt.Sprintf("Q%d: %s\n", i+1, q.Question))
			findingsBuilder.WriteString(fmt.Sprintf("A%d: %s\n\n", i+1, q.KeyFindings))
		}
	}
	
	if completedCount == 0 {
		return "", 0, fmt.Errorf("no completed questions to synthesize")
	}
	
	prompt := fmt.Sprintf(`Synthesize these research findings into a coherent summary.

%s

Create a comprehensive synthesis (3-5 paragraphs) that:
1. Directly answers the root question
2. Integrates all findings logically
3. Notes any gaps or uncertainties
4. Provides actionable insights

Write synthesis as plain text (no JSON, no markdown):`, findingsBuilder.String())

	synthesis, tokens, err := e.callLLM(ctx, prompt)
	if err != nil {
		return "", tokens, fmt.Errorf("synthesis failed: %w", err)
	}
	
	return synthesis, tokens, nil
}

// storeResearchSynthesis saves synthesis as high-value collective memory
func (e *Engine) storeResearchSynthesis(ctx context.Context, goal *Goal, synthesis string) error {
	content := fmt.Sprintf("Research: %s\n\nFindings:\n%s",
		goal.ResearchPlan.RootQuestion, synthesis)
	
	embedding, err := e.embedder.Embed(ctx, content)
	if err != nil {
		return fmt.Errorf("failed to embed: %w", err)
	}
	
	// Extract concept tags from questions
	conceptTags := []string{"research", "synthesis"}
	for _, q := range goal.ResearchPlan.SubQuestions {
		words := strings.Fields(q.Question)
		if len(words) > 0 {
			tag := strings.ToLower(strings.Trim(words[0], "?,.!"))
			if len(tag) > 3 && len(tag) < 20 {
				conceptTags = append(conceptTags, tag)
			}
		}
	}
	if len(conceptTags) > 5 {
		conceptTags = conceptTags[:5]
	}
	
	mem := &memory.Memory{
		Content:         content,
		Tier:            memory.TierRecent,
		IsCollective:    true,
		CreatedAt:       time.Now(),
		LastAccessedAt:  time.Now(),
		ImportanceScore: 0.9,
		Embedding:       embedding,
		OutcomeTag:      "good",
		TrustScore:      0.8,
		ValidationCount: len(goal.ResearchPlan.SubQuestions),
		ConceptTags:     conceptTags,
		Metadata: map[string]interface{}{
			"goal_id":       goal.ID,
			"research_type": "synthesis",
		},
	}
	
	return e.storage.Store(ctx, mem)
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
	
	// First priority: check if search evaluation selected a best URL
	if action.Metadata != nil {
		if selectedURL, ok := action.Metadata["selected_url"].(string); ok && selectedURL != "" {
			url = selectedURL
			log.Printf("[Dialogue] Using evaluated best URL: %s", truncate(url, 60))
		} else if bestURL, ok := action.Metadata["best_url"].(string); ok && bestURL != "" {
			url = bestURL
			log.Printf("[Dialogue] Using best URL from metadata: %s", truncate(url, 60))
		} else if urls, ok := action.Metadata["previous_search_urls"].([]string); ok && len(urls) > 0 {
			// Fallback: use first URL (old behavior)
			url = urls[0]
			log.Printf("[Dialogue] WARNING: Using first URL from search results (evaluation may have failed): %s", truncate(url, 60))
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
		log.Printf("[Dialogue] Memory consolidation completed in %s", time.Since(startTime))
		return "Memory consolidation completed", nil
	
	case ActionToolSynthesis:
		// Synthesis happens in goal completion phase, not here
		log.Printf("[Dialogue] Synthesis action marked (will execute on goal completion)")
		return "Synthesis ready", nil
		
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

// hasPendingActions checks if a goal has any pending actions
func hasPendingActions(goal *Goal) bool {
	for _, action := range goal.Actions {
		if action.Status == ActionStatusPending {
			return true
		}
	}
	return false
}

// getPrimaryGoals filters goals by primary tier
func (e *Engine) getPrimaryGoals(goals []Goal) []Goal {
	primaries := []Goal{}
	for _, goal := range goals {
		if goal.Tier == "primary" {
			primaries = append(primaries, goal)
		}
	}
	return primaries
}

// validateGoalSupport uses LLM to validate if a secondary goal supports a primary goal
func (e *Engine) validateGoalSupport(ctx context.Context, secondary *Goal, primaryGoals []Goal) (*GoalSupportValidation, error) {
	if len(primaryGoals) == 0 {
		return nil, fmt.Errorf("no primary goals to validate against")
	}
	
	// Build context about primary goals
	var primaryContext strings.Builder
	primaryContext.WriteString("CURRENT PRIMARY GOALS:\n")
	for i, primary := range primaryGoals {
		primaryContext.WriteString(fmt.Sprintf("%d. [ID: %s] %s\n", i+1, primary.ID, primary.Description))
	}
	
	prompt := fmt.Sprintf(`Evaluate if this SECONDARY goal meaningfully supports at least one PRIMARY goal.

%s

SECONDARY GOAL TO EVALUATE:
%s

CRITICAL: A secondary goal "supports" a primary goal if completing the secondary goal:
1. Directly advances progress toward the primary goal
2. Provides knowledge/skills needed for the primary goal
3. Creates resources/artifacts used by the primary goal
4. Removes blockers preventing progress on the primary goal

Respond ONLY with this S-expression:

(goal_support_validation
  (supports_goal_id "goal_xxx")  ; ID of primary goal being supported, or "" if none
  (confidence 0.85)  ; 0.0-1.0 confidence in linkage
  (reasoning "Specific explanation of how secondary supports primary")
  (is_valid true))  ; false if secondary doesn't meaningfully support any primary

RULES:
- If secondary supports NO primary goals, set is_valid to false
- If secondary supports multiple primaries, pick the strongest linkage
- Be strict: only validate true if linkage is clear and meaningful
- Output ONLY the S-expression, no markdown`, 
		primaryContext.String(), secondary.Description)
	
	log.Printf("[GoalValidation] Validating secondary goal linkage via LLM...")
	response, tokens, err := e.callLLMWithStructuredReasoning(ctx, prompt, false)
	if err != nil {
		return nil, fmt.Errorf("LLM validation failed: %w", err)
	}
	
	log.Printf("[GoalValidation] LLM validation completed (%d tokens)", tokens)
	
	// Parse S-expression response
	validation, err := e.parseGoalSupportValidation(response.RawResponse)
	if err != nil {
		log.Printf("[GoalValidation] Failed to parse validation: %v", err)
		return nil, err
	}
	
	return validation, nil
}

// parseGoalSupportValidation extracts validation from S-expression
func (e *Engine) parseGoalSupportValidation(rawResponse string) (*GoalSupportValidation, error) {
	content := strings.TrimSpace(rawResponse)
	content = strings.TrimPrefix(content, "```lisp")
	content = strings.TrimPrefix(content, "```")
	content = strings.TrimSuffix(content, "```")
	content = strings.TrimSpace(content)
	
	// Find goal_support_validation block
	blocks := findBlocksRecursive(content, "goal_support_validation")
	if len(blocks) == 0 {
		blocks = findBlocksRecursive(content, "goal-support-validation")
	}
	
	if len(blocks) == 0 {
		return nil, fmt.Errorf("no goal_support_validation block found")
	}
	
	block := blocks[0]
	
	validation := &GoalSupportValidation{
		Confidence: 0.5, // Default
		IsValid:    false,
	}
	
	// Extract fields
	if goalID := extractFieldContent(block, "supports_goal_id"); goalID != "" {
		validation.SupportsGoalID = goalID
	} else if goalID := extractFieldContent(block, "supports-goal-id"); goalID != "" {
		validation.SupportsGoalID = goalID
	}
	
	if reasoning := extractFieldContent(block, "reasoning"); reasoning != "" {
		validation.Reasoning = reasoning
	}
	
	if confStr := extractFieldContent(block, "confidence"); confStr != "" {
		if conf, err := parseFloat(confStr); err == nil {
			validation.Confidence = conf
		}
	}
	
	if validStr := extractFieldContent(block, "is_valid"); validStr != "" {
		validation.IsValid = (validStr == "true" || validStr == "t")
	} else if validStr := extractFieldContent(block, "is-valid"); validStr != "" {
		validation.IsValid = (validStr == "true" || validStr == "t")
	}
	
	// Validation: if is_valid is true, must have a goal ID
	if validation.IsValid && validation.SupportsGoalID == "" {
		return nil, fmt.Errorf("is_valid=true but no supports_goal_id specified")
	}
	
	return validation, nil
}

// determineGoalTier assigns a tier based on goal characteristics
func (e *Engine) determineGoalTier(description string, priority int, reasoning string) string {
	descLower := strings.ToLower(description)
	reasoningLower := strings.ToLower(reasoning)
	
	// PRIMARY tier indicators:
	// - Very high priority (9-10)
	// - User-aligned or identity-related
	// - Long-term strategic goals
	if priority >= 9 {
		return "primary"
	}
	
	if strings.Contains(descLower, "develop") && 
	   (strings.Contains(descLower, "character") || 
	    strings.Contains(descLower, "personality") ||
	    strings.Contains(descLower, "identity")) {
		return "primary"
	}
	
	if strings.Contains(reasoningLower, "user aligned") ||
	   strings.Contains(reasoningLower, "user interest") ||
	   strings.Contains(reasoningLower, "core capability") {
		return "primary"
	}
	
	// TACTICAL tier indicators:
	// - Low priority (1-4)
	// - Short-term tasks
	// - Specific one-off actions
	if priority <= 4 {
		return "tactical"
	}
	
	if strings.Contains(descLower, "parse") ||
	   strings.Contains(descLower, "fetch") ||
	   strings.Contains(descLower, "check") {
		return "tactical"
	}
	
	// DEFAULT: SECONDARY tier (5-8 priority)
	// Most research and learning goals fall here
	return "secondary"
}

// performEnhancedReflection performs structured reasoning about recent activity
func (e *Engine) performEnhancedReflection(ctx context.Context, state *InternalState) (*ReasoningResponse, int, error) {
	// CRITICAL: Load principles FIRST - these define identity and values
	principles, err := memory.LoadPrinciples(e.db)
	if err != nil {
		log.Printf("[Dialogue] WARNING: Failed to load principles: %v", err)
		principles = []memory.Principle{} // Empty fallback
	} else {
		log.Printf("[Dialogue] Loaded %d principles for reflection context", len(principles))
	}
	
	// Format principles for prompt injection
	principlesContext := memory.FormatAsSystemPrompt(principles, 0.7)
	
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
	
	// Add available tools to context
	toolsContext := e.getAvailableToolsList()
	
	// Build prompt based on reasoning depth
	var prompt string
	switch e.reasoningDepth {
	case "deep":
		prompt = fmt.Sprintf(`%s

%s%s
%s

Perform deep analysis:
1. Reflect on what these memories reveal about recent interactions
2. Identify at least 3 insights or patterns
3. Assess your strengths and weaknesses honestly
4. Identify knowledge gaps that need addressing
5. Propose 1-3 specific goals with detailed action plans (use only available tools)
6. Extract learnings about what strategies work
7. Provide comprehensive self-assessment

Be thorough and analytical. Focus on actionable insights.`, principlesContext, memoryContext, goalsContext, toolsContext)
		
	case "moderate":
		prompt = fmt.Sprintf(`%s

%s%s
%s

Analyze recent activity:
1. What patterns do you see in these memories?
2. What are you doing well? What needs improvement?
3. What knowledge gaps should you address?
4. Propose 1-2 goals with action plans (use only available tools)
5. What have you learned about effective strategies?

Be analytical but concise.`, principlesContext, memoryContext, goalsContext, toolsContext)
		
	default: // conservative
		prompt = fmt.Sprintf(`%s

%s%s
%s

Brief analysis:
1. Key takeaway from recent memories?
2. One strength, one weakness
3. Most important knowledge gap to address?
4. Propose one goal if needed (use only available tools if action plan provided)

Keep it focused and actionable.`, principlesContext, memoryContext, goalsContext, toolsContext)
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

    // SMART FALLBACK: If LLM omitted reflection (common on weak models), synthesize from context
    if reasoning.Reflection == "" {
        if len(reasoning.Insights.ToSlice()) > 0 {
            reasoning.Reflection = fmt.Sprintf("Reflected on %d insights about current state.", len(reasoning.Insights.ToSlice()))
        } else if len(reasoning.Patterns.ToSlice()) > 0 {
            reasoning.Reflection = "Reflected on identified patterns in recent activity."
        } else if len(reasoning.Weaknesses.ToSlice()) > 0 {
            reasoning.Reflection = "Reflected on identified weaknesses requiring attention."
        } else {
            // Ultimate fallback
            reasoning.Reflection = "Internal reflection cycle completed."
        }
        log.Printf("[Dialogue] [SmartFallback] LLM omitted reflection, generated: %s", truncate(reasoning.Reflection, 60))
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
	// Determine tier based on priority and description
	tier := e.determineGoalTier(proposal.Description, proposal.Priority, proposal.Reasoning)
	
	goal := Goal{
		ID:              fmt.Sprintf("goal_%d", time.Now().UnixNano()),
		Description:     proposal.Description,
		Source:          GoalSourceKnowledgeGap, // Could be smarter based on reasoning
		Priority:        proposal.Priority,
		Created:         time.Now(),
		Progress:        0.0,
		Status:          GoalStatusActive,
		Actions:         []Action{},
		Tier:            tier,
		SupportsGoals:   []string{},
		DependencyScore: 0.0,
		FailureCount:    0,
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
	
	// Map to specific registered tools (not deprecated generic web_parse)
	if strings.Contains(planLower, "contextual") || strings.Contains(planLower, "purpose") {
		tool = ActionToolWebParseContextual
	} else if strings.Contains(planLower, "chunk") || strings.Contains(planLower, "incremental") {
		tool = ActionToolWebParseChunked
	} else if strings.Contains(planLower, "metadata") || strings.Contains(planLower, "lightweight") {
		tool = ActionToolWebParseMetadata
	} else if strings.Contains(planLower, "parse") || strings.Contains(planLower, "read") || strings.Contains(planLower, "fetch") {
		tool = ActionToolWebParseGeneral // Default parse tool
	} else if strings.Contains(planLower, "search") || strings.Contains(planLower, "find") || strings.Contains(planLower, "look up") {
		tool = ActionToolSearch
	}
	// NOTE: Removed ActionToolSandbox mapping - sandbox not yet implemented
	// Keywords like "test", "experiment", "try" will fall back to search
	
	// CRITICAL: Validate tool exists before creating action
	if !e.validateToolExists(tool) {
		log.Printf("[Dialogue] WARNING: Tool '%s' not registered, falling back to search", tool)
		tool = ActionToolSearch
	}
	
	return Action{
		Description: planStep,
		Tool:        tool,
		Status:      ActionStatusPending,
		Timestamp:   time.Now(),
	}
}

// validateToolExists checks if a tool is registered before creating an action
func (e *Engine) validateToolExists(toolName string) bool {
	registry := e.toolRegistry.GetRegistry()
	_, err := registry.Get(toolName)
	return err == nil
}

// getAvailableToolsList returns a formatted list of registered tools for LLM context
func (e *Engine) getAvailableToolsList() string {
	registry := e.toolRegistry.GetRegistry()
	tools := registry.List()
	var builder strings.Builder
	builder.WriteString("\nAvailable tools for creating actions:\n")
	
	// List tools in logical order
	toolOrder := []string{
		ActionToolSearch,
		ActionToolWebParseMetadata,
		ActionToolWebParseGeneral,
		ActionToolWebParseContextual,
		ActionToolWebParseChunked,
	}
	
	for _, toolName := range toolOrder {
		if desc, exists := tools[toolName]; exists {
			builder.WriteString(fmt.Sprintf("- %s: %s\n", toolName, desc))
		}
	}
	
	builder.WriteString("\nIMPORTANT: Only use tools from this list in action plans. Never invent tool names.\n")
	builder.WriteString("Default to 'search' if unsure which tool to use.\n")
	return builder.String()
}

// storeLearning stores a learning as a collective memory and returns the memory ID
func (e *Engine) storeLearning(ctx context.Context, learning Learning) (string, error) {
	content := fmt.Sprintf("LEARNING [%s]: %s (Context: %s, Confidence: %.2f)",
		learning.Category, learning.What, learning.Context, learning.Confidence)
	
	embedding, err := e.embedder.Embed(ctx, content)
	if err != nil {
		log.Printf("[Dialogue] WARNING: Failed to embed learning: %v", err)
		return "", err
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

// extractSearchKeywords intelligently extracts 2-5 keywords from goal description
func (e *Engine) extractSearchKeywords(goalDesc string) string {
	// Remove common prefixes
	desc := strings.ToLower(goalDesc)
	desc = strings.TrimPrefix(desc, "to research and model ")
	desc = strings.TrimPrefix(desc, "to research ")
	desc = strings.TrimPrefix(desc, "research ")
	desc = strings.TrimPrefix(desc, "learn about: ")
	desc = strings.TrimPrefix(desc, "learn about ")
	desc = strings.TrimPrefix(desc, "explore ")
	desc = strings.TrimPrefix(desc, "investigate ")
	desc = strings.TrimPrefix(desc, "analyze ")
	desc = strings.TrimPrefix(desc, "understand ")
	
	// Remove filler words
	fillerWords := []string{
		"the", "a", "an", "and", "or", "but", "in", "on", "at", "to", "for",
		"of", "with", "by", "from", "as", "is", "was", "are", "were", "been",
		"be", "have", "has", "had", "do", "does", "did", "will", "would",
		"should", "could", "may", "might", "can", "based", "using", "through",
		"emphasizing", "focusing", "quiet", "steady", "ordinary", "routine",
	}
	
	words := strings.Fields(desc)
	keywords := []string{}
	
	for _, word := range words {
		// Remove punctuation
		word = strings.Trim(word, ".,;:!?—-\"'()")
		
		// Skip if empty or too short
		if word == "" || len(word) < 3 {
			continue
		}
		
		// Skip filler words
		isFiller := false
		for _, filler := range fillerWords {
			if word == filler {
				isFiller = true
				break
			}
		}
		
		if !isFiller {
			keywords = append(keywords, word)
		}
		
		// Stop at 5 keywords
		if len(keywords) >= 5 {
			break
		}
	}
	
	// Join into search query
	if len(keywords) == 0 {
		// Fallback: use first 30 chars of original
		if len(goalDesc) > 30 {
			return goalDesc[:30]
		}
		return goalDesc
	}
	
	return strings.Join(keywords, " ")
}

// extractSignificantKeywords extracts meaningful words from a goal description
func (e *Engine) extractSignificantKeywords(text string) []string {
	text = strings.ToLower(text)
	
	// Remove common prefixes
	text = strings.TrimPrefix(text, "learn about: ")
	text = strings.TrimPrefix(text, "research ")
	text = strings.TrimPrefix(text, "develop ")
	text = strings.TrimPrefix(text, "create ")
	text = strings.TrimPrefix(text, "need ")
	text = strings.TrimPrefix(text, "deep ")
	text = strings.TrimPrefix(text, "deeper ")
	
	// Stop words to ignore
	stopWords := map[string]bool{
		"the": true, "a": true, "an": true, "and": true, "or": true, "but": true,
		"in": true, "on": true, "at": true, "to": true, "for": true, "of": true,
		"with": true, "by": true, "from": true, "as": true, "is": true, "was": true,
		"are": true, "were": true, "been": true, "be": true, "have": true, "has": true,
		"had": true, "do": true, "does": true, "did": true, "will": true, "would": true,
		"should": true, "could": true, "may": true, "might": true, "can": true,
		"based": true, "using": true, "about": true, "learn": true, "research": true,
	}
	
	words := strings.Fields(text)
	keywords := []string{}
	
	for _, word := range words {
		// Remove punctuation
		word = strings.Trim(word, ".,;:!?—-\"'()")
		
		// Skip if too short, empty, or stop word
		if len(word) < 4 || stopWords[word] {
			continue
		}
		
		keywords = append(keywords, word)
	}
	
	return keywords
}

// calculateKeywordOverlap computes Jaccard similarity between two keyword sets
func (e *Engine) calculateKeywordOverlap(keywords1, keywords2 []string) float64 {
	if len(keywords1) == 0 || len(keywords2) == 0 {
		return 0.0
	}
	
	// Convert to sets
	set1 := make(map[string]bool)
	set2 := make(map[string]bool)
	
	for _, kw := range keywords1 {
		set1[kw] = true
	}
	for _, kw := range keywords2 {
		set2[kw] = true
	}
	
	// Count intersection
	intersection := 0
	for kw := range set1 {
		if set2[kw] {
			intersection++
		}
	}
	
	// Count union
	union := len(set1)
	for kw := range set2 {
		if !set1[kw] {
			union++
		}
	}
	
	if union == 0 {
		return 0.0
	}
	
	// Jaccard similarity
	return float64(intersection) / float64(union)
}

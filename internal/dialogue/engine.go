package dialogue

import (
	"context"
	"fmt"
	"log"
	"strings"
	"time"

	"go-llama/internal/memory"
	"go-llama/internal/tools"
	"gorm.io/gorm"
)

// Engine manages the internal dialogue process
type Engine struct {
	storage				*memory.Storage
	embedder			*memory.Embedder
	stateManager			*StateManager
    toolRegistry			*tools.ContextualRegistry
    llmURL				string
    llmModel			string
    simpleLLMURL			string
    simpleLLMModel			string
    llmClient			interface{}	// Will be *llm.Client but avoid import cycle
    db				*gorm.DB	// For loading principles
    contextSize			int
	maxTokensPerCycle		int
	maxDurationMinutes		int
	maxThoughtsPerCycle		int
	actionRequirementInterval	int
	noveltyWindowHours		int
	// Enhanced reasoning config
	reasoningDepth		string
	enableSelfAssessment	bool
	enableMetaLearning	bool
	enableStrategyTracking	bool
	storeInsights		bool
	dynamicActionPlanning	bool
	adaptiveConfig		*AdaptiveConfig
	circuitBreaker		*tools.CircuitBreaker
}

// NewEngine creates a new dialogue engine
func NewEngine(
    storage *memory.Storage,
    embedder *memory.Embedder,
    stateManager *StateManager,
    toolRegistry *tools.ContextualRegistry,
    db *gorm.DB,	// Add DB for principles
    llmURL string,
    llmModel string,
    contextSize int,
    llmClient interface{},	// Accept queue client
    simpleLLMURL string,
    simpleLLMModel string,
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
        storage:			storage,
        embedder:			embedder,
        stateManager:			stateManager,
        toolRegistry:			toolRegistry,
        db:				db,	// Store DB
        llmURL:				llmURL,
        llmModel:			llmModel,
        simpleLLMURL:			simpleLLMURL,
        simpleLLMModel:			simpleLLMModel,
        llmClient:			llmClient,	// Store client
        contextSize:			contextSize,
		maxTokensPerCycle:		maxTokensPerCycle,
		maxDurationMinutes:		maxDurationMinutes,
		maxThoughtsPerCycle:		maxThoughtsPerCycle,
		actionRequirementInterval:	actionRequirementInterval,
		noveltyWindowHours:		noveltyWindowHours,
		reasoningDepth:			reasoningDepth,
		enableSelfAssessment:		enableSelfAssessment,
		enableMetaLearning:		enableMetaLearning,
		enableStrategyTracking:		enableStrategyTracking,
		storeInsights:			storeInsights,
		dynamicActionPlanning:		dynamicActionPlanning,
		adaptiveConfig:			NewAdaptiveConfig(0.30, 0.75, 60),
		circuitBreaker:			circuitBreaker,
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
		CycleID:	cycleID,
		StartTime:	startTime,
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
	recentThoughts := []string{}	// For novelty filtering

    // PHASE 0: Update adaptive thresholds based on current state
    totalMemories, err := e.storage.GetTotalMemoryCount(ctx)
    if err != nil {
        log.Printf("[Dialogue] WARNING: Failed to get memory count: %v", err)
        totalMemories = 0
    }
    e.adaptiveConfig.UpdateMetrics(ctx, state, totalMemories)

    // GLOBAL CLEANUP: Check for stale in-progress actions across ALL goals
    // Moved here from single-goal loop to ensure cleanup happens regardless of goal selection
    now := time.Now()
    for gIdx := range state.ActiveGoals {
        goal := &state.ActiveGoals[gIdx]
        for i := range goal.Actions {
            action := &goal.Actions[i]
            if action.Status == ActionStatusInProgress {
                age := now.Sub(action.Timestamp)

                // Use adaptive timeout calculated by the system
                baseTimeout := 10 * time.Minute // Fallback if adaptive config is nil/unavailable
                if e.adaptiveConfig != nil {
                    baseTimeout = time.Duration(e.adaptiveConfig.toolTimeout) * time.Second
                }
                
                // Initialize timeout variable in the correct scope
                var timeout time.Duration

                // Web parsing can take significantly longer due to LLM processing + Network I/O
                if action.Tool == ActionToolWebParseContextual ||
                    action.Tool == ActionToolWebParseGeneral ||
                    action.Tool == ActionToolWebParseChunked {
                    // Enforce a minimum of 5 minutes for network+llm actions, 
                    // or use the adaptive timeout * 2 if it's higher.
                    minWebTimeout := 5 * time.Minute
                    calculatedTimeout := baseTimeout * 2
                    if calculatedTimeout < minWebTimeout {
                        timeout = minWebTimeout
                    } else {
                        timeout = calculatedTimeout
                    }
                } else {
                    timeout = baseTimeout
                }

                if age > timeout {
                    log.Printf("[Dialogue] Found stale in-progress action (age: %s, timeout: %s), marking as failed: %s",
                        age.Round(time.Second), timeout, truncate(action.Description, 60))
                    action.Status = ActionStatusCompleted
                    action.Result = fmt.Sprintf("TIMEOUT: Action abandoned after %s (timeout: %s)",
                        age.Round(time.Second), timeout)
                    goal.Outcome = "bad"
                    
                    // Update goal in state immediately
                    state.ActiveGoals[gIdx] = *goal
                }
            }
        }
    }

	// PHASE 1: Enhanced Reflection with Structured Reasoning
	reasoning, principles, phaseTokens, reflectionText, err := e.runPhaseReflection(ctx, state)
	if err != nil {
		return StopReasonNaturalStop, err
	}

	thoughtCount++
	totalTokens += phaseTokens
	recentThoughts = append(recentThoughts, reflectionText)

	// Save thought record
	e.stateManager.SaveThought(ctx, &ThoughtRecord{
		CycleID:	state.CycleCount,
		ThoughtNum:	thoughtCount,
		Content:	reflectionText,
		TokensUsed:	phaseTokens,
		ActionTaken:	false,
		Timestamp:	time.Now(),
	})

	// Check token budget
	if totalTokens >= e.maxTokensPerCycle {
		metrics.ThoughtCount = thoughtCount
		metrics.ActionCount = actionCount
		metrics.TokensUsed = totalTokens
		return StopReasonMaxThoughts, nil
	}

	// PHASE 2: Reasoning-Driven Goal Management
	err = e.runPhaseGoalManagement(ctx, state, reasoning, principles, metrics, &totalTokens)
	if err != nil {
		return StopReasonNaturalStop, err
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
		var actionExecuted bool
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

		// NEW: Handle self-modification goals specially
		if topGoal.Source == GoalSourceSelfModification && topGoal.SelfModGoal != nil {
			log.Printf("[Dialogue] Executing self-modification goal for slot %d", topGoal.SelfModGoal.TargetSlot)

			// Test the proposed principle change
			success, testResult := e.testPrincipleModification(ctx, &topGoal, principles)

			if success {
				// Commit the change permanently
				err := memory.UpdatePrinciple(e.db,
					topGoal.SelfModGoal.TargetSlot,
					topGoal.SelfModGoal.ProposedPrinciple,
					0.8)	// High rating for deliberate modifications

				if err != nil {
					log.Printf("[Dialogue] ERROR: Failed to commit principle change: %v", err)
					topGoal.Status = GoalStatusAbandoned
					topGoal.Outcome = "bad"
				} else {
					log.Printf("[Dialogue] ✓ Principle modification committed to slot %d", topGoal.SelfModGoal.TargetSlot)
					log.Printf("[Dialogue]   New principle: %s", topGoal.SelfModGoal.ProposedPrinciple)
					topGoal.Status = GoalStatusCompleted
					topGoal.Outcome = "good"
					topGoal.Progress = 1.0

					// Store this as a high-value learning
					learning := Learning{
						What:		fmt.Sprintf("Modified principle (slot %d): %s", topGoal.SelfModGoal.TargetSlot, topGoal.SelfModGoal.ProposedPrinciple),
						Context:	fmt.Sprintf("Self-modification goal. Justification: %s. Test result: %s", topGoal.SelfModGoal.Justification, testResult),
						Confidence:	0.9,
						Category:	"self_modification",
					}
					e.storeLearning(ctx, learning)
				}
			} else {
				log.Printf("[Dialogue] ✗ Principle modification test failed: %s", testResult)
				log.Printf("[Dialogue]   Keeping current principle in slot %d", topGoal.SelfModGoal.TargetSlot)
				topGoal.Status = GoalStatusAbandoned
				topGoal.Outcome = "neutral"	// Not a failure, just didn't improve things
			}

			// Update goal in state
			for i := range state.ActiveGoals {
				if state.ActiveGoals[i].ID == topGoal.ID {
					state.ActiveGoals[i] = topGoal
					break
				}
			}

			// Skip normal action execution for self-mod goals
			actionExecuted = true
		} else {
			// For non-self-mod goals, start with actionExecuted = false
			actionExecuted = false
		}

		// Phase 3.2: Execute actions with tools (skip if self-mod goal already handled)
		if !actionExecuted {
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
						urls := extractURLsFromSearchResults(result)

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
											Description:	bestURL,
											Tool:		ActionToolWebParseContextual,
											Status:		ActionStatusPending,
											Timestamp:	time.Now(),
											Metadata: map[string]interface{}{
												"purpose":	purpose,
												"selected_url":	bestURL,
											},
										}
										// CRITICAL: Copy research metadata from search action to parse action
										if action.Metadata != nil {
											if questionID, ok := action.Metadata["research_question_id"].(string); ok {
												parseAction.Metadata["research_question_id"] = questionID
											}
											if questionText, ok := action.Metadata["question_text"].(string); ok {
												parseAction.Metadata["question_text"] = questionText
											}
										}
										log.Printf("[Dialogue] Auto-created contextual parse action for best URL: %s", truncate(bestURL, 60))
									} else {
										parseAction = Action{
											Description:	bestURL,
											Tool:		ActionToolWebParseGeneral,
											Status:		ActionStatusPending,
											Timestamp:	time.Now(),
											Metadata: map[string]interface{}{
												"selected_url": bestURL,
											},
										}
										// CRITICAL: Copy research metadata from search action to parse action
										if action.Metadata != nil {
											if questionID, ok := action.Metadata["research_question_id"].(string); ok {
												parseAction.Metadata["research_question_id"] = questionID
											}
											if questionText, ok := action.Metadata["question_text"].(string); ok {
												parseAction.Metadata["question_text"] = questionText
											}
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
								}
							}
						}
					}
					if err != nil {
						log.Printf("[Dialogue] Action failed: %v", err)
						action.Result = fmt.Sprintf("ERROR: %v", err)
						action.Status = ActionStatusCompleted

                    // Check if this is a "page too large" error from web_parse_general OR web_parse_contextual
                    if (action.Tool == ActionToolWebParseGeneral || action.Tool == ActionToolWebParseContextual) &&
                        strings.Contains(strings.ToLower(err.Error()), "page too large") {
                        log.Printf("[Dialogue] Page too large detected, initiating intelligent fallback...")

                        // Get the URL from the action
                        var url string
                        if action.Metadata != nil {
                            if selectedURL, ok := action.Metadata["selected_url"].(string); ok {
                                url = selectedURL
                            } else if bestURL, ok := action.Metadata["best_url"].(string); ok {
                                url = bestURL
                            }
                        }

                        // Fallback: extract URL from action description
                        if url == "" {
                            url = strings.TrimSpace(action.Description)
                            if idx := strings.Index(url, "http"); idx != -1 {
                                url = url[idx:]
                            }
                            if idx := strings.Index(url, " "); idx != -1 {
                                url = url[:idx]
                            }
                        }

                        // Call intelligent recovery handler in engine_research.go
                        // We pass &topGoal because the function expects a pointer (*Goal)
                        fallbackActions, fallbackErr := e.handleLargePageFallback(ctx, url, &topGoal)
                        
                        if fallbackErr != nil {
                            log.Printf("[Dialogue] ⚠ Intelligent fallback failed: %v. Falling back to default behavior.", fallbackErr)
                            // If intelligent fallback fails, increment failure count
                            topGoal.FailureCount++
                        } else {
                            log.Printf("[Dialogue] ✓ Intelligent fallback generated %d recovery actions", len(fallbackActions))
                            // Append the new actions to the goal
                            topGoal.Actions = append(topGoal.Actions, fallbackActions...)
                            
                            // Don't increment failure count for this specific error
                            // since we're handling it with fallback actions
                        }

                        // Update goal in state
                        for k := range state.ActiveGoals {
                            if state.ActiveGoals[k].ID == topGoal.ID {
                                state.ActiveGoals[k] = topGoal
                                break
                            }
                        }
						} else {
							// Track failures - don't abandon on first failure
							topGoal.FailureCount++
						}

						// Only mark as bad after 3+ consecutive failures
						if topGoal.FailureCount >= 3 {
							topGoal.Outcome = "bad"
							log.Printf("[Dialogue] ⚠ Goal marked as bad after %d consecutive failures", topGoal.FailureCount)
						} else {
							log.Printf("[Dialogue] ⚠ Action failed (failure %d/3), will retry", topGoal.FailureCount)
						}

						// Don't increment actionCount - this was a failure
						actionExecuted = true	// Still counts as execution attempt

					} else if action.Tool == ActionToolWebParseContextual ||
						action.Tool == ActionToolWebParseGeneral ||
						action.Tool == ActionToolWebParseChunked ||
						action.Tool == ActionToolWebParseMetadata {
						// NEW: Parse evaluation for web parse actions
						log.Printf("[Dialogue] Parse action completed, evaluating quality...")

						// Special handling for metadata action - store findings for later use
						if action.Tool == ActionToolWebParseMetadata && action.Metadata != nil {
							if action.Metadata["fallback_for"] == "page_too_large" {
								log.Printf("[Dialogue] Storing metadata findings for intelligent chunk selection")

								// Parse the metadata result to extract section information
								sections := e.extractSectionsFromMetadata(result)

								// Store sections in goal's metadata for later use by chunked action
								if topGoal.Metadata == nil {
									topGoal.Metadata = make(map[string]interface{})
								}
								topGoal.Metadata["page_sections"] = sections

								// Also identify the most relevant chunk based on the goal
								relevantChunk := e.findMostRelevantChunk(sections, topGoal.Description)
								if relevantChunk >= 0 {
									topGoal.Metadata["suggested_chunk"] = relevantChunk
									log.Printf("[Dialogue] Suggested starting chunk: %d based on goal: %s",
										relevantChunk, truncate(topGoal.Description, 60))
								}

								// Update goal in state
								for k := range state.ActiveGoals {
									if state.ActiveGoals[k].ID == topGoal.ID {
										state.ActiveGoals[k] = topGoal
										break
									}
								}
							}
						}

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
											Description:	nextURL,
											Tool:		ActionToolWebParseContextual,
											Status:		ActionStatusPending,
											Timestamp:	time.Now(),
											Metadata: map[string]interface{}{
												"purpose":		purpose,
												"selected_url":		nextURL,
												"fallback_urls":	remainingFallbacks,
												"is_fallback":		true,
											},
										}
									} else {
										fallbackAction = Action{
											Description:	nextURL,
											Tool:		ActionToolWebParseGeneral,
											Status:		ActionStatusPending,
											Timestamp:	time.Now(),
											Metadata: map[string]interface{}{
												"selected_url":		nextURL,
												"fallback_urls":	remainingFallbacks,
												"is_fallback":		true,
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
									Description:	parsedURL,
									Tool:		ActionToolWebParseChunked,
									Status:		ActionStatusPending,
									Timestamp:	time.Now(),
									Metadata: map[string]interface{}{
										"selected_url":	parsedURL,
										"chunk_index":	0,
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
										Description:	nextURL,
										Tool:		action.Tool,
										Status:		ActionStatusPending,
										Timestamp:	time.Now(),
										Metadata: map[string]interface{}{
											"selected_url":		nextURL,
											"fallback_urls":	remainingFallbacks,
											"is_fallback":		true,
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

					// NEW: Assess progress after EVERY action completes
					log.Printf("[Dialogue] Assessing progress after action completion...")
					assessment, assessTokens, err := e.assessProgress(ctx, &topGoal)
					if err != nil {
						log.Printf("[Dialogue] WARNING: Progress assessment failed: %v", err)
						// Continue with current plan on assessment failure
					} else {
						totalTokens += assessTokens
						topGoal.LastAssessment = assessment

						log.Printf("[Dialogue] Assessment: quality=%s, validity=%s, recommendation=%s",
							assessment.ProgressQuality, assessment.PlanValidity, assessment.Recommendation)
						log.Printf("[Dialogue] Reasoning: %s", truncate(assessment.Reasoning, 120))

						// Act on recommendation
						if assessment.Recommendation == "replan" && topGoal.ReplanCount < 3 {
							log.Printf("[Dialogue] Replanning goal based on assessment (attempt %d/3)...", topGoal.ReplanCount+1)

							// Clear pending actions (keep completed for context)
							completedActions := []Action{}
							for _, a := range topGoal.Actions {
								if a.Status == ActionStatusCompleted {
									completedActions = append(completedActions, a)
								}
							}
							topGoal.Actions = completedActions

							// Generate new plan
							if topGoal.ResearchPlan != nil {
								newPlan, replanTokens, err := e.replanGoal(ctx, &topGoal, assessment.Reasoning)
								if err != nil {
									log.Printf("[Dialogue] WARNING: Replan failed: %v", err)
									// Continue with existing plan
								} else {
									totalTokens += replanTokens
									topGoal.ResearchPlan = newPlan
									topGoal.ReplanCount++

									log.Printf("[Dialogue] ✓ New plan generated (replan #%d): %d questions",
										topGoal.ReplanCount, len(newPlan.SubQuestions))

									// Create first action from new plan
									nextAction := e.getNextResearchAction(ctx, &topGoal)
									if nextAction != nil {
										topGoal.Actions = append(topGoal.Actions, *nextAction)
										topGoal.HasPendingWork = true
										log.Printf("[Dialogue] ✓ Created first action from new plan: %s",
											truncate(nextAction.Description, 60))
									}

									// Update goal in state immediately
									for i := range state.ActiveGoals {
										if state.ActiveGoals[i].ID == topGoal.ID {
											state.ActiveGoals[i] = topGoal
											break
										}
									}
								}
							}
						} else if assessment.Recommendation == "replan" && topGoal.ReplanCount >= 3 {
							log.Printf("[Dialogue] ⚠ Goal reached max replans (3), marking as failed")
							topGoal.Status = GoalStatusAbandoned
							topGoal.Outcome = "bad"
                        } else if assessment.Recommendation == "adjust" {
                            log.Printf("[Dialogue] Plan needs minor adjustment, will naturally adapt on next action")
                            // No action needed - system will adjust when creating next action
                        } else if assessment.Recommendation == "complete" {
                            log.Printf("[Dialogue] Goal completed successfully!")
                            topGoal.Status = GoalStatusCompleted
                            topGoal.Outcome = "good"
                            topGoal.Progress = 1.0

                            // Find the goal index in ActiveGoals
                            completedGoalIndex := -1
                            for i, g := range state.ActiveGoals {
                                if g.ID == topGoal.ID {
                                    completedGoalIndex = i
                                    break
                                }
                            }

                            // Move from Active to Completed
                            if completedGoalIndex != -1 {
                                // Remove from active
                                state.ActiveGoals = append(state.ActiveGoals[:completedGoalIndex], state.ActiveGoals[completedGoalIndex+1:]...)
                                // Add to completed
                                state.CompletedGoals = append(state.CompletedGoals, topGoal)
                                log.Printf("[Dialogue] ✓ Moved goal to CompletedGoals: %s", truncate(topGoal.Description, 60))
                            } else {
                                log.Printf("[Dialogue] WARNING: Could not find goal in ActiveGoals to move: %s", topGoal.ID)
                            }
                        }
                    }

                    // Only execute one action per cycle
                    break
                }
            }
        }	// Close the "if !actionExecuted" block from line 643

		// If no actions were executed, check if we should create new actions
		if !actionExecuted {
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
						CycleID:	state.CycleCount,
						ThoughtNum:	thoughtCount,
						Content:	goalThought,
						TokensUsed:	tokens,
						ActionTaken:	false,
						Timestamp:	time.Now(),
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
								Description:	searchQuery,
								Tool:		ActionToolSearch,
								Status:		ActionStatusPending,
								Timestamp:	time.Now(),
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
								Description:	"Synthesize research findings",
								Tool:		ActionToolSynthesis,
								Status:		ActionStatusPending,
								Timestamp:	time.Now(),
							}
							topGoal.Actions = append(topGoal.Actions, synthesisAction)
							log.Printf("[Dialogue] ✓ Research complete, synthesis action created")
						}
					} else {
						// Simple goal without research plan - extract keywords
						searchQuery := extractSearchKeywords(topGoal.Description)

						searchAction := Action{
							Description:	searchQuery,
							Tool:		ActionToolSearch,
							Status:		ActionStatusPending,
							Timestamp:	time.Now(),
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
			topGoal.Progress = 0.99	// Almost complete
		}

		if topGoal.Progress >= 1.0 && !hasPendingActions {
			// Validate that the goal actually achieved something useful
			hasUsefulOutcome := false
			hasFailures := false
			totalOutputLength := 0

            for _, action := range topGoal.Actions {
                if action.Status == ActionStatusCompleted {
                    // Filter out abandoned/stale actions to avoid noise in reflection
                    // These are environment/timeout issues, not strategic tool failures
                    if strings.HasPrefix(action.Result, "TIMEOUT: Action abandoned") {
                        continue
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

                    if len(action.Result) > minLength {
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

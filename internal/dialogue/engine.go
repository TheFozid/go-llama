package dialogue

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"math/rand"
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

						// Check if this is a "page too large" error from web_parse_general
						if action.Tool == ActionToolWebParseGeneral &&
							strings.Contains(strings.ToLower(err.Error()), "page too large") {
							log.Printf("[Dialogue] Creating fallback actions for large page")

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

							// Create metadata action to get page structure
							metadataAction := Action{
								Description:	url,
								Tool:		ActionToolWebParseMetadata,
								Status:		ActionStatusPending,
								Timestamp:	time.Now(),
								Metadata: map[string]interface{}{
									"selected_url":		url,
									"original_goal":	topGoal.Description,
									"fallback_for":		"page_too_large",
								},
							}

							// Create contextual action with purpose
							purpose := "Extract information relevant to: " + topGoal.Description
							contextualAction := Action{
								Description:	url,
								Tool:		ActionToolWebParseContextual,
								Status:		ActionStatusPending,
								Timestamp:	time.Now(),
								Metadata: map[string]interface{}{
									"selected_url":		url,
									"purpose":		purpose,
									"original_goal":	topGoal.Description,
									"fallback_for":		"page_too_large",
								},
							}

							// Create chunked action as final fallback with intelligent chunk selection
							chunkIndex := 0	// Default
							if topGoal.Metadata != nil {
								if suggestedChunk, ok := topGoal.Metadata["suggested_chunk"].(int); ok {
									chunkIndex = suggestedChunk
									log.Printf("[Dialogue] Using suggested chunk %d from metadata analysis", chunkIndex)
								}
							}

							chunkedAction := Action{
								Description:	url,
								Tool:		ActionToolWebParseChunked,
								Status:		ActionStatusPending,
								Timestamp:	time.Now(),
								Metadata: map[string]interface{}{
									"selected_url":			url,
									"chunk_index":			chunkIndex,
									"original_goal":		topGoal.Description,
									"fallback_for":			"page_too_large",
									"intelligent_selection":	true,
								},
							}

							// Add actions to goal in order: metadata -> contextual -> chunked
							topGoal.Actions = append(topGoal.Actions, metadataAction, contextualAction, chunkedAction)

							log.Printf("[Dialogue] ✓ Created 3 fallback actions for large page (metadata -> contextual -> chunked)")

							// Update goal in state
							for k := range state.ActiveGoals {
								if state.ActiveGoals[k].ID == topGoal.ID {
									state.ActiveGoals[k] = topGoal
									break
								}
							}

							// Don't increment failure count for this specific error
							// since we're handling it with fallback actions
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
						}
					}

					// Only execute one action per cycle
					break
				}
			}
		}	// Close the "if !actionExecuted" block from line 643

		// If no actions were executed, check if we should create new actions
		if !actionExecuted {
			// Check for stale in-progress actions with adaptive timeouts
			now := time.Now()
			for i := range topGoal.Actions {
				action := &topGoal.Actions[i]
				if action.Status == ActionStatusInProgress {
					age := now.Sub(action.Timestamp)

					// Adaptive timeout based on action type (accounting for slow CPU inference)
					timeout := 10 * time.Minute	// Default: 10 minutes for search/general actions

					// Web parsing can take significantly longer due to LLM processing
					if action.Tool == ActionToolWebParseContextual ||
						action.Tool == ActionToolWebParseGeneral ||
						action.Tool == ActionToolWebParseChunked {
						timeout = 15 * time.Minute	// 15 minutes for parse actions
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
				"model":	e.llmModel,
				"max_tokens":	e.contextSize,
				"messages": []map[string]string{
					{
						"role":		"system",
						"content":	"You are GrowerAI's internal dialogue system. Think briefly and clearly.",
					},
					{
						"role":		"user",
						"content":	prompt,
					},
				},
				"temperature":	0.3,
				"stream":	false,
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
				Choices	[]struct {
					Message struct {
						Content string `json:"content"`
					} `json:"message"`
				}	`json:"choices"`
				Usage	struct {
					TotalTokens int `json:"total_tokens"`
				}	`json:"usage"`
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
Example: (reasoning (reflection "Good session") (insights "Learned X") (goals_to_create (goal (description "Do Y") (priority 8))))`

	reqBody := map[string]interface{}{
		"model":	e.llmModel,
		"max_tokens":	e.contextSize,
		"messages": []map[string]string{
			{
				"role":		"system",
				"content":	systemPrompt,
			},
			{
				"role":		"user",
				"content":	prompt,
			},
		},
		"temperature":	0.7,
		"stream":	false,
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
				Choices	[]struct {
					Message struct {
						Content string `json:"content"`
					} `json:"message"`
				}	`json:"choices"`
				Usage	struct {
					TotalTokens int `json:"total_tokens"`
				}	`json:"usage"`
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
					Reflection:	"Failed to parse structured reasoning. Using fallback mode.",
					RawResponse:	content,	// Preserve raw content
					Insights:	[]string{},
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

// Parse S-expression response
// Use RawResponse because Reflection might be generic fallback text

// DEBUG LOGGING: Always log what we received

// Clean up markdown fences

// IMPROVED PARSING: Use recursive search first (most robust)

// Strategy 1: Recursive search (handles nested structures like (reasoning (research_plan ...)))

// Strategy 2: Direct search as fallback

// Strategy 3: Regex fallback (handles malformed S-expressions)

// If all strategies failed, provide detailed error

// Extract Root Question

// Extract Sub Questions

// Convert to internal ResearchPlan

// Helper to extract integer fields safely

// Helper to extract dependencies list (deps ("q1" "q2"))

// Handle empty list ()

// Naive extraction of quoted strings until closing )

// getNextResearchAction determines next action from research plan

// Find next pending question (respecting dependencies)

// Check dependencies

// No questions available

// Create search action

// updateResearchProgress records findings from completed action

// Find question

// Extract findings using simple heuristics (lightweight, no LLM)
// Take first 200 chars as key finding

// Default confidence

// synthesizeResearchFindings combines all findings into coherent knowledge

// Build context from completed questions

// storeResearchSynthesis saves synthesis as high-value collective memory

// Extract concept tags from questions

// executeAction executes a tool-based action

// Check context before starting

// Map action tool to actual tool execution

// Extract search query from action description

// Store URLs in action metadata for the next parse action to use

// First priority: check if search evaluation selected a best URL

// Fallback: use first URL (old behavior)

// Fallback: extract URL from action description

// Formats handled:
//   - "https://example.com"
//   - "Parse URL: https://example.com"
//   - "Search result: https://example.com - title"
//   - "URL from search results" (placeholder - will fail with clear error)

// Handle placeholder case

// Clean up common prefixes

// Start from http

// Remove everything after first space (titles, descriptions)

// Basic validation

// For contextual parsing, extract purpose from metadata if available

// For chunked parsing, look for chunk index

// Default to first chunk - LLM should specify in future iterations

// Try to parse chunk index from description
// Format: "Read chunk 3 from URL" or "chunk_index: 3"

// Simple extraction - matches "chunk 3", "chunk 0", etc.

// Execute the appropriate web parse tool

// Check if this is a "page too large" error from web_parse_general

// Create a special error that will be handled by the goal pursuit system

// Phase 3.5: Sandbox not yet implemented

// This is internal, not a real tool

// Synthesis happens in goal completion phase, not here

// Note: Result logging happens in each case block above

// Helper functions

// Goal and state helpers (sortGoalsByPriority, truncate, hasPendingActions) moved to utils.go

// getPrimaryGoals filters goals by primary tier

// validateGoalSupport uses LLM to validate if a secondary goal supports a primary goal

// Build context about primary goals

// Parse S-expression response

// parseGoalSupportValidation extracts validation from S-expression

// Find goal_support_validation block

// Default

// Extract fields

// Validation: if is_valid is true, must have a goal ID

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
func (e *Engine) performEnhancedReflection(ctx context.Context, state *InternalState) (*ReasoningResponse, []memory.Principle, int, error) {
	// CRITICAL: Load principles FIRST - these define identity and values
	principles, err := memory.LoadPrinciples(e.db)
	if err != nil {
		log.Printf("[Dialogue] WARNING: Failed to load principles: %v", err)
		principles = []memory.Principle{}	// Empty fallback
	} else {
		log.Printf("[Dialogue] Loaded %d principles for reflection context", len(principles))
	}

	// Format principles for prompt injection
	principlesContext := memory.FormatAsSystemPrompt(principles, 0.7)

	// Find recent memories for context
	embedding, err := e.embedder.Embed(ctx, "recent activity patterns successes failures")
	if err != nil {
		return nil, nil, 0, fmt.Errorf("failed to generate embedding: %w", err)
	}

	searchThreshold := e.adaptiveConfig.GetSearchThreshold()

	// For collective memory search (learnings), use a lower threshold
	// Recent learnings might not have perfect semantic match but should still be retrieved
	collectiveThreshold := searchThreshold
	if collectiveThreshold > 0.20 {
		collectiveThreshold = 0.20	// Lower threshold for collective memories
	}

	query := memory.RetrievalQuery{
		Limit:			10,	// Increased from 8 to get more learnings
		MinScore:		collectiveThreshold,
		IncludeCollective:	true,
		IncludePersonal:	false,	// Explicitly exclude personal for collective-only search
	}

	log.Printf("[Dialogue] Searching collective memories (threshold: %.2f [adaptive: %.2f], limit: %d)",
		collectiveThreshold, searchThreshold, 10)

	results, err := e.storage.Search(ctx, query, embedding)
	if err != nil {
		return nil, nil, 0, fmt.Errorf("failed to search memories: %w", err)
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
		Limit:			5,
		MinScore:		0.15,	// Very low threshold for tagged learnings
		IncludeCollective:	true,
		IncludePersonal:	false,
		ConceptTags:		[]string{"learning"},	// Search for learning tag specifically
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

	default:	// conservative
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
		return nil, nil, tokens, err
	}

	// Override LLM's confidence with calculated confidence based on actual metrics
	calculatedConfidence := e.calculateConfidence(ctx, state)

	// If LLM provided self-assessment, adjust the confidence
	if reasoning.SelfAssessment != nil {
		// Allow LLM to adjust ±0.2 from calculated baseline
		llmConfidence := reasoning.SelfAssessment.Confidence
		adjustment := llmConfidence - 0.5	// LLM's deviation from neutral

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

	return reasoning, principles, tokens, nil
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
		confidence += (goalSuccessRate - 0.5) * 0.3	// ±0.15 based on goal success
	}

	// Factor 2: Recent memory retrieval (are we finding relevant context?)
	embedding, err := e.embedder.Embed(ctx, "recent activity patterns success")
	if err == nil {
		query := memory.RetrievalQuery{
			Limit:			5,
			MinScore:		0.5,
			IncludeCollective:	true,
		}
		results, err := e.storage.Search(ctx, query, embedding)
		if err == nil && len(results) > 0 {
			// Average relevance score of retrieved memories
			avgScore := 0.0
			for _, result := range results {
				avgScore += result.Score
			}
			avgScore /= float64(len(results))
			confidence += (avgScore - 0.5) * 0.2	// ±0.1 based on retrieval quality
		}
	}

	// Factor 3: Active goals progress
	if len(state.ActiveGoals) > 0 {
		totalProgress := 0.0
		for _, goal := range state.ActiveGoals {
			totalProgress += goal.Progress
		}
		avgProgress := totalProgress / float64(len(state.ActiveGoals))
		confidence += (avgProgress - 0.5) * 0.2	// ±0.1 based on active progress
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
		ID:			fmt.Sprintf("goal_%d", time.Now().UnixNano()),
		Description:		proposal.Description,
		Source:			GoalSourceKnowledgeGap,	// Could be smarter based on reasoning
		Priority:		proposal.Priority,
		Created:		time.Now(),
		Progress:		0.0,
		Status:			GoalStatusActive,
		Actions:		[]Action{},
		Tier:			tier,
		SupportsGoals:		[]string{},
		DependencyScore:	0.0,
		FailureCount:		0,
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

// Simple parsing: look for tool keywords
// Default to search

// Map to specific registered tools (not deprecated generic web_parse)

// Default parse tool

// NOTE: Removed ActionToolSandbox mapping - sandbox not yet implemented
// Keywords like "test", "experiment", "try" will fall back to search

// CRITICAL: Validate tool exists before creating action

// validateToolExists checks if a tool is registered before creating an action

// getAvailableToolsList returns a formatted list of registered tools for LLM context

// List tools in logical order

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
		Content:		content,
		ImportanceScore:	learning.Confidence,	// Use confidence as importance
		IsCollective:		true,			// Learnings are collective knowledge
		ConceptTags:		[]string{"learning", learning.Category},
		OutcomeTag:		"good",	// Learnings are positive
		ValidationCount:	1,	// Pre-validated
		TrustScore:		learning.Confidence,
		Tier:			memory.TierRecent,	// Start in recent tier
		CreatedAt:		time.Now(),
		LastAccessedAt:		time.Now(),
		AccessCount:		0,
		Embedding:		embedding,
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

// Misc helpers (generateJitter, truncateResponse) moved to utils.go

// assessProgress evaluates if the current plan is still optimal after completing an action

// Gather completed and pending actions

// Build action summaries

// Build research plan summary if exists

// Parse S-expression response

// parseAssessmentSExpr extracts assessment from S-expression response

// Find assessment block

// FALLBACK: If recursive search fails, try to find the block manually
// This handles cases where LLM adds conversational text before the block
// or uses slightly different formatting.

// Find last occurrence (most likely to be the actual data)

// Find matching closing parenthesis

// Include closing paren

// Validate required fields

// replanGoal generates a new plan based on what we've learned so far

// Summarize what we've learned from completed actions

// Analyze if this was useful or not

// Get original plan summary

// Parse using existing research plan parser

// Use existing parsing logic

// Extract fields using existing helpers

// Parse questions using existing logic

// evaluatePrincipleEffectiveness checks if current principles are working well

// Only check if we have recent failures

// Get recent goal outcomes (last 10)

// Count failures

// Need at least 3 failures to consider modification

// Build context about failures

// Build current principles context (AI-managed only)

// Parse response

// PrincipleFeedback represents LLM's evaluation of whether to modify principles

// parsePrincipleFeedback extracts feedback from S-expression

// Extract modification details

// Validate slot range

// Validate required fields

// createSelfModificationGoal creates a goal to test and potentially commit a principle change

// Generate test actions based on strategy

// High priority - self-improvement is important

// testPrincipleModification validates a proposed principle change

// Simple validation test: Does the new principle make semantic sense?
// In a full implementation, this would execute test actions and compare results

// Parse validation

// extractSectionsFromMetadata parses metadata result to extract section information
func (e *Engine) extractSectionsFromMetadata(metadataResult string) []PageSection {
	sections := []PageSection{}

	lines := strings.Split(metadataResult, "\n")
	currentSection := ""
	chunkIndex := 0

	for _, line := range lines {
		line = strings.TrimSpace(line)

		// Look for headings (lines that start with # or contain "Section" or "Chapter")
		if strings.HasPrefix(line, "#") ||
			strings.HasPrefix(line, "##") ||
			strings.HasPrefix(line, "###") ||
			strings.Contains(line, "Section:") ||
			strings.Contains(line, "Chapter:") {

			// Store the previous section if it exists
			if currentSection != "" {
				sections = append(sections, PageSection{
					Heading:	currentSection,
					ChunkIndex:	chunkIndex,
				})
			}

			// Extract heading text
			heading := line
			if idx := strings.Index(line, ":"); idx != -1 {
				heading = strings.TrimSpace(line[idx+1:])
			}
			heading = strings.TrimPrefix(heading, "#")
			heading = strings.TrimPrefix(heading, "##")
			heading = strings.TrimPrefix(heading, "###")
			currentSection = strings.TrimSpace(heading)

			// Estimate chunk index (rough approximation)
			chunkIndex++
		}
	}

	// Don't forget the last section
	if currentSection != "" {
		sections = append(sections, PageSection{
			Heading:	currentSection,
			ChunkIndex:	chunkIndex,
		})
	}

	log.Printf("[Dialogue] Extracted %d sections from metadata", len(sections))
	return sections
}

// findMostRelevantChunk identifies the most relevant chunk based on goal and sections
func (e *Engine) findMostRelevantChunk(sections []PageSection, goalDescription string) int {
	if len(sections) == 0 {
		return 0	// Default to chunk 0 if no sections found
	}

	goalLower := strings.ToLower(goalDescription)

	// Keywords for different types of goals
	goalKeywords := map[string][]string{
		"methodology":		{"method", "methodology", "approach", "technique", "procedure"},
		"results":		{"result", "finding", "outcome", "conclusion", "summary"},
		"background":		{"background", "introduction", "overview", "context", "history"},
		"analysis":		{"analysis", "discussion", "evaluation", "assessment", "review"},
		"implementation":	{"implementation", "deployment", "execution", "application", "practice"},
	}

	// Determine goal type
	goalType := "general"
	for gtype, keywords := range goalKeywords {
		for _, keyword := range keywords {
			if strings.Contains(goalLower, keyword) {
				goalType = gtype
				break
			}
		}
	}

	// Find the most relevant section
	bestScore := 0
	bestChunk := 0

	for _, section := range sections {
		score := 0
		sectionLower := strings.ToLower(section.Heading)

		// Score based on goal type
		if keywords, ok := goalKeywords[goalType]; ok {
			for _, keyword := range keywords {
				if strings.Contains(sectionLower, keyword) {
					score += 10
				}
			}
		}

		// Bonus for exact matches
		if strings.Contains(sectionLower, goalType) {
			score += 5
		}

		// Update best match
		if score > bestScore {
			bestScore = score
			bestChunk = section.ChunkIndex
		}
	}

	log.Printf("[Dialogue] Selected chunk %d (score: %d) for goal type: %s",
		bestChunk, bestScore, goalType)

	return bestChunk
}

// PageSection represents a section in a document
type PageSection struct {
	Heading		string	`json:"heading"`
	ChunkIndex	int	`json:"chunk_index"`
}

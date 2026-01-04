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
	maxTokensPerCycle         int
	maxDurationMinutes        int
	maxThoughtsPerCycle       int
	actionRequirementInterval int
	noveltyWindowHours        int
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
) *Engine {
	return &Engine{
		storage:                   storage,
		embedder:                  embedder,
		stateManager:              stateManager,
		toolRegistry:              toolRegistry,
		llmURL:                    llmURL,
		llmModel:                  llmModel,
		maxTokensPerCycle:         maxTokensPerCycle,
		maxDurationMinutes:        maxDurationMinutes,
		maxThoughtsPerCycle:       maxThoughtsPerCycle,
		actionRequirementInterval: actionRequirementInterval,
		noveltyWindowHours:        noveltyWindowHours,
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
	
	// PHASE 1: Reflection
	log.Printf("[Dialogue] PHASE 1: Reflection")
	
	reflection, tokens, err := e.reflectOnRecentActivity(ctx)
	if err != nil {
		return StopReasonNaturalStop, fmt.Errorf("reflection failed: %w", err)
	}
	
	thoughtCount++
	totalTokens += tokens
	recentThoughts = append(recentThoughts, reflection)
	
	// Save thought record
	e.stateManager.SaveThought(ctx, &ThoughtRecord{
		CycleID:     state.CycleCount,
		ThoughtNum:  thoughtCount,
		Content:     reflection,
		TokensUsed:  tokens,
		ActionTaken: false,
		Timestamp:   time.Now(),
	})
	
	log.Printf("[Dialogue] Reflection: %s", truncate(reflection, 80))
	
	// Check token budget
	if totalTokens >= e.maxTokensPerCycle {
		metrics.ThoughtCount = thoughtCount
		metrics.ActionCount = actionCount
		metrics.TokensUsed = totalTokens
		return StopReasonMaxThoughts, nil
	}
	
	// PHASE 2: Goal Management
	log.Printf("[Dialogue] PHASE 2: Goal Management")
	
	// Identify knowledge gaps and failures
	gaps, err := e.identifyKnowledgeGaps(ctx)
	if err != nil {
		log.Printf("[Dialogue] WARNING: Failed to identify gaps: %v", err)
		gaps = []string{}
	}
	
	failures, err := e.identifyRecentFailures(ctx)
	if err != nil {
		log.Printf("[Dialogue] WARNING: Failed to identify failures: %v", err)
		failures = []string{}
	}
	
	// Update state with findings
	state.KnowledgeGaps = gaps
	state.RecentFailures = failures
	
	if len(gaps) > 0 {
		log.Printf("[Dialogue] Identified %d knowledge gaps", len(gaps))
	}
	if len(failures) > 0 {
		log.Printf("[Dialogue] Identified %d recent failures", len(failures))
	}
	
	// Form new goals if needed
	newGoals := e.formGoals(state)
	if len(newGoals) > 0 {
		state.ActiveGoals = append(state.ActiveGoals, newGoals...)
		metrics.GoalsCreated = len(newGoals)
		log.Printf("[Dialogue] Created %d new goals", len(newGoals))
		for _, goal := range newGoals {
			log.Printf("[Dialogue]   - %s (priority: %d)", goal.Description, goal.Priority)
		}
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
		
		// If no actions were executed, think about the goal and create new actions
		if !actionExecuted {
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
				
				// Create new actions based on thought (simplified)
				// In future, LLM could suggest specific actions
				if len(topGoal.Actions) == 0 {
					// Create initial search action
					newAction := Action{
						Description: fmt.Sprintf("Search for information about: %s", topGoal.Description),
						Tool:        ActionToolSearch,
						Status:      ActionStatusPending,
						Timestamp:   time.Now(),
					}
					topGoal.Actions = append(topGoal.Actions, newAction)
					log.Printf("[Dialogue] Created new action: %s", newAction.Description)
				}
			}
		}
		
		// Update goal progress based on completed actions
		completedActions := 0
		for _, action := range topGoal.Actions {
			if action.Status == ActionStatusCompleted {
				completedActions++
			}
		}
		
		if len(topGoal.Actions) > 0 {
			topGoal.Progress = float64(completedActions) / float64(len(topGoal.Actions))
		}
		
		if topGoal.Progress >= 1.0 {
			topGoal.Status = GoalStatusCompleted
			topGoal.Outcome = "neutral" // Will be evaluated later
			log.Printf("[Dialogue] ✓ Goal completed: %s", topGoal.Description)
			metrics.GoalsCompleted++
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
	
	// PHASE 5: Cleanup (move completed goals)
	completedCount := 0
	activeGoals := []Goal{}
	for _, goal := range state.ActiveGoals {
		if goal.Status == GoalStatusCompleted {
			state.CompletedGoals = append(state.CompletedGoals, goal)
			completedCount++
		} else {
			activeGoals = append(activeGoals, goal)
		}
	}
	state.ActiveGoals = activeGoals
	
	if completedCount > 0 {
		log.Printf("[Dialogue] Moved %d completed goals to history", completedCount)
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
	// For Phase 3.1, we'll just return empty
	// In Phase 3.2+, this would analyze failed searches, etc.
	return []string{}, nil
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
	
	// Create goals from knowledge gaps
	for _, gap := range state.KnowledgeGaps {
		goal := Goal{
			ID:          fmt.Sprintf("goal_%d", time.Now().UnixNano()),
			Description: fmt.Sprintf("Learn about: %s", gap),
			Source:      GoalSourceKnowledgeGap,
			Priority:    7,
			Created:     time.Now(),
			Progress:    0.0,
			Status:      GoalStatusActive,
			Actions:     []Action{},
		}
		goals = append(goals, goal)
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
		"temperature": 0.7,
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
	
	client := &http.Client{Timeout: 30 * time.Second}
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
		
		return result.Output, nil
		
	case ActionToolWebParse:
		// Phase 3.4: Web parsing not yet implemented
		return "", fmt.Errorf("web_parse tool not yet implemented")
		
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

// generateJitter returns a random duration within the jitter window
func generateJitter(windowMinutes int) time.Duration {
	if windowMinutes <= 0 {
		return 0
	}
	
	// Random value between -windowMinutes and +windowMinutes
	jitterMinutes := rand.Intn(windowMinutes*2+1) - windowMinutes
	return time.Duration(jitterMinutes) * time.Minute
}

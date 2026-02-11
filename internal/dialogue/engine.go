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
    totalTokens := 0

    // 1. MAINTENANCE: Decay & Cleanup
    e.decayMissions(state)

    // 2. USER INPUT SCAN: Detect new missions from chat
    userMission, err := e.detectUserMissions(ctx, state)
    if err != nil {
        log.Printf("[Dialogue] Error detecting user missions: %v", err)
    }
    if userMission != nil {
        e.manageMissionQueue(state, userMission)
    }

    // 3. PROMOTION: Ensure we have an Active Mission
    if state.ActiveMission == nil || state.ActiveMission.Status == "completed" {
        if len(state.QueuedMissions) > 0 {
            // Promote #1 from queue
            state.ActiveMission = &state.QueuedMissions[0]
            state.ActiveMission.Status = "active"
            state.QueuedMissions = state.QueuedMissions[1:]
            log.Printf("[Dialogue] ✓ Promoted Mission to Active: %s", state.ActiveMission.Description)
        } else {
            // AUTONOMOUS GENERATION: If totally empty, hallucinate a mission based on principles
            log.Printf("[Dialogue] No active or queued missions. Generating autonomous mission...")
            generatedMission := e.generateAutonomousMission(ctx, state)
            state.ActiveMission = &generatedMission
            log.Printf("[Dialogue] ✓ Generated Autonomous Mission: %s", state.ActiveMission.Description)
        }
    }

    // 4. REFLECTION: (Keep existing Phase 1)
    reasoning, principles, tokens, _, err := e.runPhaseReflection(ctx, state)
    if err != nil {
        return StopReasonNaturalStop, err
    }
    thoughtCount++
    totalTokens += tokens

    // 5. STRATEGIC INTEGRATION: Fit Gaps into Current Mission
    gaps := reasoning.KnowledgeGaps.ToSlice()
    if len(gaps) > 0 && state.ActiveMission != nil {
        // Add gaps as capabilities to the active mission
        for _, gap := range gaps {
            // Check if capability exists
            exists := false
            for _, cap := range state.ActiveMission.CapabilityMatrix {
                if strings.EqualFold(cap.Name, gap) {
                    exists = true
                    break
                }
            }
            if !exists {
                newCap := Capability{
                    Name:  gap,
                    Score: 0.0, // Start at zero
                }
                state.ActiveMission.CapabilityMatrix = append(state.ActiveMission.CapabilityMatrix, newCap)
                log.Printf("[Strategic] Added Gap as Capability to Active Mission: %s", gap)
            }
        }
    }

    // 6. EXECUTION: Work on Active Mission
    if state.ActiveMission != nil {
        log.Printf("[Dialogue] EXECUTING MISSION: %s", state.ActiveMission.Description)
        
        // Check if Capabilities need deriving
        if len(state.ActiveMission.CapabilityMatrix) == 0 {
            caps, err := e.deriveCapabilities(ctx, state.ActiveMission.Description)
            if err != nil {
                log.Printf("[Dialogue] Error deriving capabilities: %v", err)
            } else {
                state.ActiveMission.CapabilityMatrix = caps
            }
        }

        // Select Focus (Lowest Score)
        focus, _, _ := e.selectStrategicFocus(ctx, state.ActiveMission.CapabilityMatrix, state.ActiveMission.Description)
        
        // Create Goal for Focus
        // We reuse the Goal struct temporarily to drive the existing tool logic
        tempGoal := Goal{
            ID:          fmt.Sprintf("temp_goal_%d", time.Now().UnixNano()),
            Description: fmt.Sprintf("Improve capability: %s", focus),
            Source:      "mission",
            Priority:    10,
            Status:      GoalStatusActive,
            Actions:     []Action{},
        }

        // Run the Goal Pursuit Logic (Phase 3 from old code)
        // This executes one action and updates the capability score
        actionExecuted := false
        for i := range tempGoal.Actions {
            if tempGoal.Actions[i].Status == ActionStatusPending {
                // Execute Action
                result, err := e.executeAction(ctx, &tempGoal.Actions[i])
                if err != nil {
                    log.Printf("[Dialogue] Action failed: %v", err)
                }
                tempGoal.Actions[i].Result = result
                tempGoal.Actions[i].Status = ActionStatusCompleted
                
                // Update Capability Score
                if tempGoal.Actions[i].Tool == ActionToolSimulate {
                    // If simulation, update matrix
                    // (This part links back to the metadata parsing we did earlier)
                }
                actionExecuted = true
                break
            }
        }
        
        // If no actions exist, create one (Search or Simulate)
        if !actionExecuted {
            // Simple heuristic: Alternate between Research and Simulation
            // For now, default to Search to gather knowledge first
            newAction := Action{
                Description: focus,
                Tool:        ActionToolSearch,
                Status:      ActionStatusPending,
            }
            tempGoal.Actions = append(tempGoal.Actions, newAction)
            // In next cycle, this action will be picked up and executed
        }
    }

    // Update Metrics
    metrics.ThoughtCount = thoughtCount
    metrics.TokensUsed = totalTokens
    
    return StopReasonNaturalStop, nil
}

// generateAutonomousMission creates a mission based on principles or profile when idle
func (e *Engine) generateAutonomousMission(ctx context.Context, state *InternalState) Mission {
    // Heuristic: Look at principles or user profile for inspiration
    profile, _ := e.BuildUserProfile(ctx)
    
    desc := "Self-improvement cycle"
    if len(profile.TopTopics) > 0 {
        desc = fmt.Sprintf("Deepen understanding of %s", profile.TopTopics[0])
    }

    return Mission{
        ID:          fmt.Sprintf("mission_%d", time.Now().UnixNano()),
        Description: desc,
        Source:      "ai",
        Priority:    0.5,
        Status:      "active",
        CreatedAt:   time.Now(),
        UpdatedAt:   time.Now(),
    }
}

package goal

import (
    "context"
    "log"
	"fmt"
	"time"
	"strings"
    "sync"
)

// ActionExecutor is the interface bridging the Goal system to the Tool system in the Dialogue Engine
type ActionExecutor interface {
    ExecuteToolAction(ctx context.Context, tool string, params map[string]interface{}) (string, error)
}

// Orchestrator manages the autonomous goal cycle
type Orchestrator struct {
    mu sync.Mutex

    // Components
    Repo         GoalRepository
    SkillRepo    SkillRepository
    Factory      *Factory
    StateManager *StateManager
    Validator    *ValidationEngine
    Selector     *GoalSelector
    Reviewer     *ReviewProcessor
    Calculator   *Calculator
    Monitor      *ProgressMonitor
    Archive      *ArchiveManager

    // Intelligence (Milestone 3)
    DerivationEngine *DerivationEngine
    TreeBuilder      *TreeBuilder

    // Milestone 5: Edge Cases & Logging
    EdgeCaseHandler  *EdgeCaseHandler
    Logger           *GoalSystemLogger
    
    // Performance Optimization
    cycleCounter     int

    // Bridges
    Executor       ActionExecutor // Implemented by Dialogue Engine
    availableTools []string       // List of tools from Dialogue Engine
    embedder       Embedder       // Embedder for semantic operations
}

// SetAvailableTools updates the list of tools available for goal validation
func (o *Orchestrator) SetAvailableTools(tools []string) {
    o.mu.Lock()
    defer o.mu.Unlock()
    o.availableTools = tools
}

// SetEmbedder connects the semantic embedding service
func (o *Orchestrator) SetEmbedder(embedder Embedder) {
    o.embedder = embedder
}

// NewOrchestrator creates a new goal orchestrator
func NewOrchestrator(
    repo GoalRepository,
    skillRepo SkillRepository,
    factory *Factory,
    stateManager *StateManager,
    selector *GoalSelector,
    reviewer *ReviewProcessor,
    calc *Calculator,
    monitor *ProgressMonitor,
    derivationEngine *DerivationEngine,
    treeBuilder *TreeBuilder,
    embedder Embedder, // ADDED: Embedder interface
) *Orchestrator {
    // Initialize Logger (Step 23)
    logger := NewGoalSystemLogger()

    // Register Logger as a listener for State Transitions
    stateManager.AddListener(func(goalID string, from, to GoalState, ts time.Time) {
        logger.LogStateTransition(goalID, from, to, "Lifecycle Event")
    })

    // Initialize ValidationEngine with Embedder and Repo to enable semantic duplicate detection
    validator := NewValidationEngine(embedder, repo)

    return &Orchestrator{
        Repo:             repo,
        SkillRepo:        skillRepo,
        Factory:          factory,
        StateManager:     stateManager,
        Validator:        validator,
        Selector:         selector,
        Reviewer:         reviewer,
        Calculator:       calc,
        Monitor:          monitor,
        Archive:          NewArchiveManager(repo),
        DerivationEngine: derivationEngine,
        TreeBuilder:      treeBuilder,
        EdgeCaseHandler:  NewEdgeCaseHandler(repo),
        Logger:           logger,
    }
}

// --- User Interaction API Methods (Milestone 5) ---

// GetActiveGoal returns the currently active goal, if any.
func (o *Orchestrator) GetActiveGoal(ctx context.Context) (*Goal, error) {
    goals, err := o.Repo.GetByState(ctx, StateActive)
    if err != nil {
        return nil, err
    }
    if len(goals) == 0 {
        return nil, nil
    }
    return goals[0], nil
}

// GetQueuedGoals returns all goals in QUEUED state.
func (o *Orchestrator) GetQueuedGoals(ctx context.Context) ([]*Goal, error) {
    return o.Repo.GetByState(ctx, StateQueued)
}

// GetGoalDetails returns a specific goal by ID.
func (o *Orchestrator) GetGoalDetails(ctx context.Context, id string) (*Goal, error) {
    return o.Repo.Get(ctx, id)
}

// StopGoal archives a goal specified by the user.
func (o *Orchestrator) StopGoal(ctx context.Context, id string) error {
    g, err := o.Repo.Get(ctx, id)
    if err != nil {
        return err
    }
    
    o.StateManager.Transition(g, StateArchived)
    g.ArchiveReason = ArchiveUserCancelled
    
    return o.Repo.Store(ctx, g)
}

// PrioritizeGoal boosts the priority of a goal.
func (o *Orchestrator) PrioritizeGoal(ctx context.Context, id string, boost int) error {
    g, err := o.Repo.Get(ctx, id)
    if err != nil {
        return err
    }
    
    g.CurrentPriority += boost
    if g.CurrentPriority > 100 {
        g.CurrentPriority = 100
    }
    
    return o.Repo.Store(ctx, g)
}

// SetExecutor connects the orchestrator to the Dialogue Engine's tool execution
func (o *Orchestrator) SetExecutor(exec ActionExecutor) {
    o.Executor = exec
}

// ExecuteCycle runs one full iteration of the autonomous goal system
func (o *Orchestrator) ExecuteCycle(ctx context.Context) error {
    o.mu.Lock()
    defer o.mu.Unlock()

    // Performance Optimization: Increment cycle counter
    o.cycleCounter++
    
    // Log Cycle Start
    o.Logger.LogGoalDecision("CYCLE_START", "Initiating maintenance and execution", nil)

    // 0. Derivation Phase: Generate new proposals from recent memories
    // Optimization: Run derivation periodically (e.g., every 5 cycles) to save resources
    if o.DerivationEngine != nil && o.cycleCounter % 5 == 0 {
        o.Logger.LogGoalDecision("DERIVATION_START", "Analyzing memories for new proposals", nil)
        proposals, err := o.DerivationEngine.AnalyzeMemories(ctx, 5)
        if err != nil {
            o.Logger.LogError("Derivation", err, nil)
        } else {
            for _, pg := range proposals.Goals {
                // Ensure valid state before storing
                pg.State = StateProposed
                if err := o.Repo.Store(ctx, pg); err != nil {
                    o.Logger.LogError("StoreProposal", err, map[string]interface{}{"goal_id": pg.ID})
                } else {
                    o.Logger.LogGoalDecision("PROPOSAL_DERIVED", "Created new goal from memory", []string{pg.ID})
                }
            }
        }
    }

    // PERFORMANCE: Fetch all states once to minimize DB hits
    proposedGoals, err := o.Repo.GetByState(ctx, StateProposed)
    if err != nil {
        return err
    }
    
    queuedGoals, err := o.Repo.GetByState(ctx, StateQueued)
    if err != nil {
        return err
    }
    
    activeGoals, err := o.Repo.GetByState(ctx, StateActive)
    if err != nil {
        return err
    }

    // 1. Process Proposals
    // Pass queuedGoals and availableTools to avoid re-fetching inside validation checks
    if err := o.processValidationQueue(ctx, proposedGoals, queuedGoals, o.availableTools); err != nil {
        o.Logger.LogError("ValidationPhase", err, nil)
    }

    // 2. Priority Maintenance
    // Pass queuedGoals to avoid re-fetching for decay logic
    if err := o.applyPriorityMaintenance(ctx, queuedGoals); err != nil {
        o.Logger.LogError("MaintenancePhase", err, nil)
    }

    // Filter queuedGoals immediately after maintenance to ensure we have a clean list
    // for both Selection and Execution phases.
    validQueued := make([]*Goal, 0)
    for _, g := range queuedGoals {
        if g.State == StateQueued {
            validQueued = append(validQueued, g)
        }
    }

    // 3. Goal Selection
    var activeGoal *Goal
    if len(activeGoals) > 0 {
        activeGoal = activeGoals[0] // Assuming single active goal
    } else {
        if len(validQueued) > 0 {
            selected := o.Selector.SelectNextGoal(validQueued)
            if selected != nil {
                if err := o.StateManager.Transition(selected, StateActive); err == nil {
                    if err := o.Repo.Store(ctx, selected); err != nil {
                        o.Logger.LogError("ActivateGoalStore", err, map[string]interface{}{"goal_id": selected.ID})
                    } else {
                        activeGoal = selected
                        o.Logger.LogGoalDecision("GOAL_ACTIVATED", "Selected from queue", []string{selected.ID})
                    }
                }
            }
        }
    }

    // 4. Active Goal Execution
    if activeGoal != nil {
        // Pass valid queued goals for review comparisons
        if err := o.executeActiveGoal(ctx, activeGoal, validQueued); err != nil {
            o.Logger.LogError("ExecuteActiveGoal", err, map[string]interface{}{"goal_id": activeGoal.ID})
        }
    }

    // 5. Skill Maintenance
    // Optimization: Run skill maintenance only every 10 cycles to reduce load
    if o.cycleCounter % 10 == 0 {
        if err := o.maintainSkills(ctx); err != nil {
            o.Logger.LogError("SkillMaintenance", err, nil)
        }
    }

    return nil
}

// Refactored to accept pre-fetched lists and tools to optimize DB access
func (o *Orchestrator) processValidationQueue(ctx context.Context, proposed []*Goal, existing []*Goal, availableTools []string) error {
    for _, g := range proposed {
        res := o.Validator.Validate(g, availableTools, existing)
        
        if res.IsValid {
            if err := o.StateManager.Transition(g, StateQueued); err == nil {
                existing = append(existing, g)
            }
            o.Repo.Store(ctx, g)
        } else {
            // Handle specific validation actions
            switch res.Action {
            case "MERGE":
                // Strengthen the existing goal
                for _, eg := range existing {
                    if strings.Contains(res.Reason, eg.ID) {
                        o.Calculator.ApplyStrengthening(eg)
                        o.Repo.Store(ctx, eg)
                        o.Logger.LogGoalDecision("MERGE", "Strengthened existing goal: "+eg.ID, nil)
                        break
                    }
                }
                // Archive the new proposal as duplicate
                o.StateManager.Transition(g, StateArchived)
                g.ArchiveReason = ArchiveDuplicate
                o.Repo.Store(ctx, g)

            case "SUBSUME":
                // MDD 9.2: Add as sub-goal to existing parent.
                // Parse Parent ID from Reason string ("SUB_GOAL: Is subset of existing goal <ID>")
                parts := strings.Split(res.Reason, " ")
                if len(parts) > 0 {
                    parentID := parts[len(parts)-1]
                    
                    parentGoal, err := o.Repo.Get(ctx, parentID)
                    if err != nil {
                        o.Logger.LogError("SubsumeParentFetch", err, map[string]interface{}{"parent_id": parentID})
                        // Fallback: If parent fetch fails, archive the proposal to avoid orphan goals
                        o.StateManager.Transition(g, StateArchived)
                        g.ArchiveReason = ArchiveValidationFailed
                    } else {
                        // Create new SubGoal struct
                        newSubGoal := SubGoal{
                            ID:          fmt.Sprintf("%d.%d", len(parentGoal.SubGoals)+1, 0),
                            Title:       g.Title,
                            Description: g.Description,
                            Status:      SubGoalPending,
                            // Inherit parent context
                        }
                        
                        parentGoal.SubGoals = append(parentGoal.SubGoals, newSubGoal)
                        
                        // Save updated parent
                        if err := o.Repo.Store(ctx, parentGoal); err != nil {
                            o.Logger.LogError("SubsumeStore", err, nil)
                        } else {
                            o.Logger.LogGoalDecision("SUBSUME_SUCCESS", "Added as sub-goal to "+parentID, []string{g.ID})
                        }
                        
                        // Archive the proposal so it doesn't clutter the queue
                        o.StateManager.Transition(g, StateArchived)
                        g.ArchiveReason = ArchiveDuplicate
                    }
                }
                o.Repo.Store(ctx, g)

            case "PARENT_DEMOTION":
                // MDD 9.2: Proposed goal absorbs existing goal.
                // Logic: The proposed goal is valid (IsValid=true), but an existing goal should be demoted.
                // 1. Create the new goal (it's valid).
                g.State = StateQueued
                o.Repo.Store(ctx, g)
                
                // 2. Find the existing goal mentioned in the reason and demote it.
                // Reason format: "PARENT_DEMOTION: Existing goal <ID> should become sub-goal"
                parts := strings.Split(res.Reason, " ")
                if len(parts) > 0 {
                    existingID := parts[len(parts)-1]
                    existingGoal, err := o.Repo.Get(ctx, existingID)
                    if err == nil {
                        // Demote existing goal to a sub-goal of the new goal
                        newSub := SubGoal{
                            ID:          fmt.Sprintf("%d", len(g.SubGoals)+1),
                            Title:       existingGoal.Title,
                            Description: existingGoal.Description,
                            Status:      SubGoalPending,
                        }
                        g.SubGoals = append(g.SubGoals, newSub)
                        
                        // Archive the old goal (now absorbed)
                        o.StateManager.Transition(existingGoal, StateArchived)
                        existingGoal.ArchiveReason = ArchiveDuplicate // Absorbed
                        o.Repo.Store(ctx, existingGoal)
                        o.Repo.Store(ctx, g) // Update new parent
                        o.Logger.LogGoalDecision("PARENT_DEMOTION", "Demoted "+existingID+" to sub-goal of "+g.ID, nil)
                    }
                }

            default: // "ARCHIVE" or other failures
                reason := ArchiveValidationFailed
                if strings.Contains(res.Reason, "MISSING_TOOLS") {
                    reason = ArchiveMissingTools
                }
                o.StateManager.Transition(g, StateArchived)
                g.ArchiveReason = reason
                o.Logger.LogGoalDecision("VALIDATION_FAILED", string(reason), []string{g.ID})
                o.Repo.Store(ctx, g)
            }
        }
    }
    return nil
}

// Refactored to accept pre-fetched list
func (o *Orchestrator) applyPriorityMaintenance(ctx context.Context, queued []*Goal) error {
    for _, g := range queued {
        oldP := g.CurrentPriority
        // Applying 1 cycle decay for this tick
        o.Calculator.ApplyDecay(g, 1)
        
        // Log priority change if it occurred
        if oldP != g.CurrentPriority {
             o.Logger.LogPriorityChange(g.ID, oldP, g.CurrentPriority, "Cycle Decay")
        }

        if g.CurrentPriority < 10 {
            o.StateManager.Transition(g, StateArchived)
            g.ArchiveReason = ArchivePriorityDecay
            // State transition logged by listener
        }
        o.Repo.Store(ctx, g)
    }
    return nil
}

// Refactored to accept queued goals for review logic
func (o *Orchestrator) executeActiveGoal(ctx context.Context, g *Goal, queued []*Goal) error {
    // 1. Check Review Triggers (Time or Stagnation)
    // Calculate progress and update stagnation tracking
    previousProgress := g.ProgressPercentage
    currentProgress := o.Monitor.CalculateProgressPercentage(g)
    
    // Update stagnation counter based on progress changes
    if currentProgress > previousProgress {
        o.Monitor.ResetStagnation(g)
    } else {
        o.Monitor.IncrementStagnation(g)
    }
    
    needsReview := o.Monitor.DetectStagnation(g)
    
    if needsReview {
        o.StateManager.Transition(g, StateReviewing)
        o.Repo.Store(ctx, g)
        
        // Use passed-in queued list
        outcome := o.Reviewer.ExecuteReview(g, queued)
        
        o.Logger.LogReviewOutcome(g.ID, outcome.Decision, outcome.Reason)
        
        switch outcome.Decision {
        case "COMPLETE":
            o.StateManager.Transition(g, StateCompleted)
case "DEMOTE":
    // MDD Table 13: Demoted goals return to QUEUED state.
    o.StateManager.Transition(g, StateQueued) 
    o.Repo.Store(ctx, g)
            // Activate next is handled in next cycle selection
        case "REPLAN":
            o.StateManager.Transition(g, StateActive)
            // Clear sub-goals for replan (TreeBuilder would be called here in full logic)
            g.SubGoals = []SubGoal{}
        case "CONTINUE":
            o.StateManager.Transition(g, StateActive)
        case "ARCHIVE":
            o.StateManager.Transition(g, StateArchived)
            g.ArchiveReason = ArchiveImpossible
        }
        o.Repo.Store(ctx, g)
        return nil
    }

    // 2. Plan Execution (Ensure Tree exists)
    if len(g.SubGoals) == 0 {
        // No plan yet - invoke TreeBuilder (Intelligence Layer)
        if o.TreeBuilder != nil {
            log.Printf("[Orchestrator] No subgoals found. Invoking TreeBuilder for %s", g.ID)
            if err := o.TreeBuilder.DecomposeGoal(ctx, g); err != nil {
                log.Printf("[Orchestrator] ERROR: TreeBuilder failed: %v", err)
                // If we can't plan, we can't proceed. Force review.
                o.StateManager.Transition(g, StateReviewing)
                return o.Repo.Store(ctx, g)
            }
            // Save the new plan
            if err := o.Repo.Store(ctx, g); err != nil {
                return err
            }
        } else {
            log.Printf("[Orchestrator] WARNING: No TreeBuilder configured. Cannot decompose goal.")
            return nil
        }
    }
    
    // Re-check if tree was built
    if len(g.SubGoals) == 0 {
         log.Printf("[Orchestrator] No subgoals available post-planning. Skipping.")
         return nil
    }

    // Find next pending subgoal whose dependencies are met
    var activeSG *SubGoal
    for i := range g.SubGoals {
        if g.SubGoals[i].Status == SubGoalPending {
            // Check dependencies
            if o.areDependenciesMet(g, g.SubGoals[i].Dependencies) {
                activeSG = &g.SubGoals[i]
                break
            }
        }
    }

    if activeSG == nil {
        // All subgoals done?
        g.ProgressPercentage = 100.0
        o.StateManager.Transition(g, StateCompleted)
        o.Repo.Store(ctx, g)
        return nil
    }

    // Milestone 5: Strategy Loop Prevention
    if o.EdgeCaseHandler != nil {
        if o.EdgeCaseHandler.HandleStrategyLoop(ctx, g, activeSG.Description) {
            log.Printf("[Orchestrator] Skipping sub-goal due to strategy loop: %s", activeSG.Description)
            activeSG.Status = SubGoalSkipped
            activeSG.FailureReason = "Strategy loop detected"
            o.Repo.Store(ctx, g)
            return nil
        }
    }

    // Execute via Tool Bridge
if o.Executor != nil {
    activeSG.Status = SubGoalActive
    log.Printf("[Orchestrator] Executing SubGoal: %s", activeSG.Description)

    // Determine Tool: Use specific tool from SubGoal metadata, or infer from ActionType
    toolName := "search" // Default fallback
    if activeSG.ToolName != "" {
        toolName = activeSG.ToolName
    } else {
        // Fallback heuristics based on ActionType
        switch activeSG.ActionType {
        case ActionExecuteTool:
            toolName = "search" // Generic execution
        case ActionResearch:
            toolName = "search"
        case ActionPractice:
            toolName = "simulation" 
        }
    }

        start := time.Now()
        result, err := o.Executor.ExecuteToolAction(ctx, toolName, map[string]interface{}{
            "query": activeSG.Description,
        })
        duration := time.Since(start)

        if err != nil {
            activeSG.Status = SubGoalFailed
            activeSG.FailureReason = err.Error()
            o.Logger.LogSubGoalExecution(activeSG.ID, "FAILED: "+err.Error(), duration)
            
            // Milestone 5: Handle Sub-Goal Failure
            if o.EdgeCaseHandler != nil {
                outcome := o.EdgeCaseHandler.HandleSubGoalFailure(ctx, activeSG, g)
                if outcome == "CRITICAL_FAILURE" {
                    o.Logger.LogGoalDecision("CRITICAL_FAILURE", "SubGoal failure critical, forcing review", []string{g.ID, activeSG.ID})
                    // Force state transition to REVIEWING to let ReviewProcessor decide fate
                    o.StateManager.Transition(g, StateReviewing)
                }
            }
        } else {
            activeSG.Status = SubGoalCompleted
            activeSG.Outcome = result
            o.Logger.LogSubGoalExecution(activeSG.ID, "SUCCESS", duration)
        }
        
        // Save progress
        o.Repo.Store(ctx, g)
    }

    return nil
}

// areDependenciesMet checks if all prerequisite sub-goals are completed.
func (o *Orchestrator) areDependenciesMet(g *Goal, dependencies []string) bool {
    if len(dependencies) == 0 {
        return true
    }
    
    // Create a map of completed sub-goal IDs for fast lookup
    completedMap := make(map[string]bool)
    for _, sg := range g.SubGoals {
        if sg.Status == SubGoalCompleted {
            completedMap[sg.ID] = true
        }
    }
    
    for _, depID := range dependencies {
        if !completedMap[depID] {
            return false
        }
    }
    return true
}

func (o *Orchestrator) maintainSkills(ctx context.Context) error {
    skills, err := o.SkillRepo.GetAll(ctx)
    if err != nil {
        return err
    }
    
    for _, s := range skills {
        // Simple decay logic: Reduce freshness by 1 per cycle
        s.FreshnessScore -= 1
        if s.FreshnessScore < 0 { s.FreshnessScore = 0 }
        
        // Save back
        o.SkillRepo.Store(ctx, s)
    }
    return nil
}

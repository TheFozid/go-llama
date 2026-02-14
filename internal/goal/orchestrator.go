package goal

import (
    "context"
    "fmt"
    "log"
    "sync"
    "time"
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

    // Milestone 5: Edge Cases
    EdgeCaseHandler  *EdgeCaseHandler

    // Bridges
    Executor ActionExecutor // Implemented by Dialogue Engine
}

// NewOrchestrator creates a new goal orchestrator
func NewOrchestrator(
    repo GoalRepository,
    skillRepo SkillRepository,
    factory *Factory,
    stateManager *StateManager,
    validator *ValidationEngine,
    selector *GoalSelector,
    reviewer *ReviewProcessor,
    calc *Calculator,
    monitor *ProgressMonitor,
    derivationEngine *DerivationEngine,
    treeBuilder *TreeBuilder,
) *Orchestrator {
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

    log.Println("[Orchestrator] Starting Goal Cycle")

    // 1. Process Proposals (Derivation would feed this in Phase 3+)
    // For now, we assume goals are created externally or via DerivationEngine hooked in main
    // We scan for PROPOSED goals and validate them.
    if err := o.processValidationQueue(ctx); err != nil {
        log.Printf("[Orchestrator] ERROR in validation phase: %v", err)
    }

    // 2. Priority Maintenance (Decay)
    if err := o.applyPriorityMaintenance(ctx); err != nil {
        log.Printf("[Orchestrator] ERROR in maintenance phase: %v", err)
    }

    // 3. Goal Selection
    activeGoals, err := o.Repo.GetByState(ctx, StateActive)
    if err != nil {
        return err
    }

    var activeGoal *Goal
    if len(activeGoals) > 0 {
        activeGoal = activeGoals[0] // Assuming single active goal
    } else {
        // Select new goal if none active
        queuedGoals, err := o.Repo.GetByState(ctx, StateQueued)
        if err != nil {
            return err
        }
        if len(queuedGoals) > 0 {
            selected := o.Selector.SelectNextGoal(queuedGoals)
            if selected != nil {
                if err := o.StateManager.Transition(selected, StateActive); err == nil {
                    if err := o.Repo.Store(ctx, selected); err != nil {
                        log.Printf("[Orchestrator] ERROR activating goal: %v", err)
                    } else {
                        activeGoal = selected
                        log.Printf("[Orchestrator] Activated goal: %s", selected.Description)
                    }
                }
            }
        }
    }

    // 4. Active Goal Execution
    if activeGoal != nil {
        if err := o.executeActiveGoal(ctx, activeGoal); err != nil {
            log.Printf("[Orchestrator] ERROR executing goal %s: %v", activeGoal.ID, err)
        }
    }

    // 5. Skill Maintenance
    if err := o.maintainSkills(ctx); err != nil {
        log.Printf("[Orchestrator] ERROR in skill maintenance: %v", err)
    }

    return nil
}

func (o *Orchestrator) processValidationQueue(ctx context.Context) error {
    proposed, err := o.Repo.GetByState(ctx, StateProposed)
    if err != nil {
        return err
    }

    // Note: In full implementation, we'd check tools available. 
    // For now, using empty list for tools check.
    availableTools := []string{} 

    for _, g := range proposed {
        existing, _ := o.Repo.GetByState(ctx, StateQueued) // Simplified check
        
        res := o.Validator.Validate(g, availableTools, existing)
        
        if res.IsValid {
            o.StateManager.Transition(g, StateQueued)
            log.Printf("[Orchestrator] Validated goal -> QUEUED: %s", g.Description)
        } else {
            // Handle Archive logic
            reason := ArchiveValidationFailed
            if res.Action == "ARCHIVE" {
                reason = ArchiveImpossible // simplistic mapping
            }
            o.StateManager.Transition(g, StateArchived)
            g.ArchiveReason = reason
            log.Printf("[Orchestrator] Archived goal (%s): %s", reason, g.Description)
        }
        o.Repo.Store(ctx, g)
    }
    return nil
}

func (o *Orchestrator) applyPriorityMaintenance(ctx context.Context) error {
    // Decay QUEUED goals
    queued, err := o.Repo.GetByState(ctx, StateQueued)
    if err != nil {
        return err
    }
    for _, g := range queued {
        // Applying 1 cycle decay for this tick
        o.Calculator.ApplyDecay(g, 1)
        if g.CurrentPriority < 10 {
            o.StateManager.Transition(g, StateArchived)
            g.ArchiveReason = ArchivePriorityDecay
            log.Printf("[Orchestrator] Archived goal due to decay: %s", g.Description)
        }
        o.Repo.Store(ctx, g)
    }
    return nil
}

func (o *Orchestrator) executeActiveGoal(ctx context.Context, g *Goal) error {
    // 1. Check Review Triggers (Time or Stagnation)
    // Simplification: Check if stagnation detected
    o.Monitor.CalculateProgressPercentage(g)
    
    needsReview := o.Monitor.DetectStagnation(g)
    
    if needsReview {
        log.Printf("[Orchestrator] Goal entered REVIEWING: %s", g.Description)
        o.StateManager.Transition(g, StateReviewing)
        o.Repo.Store(ctx, g)
        
        queued, _ := o.Repo.GetByState(ctx, StateQueued)
        outcome := o.Reviewer.ExecuteReview(g, queued)
        
        log.Printf("[Orchestrator] Review Outcome: %s (Reason: %s)", outcome.Decision, outcome.Reason)
        
        switch outcome.Decision {
        case "COMPLETE":
            o.StateManager.Transition(g, StateCompleted)
        case "DEMOTE":
            o.StateManager.Transition(g, StatePaused) // Pause current
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

    // 2. Execute Next Sub-Goal
    // Note: TreeBuilder logic should populate SubGoals. 
    // We assume SubGoals exist or we fail gracefully.
    if len(g.SubGoals) == 0 {
        // No plan yet - in full version, TreeBuilder is invoked here.
        // For integration, we mark as complete or just log.
        log.Printf("[Orchestrator] No subgoals for active goal. Skipping execution.")
        return nil
    }

    // Find next pending subgoal
    var activeSG *SubGoal
    for i := range g.SubGoals {
        if g.SubGoals[i].Status == SubGoalPending {
            activeSG = &g.SubGoals[i]
            break
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
        // Here we just use a generic tool call for demonstration.
        // Tool is determined by subgoal metadata or LLM.
        toolName := "search" // Default fallback
        if activeSG.ToolCallsEstimate > 0 {
            // Heuristic logic to pick tool could go here
        }

        result, err := o.Executor.ExecuteToolAction(ctx, toolName, map[string]interface{}{
            "query": activeSG.Description,
        })

        if err != nil {
            activeSG.Status = SubGoalFailed
            activeSG.FailureReason = err.Error()
            
            // Milestone 5: Handle Sub-Goal Failure
            if o.EdgeCaseHandler != nil {
                outcome := o.EdgeCaseHandler.HandleSubGoalFailure(ctx, activeSG, g)
                if outcome == "CRITICAL_FAILURE" {
                    log.Printf("[Orchestrator] Critical sub-goal failure detected. Forcing review.")
                    // Force state transition to REVIEWING to let ReviewProcessor decide fate
                    o.StateManager.Transition(g, StateReviewing)
                }
            }
        } else {
            activeSG.Status = SubGoalCompleted
            activeSG.Outcome = result
        }
        
        // Save progress
        o.Repo.Store(ctx, g)
    }

    return nil
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

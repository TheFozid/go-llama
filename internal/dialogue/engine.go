package dialogue

import (
	"context"
	"fmt"
	"log"
	"time"

	"go-llama/internal/memory"
	"go-llama/internal/tools"
	"gorm.io/gorm"
    "go-llama/internal/goal"
    "github.com/qdrant/go-client/qdrant"
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
    // MILESTONE 4: Goal System Integration
    goalOrchestrator		*goal.Orchestrator
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
    // MILESTONE 4: Initialize Goal System Components
    // We need the raw Qdrant client and the Embedder.
    
    // 1. Get Qdrant Client from Storage
    var qdrantClient *qdrant.Client
    if sc, ok := interface{}(storage).(*memory.Storage); ok {
        qdrantClient = sc.Client 
    }
    
    // 2. Create Embedder Adapter for Goal Package
    // We use the package-level embedderAdapter struct defined at the bottom of this file.
    adapter := &embedderAdapter{emb: embedder}
    // Compile-time check to ensure it satisfies the interface
    var _ goal.Embedder = adapter
    
    // 3. Initialize Goal Repository with Embedder (Fixes Semantic Search)
    goalRepo, err := memory.NewGoalRepository(qdrantClient, "goals", adapter)
    if err != nil {
        log.Printf("[Engine] WARNING: Failed to init GoalRepo: %v", err)
    }
    
skillRepo, err := memory.NewSkillRepository(qdrantClient, "skills", adapter)
    if err != nil {
        log.Printf("[Engine] WARNING: Failed to init SkillRepo: %v", err)
    }

    // Wire up the Goal Subsystem
    
    // 1. LLM Adapter (Milestone 3 Integration)
    // We assert that llmClient implements the LLMCaller interface required by the adapter.
    var llmAdapter goal.LLMService
    if llmClient != nil {
        // Type assertion: convert interface{} to goal.LLMCaller
        caller, ok := llmClient.(goal.LLMCaller)
        if !ok {
            log.Printf("[Engine] WARNING: llmClient does not implement goal.LLMCaller. Goal System cannot use LLM.")
        } else {
            adapter := goal.NewQueueLLMAdapter(caller, llmURL, llmModel)
            llmAdapter = adapter
            log.Printf("[Engine] Goal System connected to LLM Queue")
        }
    } else {
        log.Printf("[Engine] WARNING: Goal System has no LLM connection")
    }
    
    factory := goal.NewFactory(nil)
    stateMgr := goal.NewStateManager()
    calc := goal.NewCalculator(nil)
    selector := goal.NewGoalSelector(calc)
    monitor := goal.NewProgressMonitor() 
    reviewer := goal.NewReviewProcessor(selector, calc, monitor)
    // validator := goal.NewValidationEngine() // REMOVED: Created inside Orchestrator

    // Initialize Intelligence Components (Milestone 3)
    // We use the package-level memorySearcherAdapter struct defined at the bottom of this file.
    searcherAdapter := &memorySearcherAdapter{st: storage, emb: embedder}
    
    var derivationEngine *goal.DerivationEngine
    var treeBuilder *goal.TreeBuilder
    
    // Connect Intelligence components if LLM is available
    if llmAdapter != nil {
        treeBuilder = goal.NewTreeBuilder(llmAdapter)
        // Connect Derivation Engine with LLM, Searcher, and Embedder
        derivationEngine = goal.NewDerivationEngine(llmAdapter, searcherAdapter, adapter)
    }
    
    // Orchestrator initialization (injecting embedder for validation)
    orchestrator := goal.NewOrchestrator(goalRepo, skillRepo, factory, stateMgr, selector, reviewer, calc, monitor, derivationEngine, treeBuilder, adapter)
    
    // Post-Initialization Wiring
// Set available tools from registry
if toolRegistry != nil {
    // IMPORTANT: We must pass actual tool names for Viability Validation to work.
    // Since ContextualRegistry API is not confirmed, we list known default tools 
    // to prevent validation from archiving everything as MISSING_TOOLS.
    knownTools := []string{"search", "browser", "execute_bash", "memory_recall", "simulate"}
    orchestrator.SetAvailableTools(knownTools) 
}
    
    // Set embedder for Orchestrator (used in semantic operations if needed directly)
    orchestrator.SetEmbedder(adapter)

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
        // Milestone 4
        goalOrchestrator:		orchestrator,
    }
}

// GetOrchestrator exposes the goal system for API handlers (Milestone 5)
func (e *Engine) GetOrchestrator() *goal.Orchestrator {
    return e.goalOrchestrator
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
    // MAINTENANCE: Apply time-based confidence decay to principles
    if err := memory.ApplyConfidenceDecay(e.db); err != nil {
        log.Printf("[Dialogue] WARNING: Failed to apply principle decay: %v", err)
    }

    // MILESTONE 4: Handoff to Goal Orchestrator
    // The Orchestrator handles validation, selection, and execution.
    // It calls back into the Engine for Tool Execution (via interface).
    if e.goalOrchestrator != nil {
        // Connect the bridge for this cycle
        e.goalOrchestrator.SetExecutor(e) 
        
        if err := e.goalOrchestrator.ExecuteCycle(ctx); err != nil {
            log.Printf("[Dialogue] Goal Cycle Error: %v", err)
        }
    } else {
        log.Printf("[Dialogue] WARNING: GoalOrchestrator not initialized")
    }

    // Legacy thought process for reflection (Phase 1) can remain here if desired,
    // but the core Goal logic is now delegated.
    
    // For the purpose of the migration, we are replacing the manual goal logic
    // but keeping the reflection logic which drives the "Proposals" for the new system.
    
    // Execute Phase 1 Reflection to generate thoughts/proposals
    thoughtCount := 0
    totalTokens := 0
    
    reasoning, principles, phaseTokens, reflectionText, err := e.runPhaseReflection(ctx, state)
    if err != nil {
        return StopReasonNaturalStop, err
    }
    
    thoughtCount++
    totalTokens += phaseTokens
    
    // Save thought record
    e.stateManager.SaveThought(ctx, &ThoughtRecord{
        CycleID:	state.CycleCount,
        ThoughtNum:	thoughtCount,
        Content:	reflectionText,
        TokensUsed:	phaseTokens,
        ActionTaken:	false,
        Timestamp:	time.Now(),
    })

    // TODO: Connect Reflection output to Goal Derivation Engine (Milestone 3 Integration)
    // For now, we assume DerivationEngine is called separately or the Orchestrator has it.
    // In this integration, we are relying on the Orchestrator to drive the state.
    
    _ = reasoning // Avoid unused variable error for now
    _ = principles

    // Update metrics for this simplified cycle
    metrics.ThoughtCount = thoughtCount
    metrics.ActionCount = 0 // Action counting is now internal to Orchestrator
    metrics.TokensUsed = totalTokens

    return StopReasonNaturalStop, nil
}

// ExecuteToolAction implements the goal.ActionExecutor interface.
// It bridges the autonomous Goal system to the Dialogue Engine's tool registry.
func (e *Engine) ExecuteToolAction(ctx context.Context, tool string, params map[string]interface{}) (string, error) {
    if e.toolRegistry == nil {
        return "", fmt.Errorf("tool registry not initialized")
    }

    // We use ExecuteIdle because the Goal System runs autonomously in the background
    // and requires the longer timeouts and higher result limits associated with idle exploration.
    log.Printf("[Engine] Bridging Goal action to Tool Registry (Idle Mode): %s", tool)

    result, err := e.toolRegistry.ExecuteIdle(ctx, tool, params)
    if err != nil {
        return "", err
    }

    if result == nil {
        return "", nil
    }

    // ToolResult.Output contains the string result from the tool execution
    return result.Output, nil
}

// embedderAdapter wraps memory.Embedder to implement goal.Embedder
type embedderAdapter struct {
    emb *memory.Embedder
}

// Embed implements the goal.Embedder interface method
func (a *embedderAdapter) Embed(ctx context.Context, text string) ([]float32, error) {
    return a.emb.Embed(ctx, text)
}

// memorySearcherAdapter wraps memory.Storage and Embedder to implement goal.MemorySearcher
type memorySearcherAdapter struct {
    st  *memory.Storage
    emb *memory.Embedder
}

// SearchRelevant implements the goal.MemorySearcher interface method.
// It performs a semantic search using the provided query string.
func (s *memorySearcherAdapter) SearchRelevant(ctx context.Context, query string, limit int) ([]string, error) {
    // 1. Generate embedding for the query string
    vector, err := s.emb.Embed(ctx, query)
    if err != nil {
        return nil, fmt.Errorf("failed to embed query for memory search: %w", err)
    }

    // 2. Perform the search using the existing Storage logic
    results, err := s.st.Search(ctx, memory.RetrievalQuery{
        Limit:             limit,
        IncludeCollective: true, // Goals should be derived from collective knowledge
        MinScore:          0.5,  // Filter low-relevance results
    }, vector)
    if err != nil {
        return nil, fmt.Errorf("memory search failed: %w", err)
    }

    // 3. Extract just the content strings from the results
    contents := make([]string, len(results))
    for i, r := range results {
        contents[i] = r.Memory.Content
    }
    return contents, nil
}

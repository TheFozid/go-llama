package dialogue

import (
    "context"
    "fmt"
    "log"
    "math/rand"
	"strconv"
    "strings"
    "time"

    "go-llama/internal/memory"
)

// runPhaseReflection executes the reflection phase
func (e *Engine) runPhaseReflection(ctx context.Context, state *InternalState) (*ReasoningResponse, []memory.Principle, int, string, error) {
    log.Printf("[Dialogue] PHASE 1: Enhanced Reflection")

    // Check context before expensive operation
    if ctx.Err() != nil {
        return nil, nil, 0, "", fmt.Errorf("cycle cancelled before reflection: %w", ctx.Err())
    }

    reasoning, principles, tokens, err := e.performEnhancedReflection(ctx, state)
    if err != nil {
        return nil, nil, 0, "", fmt.Errorf("reflection failed: %w", err)
    }

    // Log reflection content
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

    return reasoning, principles, tokens, reasoning.Reflection, nil
}

// runPhaseGoalManagement handles goal creation, validation, and metacognitive checks.
func (e *Engine) runPhaseGoalManagement(ctx context.Context, state *InternalState, reasoning *ReasoningResponse, principles []memory.Principle, metrics *CycleMetrics, totalTokens *int) error {
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

    log.Printf("[Dialogue] PHASE 2: Reasoning-Driven Goal Management")

    // Check context before continuing
    if ctx.Err() != nil {
        return fmt.Errorf("cycle cancelled before goal management: %w", ctx.Err())
    }

    // Use insights from reflection to identify gaps
    gaps := reasoning.KnowledgeGaps.ToSlice()
    if len(gaps) == 0 {
        // Fallback to old method if reasoning didn't find any
        var err error
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

    // HEALTH CHECK: Prevent goal churn when success rate is critically low
    const CRITICAL_SUCCESS_THRESHOLD = 0.15

    if e.adaptiveConfig.recentGoalSuccessRate < CRITICAL_SUCCESS_THRESHOLD && len(state.ActiveGoals) < 5 {
        log.Printf("[Dialogue] ⚠ CRITICAL: Goal success rate is %.2f (below %.2f). Halting LLM proposals to break failure loop.", e.adaptiveConfig.recentGoalSuccessRate, CRITICAL_SUCCESS_THRESHOLD)

        // Force exploratory goal based on user interests to reset context
        userInterests, err := e.analyzeUserInterests(ctx)
        if err != nil {
            log.Printf("[Dialogue] WARNING: Failed to analyze user interests for recovery: %v", err)
            userInterests = []string{}
        }

        // Extract recent descriptions to avoid repeating failures
        recentGoalDescriptions := []string{}
        recentGoals := state.CompletedGoals
        if len(recentGoals) > 5 {
            recentGoals = recentGoals[len(recentGoals)-5:]
        }
        for _, goal := range recentGoals {
            recentGoalDescriptions = append(recentGoalDescriptions, goal.Description)
        }

        recoveryGoal := e.generateExploratoryGoal(ctx, userInterests, "system failure", recentGoalDescriptions)
        newGoals = append(newGoals, recoveryGoal)

        log.Printf("[Dialogue] ✓ Created RECOVERY goal to stabilize system: %s", truncate(recoveryGoal.Description, 60))

    } else if len(reasoning.GoalsToCreate.ToSlice()) > 0 && len(state.ActiveGoals) < 15 {
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
        metrics.GoalsCreated += len(newGoals) // Increment properly
        log.Printf("[Dialogue] Created %d new goals total", len(newGoals))
    }

    // NEW: Metacognitive evaluation - should we modify our thinking principles?
    if e.enableMetaLearning {
        log.Printf("[Dialogue] Evaluating principle effectiveness (metacognitive check)...")
        principleFeedback, feedbackTokens, err := e.evaluatePrincipleEffectiveness(ctx, principles, state)
        if err != nil {
            log.Printf("[Dialogue] WARNING: Principle evaluation failed: %v", err)
        } else {
            *totalTokens += feedbackTokens

            if principleFeedback.ShouldModify {
                log.Printf("[Dialogue] ✓ Principle modification recommended:")
                log.Printf("[Dialogue]   Target: Slot %d", principleFeedback.TargetSlot)
                log.Printf("[Dialogue]   Current: %s", truncate(principleFeedback.CurrentPrinciple, 60))
                log.Printf("[Dialogue]   Proposed: %s", truncate(principleFeedback.ProposedPrinciple, 60))
                log.Printf("[Dialogue]   Justification: %s", truncate(principleFeedback.Justification, 100))

                // Create self-modification goal
                modGoal := e.createSelfModificationGoal(principleFeedback)
                state.ActiveGoals = append(state.ActiveGoals, modGoal)
                metrics.GoalsCreated++
                log.Printf("[Dialogue] ✓ Created self-modification goal: %s", truncate(modGoal.Description, 60))
            } else {
                log.Printf("[Dialogue] Current principles are working well, no modification needed")
            }
        }
    }

    return nil
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
        Limit:            10,
        MinScore:         0.3,
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
    reflection, tokens, err := e.callLLM(ctx, prompt, true) // Use Simple Model
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
        Limit:            10,
        MinScore:         0.4,
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
            priority = 8     // Higher priority for explicit research requests
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
    proposalKeywords := extractSignificantKeywords(proposalDesc)
    for _, existingGoal := range existingGoals {
        existingKeywords := extractSignificantKeywords(existingGoal.Description)

        // Calculate keyword overlap ratio
        overlap := calculateKeywordOverlap(proposalKeywords, existingKeywords)

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

// deriveCapabilities analyzes a mission text and generates the required capability matrix
func (e *Engine) deriveCapabilities(ctx context.Context, missionText string) ([]Capability, error) {
    prompt := fmt.Sprintf(`Analyze this mission and list the specific capabilities required to achieve it.

MISSION: %s

INSTRUCTIONS:
1. Identify 3-7 specific skills or knowledge areas required.
2. Assign a starting score of 0.0 to all (we are starting from scratch).
3. Output as a FLAT S-expression list.

Format:
(capability "CapabilityName" 0.0)
(capability "AnotherSkill" 0.0)

Example:
MISSION: Act like a fox.
(capability "Stealth_Cunning" 0.0)
(capability "Foraging_Knowledge" 0.0)
(capability "Predator_Avoidance" 0.0)

Output:`, missionText)

    response, _, err := e.callLLMWithStructuredReasoning(ctx, prompt, true, "")
    if err != nil {
        return nil, err
    }

    content := response.RawResponse
    content = strings.TrimSpace(content)
    content = strings.TrimPrefix(content, "```lisp")
    content = strings.TrimPrefix(content, "```")
    content = strings.TrimSuffix(content, "```")

    // Parse flat list (capability "Name" Score)
    tokensList := tokenizeSimple(content)
    capabilities := []Capability{}

    for i := 0; i < len(tokensList); i++ {
        if tokensList[i].value == "capability" {
            if i+2 < len(tokensList) {
                name := tokensList[i+1].value
                scoreStr := tokensList[i+2].value
                score := 0.0
                if s, err := strconv.ParseFloat(scoreStr, 64); err == nil {
                    score = s
                }
                capabilities = append(capabilities, Capability{Name: name, Score: score})
            }
        }
    }

    if len(capabilities) == 0 {
        return nil, fmt.Errorf("no capabilities derived")
    }

    log.Printf("[Strategist] Derived %d capabilities for mission: %s", len(capabilities), missionText)
    return capabilities, nil
}

// selectStrategicFocus analyzes the capability matrix and selects the next focus area
func (e *Engine) selectStrategicFocus(ctx context.Context, matrix []Capability, missionText string) (string, string, error) {
    // Find capability with lowest score
    if len(matrix) == 0 {
        return "", "", fmt.Errorf("empty capability matrix")
    }

    // Sort by score asc
    sorted := make([]Capability, len(matrix))
    copy(sorted, matrix)
    
    for i := 0; i < len(sorted); i++ {
        for j := i + 1; j < len(sorted); j++ {
            if sorted[j].Score < sorted[i].Score {
                sorted[i], sorted[j] = sorted[j], sorted[i]
            }
        }
    }

    // Pick the lowest one
    target := sorted[0]
    
    // Use LLM to justify the focus (optional, but adds depth)
    // For now, we return the raw focus
    focusName := target.Name
    
    log.Printf("[Strategist] Selected strategic focus: %s (current score: %.2f)", focusName, target.Score)
    return focusName, fmt.Sprintf("Currently lowest score (%.2f) in capability matrix.", target.Score), nil
}

// thinkAboutGoal generates thoughts about pursuing a goal
func (e *Engine) thinkAboutGoal(ctx context.Context, goal *Goal) (string, int, error) {
    prompt := fmt.Sprintf("You are pursuing this goal: %s\n\nThink about how to approach this. What should you do next? Keep it brief (2-3 sentences).", goal.Description)

    thought, tokens, err := e.callLLM(ctx, prompt, true) // Use Simple Model
    if err != nil {
        return "", 0, err
    }

    return thought, tokens, nil
}

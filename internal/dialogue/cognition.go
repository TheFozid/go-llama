package dialogue

import (
    "context"
    "fmt"
    "log"
    "math/rand"
    "sort"
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

// detectUserMissions scans recent chat memories for user-imperative commands
func (e *Engine) detectUserMissions(ctx context.Context, state *InternalState) (*Mission, error) {
    // 1. Search for recent user memories that look like commands
    // We look for "goal", "mission", "task", "objective" keywords
    embedding, _ := e.embedder.Embed(ctx, "user command objective goal task mission set target")
    
    query := memory.RetrievalQuery{
        Limit: 5,
        MinScore: 0.4,
        IncludePersonal: true,
        IncludeCollective: false,
    }
    
    results, err := e.storage.Search(ctx, query, embedding)
    if err != nil || len(results) == 0 {
        return nil, nil // No error, just nothing found
    }

    // 2. Analyze top result with LLM to see if it's a Mission Proposal
    topMemory := results[0].Memory.Content
    
    prompt := fmt.Sprintf(`Analyze this user input to see if it sets a goal or objective for the AI.

Input: "%s"

If this is a command or objective, extract the mission.
If it is just conversation, return nothing.

Output strictly in FLAT S-expression:
(mission "Description of the mission")
(status "none")

Output:`, topMemory)

    response, _, err := e.callLLMWithStructuredReasoning(ctx, prompt, true, "")
    if err != nil {
        return nil, err
    }

    content := strings.TrimSpace(response.RawResponse)
    if !strings.Contains(content, "(mission") {
        return nil, nil // No mission detected
    }

    // Parse mission
    missionText := extractFieldContent(content, "mission")
    if missionText == "" {
        return nil, nil
    }

    log.Printf("[Strategist] Detected User Mission: %s", missionText)

    return &Mission{
        ID:          fmt.Sprintf("mission_%d", time.Now().UnixNano()),
        Description: missionText,
        Source:      "user",
        Priority:    0.8, // Base user priority
        Status:      "queued",
        CreatedAt:   time.Now(),
        UpdatedAt:   time.Now(),
    }, nil
}

// manageMissionQueue handles insertion, priority, decay, and bumping
func (e *Engine) manageMissionQueue(state *InternalState, newMission *Mission) {
    if newMission == nil {
        return
    }

    // 1. Check for duplicates in Queue or Active
    for i := range state.QueuedMissions {
        if state.QueuedMissions[i].Description == newMission.Description {
            // Boost priority
            state.QueuedMissions[i].Priority += 0.05
            if state.QueuedMissions[i].Priority > 1.0 { state.QueuedMissions[i].Priority = 1.0 }
            state.QueuedMissions[i].UpdatedAt = time.Now()
            log.Printf("[Strategist] Boosted existing queued mission priority: %s", newMission.Description)
            return
        }
    }
    
    if state.ActiveMission != nil && state.ActiveMission.Description == newMission.Description {
        // Boost active priority? (Active is immutable, but we can log)
        log.Printf("[Strategist] User repeated Active Mission, acknowledging focus.")
        return
    }

    // 2. Set Priority Ceiling for AI missions
    if newMission.Source == "ai" && newMission.Priority > 0.79 {
        newMission.Priority = 0.79
    }

    // 3. Insert into Queue
    state.QueuedMissions = append(state.QueuedMissions, *newMission)
    
    // 4. Sort by Priority (Desc)
    sort.Slice(state.QueuedMissions, func(i, j int) bool {
        return state.QueuedMissions[i].Priority > state.QueuedMissions[j].Priority
    })

    // 5. Enforce Limit (Drop #6+)
    if len(state.QueuedMissions) > 5 {
        dropped := state.QueuedMissions[5]
        state.QueuedMissions = state.QueuedMissions[:5]
        log.Printf("[Strategist] Queue full. Dropped mission: %s", dropped.Description)
    }
    
    log.Printf("[Strategist] Added new mission to queue: %s (Priority: %.2f)", newMission.Description, newMission.Priority)
}

// decayMissions reduces priority of queued missions over time
func (e *Engine) decayMissions(state *InternalState) {
    now := time.Now()
    decayedCount := 0
    
    validQueue := []Mission{}
    
    for _, m := range state.QueuedMissions {
        // Check if 24h has passed since last update
        if now.Sub(m.UpdatedAt) >= 24*time.Hour {
            m.Priority -= 0.01
            m.UpdatedAt = now
            decayedCount++
        }
        
        // Keep if priority > 0.1
        if m.Priority > 0.1 {
            validQueue = append(validQueue, m)
        } else {
            log.Printf("[Strategist] Mission decayed and dropped: %s", m.Description)
        }
    }
    
    state.QueuedMissions = validQueue
    if decayedCount > 0 {
        log.Printf("[Strategist] Applied decay to %d queued missions.", decayedCount)
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

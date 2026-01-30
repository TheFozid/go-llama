package dialogue

import (
    "context"
    "encoding/json"
    "fmt"
    "log"
    "strings"
    "time"

    "go-llama/internal/memory"
)

func (e *Engine) detectPatterns(ctx context.Context) ([]string, error) {
    // For Phase 3.1, return empty
    // In Phase 3.2+, this would analyze concept tags, co-occurrences, etc.
    return []string{}, nil
}

// callLLM makes a request to the reasoning or simple model
func (e *Engine) callLLM(ctx context.Context, prompt string, useSimpleModel bool) (string, int, error) {
    // Determine target URL and Model based on routing flag
    targetURL := e.llmURL
    targetModel := e.llmModel

    if useSimpleModel {
        if e.simpleLLMURL != "" {
            targetURL = e.simpleLLMURL
            targetModel = e.simpleLLMModel
            log.Printf("[Dialogue] Routing to Simple Model (1B)")
        } else {
            // Fallback to reasoning model if simple model not configured
            log.Printf("[Dialogue] Simple Model requested but not configured, using Reasoning Model (8B)")
        }
    }

    // If queue client is available, use it
    if e.llmClient != nil {
        // Type assertion (safe because we control initialization)
        type LLMCaller interface {
            Call(ctx context.Context, url string, payload map[string]interface{}) ([]byte, error)
        }

        if client, ok := e.llmClient.(LLMCaller); ok {
            reqBody := map[string]interface{}{
                "model":	targetModel,
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

            body, err := client.Call(ctx, targetURL, reqBody)
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

func (e *Engine) callLLMWithStructuredReasoning(ctx context.Context, prompt string, expectJSON bool, systemPromptOverride string) (*ReasoningResponse, int, error) {
    // Default system prompt for general reasoning
    defaultSystemPrompt := `Output ONLY S-expressions (Lisp-style). No Markdown.
Format: (reasoning (reflection "...") (insights "...") (goals_to_create (goal (description "...") (priority 8))))
Example: (reasoning (reflection "Good session") (insights "Learned X") (goals_to_create (goal (description "Do Y") (priority 8))))`

    // Use override if provided (e.g., for assessments), otherwise default
    systemPrompt := defaultSystemPrompt
    if systemPromptOverride != "" {
        systemPrompt = systemPromptOverride
    }

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

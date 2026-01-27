package dialogue

import (
    "context"
    "fmt"
    "log"
    "strconv"
    "strings"
    "time"

    "go-llama/internal/memory"
    "go-llama/internal/tools"
)

// generateResearchPlan creates a structured research plan from LLM reasoning
func (e *Engine) generateResearchPlan(ctx context.Context, goal *Goal) (*ResearchPlan, int, error) {
    // Call LLM to get research plan
    prompt := fmt.Sprintf(`Generate a detailed research plan to achieve this goal.

GOAL: %s

INSTRUCTIONS:
1. Break down the goal into 3-7 specific questions.
2. Order questions logically (prerequisites first).
3. For each question, provide a search query.
4. Assign priorities (10=highest, 1=lowest).
5. List dependencies if a question requires answer from another.

Respond with S-expression:

(research_plan
  (root_question "Main question driving this goal")
  (sub_questions
    (question
      (id "q1")
      (text "First question")
      (search_query "Search terms for q1")
      (priority 10)
      (deps ()))
    (question
      (id "q2")
      (text "Second question")
      (search_query "Search terms for q2")
      (priority 8)
      (deps ("q1")))
    ...))`, goal.Description)

    response, tokens, err := e.callLLMWithStructuredReasoning(ctx, prompt, true)
    if err != nil {
        return nil, tokens, fmt.Errorf("failed to generate research plan: %w", err)
    }

    content := response.RawResponse

    // DEBUG LOGGING: Always log what we received
    log.Printf("[Dialogue] Research plan response length: %d chars", len(content))
    log.Printf("[Dialogue] Research plan response (first 300 chars): %s", truncateResponse(content, 300))

    // Clean up markdown fences
    content = strings.TrimPrefix(content, "```lisp")
    content = strings.TrimPrefix(content, "```")
    content = strings.TrimSuffix(content, "```")
    content = strings.TrimSpace(content)

    // IMPROVED PARSING: Use recursive search first (most robust)

    // Strategy 1: Recursive search (handles nested structures like (reasoning (research_plan ...)))
    planBlocks := findBlocksRecursive(content, "research_plan")

    // Strategy 2: Direct search as fallback
    if len(planBlocks) == 0 {
        log.Printf("[Dialogue] Recursive search failed, trying direct search...")
        planBlocks = findBlocks(content, "research_plan")
    }

    // Strategy 3: Regex fallback (handles malformed S-expressions)
    if len(planBlocks) == 0 {
        log.Printf("[Dialogue] Structured parsing failed, attempting regex extraction...")
        plan, err := extractResearchPlanFromMalformed(content)
        if err == nil && plan != nil {
            log.Printf("[Dialogue] ✓ Extracted research plan via regex fallback (%d questions)", len(plan.SubQuestions))
            return plan, tokens, nil
        }
        log.Printf("[Dialogue] Regex extraction also failed: %v", err)
    }

    // If all strategies failed, provide detailed error
    if len(planBlocks) == 0 {
        log.Printf("[Dialogue] ERROR: All parsing strategies failed")
        log.Printf("[Dialogue] Content structure analysis:")
        log.Printf("[Dialogue]   - Contains '(reasoning': %v", strings.Contains(content, "(reasoning"))
        log.Printf("[Dialogue]   - Contains '(reflection': %v", strings.Contains(content, "(reflection"))
        log.Printf("[Dialogue]   - Contains '(research_plan': %v", strings.Contains(content, "(research_plan"))
        log.Printf("[Dialogue]   - Contains '(research-plan': %v", strings.Contains(content, "(research-plan"))
        log.Printf("[Dialogue]   - Contains '(question': %v", strings.Contains(content, "(question"))
        log.Printf("[Dialogue] Full content: %s", content)

        return nil, tokens, fmt.Errorf("no research_plan block found in S-expression after trying all parsing strategies")
    }

    log.Printf("[Dialogue] ✓ Found research_plan block using structured parsing")

    // Extract Root Question
    rootQuestion := extractFieldContent(planBlocks[0], "root_question")

    // Extract Sub Questions
    questionBlocks := findBlocks(planBlocks[0], "question")
    if len(questionBlocks) == 0 {
        return nil, tokens, fmt.Errorf("no question blocks found in research plan")
    }

    if len(questionBlocks) > 10 {
        questionBlocks = questionBlocks[:10]
    }

    // Convert to internal ResearchPlan
    plan := &ResearchPlan{
        RootQuestion:    rootQuestion,
        SubQuestions:    make([]ResearchQuestion, len(questionBlocks)),
        CurrentStep:     0,
        SynthesisNeeded: false,
        CreatedAt:       time.Now(),
        UpdatedAt:       time.Now(),
    }

    for i, qBlock := range questionBlocks {
        // Helper to extract integer fields safely
        getInt := func(field string) int {
            val := extractFieldContent(qBlock, field)
            if val == "" {
                return 0
            }
            if p, err := strconv.Atoi(val); err == nil {
                return p
            }
            return 0
        }

        // Helper to extract dependencies list (deps ("q1" "q2"))
        getDeps := func(field string) []string {
            pattern := "(" + field + " "
            start := strings.Index(qBlock, pattern)
            if start == -1 {
                return []string{}
            }
            start += len(pattern)

            // Handle empty list ()
            if start < len(qBlock) && qBlock[start] == ')' {
                return []string{}
            }

            // Naive extraction of quoted strings until closing )
            var deps []string
            rest := qBlock[start:]
            for {
                qStart := strings.Index(rest, `"`)
                if qStart == -1 {
                    break
                }
                qEnd := strings.Index(rest[qStart+1:], `"`)
                if qEnd == -1 {
                    break
                }
                deps = append(deps, rest[qStart+1:qStart+1+qEnd])
                rest = rest[qStart+1+qEnd+1:]
                if strings.HasPrefix(rest, ")") {
                    break
                }
            }
            return deps
        }

        plan.SubQuestions[i] = ResearchQuestion{
            ID:              extractFieldContent(qBlock, "id"),
            Question:        extractFieldContent(qBlock, "text"),
            SearchQuery:     extractFieldContent(qBlock, "search_query"),
            Priority:        getInt("priority"),
            Dependencies:    getDeps("deps"),
            Status:          ResearchStatusPending,
            SourcesFound:    []string{},
            KeyFindings:     "",
            ConfidenceLevel: 0.0,
        }
    }

    return plan, tokens, nil
}

// getNextResearchAction determines next action from research plan
func (e *Engine) getNextResearchAction(ctx context.Context, goal *Goal) *Action {
    plan := goal.ResearchPlan
    if plan == nil {
        return nil
    }

    // Find next pending question (respecting dependencies)
    var nextQuestion *ResearchQuestion

    for i := range plan.SubQuestions {
        q := &plan.SubQuestions[i]

        if q.Status != ResearchStatusPending {
            continue
        }

        // Check dependencies
        dependenciesMet := true
        for _, depID := range q.Dependencies {
            for _, dq := range plan.SubQuestions {
                if dq.ID == depID && dq.Status != ResearchStatusCompleted {
                    dependenciesMet = false
                    break
                }
            }
            if !dependenciesMet {
                break
            }
        }

        if dependenciesMet {
            nextQuestion = q
            break
        }
    }

    if nextQuestion == nil {
        return nil // No questions available
    }

    // Create search action
    return &Action{
        Description: nextQuestion.SearchQuery,
        Tool:        ActionToolSearch,
        Status:      ActionStatusPending,
        Timestamp:   time.Now(),
        Metadata: map[string]interface{}{
            "research_question_id": nextQuestion.ID,
            "question_text":        nextQuestion.Question,
        },
    }
}

// updateResearchProgress records findings from completed action
func (e *Engine) updateResearchProgress(ctx context.Context, goal *Goal, questionID string, actionResult string) error {
    plan := goal.ResearchPlan
    if plan == nil {
        return fmt.Errorf("no research plan")
    }

    // Find question
    var question *ResearchQuestion
    for i := range plan.SubQuestions {
        if plan.SubQuestions[i].ID == questionID {
            question = &plan.SubQuestions[i]
            break
        }
    }

    if question == nil {
        return fmt.Errorf("question %s not found", questionID)
    }

    // Extract findings using simple heuristics (lightweight, no LLM)
    // Take first 200 chars as key finding
    findings := actionResult
    if len(findings) > 200 {
        findings = findings[:200] + "..."
    }

    question.KeyFindings = findings
    question.ConfidenceLevel = 0.7 // Default confidence
    question.Status = ResearchStatusCompleted
    plan.UpdatedAt = time.Now()

    log.Printf("[Dialogue] ✓ Question '%s' complete: %s", questionID, truncate(findings, 80))

    return nil
}

// synthesizeResearchFindings combines all findings into coherent knowledge
func (e *Engine) synthesizeResearchFindings(ctx context.Context, goal *Goal) (string, int, error) {
    plan := goal.ResearchPlan
    if plan == nil {
        return "", 0, fmt.Errorf("no research plan")
    }

    // Build context from completed questions
    var findingsBuilder strings.Builder
    findingsBuilder.WriteString(fmt.Sprintf("Research: %s\n\n", plan.RootQuestion))

    completedCount := 0
    for i, q := range plan.SubQuestions {
        if q.Status == ResearchStatusCompleted && q.KeyFindings != "" {
            completedCount++
            findingsBuilder.WriteString(fmt.Sprintf("Q%d: %s\n", i+1, q.Question))
            findingsBuilder.WriteString(fmt.Sprintf("A%d: %s\n\n", i+1, q.KeyFindings))
        }
    }

    if completedCount == 0 {
        return "", 0, fmt.Errorf("no completed questions to synthesize")
    }

    prompt := fmt.Sprintf(`Synthesize these research findings into a coherent summary.

%s

Create a comprehensive synthesis (3-5 paragraphs) that:
1. Directly answers the root question
2. Integrates all findings logically
3. Notes any gaps or uncertainties
4. Provides actionable insights

Write synthesis as plain text (no JSON, no markdown):`, findingsBuilder.String())

    synthesis, tokens, err := e.callLLM(ctx, prompt, false) // Use Reasoning Model for synthesis
    if err != nil {
        return "", tokens, fmt.Errorf("synthesis failed: %w", err)
    }

    return synthesis, tokens, nil
}

// storeResearchSynthesis saves synthesis as high-value collective memory
func (e *Engine) storeResearchSynthesis(ctx context.Context, goal *Goal, synthesis string) error {
    content := fmt.Sprintf("Research: %s\n\nFindings:\n%s",
        goal.ResearchPlan.RootQuestion, synthesis)

    embedding, err := e.embedder.Embed(ctx, content)
    if err != nil {
        return fmt.Errorf("failed to embed: %w", err)
    }

    // Extract concept tags from questions
    conceptTags := []string{"research", "synthesis"}
    for _, q := range goal.ResearchPlan.SubQuestions {
        words := strings.Fields(q.Question)
        if len(words) > 0 {
            tag := strings.ToLower(strings.Trim(words[0], "?,.!"))
            if len(tag) > 3 && len(tag) < 20 {
                conceptTags = append(conceptTags, tag)
            }
        }
    }
    if len(conceptTags) > 5 {
        conceptTags = conceptTags[:5]
    }

    mem := &memory.Memory{
        Content:         content,
        Tier:            memory.TierRecent,
        IsCollective:    true,
        CreatedAt:       time.Now(),
        LastAccessedAt:  time.Now(),
        ImportanceScore: 0.9,
        Embedding:       embedding,
        OutcomeTag:      "good",
        TrustScore:      0.8,
        ValidationCount: len(goal.ResearchPlan.SubQuestions),
        ConceptTags:     conceptTags,
        Metadata: map[string]interface{}{
            "goal_id":       goal.ID,
            "research_type": "synthesis",
        },
    }

    return e.storage.Store(ctx, mem)
}

// executeAction executes a tool-based action
func (e *Engine) executeAction(ctx context.Context, action *Action) (string, error) {
    log.Printf("[Dialogue] Executing action with tool '%s' (description: %s)",
        action.Tool, truncate(action.Description, 60))
    startTime := time.Now()

    // Check context before starting
    if ctx.Err() != nil {
        return "", fmt.Errorf("action cancelled before execution: %w", ctx.Err())
    }

    // Map action tool to actual tool execution
    switch action.Tool {
    case ActionToolSearch:
        // Extract search query from action description
        params := map[string]interface{}{
            "query": action.Description,
        }

        log.Printf("[Dialogue] Calling search tool with query: %s", truncate(action.Description, 80))
        result, err := e.toolRegistry.ExecuteIdle(ctx, tools.ToolNameSearch, params)

        elapsed := time.Since(startTime)

        if err != nil {
            log.Printf("[Dialogue] Search tool failed after %s: %v", elapsed, err)
            return "", fmt.Errorf("search tool failed: %w", err)
        }

        if !result.Success {
            log.Printf("[Dialogue] Search returned failure after %s: %s", elapsed, result.Error)
            return "", fmt.Errorf("search failed: %s", result.Error)
        }

        log.Printf("[Dialogue] Search completed successfully in %s", elapsed)

        // Store URLs in action metadata for the next parse action to use
        urls := extractURLsFromSearchResults(result.Output)
        if len(urls) > 0 {
            log.Printf("[Dialogue] Extracted %d URLs from search results, storing for parse action", len(urls))
            if action.Metadata == nil {
                action.Metadata = make(map[string]interface{})
            }
            action.Metadata["extracted_urls"] = urls
        }

        return result.Output, nil

    case ActionToolWebParse,
        ActionToolWebParseMetadata,
        ActionToolWebParseGeneral,
        ActionToolWebParseContextual,
        ActionToolWebParseChunked:

        var url string

        // First priority: check if search evaluation selected a best URL
        if action.Metadata != nil {
            if selectedURL, ok := action.Metadata["selected_url"].(string); ok && selectedURL != "" {
                url = selectedURL
                log.Printf("[Dialogue] Using evaluated best URL: %s", truncate(url, 60))
            } else if bestURL, ok := action.Metadata["best_url"].(string); ok && bestURL != "" {
                url = bestURL
                log.Printf("[Dialogue] Using best URL from metadata: %s", truncate(url, 60))
            } else if urls, ok := action.Metadata["previous_search_urls"].([]string); ok && len(urls) > 0 {
                // Fallback: use first URL (old behavior)
                url = urls[0]
                log.Printf("[Dialogue] WARNING: Using first URL from search results (evaluation may have failed): %s", truncate(url, 60))
            }
        }

        // Fallback: extract URL from action description
        if url == "" {
            // Formats handled:
            //   - "https://example.com"
            //   - "Parse URL: https://example.com"
            //   - "Search result: https://example.com - title"
            //   - "URL from search results" (placeholder - will fail with clear error)
            url = strings.TrimSpace(action.Description)

            // Handle placeholder case
            if url == "URL from search results" {
                return "", fmt.Errorf("parse action has placeholder URL - previous search may have failed or returned no URLs")
            }

            // Clean up common prefixes
            if idx := strings.Index(url, "http"); idx != -1 {
                url = url[idx:] // Start from http
            }

            // Remove everything after first space (titles, descriptions)
            if idx := strings.Index(url, " "); idx != -1 {
                url = url[:idx]
            }

            // Basic validation
            if !strings.HasPrefix(url, "http://") && !strings.HasPrefix(url, "https://") {
                return "", fmt.Errorf("invalid URL in action description: %s", action.Description)
            }
        }

        params := map[string]interface{}{
            "url": url,
        }

        // For contextual parsing, extract purpose from metadata if available
        if action.Tool == ActionToolWebParseContextual {
            if action.Metadata != nil {
                if purpose, ok := action.Metadata["purpose"].(string); ok && purpose != "" {
                    params["purpose"] = purpose
                    log.Printf("[Dialogue] Using contextual parser with purpose: %s", truncate(purpose, 60))
                } else {
                    params["purpose"] = "Extract relevant information for research goal"
                }
            } else {
                params["purpose"] = "Extract relevant information for research goal"
            }
        }

        // For chunked parsing, look for chunk index
        if action.Tool == ActionToolWebParseChunked {
            // Default to first chunk - LLM should specify in future iterations
            params["chunk_index"] = 0

            // Try to parse chunk index from description
            // Format: "Read chunk 3 from URL" or "chunk_index: 3"
            desc := strings.ToLower(action.Description)
            if strings.Contains(desc, "chunk") {
                // Simple extraction - matches "chunk 3", "chunk 0", etc.
                parts := strings.Fields(desc)
                for i, part := range parts {
                    if part == "chunk" && i+1 < len(parts) {
                        if chunkIdx, err := fmt.Sscanf(parts[i+1], "%d", new(int)); err == nil && chunkIdx >= 0 {
                            var idx int
                            fmt.Sscanf(parts[i+1], "%d", &idx)
                            params["chunk_index"] = idx
                            break
                        }
                    }
                }
            }
        }

        // Execute the appropriate web parse tool
        log.Printf("[Dialogue] Calling web parse tool '%s' for URL: %s", action.Tool, truncate(url, 80))
        result, err := e.toolRegistry.ExecuteIdle(ctx, action.Tool, params)

        elapsed := time.Since(startTime)

        if err != nil {
            log.Printf("[Dialogue] Web parse tool failed after %s: %v", elapsed, err)
            return "", fmt.Errorf("web parse tool failed: %w", err)
        }

        if !result.Success {
            log.Printf("[Dialogue] Web parse returned failure after %s: %s", elapsed, result.Error)

            // Check if this is a "page too large" error from web_parse_general
            if action.Tool == ActionToolWebParseGeneral &&
                strings.Contains(strings.ToLower(result.Error), "page too large") {
                log.Printf("[Dialogue] Page too large for general parsing, creating fallback actions")

                // Create a special error that will be handled by the goal pursuit system
                return "", fmt.Errorf("page too large for general summary: %s", result.Error)
            }

            return "", fmt.Errorf("web parse failed: %s", result.Error)
        }

        log.Printf("[Dialogue] Web parse completed successfully in %s (%d chars output)",
            elapsed, len(result.Output))

        return result.Output, nil

    case ActionToolSandbox:
        // Phase 3.5: Sandbox not yet implemented
        return "", fmt.Errorf("sandbox tool not yet implemented")

    case ActionToolMemoryConsolidation:
        // This is internal, not a real tool
        log.Printf("[Dialogue] Memory consolidation completed in %s", time.Since(startTime))
        return "Memory consolidation completed", nil

    case ActionToolSynthesis:
        // Synthesis happens in goal completion phase, not here
        log.Printf("[Dialogue] Synthesis action marked (will execute on goal completion)")
        return "Synthesis ready", nil

    default:
        return "", fmt.Errorf("unknown tool: %s", action.Tool)
    }

    // Note: Result logging happens in each case block above
}

// getPrimaryGoals filters goals by primary tier
func (e *Engine) getPrimaryGoals(goals []Goal) []Goal {
    primaries := []Goal{}
    for _, goal := range goals {
        if goal.Tier == "primary" {
            primaries = append(primaries, goal)
        }
    }
    return primaries
}

// validateGoalSupport uses LLM to validate if a secondary goal supports a primary goal
func (e *Engine) validateGoalSupport(ctx context.Context, secondary *Goal, primaryGoals []Goal) (*GoalSupportValidation, error) {
    if len(primaryGoals) == 0 {
        return nil, fmt.Errorf("no primary goals to validate against")
    }

    // Build context about primary goals
    var primaryContext strings.Builder
    primaryContext.WriteString("CURRENT PRIMARY GOALS:\n")
    for i, primary := range primaryGoals {
        primaryContext.WriteString(fmt.Sprintf("%d. [ID: %s] %s\n", i+1, primary.ID, primary.Description))
    }

    prompt := fmt.Sprintf(`Evaluate if this SECONDARY goal meaningfully supports at least one PRIMARY goal.

%s

SECONDARY GOAL TO EVALUATE:
%s

CRITICAL: A secondary goal "supports" a primary goal if completing the secondary goal:
1. Directly advances progress toward the primary goal
2. Provides knowledge/skills needed for the primary goal
3. Creates resources/artifacts used by the primary goal
4. Removes blockers preventing progress on the primary goal

Respond ONLY with this S-expression:

(goal_support_validation
  (supports_goal_id "goal_xxx")  ; ID of primary goal being supported, or "" if none
  (confidence 0.85)  ; 0.0-1.0 confidence in linkage
  (reasoning "Specific explanation of how secondary supports primary")
  (is_valid true))  ; false if secondary doesn't meaningfully support any primary

RULES:
- If secondary supports NO primary goals, set is_valid to false
- If secondary supports multiple primaries, pick the strongest linkage
- Be strict: only validate true if linkage is clear and meaningful
- Output ONLY the S-expression, no markdown`,
        primaryContext.String(), secondary.Description)

    log.Printf("[GoalValidation] Validating secondary goal linkage via LLM...")
    response, tokens, err := e.callLLMWithStructuredReasoning(ctx, prompt, false)
    if err != nil {
        return nil, fmt.Errorf("LLM validation failed: %w", err)
    }

    log.Printf("[GoalValidation] LLM validation completed (%d tokens)", tokens)

    // Parse S-expression response
    validation, err := e.parseGoalSupportValidation(response.RawResponse)
    if err != nil {
        log.Printf("[GoalValidation] Failed to parse validation: %v", err)
        return nil, err
    }

    return validation, nil
}

// parseGoalSupportValidation extracts validation from S-expression
func (e *Engine) parseGoalSupportValidation(rawResponse string) (*GoalSupportValidation, error) {
    content := strings.TrimSpace(rawResponse)
    content = strings.TrimPrefix(content, "```lisp")
    content = strings.TrimPrefix(content, "```")
    content = strings.TrimSuffix(content, "```")
    content = strings.TrimSpace(content)

    // Find goal_support_validation block
    blocks := findBlocksRecursive(content, "goal_support_validation")
    if len(blocks) == 0 {
        blocks = findBlocksRecursive(content, "goal-support-validation")
    }

    if len(blocks) == 0 {
        return nil, fmt.Errorf("no goal_support_validation block found")
    }

    block := blocks[0]

    validation := &GoalSupportValidation{
        Confidence: 0.5, // Default
        IsValid:    false,
    }

    // Extract fields
    if goalID := extractFieldContent(block, "supports_goal_id"); goalID != "" {
        validation.SupportsGoalID = goalID
    } else if goalID := extractFieldContent(block, "supports-goal-id"); goalID != "" {
        validation.SupportsGoalID = goalID
    }

    if reasoning := extractFieldContent(block, "reasoning"); reasoning != "" {
        validation.Reasoning = reasoning
    }

    if confStr := extractFieldContent(block, "confidence"); confStr != "" {
        if conf, err := parseFloat(confStr); err == nil {
            validation.Confidence = conf
        }
    }

    if validStr := extractFieldContent(block, "is_valid"); validStr != "" {
        validation.IsValid = (validStr == "true" || validStr == "t")
    } else if validStr := extractFieldContent(block, "is-valid"); validStr != "" {
        validation.IsValid = (validStr == "true" || validStr == "t")
    }

    // Validation: if is_valid is true, must have a goal ID
    if validation.IsValid && validation.SupportsGoalID == "" {
        // Try to find the goal ID from the reasoning
        return nil, fmt.Errorf("is_valid true but no supports_goal_id found")
    }

    return validation, nil
}

// parseActionFromPlan parses a plan step into an Action
func (e *Engine) parseActionFromPlan(planStep string) Action {
    // Simple parsing: look for tool keywords
    tool := ActionToolSearch // Default to search
    planLower := strings.ToLower(planStep)

    // Map to specific registered tools (not deprecated generic web_parse)
    if strings.Contains(planLower, "contextual") || strings.Contains(planLower, "purpose") {
        tool = ActionToolWebParseContextual
    } else if strings.Contains(planLower, "chunk") || strings.Contains(planLower, "incremental") {
        tool = ActionToolWebParseChunked
    } else if strings.Contains(planLower, "metadata") || strings.Contains(planLower, "lightweight") {
        tool = ActionToolWebParseMetadata
    } else if strings.Contains(planLower, "parse") || strings.Contains(planLower, "read") || strings.Contains(planLower, "fetch") {
        tool = ActionToolWebParseGeneral // Default parse tool
    } else if strings.Contains(planLower, "search") || strings.Contains(planLower, "find") || strings.Contains(planLower, "look up") {
        tool = ActionToolSearch
    }
    // NOTE: Removed ActionToolSandbox mapping - sandbox not yet implemented
    // Keywords like "test", "experiment", "try" will fall back to search

    // CRITICAL: Validate tool exists before creating action
    if !e.validateToolExists(tool) {
        log.Printf("[Dialogue] WARNING: Tool '%s' not registered, falling back to search", tool)
        tool = ActionToolSearch
    }

    return Action{
        Description: planStep,
        Tool:        tool,
        Status:      ActionStatusPending,
        Timestamp:   time.Now(),
    }
}

// validateToolExists checks if a tool is registered before creating an action
func (e *Engine) validateToolExists(toolName string) bool {
    registry := e.toolRegistry.GetRegistry()
    _, err := registry.Get(toolName)
    return err == nil
}

// getAvailableToolsList returns a formatted list of registered tools for LLM context
func (e *Engine) getAvailableToolsList() string {
    registry := e.toolRegistry.GetRegistry()
    tools := registry.List()
    var builder strings.Builder
    builder.WriteString("\nAvailable tools for creating actions:\n")

    // List tools in logical order
    toolOrder := []string{
        ActionToolSearch,
        ActionToolWebParseMetadata,
        ActionToolWebParseGeneral,
        ActionToolWebParseContextual,
        ActionToolWebParseChunked,
    }

    for _, toolName := range toolOrder {
        if desc, exists := tools[toolName]; exists {
            builder.WriteString(fmt.Sprintf("- %s: %s\n", toolName, desc))
        }
    }

    builder.WriteString("\nIMPORTANT: Only use tools from this list in action plans. Never invent tool names.\n")
    builder.WriteString("Default to 'search' if unsure which tool to use.\n")
    return builder.String()
}

// assessProgress evaluates if the current plan is still optimal after completing an action
func (e *Engine) assessProgress(ctx context.Context, goal *Goal) (*PlanAssessment, int, error) {
    // Gather completed and pending actions
    completedActions := []Action{}
    pendingActions := []Action{}

    for _, action := range goal.Actions {
        if action.Status == ActionStatusCompleted {
            completedActions = append(completedActions, action)
        } else if action.Status == ActionStatusPending {
            pendingActions = append(pendingActions, action)
        }
    }

    // Build action summaries
    completedSummary := ""
    for i, action := range completedActions {
        resultPreview := action.Result
        if len(resultPreview) > 200 {
            resultPreview = resultPreview[:200] + "..."
        }
        completedSummary += fmt.Sprintf("%d. %s [%s]\n   Result: %s\n",
            i+1, action.Tool, action.Description, resultPreview)
    }

    pendingSummary := ""
    for i, action := range pendingActions {
        pendingSummary += fmt.Sprintf("%d. %s [%s]\n",
            i+1, action.Tool, action.Description)
    }

    // Build research plan summary if exists
    planSummary := ""
    if goal.ResearchPlan != nil {
        planSummary = fmt.Sprintf("Root Question: %s\n", goal.ResearchPlan.RootQuestion)
        planSummary += fmt.Sprintf("Total Questions: %d\n", len(goal.ResearchPlan.SubQuestions))
        planSummary += fmt.Sprintf("Current Step: %d\n", goal.ResearchPlan.CurrentStep+1)
    }

    prompt := fmt.Sprintf(`Assess progress toward this goal after completing an action.

GOAL: %s

COMPLETED ACTIONS (most recent last):
%s

PENDING ACTIONS:
%s

CURRENT RESEARCH PLAN:
%s

EVALUATION CRITERIA:
1. Did the last action produce useful, relevant results?
2. Are we making progress toward the goal?
3. Is the remaining plan still optimal given what we learned?
4. Do we need to change direction?

RESPOND ONLY with S-expression (no markdown):

(assessment
  (progress_quality "good|partial|poor")
  (plan_validity "valid|needs_adjustment|needs_replan")
  (reasoning "1-2 sentence explanation of current state and why")
  (recommendation "continue|adjust|replan"))

DECISION RULES:
- progress_quality "good" = action produced relevant, useful information
- progress_quality "partial" = action produced some info but not ideal
- progress_quality "poor" = action failed or irrelevant results
- plan_validity "valid" = remaining plan is good
- plan_validity "needs_adjustment" = tweak remaining actions (change URLs, refine queries)
- plan_validity "needs_replan" = generate entirely new plan
- recommendation "continue" = proceed to next action
- recommendation "adjust" = modify next action parameters
- recommendation "replan" = call replan function`,
        goal.Description, completedSummary, pendingSummary, planSummary)

    response, tokens, err := e.callLLMWithStructuredReasoning(ctx, prompt, false)
    if err != nil {
        return nil, tokens, fmt.Errorf("assessment failed: %w", err)
    }

    // Parse assessment
    assessment, err := e.parseAssessmentSExpr(response.RawResponse)
    if err != nil {
        return nil, tokens, err
    }

    return assessment, tokens, nil
}

// parseAssessmentSExpr parses the assessment S-expression
func (e *Engine) parseAssessmentSExpr(rawResponse string) (*PlanAssessment, error) {
    content := strings.TrimSpace(rawResponse)
    content = strings.TrimPrefix(content, "```lisp")
    content = strings.TrimPrefix(content, "```")
    content = strings.TrimSuffix(content, "```")
    content = strings.TrimSpace(content)

    // Find assessment block
    blocks := findBlocksRecursive(content, "assessment")

    // FALLBACK: If recursive search fails, try to find the block manually
    // This handles cases where LLM adds conversational text before the block
    // or uses slightly different formatting.
    if len(blocks) == 0 {
        if strings.Contains(content, "(assessment") {
            // Find last occurrence (most likely to be the actual data)
            startIndex := strings.LastIndex(content, "(assessment")
            if startIndex != -1 {
                // Find matching closing parenthesis
                depth := 0
                endIndex := -1
                for i := startIndex; i < len(content); i++ {
                    if content[i] == '(' {
                        depth++
                    } else if content[i] == ')' {
                        depth--
                        if depth == 0 {
                            endIndex = i + 1 // Include closing paren
                            break
                        }
                    }
                }

                if endIndex != -1 {
                    blocks = append(blocks, content[startIndex:endIndex])
                }
            }
        }
    }

    if len(blocks) == 0 {
        return nil, fmt.Errorf("no assessment block found in response")
    }

    block := blocks[0]

    assessment := &PlanAssessment{
        ProgressQuality: extractFieldContent(block, "progress_quality"),
        PlanValidity:    extractFieldContent(block, "plan_validity"),
        Reasoning:       extractFieldContent(block, "reasoning"),
        Recommendation:  extractFieldContent(block, "recommendation"),
    }

    // Validate required fields
    if assessment.ProgressQuality == "" {
        assessment.ProgressQuality = "unknown"
    }
    if assessment.PlanValidity == "" {
        assessment.PlanValidity = "valid"
    }
    if assessment.Recommendation == "" {
        assessment.Recommendation = "continue"
    }

    return assessment, nil
}

// replanGoal generates a new plan based on what we've learned so far
func (e *Engine) replanGoal(ctx context.Context, goal *Goal, reason string) (*ResearchPlan, int, error) {
    // Summarize what we've learned from completed actions
    completedSummary := ""
    for i, action := range goal.Actions {
        if action.Status == ActionStatusCompleted {
            resultPreview := action.Result
            if len(resultPreview) > 300 {
                resultPreview = resultPreview[:300] + "..."
            }

            // Analyze if this was useful or not
            quality := "unknown"
            resultLower := strings.ToLower(action.Result)
            if strings.HasPrefix(resultLower, "error:") ||
                strings.HasPrefix(resultLower, "failed:") ||
                strings.Contains(resultLower[:min(100, len(resultLower))], "no suitable urls") {
                quality = "failed"
            } else if len(action.Result) > 500 {
                quality = "success"
            } else {
                quality = "partial"
            }

            completedSummary += fmt.Sprintf("%d. [%s] %s → %s\n   Result: %s\n",
                i+1, quality, action.Tool, action.Description, resultPreview)
        }
    }

    // Get original plan summary
    originalPlanSummary := ""
    if goal.ResearchPlan != nil {
        originalPlanSummary = fmt.Sprintf("Original Root Question: %s\n", goal.ResearchPlan.RootQuestion)
        for i, q := range goal.ResearchPlan.SubQuestions {
            status := q.Status
            if status == "" {
                status = "pending"
            }
            originalPlanSummary += fmt.Sprintf("  Q%d [%s]: %s\n", i+1, status, q.Question)
        }
    }

    prompt := fmt.Sprintf(`Generate a NEW research plan for a goal that needs replanning.

ORIGINAL GOAL: %s

WHAT WE'VE TRIED SO FAR:
%s

ORIGINAL PLAN:
%s

WHY WE NEED TO REPLAN:
%s

REQUIREMENTS FOR NEW PLAN:
1. Learn from failures - don't repeat approaches that didn't work
2. Adjust search strategy based on what we learned
3. Stay focused on ORIGINAL goal (don't drift)
4. Keep plan achievable (3-7 questions)
5. Make questions more specific if previous ones were too broad
6. Try different angles if direct approach failed

Generate research plan in SAME S-expression format as before:

(research_plan
  (root_question "Rephrased or refocused version of original goal")
  (sub_questions
    (question
      (id "q1")
      (text "First question - informed by what we learned")
      (search_query "better search terms")
      (priority 10)
      (deps ()))
    ... more questions ...))

CRITICAL: 
- If searches failed due to bad keywords, use DIFFERENT, MORE SPECIFIC terms
- If results were too technical, target beginner/practical resources  
- If results were too general, add specific constraints to searches
- Keep root_question aligned with original goal`,
        goal.Description,
        completedSummary,
        originalPlanSummary,
        reason)

    response, tokens, err := e.callLLMWithStructuredReasoning(ctx, prompt, true)
    if err != nil {
        return nil, tokens, fmt.Errorf("replan LLM call failed: %w", err)
    }

    // Parse using existing research plan parser
    content := response.RawResponse
    content = strings.TrimPrefix(content, "```lisp")
    content = strings.TrimPrefix(content, "```")
    content = strings.TrimSuffix(content, "```")
    content = strings.TrimSpace(content)

    // Use existing parsing logic
    planBlocks := findBlocksRecursive(content, "research_plan")
    if len(planBlocks) == 0 {
        planBlocks = findBlocks(content, "research_plan")
    }

    if len(planBlocks) == 0 {
        return nil, tokens, fmt.Errorf("no research_plan block found in replan response")
    }

    // Extract fields using existing helpers
    rootQuestion := extractFieldContent(planBlocks[0], "root_question")
    questionBlocks := findBlocks(planBlocks[0], "question")

    if len(questionBlocks) == 0 {
        return nil, tokens, fmt.Errorf("no question blocks found in research plan")
    }

    if len(questionBlocks) > 10 {
        questionBlocks = questionBlocks[:10]
    }

    newPlan := &ResearchPlan{
        RootQuestion:    rootQuestion,
        SubQuestions:    make([]ResearchQuestion, len(questionBlocks)),
        CurrentStep:     0,
        SynthesisNeeded: false,
        CreatedAt:       time.Now(),
        UpdatedAt:       time.Now(),
    }

    // Parse questions using existing logic
    for i, qBlock := range questionBlocks {
        getInt := func(field string) int {
            val := extractFieldContent(qBlock, field)
            if val == "" {
                return 0
            }
            if p, err := strconv.Atoi(val); err == nil {
                return p
            }
            return 0
        }

        getDeps := func(field string) []string {
            pattern := "(" + field + " "
            start := strings.Index(qBlock, pattern)
            if start == -1 {
                return []string{}
            }
            start += len(pattern)

            if start < len(qBlock) && qBlock[start] == ')' {
                return []string{}
            }

            var deps []string
            rest := qBlock[start:]
            for {
                qStart := strings.Index(rest, `"`)
                if qStart == -1 {
                    break
                }
                qEnd := strings.Index(rest[qStart+1:], `"`)
                if qEnd == -1 {
                    break
                }
                deps = append(deps, rest[qStart+1:qStart+1+qEnd])
                rest = rest[qStart+1+qEnd+1:]
                if strings.HasPrefix(rest, ")") {
                    break
                }
            }
            return deps
        }

        newPlan.SubQuestions[i] = ResearchQuestion{
            ID:              extractFieldContent(qBlock, "id"),
            Question:        extractFieldContent(qBlock, "text"),
            SearchQuery:     extractFieldContent(qBlock, "search_query"),
            Priority:        getInt("priority"),
            Dependencies:    getDeps("deps"),
            Status:          ResearchStatusPending,
            SourcesFound:    []string{},
            KeyFindings:     "",
            ConfidenceLevel: 0.0,
        }
    }

    return newPlan, tokens, nil
}

// evaluatePrincipleEffectiveness checks if current principles are working well
func (e *Engine) evaluatePrincipleEffectiveness(ctx context.Context, principles []memory.Principle, state *InternalState) (*PrincipleFeedback, int, error) {
    // Only check if we have recent failures
    if len(state.RecentFailures) == 0 {
        return &PrincipleFeedback{ShouldModify: false}, 0, nil
    }

    // Get recent goal outcomes (last 10)
    recentGoals := state.CompletedGoals
    if len(recentGoals) > 10 {
        recentGoals = recentGoals[len(recentGoals)-10:]
    }

    // Count failures
    failureCount := 0
    for _, goal := range recentGoals {
        if goal.Outcome == "bad" {
            failureCount++
        }
    }

    // Need at least 3 failures to consider modification
    if failureCount < 3 {
        return &PrincipleFeedback{ShouldModify: false}, 0, nil
    }

    // Build context about failures
    failureContext := ""
    for i, goal := range recentGoals {
        if goal.Outcome == "bad" {
            failureContext += fmt.Sprintf("%d. %s (Source: %s)\n", i+1, truncate(goal.Description, 80), goal.Source)
        }
    }

    // Build current principles context (AI-managed only)
    aiPrinciples := ""
    for _, p := range principles {
        if p.Slot >= 4 && p.Slot <= 10 && p.Content != "" {
            aiPrinciples += fmt.Sprintf("Slot %d: %s\n", p.Slot, p.Content)
        }
    }

    if aiPrinciples == "" {
        aiPrinciples = "No AI-managed principles defined yet.\n"
    }

    prompt := fmt.Sprintf(`Evaluate if current thinking principles need modification based on recent failures.

CURRENT AI-MANAGED PRINCIPLES (Slots 4-10):
%s

RECENT FAILURES (%d out of last %d goals):
%s

METACOGNITIVE ANALYSIS:
1. Is there a pattern in these failures?
2. Would modifying a principle help prevent similar failures?
3. Which specific principle (slot 4-10) should change, if any?

CRITICAL RULES:
- NEVER propose modifying slots 1-3 (admin principles)
- Only propose modification if pattern is clear
- Principle must be BEHAVIORAL (how to think/act), not a goal
- Must be specific enough to actually change behavior

RESPOND with S-expression (no markdown):

(principle_evaluation
  (should_modify true|false)
  (target_slot 4-10)  ; Only if should_modify=true
  (current_principle "Current text from that slot")
  (proposed_principle "New behavioral principle to replace it")
  (justification "Why this specific change addresses the failure pattern")
  (test_strategy "How to validate this change works"))

If should_modify=false, only include (should_modify false).`,
        aiPrinciples,
        failureCount,
        len(recentGoals),
        failureContext)

    response, tokens, err := e.callLLMWithStructuredReasoning(ctx, prompt, true)
    if err != nil {
        return nil, tokens, fmt.Errorf("principle evaluation failed: %w", err)
    }

    // Parse response
    feedback, err := e.parsePrincipleFeedback(response.RawResponse)
    if err != nil {
        return nil, tokens, fmt.Errorf("failed to parse principle feedback: %w", err)
    }

    return feedback, tokens, nil
}

// PrincipleFeedback represents LLM's evaluation of whether to modify principles
type PrincipleFeedback struct {
    ShouldModify       bool
    TargetSlot         int
    CurrentPrinciple   string
    ProposedPrinciple  string
    Justification      string
    TestStrategy       string
}

// parsePrincipleFeedback extracts feedback from S-expression
func (e *Engine) parsePrincipleFeedback(rawResponse string) (*PrincipleFeedback, error) {
    content := strings.TrimSpace(rawResponse)
    content = strings.TrimPrefix(content, "```lisp")
    content = strings.TrimPrefix(content, "```")
    content = strings.TrimSuffix(content, "```")
    content = strings.TrimSpace(content)

    blocks := findBlocksRecursive(content, "principle_evaluation")
    if len(blocks) == 0 {
        return nil, fmt.Errorf("no principle_evaluation block found")
    }

    block := blocks[0]

    shouldModifyStr := extractFieldContent(block, "should_modify")
    shouldModify := (shouldModifyStr == "true" || shouldModifyStr == "t")

    feedback := &PrincipleFeedback{
        ShouldModify: shouldModify,
    }

    if !shouldModify {
        return feedback, nil
    }

    // Extract modification details
    targetSlotStr := extractFieldContent(block, "target_slot")
    if targetSlotStr != "" {
        if slot, err := strconv.Atoi(targetSlotStr); err == nil {
            feedback.TargetSlot = slot
        }
    }

    feedback.CurrentPrinciple = extractFieldContent(block, "current_principle")
    feedback.ProposedPrinciple = extractFieldContent(block, "proposed_principle")
    feedback.Justification = extractFieldContent(block, "justification")
    feedback.TestStrategy = extractFieldContent(block, "test_strategy")

    // Validate slot range
    if feedback.TargetSlot < 4 || feedback.TargetSlot > 10 {
        return nil, fmt.Errorf("invalid target slot: %d (must be 4-10)", feedback.TargetSlot)
    }

    // Validate required fields
    if feedback.ProposedPrinciple == "" {
        return nil, fmt.Errorf("proposed_principle is required when should_modify=true")
    }

    return feedback, nil
}

// createSelfModificationGoal creates a goal to test and potentially commit a principle change
func (e *Engine) createSelfModificationGoal(feedback *PrincipleFeedback) Goal {
    // Generate test actions based on strategy
    testActions := []Action{
        {
            Description: "Test new principle with search task",
            Tool:        ActionToolSearch,
            Status:      ActionStatusPending,
            Timestamp:   time.Now(),
            Metadata: map[string]interface{}{
                "is_principle_test": true,
                "test_type":         "search_quality",
            },
        },
    }

    goal := Goal{
        ID:          fmt.Sprintf("goal_%d", time.Now().UnixNano()),
        Description: fmt.Sprintf("Test principle modification: %s", truncate(feedback.ProposedPrinciple, 60)),
        Source:      GoalSourceSelfModification,
        Priority:    8, // High priority - self-improvement is important
        Created:     time.Now(),
        Progress:    0.0,
        Status:      GoalStatusActive,
        Actions:     testActions,
        Tier:        "primary",
        SelfModGoal: &SelfModificationGoal{
            TargetSlot:         feedback.TargetSlot,
            CurrentPrinciple:   feedback.CurrentPrinciple,
            ProposedPrinciple:  feedback.ProposedPrinciple,
            Justification:      feedback.Justification,
            TestActions:        testActions,
            BaselineComparison: "Compare to recent failures with current principle",
            ValidationStatus:   "pending",
        },
    }

    return goal
}

// testPrincipleModification validates a proposed principle change
func (e *Engine) testPrincipleModification(ctx context.Context, goal *Goal, currentPrinciples []memory.Principle) (bool, string) {
    if goal.SelfModGoal == nil {
        return false, "No self-modification data"
    }

    modGoal := goal.SelfModGoal

    // Simple validation test: Does the new principle make semantic sense?
    // In a full implementation, this would execute test actions and compare results

    prompt := fmt.Sprintf(`Validate a proposed principle modification.

CURRENT PRINCIPLE (Slot %d):
%s

PROPOSED PRINCIPLE:
%s

JUSTIFICATION:
%s

VALIDATION CRITERIA:
1. Is the proposed principle BEHAVIORAL (how to think/act, not a task/goal)?
2. Is it specific enough to actually change behavior?
3. Does it address the stated justification?
4. Would it likely improve outcomes based on the justification?

RESPOND with S-expression:

(validation
  (is_valid true|false)
  (reasoning "Why this change would/wouldn't help")
  (predicted_improvement "low|medium|high"))`,
        modGoal.TargetSlot,
        modGoal.CurrentPrinciple,
        modGoal.ProposedPrinciple,
        modGoal.Justification)

    response, _, err := e.callLLMWithStructuredReasoning(ctx, prompt, true)
    if err != nil {
        return false, fmt.Sprintf("Validation failed: %v", err)
    }

    // Parse validation
    content := strings.TrimSpace(response.RawResponse)
    content = strings.TrimPrefix(content, "```lisp")
    content = strings.TrimPrefix(content, "```")
    content = strings.TrimSuffix(content, "```")
    content = strings.TrimSpace(content)

    blocks := findBlocksRecursive(content, "validation")
    if len(blocks) == 0 {
        return false, "Could not parse validation response"
    }

    block := blocks[0]
    isValidStr := extractFieldContent(block, "is_valid")
    reasoning := extractFieldContent(block, "reasoning")

    isValid := (isValidStr == "true" || isValidStr == "t")

    if isValid {
        return true, fmt.Sprintf("Validated: %s", reasoning)
    } else {
        return false, fmt.Sprintf("Rejected: %s", reasoning)
    }
}

// handleLargePageFallback attempts to recover from a "page too large" error by
// fetching metadata and asking the LLM to select relevant chunks to parse.
func (e *Engine) handleLargePageFallback(ctx context.Context, url string, goal *Goal) ([]Action, error) {
    log.Printf("[Dialogue] Initiating fallback strategy for large page (URL: %s)", truncate(url, 60))

    // Step 1: Get Metadata
    params := map[string]interface{}{
        "url": url,
    }

    metadataResult, err := e.toolRegistry.ExecuteIdle(ctx, ActionToolWebParseMetadata, params)
    if err != nil {
        return nil, fmt.Errorf("fallback failed: metadata tool error: %w", err)
    }
    if !metadataResult.Success {
        return nil, fmt.Errorf("fallback failed: metadata tool returned failure: %s", metadataResult.Error)
    }

    log.Printf("[Dialogue] ✓ Metadata retrieved (%d chars), analyzing with LLM...", len(metadataResult.Output))

    // Step 2: Ask LLM to select chunks based on metadata and goal
    // We use callLLMWithStructuredReasoning to ensure we get a clean list of indices
    prompt := fmt.Sprintf(`We are researching a large web page but cannot parse it fully in one go.
We have retrieved the METADATA for the page.

GOAL: %s

URL: %s

PAGE METADATA:
%s

AVAILABLE CAPABILITY:
- web_parse_chunked: Read specific 500-token chunks by index (0, 1, 2, ...).

ANALYSIS INSTRUCTIONS:
1. Analyze the metadata to understand the page structure (sections, headings, length).
2. Identify which parts of the page are most relevant to the GOAL.
3. Select specific chunk indices that cover these relevant parts.
   - Example: If metadata shows "Introduction (chunks 0-2)" and goal is intro, select 0, 1, 2.
   - Example: If metadata shows "Conclusion (chunk 9)" and goal is summary, select 9.
4. Estimate indices if metadata implies location (e.g., start/middle/end).
5. Limit selection to 3-5 chunks to stay efficient.

RESPOND ONLY with this S-expression (no markdown):

(chunk_selection_plan
  (selected_chunks (0 3 4))  ; List of integers (e.g., 0 3 4)
  (reasoning "Brief explanation of why these chunks are relevant"))

Rules:
- selected_chunks must be a list of integers.
- Prioritize relevance over completeness.
- If metadata is vague, estimate start (0) and subsequent indices.`, goal.Description, url, metadataResult.Output)

    response, tokens, err := e.callLLMWithStructuredReasoning(ctx, prompt, false)
    if err != nil {
        return nil, fmt.Errorf("fallback failed: LLM selection failed: %w", err)
    }

    // Step 3: Parse the chunk indices
    chunks, reasoning, err := parseChunkSelectionSExpr(response.RawResponse)
    if err != nil {
        log.Printf("[Dialogue] Failed to parse chunk selection: %v", err)
        return nil, fmt.Errorf("fallback failed: parsing error: %w", err)
    }

    log.Printf("[Dialogue] ✓ LLM selected %d chunks: %v. Reasoning: %s", len(chunks), chunks, truncate(reasoning, 100))

    // Step 4: Create actions for the selected chunks
    var actions []Action
    for _, idx := range chunks {
        actions = append(actions, Action{
            Description: fmt.Sprintf("Read chunk %d from %s", idx, url),
            Tool:        ActionToolWebParseChunked,
            Status:      ActionStatusPending,
            Timestamp:   time.Now(),
            Metadata: map[string]interface{}{
                "url":         url,
                "chunk_index": idx,
                "recovery":    "metadata_driven_chunking",
            },
        })
    }

    return actions, nil
}

// parseChunkSelectionSExpr extracts the list of chunk indices from LLM response
func (e *Engine) parseChunkSelectionSExpr(rawResponse string) ([]int, string, error) {
    content := strings.TrimSpace(rawResponse)
    content = strings.TrimPrefix(content, "```lisp")
    content = strings.TrimPrefix(content, "```")
    content = strings.TrimSuffix(content, "```")
    content = strings.TrimSpace(content)

    // Find chunk_selection_plan block
    blocks := findBlocksRecursive(content, "chunk_selection_plan")
    if len(blocks) == 0 {
        return nil, "", fmt.Errorf("no chunk_selection_plan block found")
    }

    block := blocks[0]

    // Extract reasoning
    reasoning := extractFieldContent(block, "reasoning")

    // Extract selected_chunks list
    // We expect format: (selected_chunks (0 3 4))
    indicesPattern := "(selected_chunks "
    start := strings.Index(block, indicesPattern)
    if start == -1 {
        return nil, reasoning, fmt.Errorf("selected_chunks field not found")
    }

    start += len(indicesPattern)

    // Extract content until closing paren
    contentEnd := strings.Index(block[start:], ")")
    if contentEnd == -1 {
        return nil, reasoning, fmt.Errorf("malformed selected_chunks list")
    }

    listContent := strings.TrimSpace(block[start : start+contentEnd])

    // Parse integers from the list
    var indices []int
    parts := strings.Fields(listContent)
    for _, part := range parts {
        if idx, err := strconv.Atoi(part); err == nil {
            indices = append(indices, idx)
        }
    }

    if len(indices) == 0 {
        return nil, reasoning, fmt.Errorf("no indices found in selected_chunks")
    }

    return indices, reasoning, nil
}

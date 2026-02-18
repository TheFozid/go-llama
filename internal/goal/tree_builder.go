// internal/goal/tree_builder.go
package goal

import (
    "context"
    "fmt"
    "log"
    "regexp"
    "strconv"
    "strings"
)

// TreeBuilder decomposes goals into hierarchical sub-goals.
type TreeBuilder struct {
    llm LLMService
}

// NewTreeBuilder creates a new tree builder.
func NewTreeBuilder(llm LLMService) *TreeBuilder {
    return &TreeBuilder{llm: llm}
}

// DecomposeGoal generates a sub-goal tree for the given goal.
func (t *TreeBuilder) DecomposeGoal(ctx context.Context, g *Goal, availableTools []string) error {
    if len(g.SubGoals) > 0 {
        log.Printf("[TreeBuilder] Goal %s already has sub-goals, skipping.", g.ID)
        return nil
    }

    log.Printf("[TreeBuilder] Decomposing goal: %s", g.Description)

    // 1. Format tool list dynamically (No hardcoded descriptions)
    toolList := "None"
    if len(availableTools) > 0 {
        toolList = strings.Join(availableTools, ", ")
    }

    // 2. S-Expression Prompt
    prompt := fmt.Sprintf(`You are a strategic planner AI. Decompose the following goal into a hierarchy of actionable sub-goals.

Goal: %s
Type: %s

CONSTRAINTS:
1. Available Tools: [%s].
   - You MUST select 'tool_name' EXACTLY from this list. 
   - Do NOT invent tool names.
2. Action Types: RESEARCH, PRACTICE, EXECUTE_TOOL, REFLECT, CREATE.
   - RESEARCH must use tool: search.
   - EXECUTE_TOOL must use a tool from the list.

OUTPUT FORMAT:
Output a valid S-expression (Lisp-style list).
Structure:
(plan
  (step 
    (id "1")
    (title "...")
    (description "...")
    (effort "MEDIUM")
    (action_type "RESEARCH")
    (tool_name "search")
    (params (query "..."))
    (dependencies ())
    (sub_steps
      (step (id "1.1") (title "...") ... (tool_name "web_parse_unified") ...)
    )
  )
)

Rules:
- Use double quotes for strings.
- 'params' is a list of key-value pairs, e.g., (params (url "http://...") (query "...")).
- If a URL depends on a previous step, use "EXTRACT_FROM_PREVIOUS_STEP".
- Every step MUST have a valid 'tool_name' if it interacts with data.

Output ONLY the S-expression.
`, g.Description, g.Type, toolList)

    responseText, err := t.llm.GenerateText(ctx, prompt)
    if err != nil {
        return fmt.Errorf("failed to decompose goal: %w", err)
    }

    // 3. Parse S-Expression
    planSteps, err := parseSExprPlan(responseText)
    if err != nil {
        return fmt.Errorf("failed to parse plan S-expr: %w", err)
    }

    // 4. Flatten hierarchy into SubGoal list (Same logic as before)
    var subGoals []SubGoal
    for _, step := range planSteps {
        sg := SubGoal{
            ID:              step.ID,
            Title:           step.Title,
            Description:     step.Description,
            Status:          SubGoalPending,
            Dependencies:    step.Dependencies,
            EstimatedEffort: step.Effort,
            ActionType:      ActionType(step.ActionType),
            ToolName:        step.ToolName,
            Params:          step.Params,
        }
        subGoals = append(subGoals, sg)

        for _, sub := range step.SubSteps {
            ssg := SubGoal{
                ID:              sub.ID,
                Title:           sub.Title,
                Description:     sub.Description,
                Status:          SubGoalPending,
                Dependencies:    sub.Dependencies,
                EstimatedEffort: sub.Effort,
                ActionType:      ActionType(sub.ActionType),
                ToolName:        sub.ToolName,
                Params:          sub.Params,
            }
            subGoals = append(subGoals, ssg)
        }
    }

    g.SubGoals = subGoals
    g.TreeDepth = calculateDepth(subGoals)

    log.Printf("[TreeBuilder] Created tree with %d sub-goals for goal %s", len(subGoals), g.ID)
    return nil
}

// ReplanSubTree uses S-expr for consistency.
func (t *TreeBuilder) ReplanSubTree(ctx context.Context, g *Goal, failedSubGoalID string, failureReason string, availableTools []string) error {
    log.Printf("[TreeBuilder] Replanning branch starting at %s due to: %s", failedSubGoalID, failureReason)

    toolList := strings.Join(availableTools, ", ")

    prompt := fmt.Sprintf(`You are a strategic planner AI. A plan failed and needs adjustment.

Original Goal: %s
Failed Step: %s
Failure Reason: %s

Available Tools: [%s].
You MUST select 'tool_name' from this list.

Propose 2-3 alternative sub-goals. Output ONLY an S-expression list of steps.
(new_plan
  (step 
    (id "%s_alt1")
    (title "...")
    (tool_name "search")
    ...
  )
)
`, g.Description, failedSubGoalID, failureReason, toolList, failedSubGoalID)

    responseText, err := t.llm.GenerateText(ctx, prompt)
    if err != nil {
        return fmt.Errorf("failed to replan sub-tree: %w", err)
    }

    planSteps, err := parseSExprPlan(responseText)
    if err != nil {
        return fmt.Errorf("failed to parse replan S-expr: %w", err)
    }

    for _, np := range planSteps {
        newSG := SubGoal{
            ID:              np.ID,
            Title:           np.Title,
            Description:     np.Description,
            Status:          SubGoalPending,
            EstimatedEffort: np.Effort,
            ActionType:      ActionType(np.ActionType),
            ToolName:        np.ToolName,
            Dependencies:    []string{failedSubGoalID},
        }
        g.SubGoals = append(g.SubGoals, newSG)
    }

    // Mark original failed sub-goal as FAILED
    for i, sg := range g.SubGoals {
        if sg.ID == failedSubGoalID {
            g.SubGoals[i].Status = SubGoalFailed
            g.SubGoals[i].FailureReason = failureReason
        }
    }

    log.Printf("[TreeBuilder] Added %d alternative steps for failed branch %s", len(planSteps), failedSubGoalID)
    return nil
}

func calculateDepth(sgs []SubGoal) int {
    maxDepth := 0
    for _, sg := range sgs {
        depth := strings.Count(sg.ID, ".") + 1
        if depth > maxDepth {
            maxDepth = depth
        }
    }
    return maxDepth
}

// --- S-Expression Parser Helpers ---

type parsedStep struct {
    ID, Title, Description, Effort, ActionType, ToolName string
    Params                                               map[string]interface{}
    Dependencies                                         []string
    SubSteps                                             []parsedStep
}

// parseSExprPlan is a lightweight parser for the specific S-expr format.
func parseSExprPlan(input string) ([]parsedStep, error) {
    // 1. Clean Input: Remove markdown code blocks if present
    input = strings.TrimSpace(input)
    if strings.HasPrefix(input, "```") {
        // Remove opening ```lisp or ```
        firstNewline := strings.Index(input, "\n")
        if firstNewline != -1 {
            input = input[firstNewline+1:]
        }
        // Remove closing ```
        if idx := strings.LastIndex(input, "```"); idx != -1 {
            input = input[:idx]
        }
    }
    
    // Normalize whitespace for simpler parsing
    input = strings.ReplaceAll(input, "\n", " ")
    input = strings.ReplaceAll(input, "\t", " ")

    // 2. Find the root container. We prefer (plan) or (new_plan), but fall back to raw steps.
    rootNode := ""
    
    // Try to find explicit roots first
    if idx := strings.Index(input, "(plan"); idx != -1 {
        rootNode = findNode(input, "plan")
    }
    if rootNode == "" {
        if idx := strings.Index(input, "(new_plan"); idx != -1 {
            rootNode = findNode(input, "new_plan")
        }
    }
    
    // 3. Fallback: If no root found, treat the whole input as a container of steps
    // This handles cases where LLM outputs just (step ...) (step ...)
    if rootNode == "" {
        // If we find (step, assume the input is a list of steps
        if strings.Contains(input, "(step") {
            // We create a fake root context for parseSteps to work on the whole string
            rootNode = input 
        } else {
            return nil, fmt.Errorf("no valid S-expression structure found")
        }
    }

    return parseSteps(rootNode)
}

func parseSteps(input string) ([]parsedStep, error) {
    steps := []parsedStep{}
    
    // Find all (step ...) occurrences
    stepRegex := regexp.MustCompile(`\(step\s+`)
    indices := stepRegex.FindAllStringIndex(input, -1)

    for _, idx := range indices {
        start := idx[0]
        // Find the matching closing paren for this step
        end := findMatchingParen(input, start)
        if end == -1 || end <= start {
            continue
        }
        
        stepContent := input[start : end+1]
        step, err := parseSingleStep(stepContent)
        if err != nil {
            log.Printf("[TreeBuilder] Warn: failed to parse step: %v", err)
            continue
        }
        steps = append(steps, step)
    }

    return steps, nil
}

func parseSingleStep(input string) (parsedStep, error) {
    s := parsedStep{
        Params: make(map[string]interface{}),
    }

    // Helper to extract field
    extract := func(fieldName string) string {
        node := findNode(input, fieldName)
        if node == "" {
            return ""
        }
        // Remove (fieldName and outer parens
        // e.g., (title "My Title") -> "My Title"
        content := strings.TrimSpace(node)
        content = strings.TrimPrefix(content, "("+fieldName)
        content = strings.TrimSuffix(content, ")")
        content = strings.TrimSpace(content)
        
        // Strip quotes if present
        content = strings.Trim(content, "\"")
        return content
    }

    s.ID = extract("id")
    s.Title = extract("title")
    s.Description = extract("description")
    s.Effort = extract("effort")
    s.ActionType = extract("action_type")
    s.ToolName = extract("tool_name")

    // Parse Dependencies (list of strings)
    depsNode := findNode(input, "dependencies")
    if depsNode != "" {
        depsContent := strings.TrimSpace(depsNode)
        depsContent = strings.TrimPrefix(depsContent, "(dependencies")
        depsContent = strings.TrimSuffix(depsContent, ")")
        depsContent = strings.TrimSpace(depsContent)
        
        // Extract quoted strings
        if strings.Contains(depsContent, "\"") {
            re := regexp.MustCompile(`"([^"]*)"`)
            matches := re.FindAllStringSubmatch(depsContent, -1)
            for _, m := range matches {
                if len(m) > 1 {
                    s.Dependencies = append(s.Dependencies, m[1])
                }
            }
        }
    }

    // Parse Params
    paramsNode := findNode(input, "params")
    if paramsNode != "" {
        paramsContent := strings.TrimSpace(paramsNode)
        paramsContent = strings.TrimPrefix(paramsContent, "(params")
        paramsContent = strings.TrimSuffix(paramsContent, ")")
        
        // Naive param parsing: (key "value")
        re := regexp.MustCompile(`\((\w+)\s+"([^"]*)"\)`)
        matches := re.FindAllStringSubmatch(paramsContent, -1)
        for _, m := range matches {
            if len(m) > 2 {
                if m[1] == "limit" {
                    if i, err := strconv.Atoi(m[2]); err == nil {
                        s.Params[m[1]] = i
                    }
                } else {
                    s.Params[m[1]] = m[2]
                }
            }
        }
    }

    // Parse SubSteps recursively
    subStepsNode := findNode(input, "sub_steps")
    if subStepsNode != "" {
        subs, err := parseSteps(subStepsNode)
        if err == nil {
            s.SubSteps = subs
        }
    }

    return s, nil
}

// findNode extracts the content of a specific named list, e.g., (title "...")
func findNode(input, name string) string {
    target := "(" + name
    start := strings.Index(input, target)
    if start == -1 {
        return ""
    }
    
    end := findMatchingParen(input, start)
    if end == -1 {
        return ""
    }
    return input[start : end+1]
}

// findMatchingParen finds the index of the closing ')' for the '(' at startIndex.
func findMatchingParen(input string, startIndex int) int {
    if startIndex >= len(input) || input[startIndex] != '(' {
        return -1
    }
    balance := 1
    for i := startIndex + 1; i < len(input); i++ {
        if input[i] == '(' {
            balance++
        } else if input[i] == ')' {
            balance--
        }
        if balance == 0 {
            return i
        }
    }
    return -1
}

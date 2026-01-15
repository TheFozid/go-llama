package dialogue

import (
    "fmt"
    "strconv"
    "strings"
)

// ParseReasoningSExpr parses S-expression format reasoning
func ParseReasoningSExpr(input string) (*ReasoningResponse, error) {
    input = strings.TrimSpace(input)
    
    // Remove markdown fences
    input = strings.TrimPrefix(input, "```lisp")
    input = strings.TrimPrefix(input, "```")
    input = strings.TrimSuffix(input, "```")
    input = strings.TrimSpace(input)
    
    // CRITICAL FIX: Remove problematic outer quotes
    // LLM sometimes generates: (reasoning "(insights ...)")
    // Instead of: (reasoning (insights ...))
    input = fixMalformedQuotedSexpr(input)
    
    // Auto-fix unbalanced parentheses
    input = autoBalanceParens(input)
    
    // Tokenize
    tokens := tokenize(input)
    if len(tokens) == 0 {
        return nil, fmt.Errorf("empty input")
    }
    
    // Parse root with error recovery
    root, _, err := parseExpr(tokens, 0)
    if err != nil {
        // CRITICAL FIX: Try to extract what we can from malformed input
        return extractPartialReasoning(input)
    }
    
    // Convert to ReasoningResponse
    return sexprToReasoning(root)
}

// NEW FUNCTION: Extract partial reasoning from malformed input
func extractPartialReasoning(input string) (*ReasoningResponse, error) {
    response := &ReasoningResponse{
        Insights:      []string{},
        Strengths:     []string{},
        Weaknesses:    []string{},
        KnowledgeGaps: []string{},
        Patterns:      []string{},
        GoalsToCreate: GoalsOrString{},
        Learnings:     LearningsOrString{},
    }
    
    // Try to extract key fields using regex-like approach
    // Look for patterns like (reflection "text") or (reflection text)
    
    // Extract reflection
    if match := extractFieldContent(input, "reflection"); match != "" {
        response.Reflection = match
    }
    
    // Extract insights
    if matches := extractMultipleFieldContents(input, "insights"); len(matches) > 0 {
        response.Insights = matches
    }
    
    // Extract goals
    if goals := extractGoalsFromMalformed(input); len(goals) > 0 {
        response.GoalsToCreate = GoalsOrString(goals)
    }
    
    // Even if we couldn't parse properly, return what we found
    return response, nil
}

// NEW FUNCTION: Extract goals from malformed S-expressions
func extractGoalsFromMalformed(input string) []GoalProposal {
    var goals []GoalProposal
    
    // Look for goal patterns even in malformed input
    // Pattern: (goal (description "text") ...)
    
    // Find all goal blocks
    goalBlocks := findBlocks(input, "goal")
    
    for _, block := range goalBlocks {
        goal := GoalProposal{
            Priority:   7, // Default
            ActionPlan: []string{},
        }
        
        // Extract description
        if desc := extractFieldContent(block, "description"); desc != "" {
            goal.Description = desc
        }
        
        // Extract priority
        if prio := extractFieldContent(block, "priority"); prio != "" {
            if p, err := strconv.Atoi(prio); err == nil {
                goal.Priority = p
            }
        }
        
        // Extract reasoning
        if reason := extractFieldContent(block, "reasoning"); reason != "" {
            goal.Reasoning = reason
        }
        
        // Only add if we have at least a description
        if goal.Description != "" {
            goals = append(goals, goal)
        }
    }
    
    return goals
}

// NEW FUNCTION: Find all blocks with a specific name
func findBlocks(input, blockName string) []string {
    var blocks []string
    
    // Look for patterns like (blockName ...)
    start := 0
    for {
        // Find opening
        openPos := strings.Index(input[start:], "("+blockName)
        if openPos == -1 {
            break
        }
        openPos += start
        
        // Find matching close
        depth := 0
        closePos := -1
        for i := openPos; i < len(input); i++ {
            if input[i] == '(' {
                depth++
            } else if input[i] == ')' {
                depth--
                if depth == 0 {
                    closePos = i
                    break
                }
            }
        }
        
        if closePos == -1 {
            break // No matching close
        }
        
        // Extract block
        blocks = append(blocks, input[openPos:closePos+1])
        start = closePos + 1
    }
    
    return blocks
}

// NEW FUNCTION: Extract field content from malformed S-expression
func extractFieldContent(input, fieldName string) string {
    // Look for patterns like (fieldName "content") or (fieldName content)
    pattern := "(" + fieldName + " "
    start := strings.Index(input, pattern)
    if start == -1 {
        return ""
    }
    
    start += len(pattern)
    
    // Check if content is quoted
    if start < len(input) && input[start] == '"' {
        // Find closing quote
        end := strings.Index(input[start+1:], "\"")
        if end == -1 {
            return "" // No closing quote
        }
        return input[start+1 : start+1+end]
    } else {
        // Find next space or closing paren
        end := strings.IndexAny(input[start:], " )")
        if end == -1 {
            return input[start:] // No delimiter, return rest
        }
        return input[start : start+end]
    }
}

// NEW FUNCTION: Extract multiple field contents
func extractMultipleFieldContents(input, fieldName string) []string {
    var contents []string
    
    // Find all occurrences of the field
    pattern := "(" + fieldName + " "
    start := 0
    
    for {
        pos := strings.Index(input[start:], pattern)
        if pos == -1 {
            break
        }
        pos += start + len(pattern)
        
        // Extract content
        var content string
        if pos < len(input) && input[pos] == '"' {
            // Quoted content
            end := strings.Index(input[pos+1:], "\"")
            if end == -1 {
                break
            }
            content = input[pos+1 : pos+1+end]
        } else {
            // Unquoted content
            end := strings.IndexAny(input[pos:], " )")
            if end == -1 {
                content = input[pos:]
            } else {
                content = input[pos : pos+end]
            }
        }
        
        if content != "" {
            contents = append(contents, content)
        }
        
        start = pos + len(content) + 1
    }
    
    return contents
}

// ENHANCED FUNCTION: Better auto-balancing of parentheses
func autoBalanceParens(input string) string {
    // First, try to find the last complete S-expression
    // This helps when the LLM output was cut off mid-expression
    
    // Find the position of the last complete expression
    // by counting parentheses from the end
    depth := 0
    lastCompletePos := -1
    
    for i := len(input) - 1; i >= 0; i-- {
        if input[i] == ')' {
            depth++
        } else if input[i] == '(' {
            depth--
            if depth == 0 {
                lastCompletePos = i
                break
            }
        }
    }
    
    // If we found a complete expression, truncate to that point
    if lastCompletePos > 0 {
        // Find the end of this expression
        depth = 0
        for i := lastCompletePos; i < len(input); i++ {
            if input[i] == '(' {
                depth++
            } else if input[i] == ')' {
                depth--
                if depth == 0 {
                    // Found the end of the complete expression
                    input = input[:i+1]
                    break
                }
            }
        }
    }
    
    // Now balance any remaining unbalanced parentheses
    openCount := strings.Count(input, "(")
    closeCount := strings.Count(input, ")")
    
    if openCount > closeCount {
        // Add missing closing parens
        missing := openCount - closeCount
        input = input + strings.Repeat(")", missing)
    } else if closeCount > openCount {
        // Remove extra closing parens (trim from end)
        for closeCount > openCount && strings.HasSuffix(input, ")") {
            input = strings.TrimSuffix(input, ")")
            closeCount--
        }
    }
    
    return input
}

// ENHANCED FUNCTION: More robust field extraction
func extractStringList(exprs []expr) []string {
    var result []string
    
    for _, e := range exprs {
        if e.isAtom {
            result = append(result, e.atom)
        } else if len(e.list) > 0 {
            // Handle nested lists
            nested := extractStringList(e.list)
            result = append(result, nested...)
        }
    }
    
    return result
}

// ENHANCED FUNCTION: More robust reflection parsing
func ParseReflectionSExpr(content string) (*ReflectionData, error) {
    r := &ReflectionData{}
    content = strings.TrimSpace(content)
    
    // Remove outer wrapper
    if strings.HasPrefix(content, "(reflection") {
        content = strings.TrimPrefix(content, "(reflection")
        content = strings.TrimSuffix(content, ")")
    }
    
    // Try to parse field by field, but be forgiving of errors
    for {
        content = strings.TrimSpace(content)
        if content == "" {
            break
        }
        
        if !strings.HasPrefix(content, "(") {
            // If we don't see an opening paren, we might have malformed input
            // Try to extract what we can
            break
        }
        
        // Find matching close paren
        depth := 0
        end := -1
        for i, ch := range content {
            if ch == '(' {
                depth++
            } else if ch == ')' {
                depth--
                if depth == 0 {
                    end = i
                    break
                }
            }
        }
        
        if end == -1 {
            // Unbalanced parentheses - try to extract what we can
            break
        }
        
        // Extract field
        field := content[1:end]
        content = content[end+1:]
        
        // Parse field name and value
        parts := strings.SplitN(field, " ", 2)
        if len(parts) < 2 {
            continue
        }
        
        name := strings.TrimSpace(parts[0])
        value := strings.TrimSpace(parts[1])
        value = strings.Trim(value, `"`)
        
        switch name {
        case "outcome_quality":
            r.OutcomeQuality = value
        case "reasoning":
            r.Reasoning = value
        case "mistake_made":
            r.MistakeMade = value == "true"
        case "mistake_description":
            r.MistakeDescription = value
        case "user_requested_goal":
            r.UserRequestedGoal = value == "true"
        case "goal_description":
            r.GoalDescription = value
        case "user_gave_feedback":
            r.UserGaveFeedback = value == "true"
        case "feedback_type":
            r.FeedbackType = value
        case "feedback_summary":
            r.FeedbackSummary = value
        case "important_learning":
            r.ImportantLearning = value == "true"
        case "learning_content":
            r.LearningContent = value
        }
    }
    
    // Only require outcome_quality - everything else is optional
    return r, nil
}

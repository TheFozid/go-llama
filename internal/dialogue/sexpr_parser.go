package dialogue

import (
    "fmt"
    "strconv"
    "strings"
	"time"
)

// ParseReasoningSExpr parses S-expression format reasoning
func ParseReasoningSExpr(input string) (*ReasoningResponse, error) {
    input = strings.TrimSpace(input)

    // FIX: Handle global quote wrapping (e.g., LLM returns "(...)" as a string)
    if len(input) >= 2 && strings.HasPrefix(input, "\"") && strings.HasSuffix(input, "\"") {
        // If it looks like a quoted S-Expr (contains parens), unwrap it
        if strings.Contains(input, "(") || strings.Contains(input, ")") {
            input = input[1 : len(input)-1]
        }
    }

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

// Token types
type token struct {
    typ   string // "lparen", "rparen", "atom", "string"
    value string
}

// Expr represents an S-expression
type expr struct {
    isAtom bool
    atom   string
    list   []expr
}

// tokenize breaks input into tokens
func tokenize(input string) []token {
    var tokens []token
    i := 0
    
    for i < len(input) {
        ch := input[i]
        
        // Skip whitespace
        if ch == ' ' || ch == '\n' || ch == '\t' || ch == '\r' {
            i++
            continue
        }
        
        // Left paren
        if ch == '(' {
            tokens = append(tokens, token{typ: "lparen", value: "("})
            i++
            continue
        }
        
        // Right paren
        if ch == ')' {
            tokens = append(tokens, token{typ: "rparen", value: ")"})
            i++
            continue
        }
        
        // String literal
        if ch == '"' {
            i++ // Skip opening quote
            start := i
            escaped := false
            
            for i < len(input) {
                if escaped {
                    escaped = false
                    i++
                    continue
                }
                
                if input[i] == '\\' {
                    escaped = true
                    i++
                    continue
                }
                
                if input[i] == '"' {
                    break
                }
                
                i++
            }
            
            value := input[start:i]
            // Unescape
            value = strings.ReplaceAll(value, `\"`, `"`)
            value = strings.ReplaceAll(value, `\\`, `\`)
            
            tokens = append(tokens, token{typ: "string", value: value})
            i++ // Skip closing quote
            continue
        }
        
        // Atom (symbol or number)
        start := i
        for i < len(input) && input[i] != '(' && input[i] != ')' && 
            input[i] != ' ' && input[i] != '\n' && input[i] != '\t' && input[i] != '\r' {
            i++
        }
        
        atom := input[start:i]
        if atom != "" {
            tokens = append(tokens, token{typ: "atom", value: atom})
        }
    }
    
    return tokens
}

// parseExpr parses one S-expression
func parseExpr(tokens []token, pos int) (expr, int, error) {
    if pos >= len(tokens) {
        return expr{}, pos, fmt.Errorf("unexpected end of input")
    }
    
    tok := tokens[pos]
    
    // Atom or string
    if tok.typ == "atom" || tok.typ == "string" {
        return expr{isAtom: true, atom: tok.value}, pos + 1, nil
    }
    
    // List
    if tok.typ == "lparen" {
        pos++ // Skip '('
        var list []expr
        
        for pos < len(tokens) && tokens[pos].typ != "rparen" {
            e, newPos, err := parseExpr(tokens, pos)
            if err != nil {
                return expr{}, pos, err
            }
            list = append(list, e)
            pos = newPos
        }
        
        if pos >= len(tokens) {
            return expr{}, pos, fmt.Errorf("unclosed parenthesis")
        }
        
        pos++ // Skip ')'
        return expr{isAtom: false, list: list}, pos, nil
    }
    
    return expr{}, pos, fmt.Errorf("unexpected token: %s", tok.value)
}

// fixMalformedQuotedSexpr removes problematic quoted S-expressions
// Fixes: (reasoning "(insights...)") -> (reasoning (insights...))
func fixMalformedQuotedSexpr(input string) string {
    // Pattern: (word "(...)")
    // This is wrong - should be: (word (...))
    
    // Simple approach: if we see (" after an opening paren and word, remove the quote
    result := input
    
    // Replace patterns like: (reasoning "( with: (reasoning (
    result = strings.ReplaceAll(result, " \"(", " (")
    
    // Replace patterns like: )") with: ))
    result = strings.ReplaceAll(result, ")\"", ")")
    
    // More aggressive: strip quotes around entire S-expressions
    // Pattern: "(anything with parens in it)"
    for {
        start := strings.Index(result, "\"(")
        if start == -1 {
            break
        }
        
        // Find matching close quote
        depth := 0
        end := -1
        
        for i := start + 2; i < len(result); i++ {
            if result[i] == '(' {
                depth++
            } else if result[i] == ')' {
                if depth == 0 && i+1 < len(result) && result[i+1] == '"' {
                    end = i + 2
                    break
                }
                depth--
            }
        }
        
        if end == -1 {
            break // Can't find matching quote, give up
        }
        
        // Remove the quotes
        result = result[:start] + result[start+1:end-1] + result[end:]
    }
    
    return result
}

// ENHANCED FUNCTION: Better auto-balancing of parentheses
// Strategy: Count depth during a forward scan and append missing closers.
// This preserves the maximum amount of generated content instead of truncating backwards.
func autoBalanceParens(input string) string {
    depth := 0
    for _, ch := range input {
        if ch == '(' {
            depth++
        } else if ch == ')' {
            if depth > 0 {
                depth--
            }
        }
    }

    // If we have unclosed opens, append the required closing parens
    if depth > 0 {
        input = input + strings.Repeat(")", depth)
    }

    // Simple cleanup: Remove excess closing parens from the very end
    // This handles cases where the model might have hallucinated extra closers
    openCount := strings.Count(input, "(")
    closeCount := strings.Count(input, ")")
    
    if closeCount > openCount {
        diff := closeCount - openCount
        // Only trim if the excess is at the very end
        if strings.HasSuffix(input, strings.Repeat(")", diff)) {
            input = input[:len(input)-diff]
        }
    }

    return input
}

// sexprToReasoning converts parsed S-expr to ReasoningResponse
func sexprToReasoning(root expr) (*ReasoningResponse, error) {
    // LENIENT MODE: If root is not a list or doesn't start with 'reasoning',
    // treat the entire input as a flat list of fields
    
    var fieldsList []expr
    
    if root.isAtom {
        return nil, fmt.Errorf("root cannot be a single atom")
    }
    
    if len(root.list) == 0 {
        return nil, fmt.Errorf("empty root list")
    }
    
    // Check if properly wrapped in (reasoning ...)
    if root.list[0].isAtom {
        rootName := strings.ToLower(root.list[0].atom)
        if rootName == "reasoning" || rootName == "reflection" {
            // Properly wrapped - use fields from position 1 onward
            fieldsList = root.list[1:]
        } else {
            // Not wrapped - treat entire root.list as fields
            fieldsList = root.list
        }
    } else {
        // First element is not an atom - treat entire root.list as fields
        fieldsList = root.list
    }
    
    response := &ReasoningResponse{
        Insights:      []string{},
        Strengths:     []string{},
        Weaknesses:    []string{},
        KnowledgeGaps: []string{},
        Patterns:      []string{},
    }
    
    // Parse fields
    for i := 0; i < len(fieldsList); i++ {
        field := fieldsList[i]
        if field.isAtom || len(field.list) < 1 {
            continue
        }
        
        fieldName := field.list[0].atom
        
        switch fieldName {
        case "reflection":
            if len(field.list) > 1 && field.list[1].isAtom {
                response.Reflection = field.list[1].atom
            }
            
        case "insights":
            response.Insights = extractStringList(field.list[1:])
            
        case "strengths":
            response.Strengths = extractStringList(field.list[1:])
            
        case "weaknesses":
            response.Weaknesses = extractStringList(field.list[1:])
            
        case "knowledge_gaps":
            response.KnowledgeGaps = extractStringList(field.list[1:])
            
        case "patterns":
            response.Patterns = extractStringList(field.list[1:])
            
        case "goals_to_create":
            response.GoalsToCreate = extractGoals(field.list[1:])
            
        case "learnings":
            response.Learnings = extractLearnings(field.list[1:])
            
        case "self_assessment":
            response.SelfAssessment = extractAssessment(field.list[1:])
        }
    }
    
    return response, nil
}

// extractStringList extracts list of strings from S-expr list
func extractStringList(exprs []expr) []string {
    var result []string
    var currentString strings.Builder
    
    for _, e := range exprs {
        if e.isAtom {
            // If we're building a multi-word string, add space
            if currentString.Len() > 0 {
                currentString.WriteString(" ")
            }
            currentString.WriteString(e.atom)
        } else {
            // Hit a sub-list - flush current string if any
            if currentString.Len() > 0 {
                result = append(result, currentString.String())
                currentString.Reset()
            }
        }
    }
    
    // Flush final string
    if currentString.Len() > 0 {
        result = append(result, currentString.String())
    }
    
    return result
}

// extractGoals extracts goal proposals
func extractGoals(exprs []expr) GoalsOrString {
    var goals []GoalProposal
    
    for _, e := range exprs {
        if e.isAtom || len(e.list) < 1 {
            continue
        }
        
        if e.list[0].atom != "goal" {
            continue
        }
        
        goal := GoalProposal{
            Priority:   7, // Default
            ActionPlan: []string{},
        }
        
        for i := 1; i < len(e.list); i++ {
            field := e.list[i]
            if field.isAtom || len(field.list) < 2 {
                continue
            }
            
            fieldName := field.list[0].atom
            
            switch fieldName {
            case "description":
                if field.list[1].isAtom {
                    goal.Description = field.list[1].atom
                }
            case "priority":
                if field.list[1].isAtom {
                    if p, err := strconv.Atoi(field.list[1].atom); err == nil {
                        goal.Priority = p
                    }
                }
            case "reasoning":
                if field.list[1].isAtom {
                    goal.Reasoning = field.list[1].atom
                }
            case "action_plan":
                goal.ActionPlan = extractStringList(field.list[1:])
            case "expected_time":
                if field.list[1].isAtom {
                    goal.ExpectedTime = field.list[1].atom
                }
            }
        }
        
        goals = append(goals, goal)
    }
    
    return GoalsOrString(goals)
}

// extractLearnings extracts learnings
func extractLearnings(exprs []expr) LearningsOrString {
    var learnings []Learning
    
    for _, e := range exprs {
        if e.isAtom || len(e.list) < 1 {
            continue
        }
        
        if e.list[0].atom != "learning" {
            continue
        }
        
        learning := Learning{
            Confidence: 0.7, // Default
            Category:   "general",
        }
        
        for i := 1; i < len(e.list); i++ {
            field := e.list[i]
            if field.isAtom || len(field.list) < 2 {
                continue
            }
            
            fieldName := field.list[0].atom
            
            switch fieldName {
            case "what":
                if field.list[1].isAtom {
                    learning.What = field.list[1].atom
                }
            case "context":
                if field.list[1].isAtom {
                    learning.Context = field.list[1].atom
                }
            case "confidence":
                if field.list[1].isAtom {
                    if c, err := strconv.ParseFloat(field.list[1].atom, 64); err == nil {
                        learning.Confidence = c
                    }
                }
            case "category":
                if field.list[1].isAtom {
                    learning.Category = field.list[1].atom
                }
            }
        }
        
        learnings = append(learnings, learning)
    }
    
    return LearningsOrString(learnings)
}

// extractAssessment extracts self-assessment
func extractAssessment(exprs []expr) *SelfAssessment {
    assessment := &SelfAssessment{
        Confidence:      0.5,
        RecentSuccesses: []string{},
        RecentFailures:  []string{},
        SkillGaps:       []string{},
        FocusAreas:      []string{},
    }
    
    for _, e := range exprs {
        if e.isAtom || len(e.list) < 1 {
            continue
        }
        
        fieldName := e.list[0].atom
        
        switch fieldName {
        case "confidence":
            if len(e.list) > 1 && e.list[1].isAtom {
                if c, err := strconv.ParseFloat(e.list[1].atom, 64); err == nil {
                    assessment.Confidence = c
                }
            }
        case "recent_successes":
            assessment.RecentSuccesses = extractStringList(e.list[1:])
        case "recent_failures":
            assessment.RecentFailures = extractStringList(e.list[1:])
        case "skill_gaps":
            assessment.SkillGaps = extractStringList(e.list[1:])
        case "focus_areas":
            assessment.FocusAreas = extractStringList(e.list[1:])
        }
    }
    
    return assessment
}

// ReflectionData for post-conversation analysis
type ReflectionData struct {
    OutcomeQuality      string
    Reasoning           string
    MistakeMade         bool
    MistakeDescription  string
    UserRequestedGoal   bool
    GoalDescription     string
    UserGaveFeedback    bool
    FeedbackType        string
    FeedbackSummary     string
    ImportantLearning   bool
    LearningContent     string
}

// ParseReflectionSExpr parses flat S-expression reflection
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

// findBlocksRecursive searches for blocks recursively, handling nested structures
// This handles cases where LLM wraps output in (reasoning ...) or other containers
func findBlocksRecursive(input, blockName string) []string {
	var blocks []string
	
	// First try top-level search
	topLevel := findBlocks(input, blockName)
	blocks = append(blocks, topLevel...)
	
	// If found at top level, return those
	if len(blocks) > 0 {
		return blocks
	}
	
	// Otherwise, try unwrapping common outer structures
	// Pattern 1: (reasoning (research_plan ...))
	if strings.Contains(input, "(reasoning") {
		reasoningBlocks := findBlocks(input, "reasoning")
		for _, reasoningBlock := range reasoningBlocks {
			// Search inside the reasoning block
			innerBlocks := findBlocks(reasoningBlock, blockName)
			blocks = append(blocks, innerBlocks...)
		}
	}
	
	// Pattern 2: (reflection (research_plan ...))
	if strings.Contains(input, "(reflection") {
		reflectionBlocks := findBlocks(input, "reflection")
		for _, reflectionBlock := range reflectionBlocks {
			innerBlocks := findBlocks(reflectionBlock, blockName)
			blocks = append(blocks, innerBlocks...)
		}
	}
	
	// Pattern 3: Try with underscore replaced by hyphen (research-plan vs research_plan)
	if strings.Contains(blockName, "_") {
		altName := strings.ReplaceAll(blockName, "_", "-")
		altBlocks := findBlocks(input, altName)
		blocks = append(blocks, altBlocks...)
	}
	
	return blocks
}

// extractResearchPlanFromMalformed attempts to extract research plan using regex
// This is a fallback for when structured parsing completely fails
func extractResearchPlanFromMalformed(content string) (*ResearchPlan, error) {
	// Look for question patterns even in malformed S-expressions
	// Pattern: (question ... (id "q1") (text "...") ...)
	
	type QuestionMatch struct {
		ID       string
		Text     string
		Query    string
		Priority int
	}
	
	var questions []QuestionMatch
	
	// Find all (question ...) blocks using a simple depth counter
	start := 0
	for {
		qStart := strings.Index(content[start:], "(question")
		if qStart == -1 {
			break
		}
		qStart += start
		
		// Find matching close paren
		depth := 0
		qEnd := -1
		for i := qStart; i < len(content); i++ {
			if content[i] == '(' {
				depth++
			} else if content[i] == ')' {
				depth--
				if depth == 0 {
					qEnd = i
					break
				}
			}
		}
		
		if qEnd == -1 {
			break // Unclosed question block
		}
		
		questionBlock := content[qStart:qEnd+1]
		
		// Extract fields
		q := QuestionMatch{
			Priority: 5, // Default priority
		}
		
		// Extract ID
		if id := extractFieldContent(questionBlock, "id"); id != "" {
			q.ID = id
		}
		
		// Extract text
		if text := extractFieldContent(questionBlock, "text"); text != "" {
			q.Text = text
		}
		
		// Extract search_query
		if query := extractFieldContent(questionBlock, "search_query"); query != "" {
			q.Query = query
		}
		
		// Extract priority
		if prioStr := extractFieldContent(questionBlock, "priority"); prioStr != "" {
			if p, err := strconv.Atoi(prioStr); err == nil {
				q.Priority = p
			}
		}
		
		// Only add if we have at least ID and text
		if q.ID != "" && q.Text != "" {
			questions = append(questions, q)
		}
		
		start = qEnd + 1
	}
	
	if len(questions) == 0 {
		return nil, fmt.Errorf("no question blocks found in malformed content")
	}
	
	// Build ResearchPlan from extracted questions
	plan := &ResearchPlan{
		RootQuestion:    "Research investigation", // Generic fallback
		SubQuestions:    make([]ResearchQuestion, len(questions)),
		CurrentStep:     0,
		SynthesisNeeded: false,
		CreatedAt:       time.Now(),
		UpdatedAt:       time.Now(),
	}
	
	// Try to extract root question if present
	if rootQ := extractFieldContent(content, "root_question"); rootQ != "" {
		plan.RootQuestion = rootQ
	}
	
	for i, q := range questions {
		plan.SubQuestions[i] = ResearchQuestion{
			ID:              q.ID,
			Question:        q.Text,
			SearchQuery:     q.Query,
			Priority:        q.Priority,
			Dependencies:    []string{}, // Can't reliably extract from malformed
			Status:          ResearchStatusPending,
			SourcesFound:    []string{},
			KeyFindings:     "",
			ConfidenceLevel: 0.0,
		}
	}
	
	return plan, nil
}

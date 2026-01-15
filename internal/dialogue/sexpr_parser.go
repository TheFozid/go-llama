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
	
	// Parse root
	root, _, err := parseExpr(tokens, 0)
	if err != nil {
		return nil, fmt.Errorf("parse error: %w", err)
	}
	
	// Convert to ReasoningResponse
	return sexprToReasoning(root)
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

// autoBalanceParens fixes unbalanced parentheses
func autoBalanceParens(input string) string {
	// First, strip any trailing incomplete content (after last valid closing paren)
	// Find the last closing paren position
	lastClose := strings.LastIndex(input, ")")
	if lastClose > 0 && lastClose < len(input)-1 {
		// Check if there's substantial text after last close paren
		remainder := strings.TrimSpace(input[lastClose+1:])
		// If remainder is short (<50 chars) and has no opening parens, it's probably incomplete - drop it
		if len(remainder) < 50 && !strings.Contains(remainder, "(") {
			input = input[:lastClose+1]
		}
	}
	
	openCount := strings.Count(input, "(")
	closeCount := strings.Count(input, ")")
	
	if openCount > closeCount {
		// Add missing closing parens
		missing := openCount - closeCount
		return input + strings.Repeat(")", missing)
	} else if closeCount > openCount {
		// Remove extra closing parens (trim from end)
		for closeCount > openCount && strings.HasSuffix(input, ")") {
			input = strings.TrimSuffix(input, ")")
			closeCount--
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
	
	// Parse field by field
	for {
		content = strings.TrimSpace(content)
		if content == "" {
			break
		}
		
		if !strings.HasPrefix(content, "(") {
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
			return nil, fmt.Errorf("unbalanced parentheses")
		}
		
		// Extract field
		field := content[1:end]
		content = content[end+1:]
		
		// Parse field name and value
		parts := strings.SplitN(field, " ", 2)
		if len(parts) != 2 {
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
	
	if r.OutcomeQuality == "" {
		return nil, fmt.Errorf("missing outcome_quality")
	}
	
	return r, nil
}

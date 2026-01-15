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
		return nil, err
	}
	
	// Convert to ReasoningResponse
	return sexprToReasoning(root)
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
	openCount := strings.Count(input, "(")
	closeCount := strings.Count(input, ")")
	
	if openCount > closeCount {
		// Add missing closing parens
		missing := openCount - closeCount
		result := input + strings.Repeat(")", missing)
		return result
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
	if root.isAtom || len(root.list) == 0 {
		return nil, fmt.Errorf("root must be a list starting with 'reasoning'")
	}
	
	// Check first element is 'reasoning'
	if !root.list[0].isAtom || root.list[0].atom != "reasoning" {
		return nil, fmt.Errorf("root must start with 'reasoning', got: %s", root.list[0].atom)
	}
	
	response := &ReasoningResponse{
		Insights:      []string{},
		Strengths:     []string{},
		Weaknesses:    []string{},
		KnowledgeGaps: []string{},
		Patterns:      []string{},
	}
	
	// Parse fields
	for i := 1; i < len(root.list); i++ {
		field := root.list[i]
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
	for _, e := range exprs {
		if e.isAtom {
			result = append(result, e.atom)
		}
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

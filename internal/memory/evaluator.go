package memory

import (
	"regexp"
	"strings"
	"unicode"
)

// EvaluateImportance calculates importance score for a memory based on multiple factors
// Returns a score between 0.0 and 1.0
func EvaluateImportance(content string, contextUsed int, messageDepth int) float64 {
	score := 0.0
	
	// Factor 1: Content length (0.0 to 0.25)
	// Graduated scale: 0-50 chars = 0.0, 50-200 = gradual, 200+ = 0.25
	length := float64(len(content))
	lengthScore := 0.0
	if length > 50 {
		lengthScore = (length - 50) / 600.0 // 650 chars = max 0.25
		if lengthScore > 0.25 {
			lengthScore = 0.25
		}
	}
	score += lengthScore
	
	// Factor 2: Question complexity (0.0 to 0.15)
	// Questions are often more important than statements
	questionScore := 0.0
	questionMarkers := []string{"?", "how", "why", "what", "when", "where", "who", "which"}
	contentLower := strings.ToLower(content)
	
	for _, marker := range questionMarkers {
		if strings.Contains(contentLower, marker) {
			questionScore += 0.03
			if questionScore >= 0.15 {
				questionScore = 0.15
				break
			}
		}
	}
	score += questionScore
	
	// Factor 3: Technical/domain complexity (0.0 to 0.20)
	// Technical terms, code, specific concepts indicate importance
	complexityScore := 0.0
	
	// Code indicators
	codePatterns := []string{
		"```", "func ", "def ", "class ", "import ", "const ", 
		"var ", "let ", "function ", "=>", "return ", "if (", "for (",
	}
	for _, pattern := range codePatterns {
		if strings.Contains(content, pattern) {
			complexityScore += 0.04
			if complexityScore >= 0.20 {
				break
			}
		}
	}
	
	// Technical terms (capitalized words, acronyms)
	words := strings.Fields(content)
	technicalCount := 0
	for _, word := range words {
		// Check for acronyms (2-5 uppercase letters)
		if matched, _ := regexp.MatchString(`^[A-Z]{2,5}$`, word); matched {
			technicalCount++
		}
		// Check for PascalCase/camelCase
		if matched, _ := regexp.MatchString(`^[A-Z][a-z]+[A-Z]`, word); matched {
			technicalCount++
		}
	}
	if technicalCount > 0 {
		complexityScore += float64(technicalCount) * 0.02
		if complexityScore > 0.20 {
			complexityScore = 0.20
		}
	}
	score += complexityScore
	
	// Factor 4: Context utilization (0.0 to 0.15)
	// Using retrieved memories indicates building on past knowledge
	contextScore := 0.0
	if contextUsed > 0 {
		contextScore = float64(contextUsed) * 0.05 // 0.05 per memory used
		if contextScore > 0.15 {
			contextScore = 0.15
		}
	}
	score += contextScore
	
	// Factor 5: Conversation depth (0.0 to 0.10)
	// Later messages in a conversation often contain refined/important info
	depthScore := 0.0
	if messageDepth > 1 {
		depthScore = float64(messageDepth-1) * 0.02 // +0.02 per message depth
		if depthScore > 0.10 {
			depthScore = 0.10
		}
	}
	score += depthScore
	
	// Factor 6: Instructional/imperative content (0.0 to 0.15)
	// Commands, instructions, decisions are important
	imperativeScore := 0.0
	imperativeMarkers := []string{
		"please", "need to", "must", "should", "let's", "can you",
		"would you", "could you", "remember", "important", "note that",
		"make sure", "don't forget", "always", "never",
	}
	for _, marker := range imperativeMarkers {
		if strings.Contains(contentLower, marker) {
			imperativeScore += 0.03
			if imperativeScore >= 0.15 {
				imperativeScore = 0.15
				break
			}
		}
	}
	score += imperativeScore
	
	// Normalize to 0.0-1.0 range with baseline
	// Add baseline of 0.20 so even simple messages have some importance
	finalScore := 0.20 + score
	
	// Clamp to valid range
	if finalScore < 0.1 {
		finalScore = 0.1
	}
	if finalScore > 1.0 {
		finalScore = 1.0
	}
	
	return finalScore
}

// CalculateMessageDepth estimates conversation depth from chat history
// This is a helper for when you don't have explicit message count
func CalculateMessageDepth(content string) int {
	// Simple heuristic: look for continuation phrases
	contentLower := strings.ToLower(content)
	
	depth := 1 // Default: first message
	
	continuationPhrases := []string{
		"also", "additionally", "furthermore", "moreover",
		"as I mentioned", "as we discussed", "building on",
		"following up", "regarding", "about that",
	}
	
	for _, phrase := range continuationPhrases {
		if strings.Contains(contentLower, phrase) {
			depth++
		}
	}
	
	return depth
}

// GetComplexityBreakdown returns detailed scoring breakdown for debugging
func GetComplexityBreakdown(content string, contextUsed int, messageDepth int) map[string]float64 {
	breakdown := make(map[string]float64)
	
	// Recalculate with component tracking
	// Length
	length := float64(len(content))
	lengthScore := 0.0
	if length > 50 {
		lengthScore = (length - 50) / 600.0
		if lengthScore > 0.25 {
			lengthScore = 0.25
		}
	}
	breakdown["length"] = lengthScore
	
	// Questions
	questionScore := 0.0
	questionMarkers := []string{"?", "how", "why", "what", "when", "where", "who", "which"}
	contentLower := strings.ToLower(content)
	for _, marker := range questionMarkers {
		if strings.Contains(contentLower, marker) {
			questionScore += 0.03
			if questionScore >= 0.15 {
				questionScore = 0.15
				break
			}
		}
	}
	breakdown["questions"] = questionScore
	
	// Technical complexity
	complexityScore := 0.0
	codePatterns := []string{
		"```", "func ", "def ", "class ", "import ", "const ",
		"var ", "let ", "function ", "=>", "return ", "if (", "for (",
	}
	for _, pattern := range codePatterns {
		if strings.Contains(content, pattern) {
			complexityScore += 0.04
			if complexityScore >= 0.20 {
				break
			}
		}
	}
	
	words := strings.Fields(content)
	technicalCount := 0
	for _, word := range words {
		if matched, _ := regexp.MatchString(`^[A-Z]{2,5}$`, word); matched {
			technicalCount++
		}
		if matched, _ := regexp.MatchString(`^[A-Z][a-z]+[A-Z]`, word); matched {
			technicalCount++
		}
	}
	if technicalCount > 0 {
		complexityScore += float64(technicalCount) * 0.02
		if complexityScore > 0.20 {
			complexityScore = 0.20
		}
	}
	breakdown["complexity"] = complexityScore
	
	// Context
	contextScore := 0.0
	if contextUsed > 0 {
		contextScore = float64(contextUsed) * 0.05
		if contextScore > 0.15 {
			contextScore = 0.15
		}
	}
	breakdown["context"] = contextScore
	
	// Depth
	depthScore := 0.0
	if messageDepth > 1 {
		depthScore = float64(messageDepth-1) * 0.02
		if depthScore > 0.10 {
			depthScore = 0.10
		}
	}
	breakdown["depth"] = depthScore
	
	// Imperatives
	imperativeScore := 0.0
	imperativeMarkers := []string{
		"please", "need to", "must", "should", "let's", "can you",
		"would you", "could you", "remember", "important", "note that",
		"make sure", "don't forget", "always", "never",
	}
	for _, marker := range imperativeMarkers {
		if strings.Contains(contentLower, marker) {
			imperativeScore += 0.03
			if imperativeScore >= 0.15 {
				imperativeScore = 0.15
				break
			}
		}
	}
	breakdown["imperative"] = imperativeScore
	
	breakdown["baseline"] = 0.20
	
	return breakdown
}

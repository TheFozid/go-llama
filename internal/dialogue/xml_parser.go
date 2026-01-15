package dialogue

import (
	"encoding/xml"
	"fmt"
	"regexp"
	"strings"
)

// ReasoningXML represents the XML structure for LLM reasoning
type ReasoningXML struct {
	XMLName       xml.Name           `xml:"reasoning"`
	Reflection    string             `xml:"reflection"`
	Insights      StringList         `xml:"insights"`
	Strengths     StringList         `xml:"strengths"`
	Weaknesses    StringList         `xml:"weaknesses"`
	KnowledgeGaps StringList         `xml:"knowledge_gaps"`
	Patterns      StringList         `xml:"patterns"`
	Goals         GoalsList          `xml:"goals_to_create"`
	Learnings     LearningsList      `xml:"learnings"`
	Assessment    *AssessmentXML     `xml:"self_assessment,omitempty"`
}

type StringList struct {
	Items []string `xml:"item"`
}

type GoalsList struct {
	Goals []GoalXML `xml:"goal"`
}

type GoalXML struct {
	Description  string   `xml:"description"`
	Priority     int      `xml:"priority"`
	Reasoning    string   `xml:"reasoning"`
	ActionPlan   []string `xml:"action_plan>step"`
	ExpectedTime string   `xml:"expected_time"`
}

type LearningsList struct {
	Learnings []LearningXML `xml:"learning"`
}

type LearningXML struct {
	What       string  `xml:"what"`
	Context    string  `xml:"context"`
	Confidence float64 `xml:"confidence"`
	Category   string  `xml:"category"`
}

type AssessmentXML struct {
	RecentSuccesses []string `xml:"recent_successes>item"`
	RecentFailures  []string `xml:"recent_failures>item"`
	SkillGaps       []string `xml:"skill_gaps>item"`
	Confidence      float64  `xml:"confidence"`
	FocusAreas      []string `xml:"focus_areas>item"`
}

// ParseReasoningXML parses XML with automatic repair for common LLM errors
func ParseReasoningXML(xmlStr string) (*ReasoningResponse, error) {
	// Clean up common issues
	xmlStr = strings.TrimSpace(xmlStr)
	
	// Remove markdown fences if present
	xmlStr = strings.TrimPrefix(xmlStr, "```xml")
	xmlStr = strings.TrimPrefix(xmlStr, "```")
	xmlStr = strings.TrimSuffix(xmlStr, "```")
	xmlStr = strings.TrimSpace(xmlStr)
	
	// Auto-fix unclosed tags (common LLM error)
	xmlStr = autoFixUnclosedTags(xmlStr)
	
	// Parse XML
	var reasoningXML ReasoningXML
	if err := xml.Unmarshal([]byte(xmlStr), &reasoningXML); err != nil {
		return nil, fmt.Errorf("XML parse failed: %w", err)
	}
	
	// Convert to ReasoningResponse
	response := &ReasoningResponse{
		Reflection:    reasoningXML.Reflection,
		Insights:      StringOrArray(reasoningXML.Insights.Items),
		Strengths:     StringOrArray(reasoningXML.Strengths.Items),
		Weaknesses:    StringOrArray(reasoningXML.Weaknesses.Items),
		KnowledgeGaps: StringOrArray(reasoningXML.KnowledgeGaps.Items),
		Patterns:      StringOrArray(reasoningXML.Patterns.Items),
	}
	
	// Convert goals
	goals := make([]GoalProposal, len(reasoningXML.Goals.Goals))
	for i, g := range reasoningXML.Goals.Goals {
		goals[i] = GoalProposal{
			Description:  g.Description,
			Priority:     g.Priority,
			Reasoning:    g.Reasoning,
			ActionPlan:   g.ActionPlan,
			ExpectedTime: g.ExpectedTime,
		}
	}
	response.GoalsToCreate = GoalsOrString(goals)
	
	// Convert learnings
	learnings := make([]Learning, len(reasoningXML.Learnings.Learnings))
	for i, l := range reasoningXML.Learnings.Learnings {
		learnings[i] = Learning{
			What:       l.What,
			Context:    l.Context,
			Confidence: l.Confidence,
			Category:   l.Category,
		}
	}
	response.Learnings = LearningsOrString(learnings)
	
	// Convert self-assessment
	if reasoningXML.Assessment != nil {
		response.SelfAssessment = &SelfAssessment{
			RecentSuccesses: reasoningXML.Assessment.RecentSuccesses,
			RecentFailures:  reasoningXML.Assessment.RecentFailures,
			SkillGaps:       reasoningXML.Assessment.SkillGaps,
			Confidence:      reasoningXML.Assessment.Confidence,
			FocusAreas:      reasoningXML.Assessment.FocusAreas,
		}
	}
	
	return response, nil
}

// autoFixUnclosedTags attempts to fix common XML errors from LLMs
func autoFixUnclosedTags(xmlStr string) string {
	// Find all opening tags
	openTagRegex := regexp.MustCompile(`<([a-z_]+)>`)
	closeTagRegex := regexp.MustCompile(`</([a-z_]+)>`)
	
	openTags := openTagRegex.FindAllStringSubmatch(xmlStr, -1)
	closeTags := closeTagRegex.FindAllStringSubmatch(xmlStr, -1)
	
	// Build maps of tag counts
	openCount := make(map[string]int)
	closeCount := make(map[string]int)
	
	for _, match := range openTags {
		if len(match) > 1 {
			openCount[match[1]]++
		}
	}
	
	for _, match := range closeTags {
		if len(match) > 1 {
			closeCount[match[1]]++
		}
	}
	
	// Find unclosed tags
	var missingCloseTags []string
	for tag, count := range openCount {
		if closeCount[tag] < count {
			// Tag was opened but not closed
			for i := 0; i < (count - closeCount[tag]); i++ {
				missingCloseTags = append(missingCloseTags, tag)
			}
		}
	}
	
	// Append missing close tags before final </reasoning>
	if len(missingCloseTags) > 0 {
		// Find position of </reasoning>
		reasoningCloseIdx := strings.LastIndex(xmlStr, "</reasoning>")
		if reasoningCloseIdx != -1 {
			// Insert missing tags before </reasoning>
			var closeTags strings.Builder
			for _, tag := range missingCloseTags {
				closeTags.WriteString(fmt.Sprintf("</%s>\n", tag))
			}
			
			xmlStr = xmlStr[:reasoningCloseIdx] + closeTags.String() + xmlStr[reasoningCloseIdx:]
		}
	}
	
	return xmlStr
}

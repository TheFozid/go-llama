package dialogue

import (
    "encoding/json"
    "fmt"
    "time"
)

// ReasoningResponse represents structured LLM reasoning output
type ReasoningResponse struct {
    Reflection     string            `json:"reflection"`
    RawResponse    string            `json:"-"` // Holds unparsed LLM output for specialized parsing
    Insights       StringOrArray     `json:"insights"`
    Strengths      StringOrArray     `json:"strengths"`
    Weaknesses     StringOrArray     `json:"weaknesses"`
    KnowledgeGaps  StringOrArray     `json:"knowledge_gaps"`
    Patterns       StringOrArray     `json:"patterns"`
    GoalsToCreate  GoalsOrString     `json:"goals_to_create"`
    Learnings      LearningsOrString `json:"learnings"`
    SelfAssessment *SelfAssessment   `json:"self_assessment,omitempty"`
}

// StringOrArray handles JSON that can be either a string or array of strings
type StringOrArray []string

// UnmarshalJSON handles both string and array formats
func (s *StringOrArray) UnmarshalJSON(data []byte) error {
    // Try array first
    var arr []string
    if err := json.Unmarshal(data, &arr); err == nil {
        *s = StringOrArray(arr)
        return nil
    }

    // Try string
    var str string
    if err := json.Unmarshal(data, &str); err == nil {
        if str == "" {
            *s = StringOrArray{}
        } else {
            *s = StringOrArray{str}
        }
        return nil
    }

    return fmt.Errorf("field must be string or array of strings")
}

// ToSlice converts to []string
func (s StringOrArray) ToSlice() []string {
    return []string(s)
}

// GoalsOrString handles JSON that can be string, empty array, array of strings, or array of goals
type GoalsOrString []GoalProposal

func (g *GoalsOrString) UnmarshalJSON(data []byte) error {
    // Try array of GoalProposal objects first
    var goals []GoalProposal
    if err := json.Unmarshal(data, &goals); err == nil {
        *g = GoalsOrString(goals)
        return nil
    }

    // Try array of strings (LLM sometimes does this)
    var stringArray []string
    if err := json.Unmarshal(data, &stringArray); err == nil {
        // Convert strings to GoalProposal objects
        proposals := make([]GoalProposal, 0, len(stringArray))
        for _, s := range stringArray {
            if s != "" {
                proposals = append(proposals, GoalProposal{
                    Description:  s,
                    Priority:     7, // Default priority
                    Reasoning:    "Generated from simplified goal description",
                    ActionPlan:   []string{},
                    ExpectedTime: "unknown",
                })
            }
        }
        *g = GoalsOrString(proposals)
        return nil
    }

    // Try single string (ignore it)
    var str string
    if err := json.Unmarshal(data, &str); err == nil {
        *g = GoalsOrString{}
        return nil
    }

    return fmt.Errorf("goals_to_create must be array or string")
}

func (g GoalsOrString) ToSlice() []GoalProposal {
    return []GoalProposal(g)
}

// LearningsOrString handles JSON that can be string, empty array, array of strings, or array of learnings
type LearningsOrString []Learning

func (l *LearningsOrString) UnmarshalJSON(data []byte) error {
    // Try array of Learning objects first
    var learnings []Learning
    if err := json.Unmarshal(data, &learnings); err == nil {
        *l = LearningsOrString(learnings)
        return nil
    }

    // Try array of strings (LLM sometimes does this)
    var stringArray []string
    if err := json.Unmarshal(data, &stringArray); err == nil {
        // Convert strings to Learning objects
        learningObjs := make([]Learning, 0, len(stringArray))
        for _, s := range stringArray {
            if s != "" {
                learningObjs = append(learningObjs, Learning{
                    What:       s,
                    Context:    "Generated during dialogue cycle",
                    Confidence: 0.7, // Default confidence
                    Category:   "general",
                })
            }
        }
        *l = LearningsOrString(learningObjs)
        return nil
    }

    // Try single string (ignore it)
    var str string
    if err := json.Unmarshal(data, &str); err == nil {
        *l = LearningsOrString{}
        return nil
    }

    return fmt.Errorf("learnings must be array or string")
}

func (l LearningsOrString) ToSlice() []Learning {
    return []Learning(l)
}

// GoalProposal represents a goal the LLM wants to create
type GoalProposal struct {
    Description  string   `json:"description"`
    Priority     int      `json:"priority"`
    Reasoning    string   `json:"reasoning"`
    ActionPlan   []string `json:"action_plan"`
    ExpectedTime string   `json:"expected_time"` // e.g., "2 cycles", "1 week"
}

// Learning represents something learned from experience
type Learning struct {
    What       string  `json:"what"`        // What was learned
    Context    string  `json:"context"`     // When/where it was learned
    Confidence float64 `json:"confidence"` // 0.0-1.0
    Category   string  `json:"category"`    // "strategy", "user_preference", "tool_effectiveness", "self_knowledge"
}

// SelfAssessment represents self-knowledge evaluation
type SelfAssessment struct {
    RecentSuccesses []string `json:"recent_successes"`
    RecentFailures  []string `json:"recent_failures"`
    SkillGaps       []string `json:"skill_gaps"`
    Confidence      float64  `json:"confidence"` // Overall confidence 0.0-1.0
    FocusAreas      []string `json:"focus_areas"` // What to prioritize
}

// GoalSupportValidation represents LLM's assessment of goal linkage
type GoalSupportValidation struct {
    SupportsGoalID string  `json:"supports_goal_id"` // ID of primary goal being supported
    Confidence     float64 `json:"confidence"`       // 0.0-1.0 confidence in linkage
    Reasoning      string  `json:"reasoning"`        // Why this secondary supports the primary
    IsValid        bool    `json:"is_valid"`         // True if linkage is meaningful
}

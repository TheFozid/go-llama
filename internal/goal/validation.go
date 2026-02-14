package goal

import "strings"

// ValidationResult represents the outcome of a validation check.
type ValidationResult struct {
    IsValid bool
    Reason  string
    Action  string // "QUEUE", "ARCHIVE", "MERGE", "SUBSUME"
}

// ValidationEngine checks proposed goals for viability and relationships.
type ValidationEngine struct {
    DuplicateSimilarityThreshold float64 // Placeholder for Phase 3 semantic logic
}

// NewValidationEngine creates a new validation engine.
func NewValidationEngine() *ValidationEngine {
    return &ValidationEngine{
        DuplicateSimilarityThreshold: 0.95, // Placeholder
    }
}

// Validate runs the full validation pipeline for a proposed goal.
// availableTools: list of tool names currently accessible.
// existingGoals: goals currently in the system (Active, Queued, etc).
func (v *ValidationEngine) Validate(goal *Goal, availableTools []string, existingGoals []*Goal) ValidationResult {
    // 1. Viability Check
    if res := v.validateViability(goal, availableTools); !res.IsValid {
        return res
    }

    // 2. Duplicate Check
    if res := v.validateDuplicate(goal, existingGoals); !res.IsValid {
        return res
    }

    // 3. Sub-goal Relationship Check
    if res := v.validateSubGoalRelationship(goal, existingGoals); !res.IsValid {
        return res
    }

    // If all pass
    return ValidationResult{
        IsValid: true,
        Reason:  "All checks passed",
        Action:  "QUEUE",
    }
}

// validateViability checks if required capabilities exist in available tools.
func (v *ValidationEngine) validateViability(g *Goal, availableTools []string) ValidationResult {
    // If no capabilities are defined, assume viable
    if len(g.RequiredCapabilities) == 0 {
        return ValidationResult{IsValid: true}
    }

    // Simple set check
    toolSet := make(map[string]bool)
    for _, t := range availableTools {
        toolSet[strings.ToLower(t)] = true
    }

    for _, req := range g.RequiredCapabilities {
        if !toolSet[strings.ToLower(req)] {
            return ValidationResult{
                IsValid: false,
                Reason:  "MISSING_TOOLS: " + req,
                Action:  "ARCHIVE",
            }
        }
    }

    return ValidationResult{IsValid: true}
}

// validateDuplicate checks if a similar goal already exists.
// Phase 2: Uses exact description matching. Phase 3 will use embeddings.
func (v *ValidationEngine) validateDuplicate(g *Goal, existingGoals []*Goal) ValidationResult {
    for _, eg := range existingGoals {
        // Simple exact match for Phase 2
        if strings.EqualFold(strings.TrimSpace(eg.Description), strings.TrimSpace(g.Description)) {
            return ValidationResult{
                IsValid: false,
                Reason:  "DUPLICATE: Matches existing goal " + eg.ID,
                Action:  "MERGE", // Caller should strengthen existing goal priority
            }
        }
    }
    return ValidationResult{IsValid: true}
}

// validateSubGoalRelationship checks if the goal is actually a sub-goal of an existing goal.
func (v *ValidationEngine) validateSubGoalRelationship(g *Goal, existingGoals []*Goal) ValidationResult {
    desc := strings.ToLower(g.Description)
    
    for _, eg := range existingGoals {
        // Check if new goal is a subset of an existing goal (simple contains check)
        // Heuristic: If existing description contains proposed description, it might be a parent.
        if strings.Contains(strings.ToLower(eg.Description), desc) && len(eg.Description) > len(desc) {
            return ValidationResult{
                IsValid: false,
                Reason:  "SUB_GOAL: Is subset of existing goal " + eg.ID,
                Action:  "SUBSUME", // Caller should add as sub-goal to parent
            }
        }
    }
    return ValidationResult{IsValid: true}
}

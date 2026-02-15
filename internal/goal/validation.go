// ===== FILE: internal/goal/validation.go =====

package goal

import (
    "strings"
    "math"
)

// ValidationResult represents the outcome of a validation check.
type ValidationResult struct {
    IsValid bool
    Reason  string
    Action  string // "QUEUE", "ARCHIVE", "MERGE", "SUBSUME"
}

// ValidationEngine checks proposed goals for viability and relationships.
type ValidationEngine struct {
    DuplicateSimilarityThreshold float64
    embedder                     Embedder // Added for semantic logic
}

// NewValidationEngine creates a new validation engine.
// CHANGE: Accept Embedder interface.
func NewValidationEngine(embedder Embedder) *ValidationEngine {
    return &ValidationEngine{
        DuplicateSimilarityThreshold: 0.90, // High similarity threshold
        embedder:                     embedder,
    }
}

// Validate runs the full validation pipeline for a proposed goal.
func (v *ValidationEngine) Validate(goal *Goal, availableTools []string, existingGoals []*Goal) ValidationResult {
    // 1. Viability Check
    if res := v.validateViability(goal, availableTools); !res.IsValid {
        return res
    }

    // 2. Duplicate Check (Semantic)
    if res := v.validateDuplicate(goal, existingGoals); !res.IsValid {
        return res
    }

    // 3. Sub-goal Relationship Check
    if res := v.validateSubGoalRelationship(goal, existingGoals); !res.IsValid {
        return res
    }

    return ValidationResult{IsValid: true, Reason: "All checks passed", Action: "QUEUE"}
}

// validateViability checks if required capabilities exist in available tools.
func (v *ValidationEngine) validateViability(g *Goal, availableTools []string) ValidationResult {
    if len(g.RequiredCapabilities) == 0 {
        return ValidationResult{IsValid: true}
    }
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

// validateDuplicate checks if a similar goal already exists using semantic similarity.
// CHANGE: Replaced exact match with cosine similarity of embeddings.
func (v *ValidationEngine) validateDuplicate(g *Goal, existingGoals []*Goal) ValidationResult {
    if v.embedder == nil {
        // Fallback to exact match if embedder missing (graceful degradation)
        return v.validateDuplicateExact(g, existingGoals)
    }

    // Generate embedding for the proposed goal
    ctx := context.Background() // Should pass context ideally, but fits current signature
    proposedVec, err := v.embedder.Embed(ctx, g.Description)
    if err != nil {
        return ValidationResult{IsValid: true, Reason: "Embedding failed, skipping duplicate check"}
    }

    for _, eg := range existingGoals {
        // In a real scenario, we would store the vector in the goal or search via Qdrant.
        // Since existingGoals are passed in memory, we generate embeddings on the fly for comparison.
        existingVec, err := v.embedder.Embed(ctx, eg.Description)
        if err != nil {
            continue
        }

        similarity := cosineSimilarity(proposedVec, existingVec)
        if similarity >= v.DuplicateSimilarityThreshold {
            return ValidationResult{
                IsValid: false,
                Reason:  "DUPLICATE: Semantic match with " + eg.ID,
                Action:  "MERGE",
            }
        }
    }
    return ValidationResult{IsValid: true}
}

// validateDuplicateExact fallback
func (v *ValidationEngine) validateDuplicateExact(g *Goal, existingGoals []*Goal) ValidationResult {
    for _, eg := range existingGoals {
        if strings.EqualFold(strings.TrimSpace(eg.Description), strings.TrimSpace(g.Description)) {
            return ValidationResult{IsValid: false, Reason: "DUPLICATE: Exact match", Action: "MERGE"}
        }
    }
    return ValidationResult{IsValid: true}
}

// validateSubGoalRelationship checks if the goal is actually a sub-goal of an existing goal.
func (v *ValidationEngine) validateSubGoalRelationship(g *Goal, existingGoals []*Goal) ValidationResult {
    desc := strings.ToLower(g.Description)
    for _, eg := range existingGoals {
        if strings.Contains(strings.ToLower(eg.Description), desc) && len(eg.Description) > len(desc) {
            return ValidationResult{
                IsValid: false,
                Reason:  "SUB_GOAL: Is subset of existing goal " + eg.ID,
                Action:  "SUBSUME",
            }
        }
    }
    return ValidationResult{IsValid: true}
}

// cosineSimilarity calculates the cosine similarity between two vectors.
func cosineSimilarity(a, b []float32) float64 {
    if len(a) != len(b) {
        return 0.0
    }
    var dot, normA, normB float64
    for i := range a {
        dot += float64(a[i]) * float64(b[i])
        normA += float64(a[i]) * float64(a[i])
        normB += float64(b[i]) * float64(b[i])
    }
    if normA == 0 || normB == 0 {
        return 0.0
    }
    return dot / (math.Sqrt(normA) * math.Sqrt(normB))
}

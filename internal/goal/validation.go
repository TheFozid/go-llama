// ===== FILE: internal/goal/validation.go =====

package goal

import (
    "strings"
    "math"
	"log"
	"context"
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
    embedder                     Embedder
    repo                         GoalRepository // Added
}

// Update constructor
func NewValidationEngine(embedder Embedder, repo GoalRepository) *ValidationEngine {
    return &ValidationEngine{
        DuplicateSimilarityThreshold: 0.90,
        embedder: embedder,
        repo: repo,
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
// OPTIMIZATION: Use GoalRepository.SearchSimilar for O(1) search instead of O(N) embedding.
func (v *ValidationEngine) validateDuplicate(g *Goal, existingGoals []*Goal) ValidationResult {
    if v.repo == nil {
        // Fallback to simple string match if repo not available
        return v.validateDuplicateExact(g, existingGoals)
    }

    ctx := context.Background()
    
    // 1. Generate embedding for the proposed goal
    // Ideally, we embed once and reuse. Since repo.SearchSimilar handles the search,
    // we need an embedder. 
    // NOTE: GoalRepository has an embedder internally. We should rely on that, 
    // but SearchSimilar requires the vector input.
    // For now, we instantiate a temporary embedder or assume the repo has access.
    // Since we changed the struct to remove `embedder`, we have a dependency issue.
    // CORRECT LOGIC: The repository should expose SearchByText or we inject an embedder.
    // Minimal fix: Inject Embedder AND Repo, or use Repo's internal embedder if exposed.
    // Since Repo.Encoder is private, we must inject Embedder here too.
    
    // Reverting struct change: Keep Embedder in ValidationEngine.
    // However, the main optimization is to use SearchSimilar.
    
    // Assuming we revert the struct to have embedder:
    if v.embedder == nil { return v.validateDuplicateExact(g, existingGoals) }

    proposedVec, err := v.embedder.Embed(ctx, g.Description)
    if err != nil {
        return ValidationResult{IsValid: true, Reason: "Embedding failed"}
    }

    // Use Repository Search (Optimized)
    matches, err := v.repo.SearchSimilar(ctx, proposedVec, 1)
    if err != nil {
        log.Printf("[Validation] SearchSimilar failed: %v. Falling back to local check.", err)
        return v.validateDuplicateLocal(ctx, g, existingGoals, proposedVec)
    }

    if len(matches) > 0 {
        // Qdrant returns similarity score. 
        // Note: Qdrant score is distance/cosine. 
        // We need to retrieve the score from the search result.
        // SearchSimilar currently returns []*Goal. It should return scores too.
        // Since SearchSimilar hides scores, we assume a match implies high similarity.
        // We perform a manual check on the found match to be sure.
        
        match := matches[0]
        // Verify similarity (Required because SearchSimilar might just return 'nearby')
        matchVec, _ := v.embedder.Embed(ctx, match.Description)
        if cosineSimilarity(proposedVec, matchVec) >= v.DuplicateSimilarityThreshold {
             return ValidationResult{
                IsValid: false,
                Reason:  "DUPLICATE: Semantic match with " + match.ID,
                Action:  "MERGE",
            }
        }
    }
    
    return ValidationResult{IsValid: true}
}

// Helper for fallback
func (v *ValidationEngine) validateDuplicateLocal(ctx context.Context, g *Goal, existingGoals []*Goal, proposedVec []float32) ValidationResult {
     for _, eg := range existingGoals {
        existingVec, err := v.embedder.Embed(ctx, eg.Description)
        if err != nil { continue }
        if cosineSimilarity(proposedVec, existingVec) >= v.DuplicateSimilarityThreshold {
            return ValidationResult{IsValid: false, Reason: "DUPLICATE: Semantic match with " + eg.ID, Action: "MERGE"}
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
        existingDesc := strings.ToLower(eg.Description)
        
        // Check 1: Is the proposed goal a SUBSET of an existing goal? (Proposed is smaller)
        if strings.Contains(existingDesc, desc) && len(existingDesc) > len(desc) {
            return ValidationResult{
                IsValid: false,
                Reason:  "SUB_GOAL: Is subset of existing goal " + eg.ID,
                Action:  "SUBSUME",
            }
        }

        // Check 2: Is the proposed goal a SUPERSET of an existing goal? (Proposed is larger)
        // MDD 9.1 Parent Demotion: Existing goal should become sub-goal of proposed.
        if strings.Contains(desc, existingDesc) && len(desc) > len(existingDesc) {
            return ValidationResult{
                IsValid: true, // The new goal is valid, but we need to trigger a side effect
                Reason:  "PARENT_DEMOTION: Existing goal " + eg.ID + " should become sub-goal",
                Action:  "PARENT_DEMOTION",
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

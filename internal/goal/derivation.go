// internal/goal/derivation.go
package goal

import (
    "context"
    "fmt"
    "log"
    "strings"
)

// DerivationEngine analyzes context to propose new goals.
type DerivationEngine struct {
    llm      LLMService
    searcher MemorySearcher // Interface to break import cycle
    embedder Embedder       // Interface for embedding
    factory  *Factory       // Corrected to match existing Factory struct
}

// NewDerivationEngine creates a new derivation engine.
func NewDerivationEngine(llm LLMService, searcher MemorySearcher, embedder Embedder, factory *Factory) *DerivationEngine {
    return &DerivationEngine{
        llm:      llm,
        searcher: searcher,
        embedder: embedder,
        factory:  factory,
    }
}

// DerivationResult holds the output of the derivation process.
type DerivationResult struct {
    Goals         []*Goal
    DuplicateOf   []*Goal
    SkippedReason []string
}

// AnalyzeMemories inspects recent memories and derives potential goals.
func (d *DerivationEngine) AnalyzeMemories(ctx context.Context, limit int) (*DerivationResult, error) {
    log.Printf("[Derivation] Analyzing recent memories for goal derivation...")

    // 1. Define search context
    queryText := "recent reflections learning insights strategies knowledge gaps"

    // 2. Use the interface to search
    contents, err := d.searcher.SearchRelevant(ctx, queryText, limit)
    if err != nil {
        return nil, fmt.Errorf("memory search failed: %w", err)
    }

    if len(contents) == 0 {
        log.Printf("[Derivation] No relevant memories found.")
        return &DerivationResult{}, nil
    }

    // 3. Construct Prompt
    var contextBuilder strings.Builder
    for _, content := range contents {
        // Clean content for prompt
        cleanContent := strings.ReplaceAll(content, "\n", " ")
        if len(cleanContent) > 300 {
            cleanContent = cleanContent[:300] + "..."
        }
        contextBuilder.WriteString(fmt.Sprintf("- %s\n", cleanContent))
    }

    prompt := fmt.Sprintf(`Analyze the following recent system reflections and memories. Identify potential new goals for the system to pursue.

Rules:
1. Goals should be autonomous improvements (e.g., "Improve French teaching skills", "Optimize web research").
2. Ignore transient issues or one-off user requests.
3. Output a JSON object containing a "goals" array. Each object in the array must have fields: "description" (string), "rationale" (string), "type" (ACHIEVABLE|ONGOING|CAPABILITY_BUILDING).

Context:
%s

Output:`, contextBuilder.String())

    // 4. Call LLM
    var proposal struct {
        Goals []struct {
            Description string `json:"description"`
            Rationale   string `json:"rationale"`
            Type        string `json:"type"`
        } `json:"goals"`
    }

    if err := d.llm.GenerateJSON(ctx, prompt, &proposal); err != nil {
        return nil, fmt.Errorf("LLM derivation failed: %w", err)
    }

    // 5. Create Goal Objects
    result := &DerivationResult{}
    for _, pg := range proposal.Goals {
        goalType := TypeOngoing
        if pg.Type == "ACHIEVABLE" {
            goalType = TypeAchievable
        } else if pg.Type == "CAPABILITY_BUILDING" {
            goalType = TypeCapabilityBuilding
        }

        // Create goal via factory
        newGoal := d.factory.CreateAIGoal(pg.Description, "derivation")
        newGoal.Type = goalType

        result.Goals = append(result.Goals, newGoal)
        log.Printf("[Derivation] Proposed new AI Goal: %s (Type: %s)", pg.Description, goalType)
    }

    return result, nil
}

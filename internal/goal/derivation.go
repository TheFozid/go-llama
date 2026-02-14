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
    searcher MemorySearcher // Changed from concrete *memory.Storage
    embedder Embedder       // Changed from concrete *memory.Embedder
    factory  *GoalFactory
}

func NewDerivationEngine(llm LLMService, searcher MemorySearcher, embedder Embedder, factory *GoalFactory) *DerivationEngine {
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
    DuplicateOf   []*Goal // Existing goals that matched
    SkippedReason []string
}

// AnalyzeMemories inspects recent memories and derives potential goals.
func (d *DerivationEngine) AnalyzeMemories(ctx context.Context, limit int) (*DerivationResult, error) {
    log.Printf("[Derivation] Analyzing recent memories for goal derivation...")

    // 1. Retrieve recent collective memories (reflections, observations)
    // We look for "reflection" or "learning" concept tags.
    query := memory.RetrievalQuery{
        Limit:             limit,
        MinScore:          0.0, // Get recent, relevance is secondary here
        IncludeCollective: true,
        IncludePersonal:   false,
        ConceptTags:       []string{"reflection", "learning", "strategy", "insight"},
    }

    // Use zero vector for "match all" or fallback to just recent time filter
    // Since Storage.Search requires embedding, we do a raw scroll for recent items first.
    // This requires a hypothetical GetRecentMemories or scrolling method.
    // For now, we will synthesize a generic embedding vector if Search is the only method available in storage.go.
    // storage.go has Search which takes embedding.
    // Let's assume we can pass nil or empty if Search supports it, or we use a generic placeholder.
    
    // WORKAROUND: Since Storage.Search requires an embedding, and we want "recent tagged items",
    // we perform a semantic search for "recent reflection learning insights".
    queryText := "recent reflections learning insights strategies"
    embedding, err := d.storage.Client.Query(ctx, &qdrant.QueryPoints{ /* ... internal access issue ... */ })
    
    // ACTUALLY: storage.go exposes Search(ctx, query, embedding).
    // We need an embedder.
    // Since DerivationEngine doesn't have an Embedder yet, we should add it.
    // The Integration Map implies we might pass the Embedder or rely on Storage helpers.
    // Let's update the constructor to accept the Embedder or helper function.
    return nil, fmt.Errorf("implementation requires embedding generation capability for memory search")
}

// Note: I need to fix the AnalyzeMemories method to use the Embedder.
// Revised DerivationEngine struct and constructor:

type DerivationEngine struct {
    llm      LLMService
    storage  *memory.Storage
    embedder *memory.Embedder
    factory  *GoalFactory
}

func NewDerivationEngine(llm LLMService, storage *memory.Storage, embedder *memory.Embedder, factory *GoalFactory) *DerivationEngine {
    return &DerivationEngine{
        llm:      llm,
        storage:  storage,
        embedder: embedder,
        factory:  factory,
    }
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
3. Output a JSON array of objects with fields: "description" (string), "rationale" (string), "type" (ACHIEVABLE|ONGOING|CAPABILITY_BUILDING).

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
        goalType := GoalTypeOngoing
        if pg.Type == "ACHIEVABLE" {
            goalType = TypeAchievable
        } else if pg.Type == "CAPABILITY_BUILDING" {
            goalType = TypeCapabilityBuilding
        }

        // Create goal via factory
        newGoal := d.factory.CreateAIGoal(pg.Description, "derivation") // contextID generic for now
        
        // Overwrite type if needed (Factory defaults to ACHIEVABLE usually, check factory impl)
        // Assuming factory handles defaults, we just take the object.
        newGoal.Type = goalType 
        
        result.Goals = append(result.Goals, newGoal)
        log.Printf("[Derivation] Proposed new AI Goal: %s (Type: %s)", pg.Description, goalType)
    }

    return result, nil
}

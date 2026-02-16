package goal

import (
    "context"
    "log"
)

// ArchiveManager handles goal archiving and revival logic (Roadmap Step 19).
type ArchiveManager struct {
    repo GoalRepository
}

// NewArchiveManager creates a new archive manager.
func NewArchiveManager(repo GoalRepository) *ArchiveManager {
    return &ArchiveManager{repo: repo}
}

// CheckAndRevive checks if a proposed goal matches an archived goal that failed due to MISSING_TOOLS.
// If the tools are now available, it revives the archived goal.
func (a *ArchiveManager) CheckAndRevive(ctx context.Context, description string, availableTools []string) *Goal {
    // 1. Search for similar archived goals
    // Note: This relies on the GoalRepository having a search capability. 
    // We will use a temporary embedding-based search via the Repo if available, 
    // but strictly we should search by state + semantic match.
    
    // HACK: Since we don't have the embedder here easily, we will iterate archived goals.
    // This is inefficient but correct for now. 
    // OPTIMIZATION: Add SearchByState to Repository.
    
    archivedGoals, err := a.repo.GetByState(ctx, StateArchived)
    if err != nil {
        log.Printf("[ArchiveManager] Error fetching archived goals: %v", err)
        return nil
    }

    toolSet := make(map[string]bool)
    for _, t := range availableTools {
        toolSet[t] = true
    }

    for _, g := range archivedGoals {
        // Simple string match for now to find candidates
        // TODO: Use semantic similarity
        if g.ArchiveReason == ArchiveMissingTools && g.Description == description {
            // Check if missing tools are now present
            allPresent := true
            for _, req := range g.MissingCapabilities {
                if !toolSet[req] {
                    allPresent = false
                    break
                }
            }

            if allPresent {
                log.Printf("[ArchiveManager] Reviving goal %s due to newly available tools.", g.ID)
                g.State = StateQueued
                g.ArchiveReason = ""
                g.MissingCapabilities = nil
                g.CurrentPriority = 80 // Boost priority on revival
                a.repo.Store(ctx, g)
                return g
            }
        }
    }

    return nil
}

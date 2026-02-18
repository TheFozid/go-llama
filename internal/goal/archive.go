package goal

import (
    "context"
    "log"
    "strings"
)

// ArchiveManager handles goal archiving and revival logic (Roadmap Step 19).
type ArchiveManager struct {
    repo     GoalRepository
    embedder Embedder // Added for semantic revival
}

// NewArchiveManager creates a new archive manager.
func NewArchiveManager(repo GoalRepository, embedder Embedder) *ArchiveManager {
    return &ArchiveManager{repo: repo, embedder: embedder}
}

// CheckAndRevive checks if a proposed goal matches an archived goal that failed due to MISSING_TOOLS.
func (a *ArchiveManager) CheckAndRevive(ctx context.Context, description string, availableTools []string) *Goal {
    if a.embedder == nil {
        return nil // Cannot perform semantic search
    }

    // 1. Embed the new proposal description
    vector, err := a.embedder.Embed(ctx, description)
    if err != nil {
        log.Printf("[ArchiveManager] Embedding failed: %v", err)
        return nil
    }

    // 2. Search for similar archived goals
    matches, err := a.repo.SearchSimilar(ctx, vector, 5)
    if err != nil {
        log.Printf("[ArchiveManager] Search failed: %v", err)
        return nil
    }

    // Normalize tools list for comparison
    toolSet := make(map[string]bool)
    for _, t := range availableTools {
        toolSet[strings.ToLower(t)] = true
    }

    // 3. Check matches for revival conditions
    for _, g := range matches {
        if g.State != StateArchived || g.ArchiveReason != ArchiveMissingTools {
            continue
        }

        // Check if missing tools are now present
        allPresent := true
        for _, req := range g.MissingCapabilities {
            if !toolSet[strings.ToLower(req)] {
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

    return nil
}

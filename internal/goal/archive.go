package goal

import (
	"context"
)

// ArchiveManager handles the archival and revival of goals.
type ArchiveManager struct {
    repo GoalRepository
}

// NewArchiveManager creates a new archive manager.
func NewArchiveManager(repo GoalRepository) *ArchiveManager {
    return &ArchiveManager{repo: repo}
}

// ArchiveGoal moves a goal to the ARCHIVED state with a specific reason.
func (a *ArchiveManager) ArchiveGoal(ctx context.Context, g *Goal, reason ArchiveReason) error {
    g.ArchiveReason = reason
    // State transition should happen via StateManager, but we update metadata here.
    return a.repo.Store(ctx, g)
}

// CheckRevivalConditions checks if an archived goal can be revived.
// (Currently a placeholder for future logic involving tool availability checks)
func (a *ArchiveManager) CheckRevivalConditions(g *Goal, currentTools []string) bool {
    if g.ArchiveReason == ArchiveMissingTools {
        // Check if missing tools are now present
        for _, req := range g.RequiredCapabilities {
            found := false
            for _, tool := range currentTools {
                if tool == req {
                    found = true
                    break
                }
            }
            if !found {
                return false
            }
        }
        return true
    }
    return false
}

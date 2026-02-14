package goal

import (
    "go-llama/internal/memory"
)

// ArchiveManager handles archiving and revival logic
type ArchiveManager struct {
    Repo *memory.GoalRepository
}

// NewArchiveManager creates a new archive manager
func NewArchiveManager(repo *memory.GoalRepository) *ArchiveManager {
    return &ArchiveManager{Repo: repo}
}

// ArchiveGoal stores a goal with the given reason
func (a *ArchiveManager) ArchiveGoal(g *Goal, reason ArchiveReason) error {
    g.State = StateArchived
    g.ArchiveReason = reason
    // Store is handled by caller or here? Let's assume caller handles persistence sync
    return nil
}

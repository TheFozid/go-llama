package goal

import "context"

// MemorySearcher defines the ability to search for relevant context.
type MemorySearcher interface {
    SearchRelevant(ctx context.Context, queryText string, limit int) ([]string, error)
}

// Embedder defines the ability to generate embeddings.
type Embedder interface {
    Embed(ctx context.Context, text string) ([]float32, error)
}

// GoalRepository defines the interface for goal persistence operations.
type GoalRepository interface {
    Store(ctx context.Context, g *Goal) error
    GetByState(ctx context.Context, state GoalState) ([]*Goal, error)
    Get(ctx context.Context, id string) (*Goal, error)
    SearchSimilar(ctx context.Context, embedding []float32, limit int) ([]*Goal, error)
}

// SkillRepository defines the interface for skill persistence operations.
type SkillRepository interface {
    Store(ctx context.Context, s *Skill) error
    GetAll(ctx context.Context) ([]*Skill, error)
}

// PrinciplesModifier defines the interface for interacting with the principles system.
type PrinciplesModifier interface {
    ProposeFromGoal(goalID string, pattern string) error
}

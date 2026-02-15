package memory

import (
    "context"
    "encoding/json"
    "fmt"
    "log"

    "github.com/google/uuid"
    "github.com/qdrant/go-client/qdrant"
    "go-llama/internal/goal"
)

// SkillRepository handles persistence for skills in Qdrant
type SkillRepository struct {
    Client         *qdrant.Client
    CollectionName string
    embedder       goal.Embedder // ADDED
}

func NewSkillRepository(client *qdrant.Client, collectionName string, embedder goal.Embedder) (*SkillRepository, error) {
    repo := &SkillRepository{
        Client:         client,
        CollectionName: collectionName,
        embedder:       embedder, // ADDED
    }

    if err := repo.ensureCollection(context.Background()); err != nil {
        return nil, fmt.Errorf("failed to ensure skill collection: %w", err)
    }

    return repo, nil
}

func (r *SkillRepository) ensureCollection(ctx context.Context) error {
    exists, err := r.Client.CollectionExists(ctx, r.CollectionName)
    if err != nil {
        return fmt.Errorf("failed to check collection existence: %w", err)
    }

    if !exists {
        err = r.Client.CreateCollection(ctx, &qdrant.CreateCollection{
            CollectionName: r.CollectionName,
            VectorsConfig: qdrant.NewVectorsConfig(&qdrant.VectorParams{
                Size:     384,
                Distance: qdrant.Distance_Cosine,
            }),
        })
        if err != nil {
            return fmt.Errorf("failed to create skills collection: %w", err)
        }
        log.Printf("[SkillRepository] ✓ Created collection: %s", r.CollectionName)
    }
    return nil
}

// Store saves a skill
func (r *SkillRepository) Store(ctx context.Context, s *goal.Skill) error {
    if s.ID == "" {
        s.ID = uuid.New().String()
    }

    skillJSON, err := json.Marshal(s)
    if err != nil {
        return fmt.Errorf("failed to marshal skill: %w", err)
    }

    payload := map[string]*qdrant.Value{
        "skill_data":    qdrant.NewValueString(string(skillJSON)),
        "name":          qdrant.NewValueString(s.Name),
        "proficiency":   qdrant.NewValueString(string(s.ProficiencyLevel)),
        "freshness":     qdrant.NewValueInt(int64(s.FreshnessScore)),
        "last_used_at":  qdrant.NewValueInt(s.LastUsedAt.Unix()),
    }

    var vectors *qdrant.Vectors
    if r.embedder != nil {
        // Generate embedding for Skill to enable semantic search
        embedding, err := r.embedder.Embed(ctx, s.Name + " " + s.Description)
        if err == nil && len(embedding) == 384 {
            vectors = qdrant.NewVectors(embedding...)
        } else {
            // Fallback to zero vector if embedding fails
            vectors = qdrant.NewVectors(make([]float32, 384)...)
        }
    } else {
        vectors = qdrant.NewVectors(make([]float32, 384)...)
    }

    point := &qdrant.PointStruct{
        Id:      qdrant.NewIDUUID(s.ID),
        Vectors: vectors,
        Payload: payload,
    }

    _, err = r.Client.Upsert(ctx, &qdrant.UpsertPoints{
        CollectionName: r.CollectionName,
        Points:         []*qdrant.PointStruct{point},
    })

    return err
}

// Get retrieves a skill by ID
func (r *SkillRepository) Get(ctx context.Context, id string) (*goal.Skill, error) {
    points, err := r.Client.Get(ctx, &qdrant.GetPoints{
        CollectionName: r.CollectionName,
        Ids:            []*qdrant.PointId{qdrant.NewIDUUID(id)},
        WithPayload:    qdrant.NewWithPayload(true),
    })
    if err != nil {
        return nil, fmt.Errorf("failed to get skill: %w", err)
    }
    if len(points) == 0 {
        return nil, fmt.Errorf("skill not found: %s", id)
    }

    return r.pointToSkill(points[0])
}

// GetAll retrieves all skills (for decay/maintenance)
func (r *SkillRepository) GetAll(ctx context.Context) ([]*goal.Skill, error) {
    scrollResult, err := r.Client.Scroll(ctx, &qdrant.ScrollPoints{
        CollectionName: r.CollectionName,
        Limit:          uint32Ptr(1000),
        WithPayload:    qdrant.NewWithPayload(true),
    })
    if err != nil {
        return nil, fmt.Errorf("failed to get all skills: %w", err)
    }

    skills := make([]*goal.Skill, 0, len(scrollResult))
    for _, point := range scrollResult {
        s, err := r.pointToSkill(point)
        if err != nil {
            continue
        }
        skills = append(skills, s)
    }

    return skills, nil
}

func (r *SkillRepository) pointToSkill(point *qdrant.RetrievedPoint) (*goal.Skill, error) {
    if point.Payload == nil {
        return nil, fmt.Errorf("point has no payload")
    }

    skillDataVal, ok := point.Payload["skill_data"]
    if !ok {
        return nil, fmt.Errorf("point missing skill_data payload")
    }

    skillJSON := skillDataVal.GetStringValue()
    var s goal.Skill
    if err := json.Unmarshal([]byte(skillJSON), &s); err != nil {
        return nil, fmt.Errorf("failed to unmarshal skill json: %w", err)
    }

    return &s, nil
}

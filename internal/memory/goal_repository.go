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

// GoalRepository handles persistence for goals in Qdrant
type GoalRepository struct {
    Client         *qdrant.Client
    CollectionName string
}

// NewGoalRepository creates a new goal repository
func NewGoalRepository(client *qdrant.Client, collectionName string) (*GoalRepository, error) {
    repo := &GoalRepository{
        Client:         client,
        CollectionName: collectionName,
    }

    // Ensure collection exists
    if err := repo.ensureCollection(context.Background()); err != nil {
        return nil, fmt.Errorf("failed to ensure goal collection: %w", err)
    }

    return repo, nil
}

// ensureCollection creates the goals collection if it doesn't exist
func (r *GoalRepository) ensureCollection(ctx context.Context) error {
    exists, err := r.Client.CollectionExists(ctx, r.CollectionName)
    if err != nil {
        return fmt.Errorf("failed to check collection existence: %w", err)
    }

    if !exists {
        // Create collection with 384 dimensions (matching existing memory system)
        err = r.Client.CreateCollection(ctx, &qdrant.CreateCollection{
            CollectionName: r.CollectionName,
            VectorsConfig: qdrant.NewVectorsConfig(&qdrant.VectorParams{
                Size:     384,
                Distance: qdrant.Distance_Cosine,
            }),
        })
        if err != nil {
            return fmt.Errorf("failed to create goals collection: %w", err)
        }
        log.Printf("[GoalRepository] ✓ Created collection: %s", r.CollectionName)
    }

    // Create indexes for common query fields
    indexes := []struct {
        field string
        typ   qdrant.PayloadSchemaType
    }{
        {"state", qdrant.PayloadSchemaType_Keyword},
        {"origin", qdrant.PayloadSchemaType_Keyword},
        {"priority", qdrant.PayloadSchemaType_Integer},
        {"created_at", qdrant.PayloadSchemaType_Integer},
        {"type", qdrant.PayloadSchemaType_Keyword},
        {"archive_reason", qdrant.PayloadSchemaType_Keyword},
    }

    for _, idx := range indexes {
        var fieldType *qdrant.FieldType
        switch idx.typ {
        case qdrant.PayloadSchemaType_Keyword:
            ft := qdrant.FieldType_FieldTypeKeyword
            fieldType = &ft
        case qdrant.PayloadSchemaType_Integer:
            ft := qdrant.FieldType_FieldTypeInteger
            fieldType = &ft
        }

        _, err := r.Client.CreateFieldIndex(ctx, &qdrant.CreateFieldIndexCollection{
            CollectionName: r.CollectionName,
            FieldName:      idx.field,
            FieldType:      fieldType,
            Wait:           boolPtr(true),
        })
        if err != nil {
            // Log but don't fail if index already exists
            log.Printf("[GoalRepository] Note: Index creation for %s: %v", idx.field, err)
        }
    }

    return nil
}

// Store saves a goal to the database
func (r *GoalRepository) Store(ctx context.Context, g *goal.Goal) error {
    if g.ID == "" {
        g.ID = uuid.New().String()
    }

    // Serialize the goal to JSON for payload storage
    goalJSON, err := json.Marshal(g)
    if err != nil {
        return fmt.Errorf("failed to marshal goal: %w", err)
    }

    payload := map[string]*qdrant.Value{
        "goal_data":  qdrant.NewValueString(string(goalJSON)),
        "state":      qdrant.NewValueString(string(g.State)),
        "origin":     qdrant.NewValueString(string(g.Origin)),
        "priority":   qdrant.NewValueInt(int64(g.CurrentPriority)),
        "created_at": qdrant.NewValueInt(g.CreationTime.Unix()),
        "type":       qdrant.NewValueString(string(g.Type)),
    }

    if g.ArchiveReason != "" {
        payload["archive_reason"] = qdrant.NewValueString(string(g.ArchiveReason))
    }

    // Note: Embedding generation will be handled by the intelligence layer (Phase 3)
    // For now, we use a zero vector to satisfy schema
    vectors := qdrant.NewVectors(make([]float32, 384)...)

    point := &qdrant.PointStruct{
        Id:      qdrant.NewIDUUID(g.ID),
        Vectors: vectors,
        Payload: payload,
    }

    _, err = r.Client.Upsert(ctx, &qdrant.UpsertPoints{
        CollectionName: r.CollectionName,
        Points:         []*qdrant.PointStruct{point},
    })

    return err
}

// Get retrieves a goal by ID
func (r *GoalRepository) Get(ctx context.Context, id string) (*goal.Goal, error) {
    points, err := r.Client.Get(ctx, &qdrant.GetPoints{
        CollectionName: r.CollectionName,
        Ids:            []*qdrant.PointId{qdrant.NewIDUUID(id)},
        WithPayload:    qdrant.NewWithPayload(true),
    })
    if err != nil {
        return nil, fmt.Errorf("failed to get goal: %w", err)
    }
    if len(points) == 0 {
        return nil, fmt.Errorf("goal not found: %s", id)
    }

    return r.pointToGoalFromRetrieved(points[0])
}

// GetByState retrieves all goals in a specific state
func (r *GoalRepository) GetByState(ctx context.Context, state goal.GoalState) ([]*goal.Goal, error) {
    filter := &qdrant.Filter{
        Must: []*qdrant.Condition{
            qdrant.NewMatch("state", string(state)),
        },
    }

    scrollResult, err := r.Client.Scroll(ctx, &qdrant.ScrollPoints{
        CollectionName: r.CollectionName,
        Filter:         filter,
        Limit:          uint32Ptr(100), // Reasonable limit for goals
        WithPayload:    qdrant.NewWithPayload(true),
    })
    if err != nil {
        return nil, fmt.Errorf("failed to get goals by state: %w", err)
    }

    goals := make([]*goal.Goal, 0, len(scrollResult))
    for _, point := range scrollResult {
        g, err := r.pointToGoalFromRetrieved(point)
        if err != nil {
            log.Printf("[GoalRepository] Warning: Failed to parse goal: %v", err)
            continue
        }
        goals = append(goals, g)
    }

    return goals, nil
}

// UpdateState updates only the state of a goal (optimized)
func (r *GoalRepository) UpdateState(ctx context.Context, id string, newState goal.GoalState) error {
    _, err := r.Client.SetPayload(ctx, &qdrant.SetPayloadPoints{
        CollectionName: r.CollectionName,
        Payload: map[string]*qdrant.Value{
            "state": qdrant.NewValueString(string(newState)),
        },
        PointsSelector: &qdrant.PointsSelector{
            PointsSelectorOneOf: &qdrant.PointsSelector_Points{
                Points: &qdrant.PointsIdsList{
                    Ids: []*qdrant.PointId{qdrant.NewIDUUID(id)},
                },
            },
        },
    })
    return err
}

// SearchSimilar performs semantic search for duplicate detection (Phase 2 prep)
// Note: Requires embeddings which are generated in Phase 3
func (r *GoalRepository) SearchSimilar(ctx context.Context, embedding []float32, limit int) ([]*goal.Goal, error) {
    searchResult, err := r.Client.Query(ctx, &qdrant.QueryPoints{
        CollectionName: r.CollectionName,
        Query:          qdrant.NewQuery(embedding...),
        Limit:          uint64Ptr(uint64(limit)),
        WithPayload:    qdrant.NewWithPayload(true),
    })
    if err != nil {
        return nil, fmt.Errorf("semantic search failed: %w", err)
    }

    goals := make([]*goal.Goal, 0, len(searchResult))
    for _, point := range searchResult {
        g, err := r.pointToGoal(point)
        if err != nil {
            continue
        }
        goals = append(goals, g)
    }

    return goals, nil
}

// pointToGoal deserializes a ScoredPoint (from Query) back to a Goal struct
func (r *GoalRepository) pointToGoal(point *qdrant.ScoredPoint) (*goal.Goal, error) {
    if point.Payload == nil {
        return nil, fmt.Errorf("point has no payload")
    }

    goalDataVal, ok := point.Payload["goal_data"]
    if !ok {
        return nil, fmt.Errorf("point missing goal_data payload")
    }

    goalJSON := goalDataVal.GetStringValue()
    var g goal.Goal
    if err := json.Unmarshal([]byte(goalJSON), &g); err != nil {
        return nil, fmt.Errorf("failed to unmarshal goal json: %w", err)
    }

    return &g, nil
}

// pointToGoalFromRetrieved deserializes a RetrievedPoint (from Get/Scroll) back to a Goal struct
func (r *GoalRepository) pointToGoalFromRetrieved(point *qdrant.RetrievedPoint) (*goal.Goal, error) {
    if point.Payload == nil {
        return nil, fmt.Errorf("point has no payload")
    }

    goalDataVal, ok := point.Payload["goal_data"]
    if !ok {
        return nil, fmt.Errorf("point missing goal_data payload")
    }

    goalJSON := goalDataVal.GetStringValue()
    var g goal.Goal
    if err := json.Unmarshal([]byte(goalJSON), &g); err != nil {
        return nil, fmt.Errorf("failed to unmarshal goal json: %w", err)
    }

    return &g, nil
}

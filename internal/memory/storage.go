// internal/memory/storage.go
package memory

import (
	"context"
	"fmt"
	"strings"
	"time"
	"log"

	"github.com/google/uuid"
	"github.com/qdrant/go-client/qdrant"
)

// Storage handles all vector database operations
type Storage struct {
	client         *qdrant.Client
	collectionName string
}

// NewStorage creates a new storage instance
func NewStorage(qdrantURL string, collectionName string, apiKey string) (*Storage, error) {
	// Strip http:// or https:// prefix and any port
	qdrantURL = strings.TrimPrefix(qdrantURL, "http://")
	qdrantURL = strings.TrimPrefix(qdrantURL, "https://")
	
	// Remove port if present - we'll set it explicitly
	host := qdrantURL
	if idx := strings.Index(qdrantURL, ":"); idx != -1 {
		host = qdrantURL[:idx]
	}
	
	client, err := qdrant.NewClient(&qdrant.Config{
		Host:   host,
		Port:   6334,  // gRPC port
		APIKey: apiKey,
		UseTLS: false,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to create Qdrant client: %w", err)
	}

	s := &Storage{
		client:         client,
		collectionName: collectionName,
	}

	// Ensure collection exists
	if err := s.ensureCollection(context.Background()); err != nil {
		return nil, fmt.Errorf("failed to ensure collection: %w", err)
	}

	return s, nil
}

// ensureCollection creates the collection if it doesn't exist
func (s *Storage) ensureCollection(ctx context.Context) error {
	// Check if collection exists
	exists, err := s.client.CollectionExists(ctx, s.collectionName)
	if err != nil {
		return fmt.Errorf("failed to check collection existence: %w", err)
	}

	if exists {
		return nil
	}

	// Create collection with 384 dimensions (all-MiniLM-L6-v2)
	err = s.client.CreateCollection(ctx, &qdrant.CreateCollection{
		CollectionName: s.collectionName,
		VectorsConfig: qdrant.NewVectorsConfig(&qdrant.VectorParams{
			Size:     384,
			Distance: qdrant.Distance_Cosine,
		}),
	})
	if err != nil {
		return fmt.Errorf("failed to create collection: %w", err)
	}

	// Create payload indexes for efficient filtering
	indexes := []struct {
		field string
		typ   qdrant.PayloadSchemaType
	}{
		{"tier", qdrant.PayloadSchemaType_Keyword},
		{"user_id", qdrant.PayloadSchemaType_Keyword},
		{"is_collective", qdrant.PayloadSchemaType_Bool},
		{"created_at", qdrant.PayloadSchemaType_Integer},
		{"importance_score", qdrant.PayloadSchemaType_Float},
	}

	for _, idx := range indexes {
		fieldType := qdrant.FieldType(idx.typ)
		_, err = s.client.CreateFieldIndex(ctx, &qdrant.CreateFieldIndexCollection{
			CollectionName: s.collectionName,
			FieldName:      idx.field,
			FieldType:      &fieldType,
			Wait:           boolPtr(true),
		})
		if err != nil {
			return fmt.Errorf("failed to create index for %s: %w", idx.field, err)
		}
	}

	return nil
}

// Store saves a memory to the vector database
func (s *Storage) Store(ctx context.Context, memory *Memory) error {
	if memory.ID == "" {
		memory.ID = uuid.New().String()
	}

	payload := map[string]interface{}{
		"content":          memory.Content,
		"compressed_from":  memory.CompressedFrom,
		"tier":             string(memory.Tier),
		"is_collective":    memory.IsCollective,
		"created_at":       memory.CreatedAt.Unix(),
		"last_accessed_at": memory.LastAccessedAt.Unix(),
		"access_count":     memory.AccessCount,
		"importance_score": memory.ImportanceScore,
		"memory_id":        memory.ID, // Store memory ID for lookups
	}

	if memory.UserID != nil {
		payload["user_id"] = *memory.UserID
	}

	// Add custom metadata
	for k, v := range memory.Metadata {
		payload[k] = v
	}

	point := &qdrant.PointStruct{
		Id:      qdrant.NewIDUUID(memory.ID), // Use memory ID as point ID
		Vectors: qdrant.NewVectors(memory.Embedding...),
		Payload: qdrant.NewValueMap(payload),
	}

	_, err := s.client.Upsert(ctx, &qdrant.UpsertPoints{
		CollectionName: s.collectionName,
		Points:         []*qdrant.PointStruct{point},
	})

	return err
}

// Search performs semantic search for relevant memories
func (s *Storage) Search(ctx context.Context, query RetrievalQuery, queryEmbedding []float32) ([]RetrievalResult, error) {
	log.Printf("[Storage] Search called - Limit: %d, MinScore: %.2f, IncludeCollective: %v", 
		query.Limit, query.MinScore, query.IncludeCollective)
	// Build filter
	var must []*qdrant.Condition

	if query.UserID != nil && query.IncludePersonal {
		must = append(must, qdrant.NewMatch("user_id", *query.UserID))
		log.Printf("[Storage] Added user_id filter: %s", *query.UserID)
	}

	if query.IncludeCollective {
		must = append(must, qdrant.NewMatch("is_collective", "true"))
		log.Printf("[Storage] Added is_collective filter")
	}

	log.Printf("[Storage] Total filter conditions: %d", len(must))

	if query.Tier != nil {
		must = append(must, qdrant.NewMatch("tier", string(*query.Tier)))
	}

	var filter *qdrant.Filter
	if len(must) > 0 {
		filter = &qdrant.Filter{
			Must: must,
		}
	}

	// Perform search
	searchResult, err := s.client.Query(ctx, &qdrant.QueryPoints{
		CollectionName: s.collectionName,
		Query:          qdrant.NewQuery(queryEmbedding...),
		Filter:         filter,
		Limit:          uint64Ptr(uint64(query.Limit)),
		WithPayload:    qdrant.NewWithPayload(true),
	})

	if err != nil {
		return nil, fmt.Errorf("search failed: %w", err)
	}

	// Convert results
	results := make([]RetrievalResult, 0, len(searchResult))
	for _, point := range searchResult {
		if float64(point.Score) < query.MinScore {
			continue
		}

		memory := s.pointToMemory(point)
		results = append(results, RetrievalResult{
			Memory: memory,
			Score:  float64(point.Score),
		})
	}

	return results, nil
}

// pointToMemory converts a Qdrant point to a Memory struct
func (s *Storage) pointToMemory(point *qdrant.ScoredPoint) Memory {
	payload := point.Payload

	memory := Memory{
		Content:         getStringFromPayload(payload, "content"),
		CompressedFrom:  getStringFromPayload(payload, "compressed_from"),
		Tier:            MemoryTier(getStringFromPayload(payload, "tier")),
		IsCollective:    getBoolFromPayload(payload, "is_collective"),
		CreatedAt:       time.Unix(getIntFromPayload(payload, "created_at"), 0),
		LastAccessedAt:  time.Unix(getIntFromPayload(payload, "last_accessed_at"), 0),
		AccessCount:     int(getIntFromPayload(payload, "access_count")),
		ImportanceScore: getFloatFromPayload(payload, "importance_score"),
		Metadata:        make(map[string]interface{}),
	}

	if userID := getStringFromPayload(payload, "user_id"); userID != "" {
		memory.UserID = &userID
	}

	return memory
}

// Helper functions for payload extraction
func getStringFromPayload(payload map[string]*qdrant.Value, key string) string {
	if val, ok := payload[key]; ok && val.GetStringValue() != "" {
		return val.GetStringValue()
	}
	return ""
}

func getBoolFromPayload(payload map[string]*qdrant.Value, key string) bool {
	if val, ok := payload[key]; ok {
		return val.GetBoolValue()
	}
	return false
}

func getIntFromPayload(payload map[string]*qdrant.Value, key string) int64 {
	if val, ok := payload[key]; ok {
		return val.GetIntegerValue()
	}
	return 0
}

func getFloatFromPayload(payload map[string]*qdrant.Value, key string) float64 {
	if val, ok := payload[key]; ok {
		return val.GetDoubleValue()
	}
	return 0.0
}

func uint64Ptr(v uint64) *uint64 {
	return &v
}

func boolPtr(v bool) *bool {
	return &v
}

// FindMemoriesForCompression finds memories eligible for compression based on age and tier
func (s *Storage) FindMemoriesForCompression(ctx context.Context, currentTier MemoryTier, ageDays int, limit int) ([]Memory, error) {
	cutoffTime := time.Now().AddDate(0, 0, -ageDays).Unix()
	
	filter := &qdrant.Filter{
		Must: []*qdrant.Condition{
			qdrant.NewMatch("tier", string(currentTier)),
			&qdrant.Condition{
				ConditionOneOf: &qdrant.Condition_Field{
					Field: &qdrant.FieldCondition{
						Key: "created_at",
						Range: &qdrant.Range{
							Lt: floatPtr(float64(cutoffTime)),
						},
					},
				},
			},
		},
	}

	scrollResult, err := s.client.Scroll(ctx, &qdrant.ScrollPoints{
		CollectionName: s.collectionName,
		Filter:         filter,
		Limit:          uint32Ptr(uint32(limit)),
		WithPayload:    qdrant.NewWithPayload(true),
	})
	
	if err != nil {
		return nil, fmt.Errorf("scroll failed: %w", err)
	}

	memories := make([]Memory, 0, len(scrollResult))
	for _, point := range scrollResult {
		memory := s.pointToMemoryFromScroll(point)
		memories = append(memories, memory)
	}

	return memories, nil
}

// UpdateMemory updates an existing memory in the database
func (s *Storage) UpdateMemory(ctx context.Context, memory *Memory) error {
	// Find the point ID first by searching with the memory ID stored in metadata
	// For now, we'll use Upsert which will update if exists
	// Note: We need the original point ID, which we should store in Memory struct
	
	payload := map[string]interface{}{
		"content":          memory.Content,
		"compressed_from":  memory.CompressedFrom,
		"tier":             string(memory.Tier),
		"is_collective":    memory.IsCollective,
		"created_at":       memory.CreatedAt.Unix(),
		"last_accessed_at": memory.LastAccessedAt.Unix(),
		"access_count":     memory.AccessCount,
		"importance_score": memory.ImportanceScore,
		"memory_id":        memory.ID, // Store memory ID in payload for lookups
	}

	if memory.UserID != nil {
		payload["user_id"] = *memory.UserID
	}

	for k, v := range memory.Metadata {
		payload[k] = v
	}

	point := &qdrant.PointStruct{
		Id:      qdrant.NewIDUUID(memory.ID), // Use memory ID as point ID
		Vectors: qdrant.NewVectors(memory.Embedding...),
		Payload: qdrant.NewValueMap(payload),
	}

	_, err := s.client.Upsert(ctx, &qdrant.UpsertPoints{
		CollectionName: s.collectionName,
		Points:         []*qdrant.PointStruct{point},
	})

	return err
}

// Helper to convert scroll point to memory
func (s *Storage) pointToMemoryFromScroll(point *qdrant.RetrievedPoint) Memory {
	payload := point.Payload

	memory := Memory{
		ID:              getStringFromPayload(payload, "memory_id"),
		Content:         getStringFromPayload(payload, "content"),
		CompressedFrom:  getStringFromPayload(payload, "compressed_from"),
		Tier:            MemoryTier(getStringFromPayload(payload, "tier")),
		IsCollective:    getBoolFromPayload(payload, "is_collective"),
		CreatedAt:       time.Unix(getIntFromPayload(payload, "created_at"), 0),
		LastAccessedAt:  time.Unix(getIntFromPayload(payload, "last_accessed_at"), 0),
		AccessCount:     int(getIntFromPayload(payload, "access_count")),
		ImportanceScore: getFloatFromPayload(payload, "importance_score"),
		Metadata:        make(map[string]interface{}),
	}

	if userID := getStringFromPayload(payload, "user_id"); userID != "" {
		memory.UserID = &userID
	}

	return memory
}

func floatPtr(v float64) *float64 {
	return &v
}

func uint32Ptr(v uint32) *uint32 {
	return &v
}

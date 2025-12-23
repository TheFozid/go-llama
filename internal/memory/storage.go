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
		// Phase 4: New indexes
		{"outcome_tag", qdrant.PayloadSchemaType_Keyword},
		{"trust_score", qdrant.PayloadSchemaType_Float},
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

	// Validate outcome tag if provided
	if memory.OutcomeTag != "" {
		if err := ValidateOutcomeTag(memory.OutcomeTag); err != nil {
			return fmt.Errorf("invalid outcome tag: %w", err)
		}
	}

	// Convert string slices to Qdrant ListValue
	relatedMemoriesValues := make([]*qdrant.Value, len(memory.RelatedMemories))
	for i, rm := range memory.RelatedMemories {
		relatedMemoriesValues[i] = qdrant.NewValueString(rm)
	}
	
	conceptTagsValues := make([]*qdrant.Value, len(memory.ConceptTags))
	for i, ct := range memory.ConceptTags {
		conceptTagsValues[i] = qdrant.NewValueString(ct)
	}
	
	conflictFlagsValues := make([]*qdrant.Value, len(memory.ConflictFlags))
	for i, cf := range memory.ConflictFlags {
		conflictFlagsValues[i] = qdrant.NewValueString(cf)
	}


	payload := map[string]*qdrant.Value{
		"content":          qdrant.NewValueString(memory.Content),
		"compressed_from":  qdrant.NewValueString(memory.CompressedFrom),
		"tier":             qdrant.NewValueString(string(memory.Tier)),
		"is_collective":    qdrant.NewValueBool(memory.IsCollective),
		"created_at":       qdrant.NewValueInteger(memory.CreatedAt.Unix()),
		"last_accessed_at": qdrant.NewValueInteger(memory.LastAccessedAt.Unix()),
		"access_count":     qdrant.NewValueInteger(int64(memory.AccessCount)),
		"importance_score": qdrant.NewValueDouble(memory.ImportanceScore),
		"memory_id":        qdrant.NewValueString(memory.ID),
		
		// Phase 4: Good/Bad Tagging
		"outcome_tag":       qdrant.NewValueString(memory.OutcomeTag),
		"trust_score":       qdrant.NewValueDouble(memory.TrustScore),
		"validation_count":  qdrant.NewValueInteger(int64(memory.ValidationCount)),
		
		// Phase 4: Memory Linking
		"related_memories":  &qdrant.Value{Kind: &qdrant.Value_ListValue{ListValue: &qdrant.ListValue{Values: relatedMemoriesValues}}},
		"concept_tags":      &qdrant.Value{Kind: &qdrant.Value_ListValue{ListValue: &qdrant.ListValue{Values: conceptTagsValues}}},
		
		// Phase 4: Temporal & Conflict
		"temporal_resolution": qdrant.NewValueString(memory.TemporalResolution),
		"conflict_flags":      &qdrant.Value{Kind: &qdrant.Value_ListValue{ListValue: &qdrant.ListValue{Values: conflictFlagsValues}}},
		
		// Phase 4: Principles
		"principle_rating":  qdrant.NewValueDouble(memory.PrincipleRating),
	}

	if memory.UserID != nil {
		payload["user_id"] = qdrant.NewValueString(*memory.UserID)
	}

	// Skip metadata for now - would need type conversion
	// for k, v := range memory.Metadata {
	// 	payload[k] = v
	// }

	point := &qdrant.PointStruct{
		Id:      qdrant.NewIDUUID(memory.ID),
		Vectors: qdrant.NewVectors(memory.Embedding...),
		Payload: payload,
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

	if query.Tier != nil {
		must = append(must, qdrant.NewMatch("tier", string(*query.Tier)))
	}
	
	// Phase 4: Outcome filter
	if query.OutcomeFilter != nil {
		must = append(must, qdrant.NewMatch("outcome_tag", string(*query.OutcomeFilter)))
		log.Printf("[Storage] Added outcome_tag filter: %s", *query.OutcomeFilter)
	}
	
	// Phase 4: Concept tags filter (match any of the provided tags)
	if len(query.ConceptTags) > 0 {
		// For simplicity, we'll search for memories that contain ANY of the concept tags
		// More sophisticated filtering would require custom logic
		log.Printf("[Storage] Concept tags filtering requested (not yet fully implemented): %v", query.ConceptTags)
	}

	log.Printf("[Storage] Total filter conditions: %d", len(must))

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
		
		// Phase 4: Good/Bad Tagging
		OutcomeTag:      getStringFromPayload(payload, "outcome_tag"),
		TrustScore:      getFloatFromPayload(payload, "trust_score"),
		ValidationCount: int(getIntFromPayload(payload, "validation_count")),
		
		// Phase 4: Memory Linking
		RelatedMemories: getStringSliceFromPayload(payload, "related_memories"),
		ConceptTags:     getStringSliceFromPayload(payload, "concept_tags"),
		
		// Phase 4: Temporal & Conflict
		TemporalResolution: getStringFromPayload(payload, "temporal_resolution"),
		ConflictFlags:      getStringSliceFromPayload(payload, "conflict_flags"),
		
		// Phase 4: Principles
		PrincipleRating: getFloatFromPayload(payload, "principle_rating"),
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

// Phase 4: Helper to extract string slices from payload
func getStringSliceFromPayload(payload map[string]*qdrant.Value, key string) []string {
	if val, ok := payload[key]; ok {
		listValue := val.GetListValue()
		if listValue == nil {
			return []string{}
		}
		
		result := make([]string, 0, len(listValue.Values))
		for _, v := range listValue.Values {
			if str := v.GetStringValue(); str != "" {
				result = append(result, str)
			}
		}
		return result
	}
	return []string{}
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
	// Validate outcome tag if provided
	if memory.OutcomeTag != "" {
		if err := ValidateOutcomeTag(memory.OutcomeTag); err != nil {
			return fmt.Errorf("invalid outcome tag: %w", err)
		}
	}
	
	// Convert string slices to Qdrant ListValue
	relatedMemoriesValues := make([]*qdrant.Value, len(memory.RelatedMemories))
	for i, rm := range memory.RelatedMemories {
		relatedMemoriesValues[i] = qdrant.NewValueString(rm)
	}
	
	conceptTagsValues := make([]*qdrant.Value, len(memory.ConceptTags))
	for i, ct := range memory.ConceptTags {
		conceptTagsValues[i] = qdrant.NewValueString(ct)
	}
	
	conflictFlagsValues := make([]*qdrant.Value, len(memory.ConflictFlags))
	for i, cf := range memory.ConflictFlags {
		conflictFlagsValues[i] = qdrant.NewValueString(cf)
	}
	

	payload := map[string]*qdrant.Value{
		"content":          qdrant.NewValueString(memory.Content),
		"compressed_from":  qdrant.NewValueString(memory.CompressedFrom),
		"tier":             qdrant.NewValueString(string(memory.Tier)),
		"is_collective":    qdrant.NewValueBool(memory.IsCollective),
		"created_at":       qdrant.NewValueInteger(memory.CreatedAt.Unix()),
		"last_accessed_at": qdrant.NewValueInteger(memory.LastAccessedAt.Unix()),
		"access_count":     qdrant.NewValueInteger(int64(memory.AccessCount)),
		"importance_score": qdrant.NewValueDouble(memory.ImportanceScore),
		"memory_id":        qdrant.NewValueString(memory.ID),
		
		// Phase 4: Good/Bad Tagging
		"outcome_tag":       qdrant.NewValueString(memory.OutcomeTag),
		"trust_score":       qdrant.NewValueDouble(memory.TrustScore),
		"validation_count":  qdrant.NewValueInteger(int64(memory.ValidationCount)),
		
		// Phase 4: Memory Linking
		"related_memories":  &qdrant.Value{Kind: &qdrant.Value_ListValue{ListValue: &qdrant.ListValue{Values: relatedMemoriesValues}}},
		"concept_tags":      &qdrant.Value{Kind: &qdrant.Value_ListValue{ListValue: &qdrant.ListValue{Values: conceptTagsValues}}},
		
		// Phase 4: Temporal & Conflict
		"temporal_resolution": qdrant.NewValueString(memory.TemporalResolution),
		"conflict_flags":      &qdrant.Value{Kind: &qdrant.Value_ListValue{ListValue: &qdrant.ListValue{Values: conflictFlagsValues}}},
		
		// Phase 4: Principles
		"principle_rating":  qdrant.NewValueDouble(memory.PrincipleRating),
	}

	if memory.UserID != nil {
		payload["user_id"] = qdrant.NewValueString(*memory.UserID)
	}

	// Skip metadata for now
	// for k, v := range memory.Metadata {
	// 	payload[k] = v
	// }

	point := &qdrant.PointStruct{
		Id:      qdrant.NewIDUUID(memory.ID),
		Vectors: qdrant.NewVectors(memory.Embedding...),
		Payload: payload,
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
		
		// Phase 4: Good/Bad Tagging
		OutcomeTag:      getStringFromPayload(payload, "outcome_tag"),
		TrustScore:      getFloatFromPayload(payload, "trust_score"),
		ValidationCount: int(getIntFromPayload(payload, "validation_count")),
		
		// Phase 4: Memory Linking
		RelatedMemories: getStringSliceFromPayload(payload, "related_memories"),
		ConceptTags:     getStringSliceFromPayload(payload, "concept_tags"),
		
		// Phase 4: Temporal & Conflict
		TemporalResolution: getStringFromPayload(payload, "temporal_resolution"),
		ConflictFlags:      getStringSliceFromPayload(payload, "conflict_flags"),
		
		// Phase 4: Principles
		PrincipleRating: getFloatFromPayload(payload, "principle_rating"),
	}

	if userID := getStringFromPayload(payload, "user_id"); userID != "" {
		memory.UserID = &userID
	}

	return memory
}

// Phase 4: FindMemoryClusters finds semantically similar memories for clustering
func (s *Storage) FindMemoryClusters(ctx context.Context, tier MemoryTier, embedding []float32, similarityThreshold float64, limit int) ([]Memory, error) {
	filter := &qdrant.Filter{
		Must: []*qdrant.Condition{
			qdrant.NewMatch("tier", string(tier)),
		},
	}

	searchResult, err := s.client.Query(ctx, &qdrant.QueryPoints{
		CollectionName: s.collectionName,
		Query:          qdrant.NewQuery(embedding...),
		Filter:         filter,
		Limit:          uint64Ptr(uint64(limit)),
		WithPayload:    qdrant.NewWithPayload(true),
	})

	if err != nil {
		return nil, fmt.Errorf("cluster search failed: %w", err)
	}

	memories := make([]Memory, 0)
	for _, point := range searchResult {
		if float64(point.Score) < similarityThreshold {
			continue
		}
		memory := s.pointToMemory(point)
		memories = append(memories, memory)
	}

	return memories, nil
}

// Phase 4: SearchByConceptTags finds memories with specific concept tags
func (s *Storage) SearchByConceptTags(ctx context.Context, tags []string, limit int) ([]Memory, error) {
	// Note: Qdrant doesn't natively support array intersection filtering efficiently
	// For now, we'll retrieve memories and filter in-memory
	// A production implementation might use a different strategy
	
	scrollResult, err := s.client.Scroll(ctx, &qdrant.ScrollPoints{
		CollectionName: s.collectionName,
		Limit:          uint32Ptr(uint32(limit * 2)), // Get more to filter
		WithPayload:    qdrant.NewWithPayload(true),
	})
	
	if err != nil {
		return nil, fmt.Errorf("scroll failed: %w", err)
	}

	memories := make([]Memory, 0)
	tagSet := make(map[string]bool)
	for _, tag := range tags {
		tagSet[tag] = true
	}

	for _, point := range scrollResult {
		memory := s.pointToMemoryFromScroll(point)
		
		// Check if memory has any of the requested tags
		hasTag := false
		for _, memTag := range memory.ConceptTags {
			if tagSet[memTag] {
				hasTag = true
				break
			}
		}
		
		if hasTag {
			memories = append(memories, memory)
			if len(memories) >= limit {
				break
			}
		}
	}

	return memories, nil
}

// Phase 4: UpdateAccessMetadata increments access count and updates timestamp
func (s *Storage) UpdateAccessMetadata(ctx context.Context, memoryID string) error {
	// We need to read the full memory, update it, and write it back
	// This is less efficient but works with the current Qdrant client API
	
	// First, search for the memory by ID stored in payload
	filter := &qdrant.Filter{
		Must: []*qdrant.Condition{
			qdrant.NewMatch("memory_id", memoryID),
		},
	}

	scrollResult, err := s.client.Scroll(ctx, &qdrant.ScrollPoints{
		CollectionName: s.collectionName,
		Filter:         filter,
		Limit:          uint32Ptr(1),
		WithPayload:    qdrant.NewWithPayload(true),
		WithVectors:    &qdrant.WithVectorsSelector{
			SelectorOptions: &qdrant.WithVectorsSelector_Enable{
				Enable: true,
			},
		},
	})
	
	if err != nil {
		return fmt.Errorf("failed to find memory: %w", err)
	}
	
	if len(scrollResult) == 0 {
		return fmt.Errorf("memory not found: %s", memoryID)
	}
	
	// Convert to Memory struct
	point := scrollResult[0]
	memory := s.pointToMemoryFromScroll(point)
	
	// Get vectors from point
	vectors := point.Vectors.GetVector()
	if vectors != nil {
		memory.Embedding = vectors.Data
	}
	
	// Update access metadata
	memory.AccessCount++
	memory.LastAccessedAt = time.Now()
	
	// Write back using UpdateMemory (which uses Upsert)
	return s.UpdateMemory(ctx, &memory)
}

func floatPtr(v float64) *float64 {
	return &v
}

func uint32Ptr(v uint32) *uint32 {
	return &v
}

// FindUntaggedMemories retrieves memories that haven't been tagged yet (OutcomeTag is empty)
func (s *Storage) FindUntaggedMemories(ctx context.Context, limit int) ([]*Memory, error) {
	if limit <= 0 {
		limit = 100
	}

	// Query for memories where outcome_tag is empty or null
	scrollResult, err := s.client.Scroll(ctx, &qdrant.ScrollPoints{
		CollectionName: s.collectionName,  // FIXED: was s.collection
		Filter: &qdrant.Filter{
			Must: []*qdrant.Condition{
				{
					ConditionOneOf: &qdrant.Condition_IsEmpty{
						IsEmpty: &qdrant.IsEmptyCondition{
							Key: "outcome_tag",
						},
					},
				},
			},
		},
		Limit:      qdrant.PtrOf(uint32(limit)),
		WithPayload: qdrant.NewWithPayload(true),
	})

	if err != nil {
		return nil, fmt.Errorf("failed to scroll untagged memories: %w", err)
	}

	memories := make([]*Memory, 0, len(scrollResult))
	for _, point := range scrollResult {
		mem := s.pointToMemoryFromScroll(point)  // FIXED: now calls as method, returns Memory not error
		memories = append(memories, &mem)        // FIXED: take address since it returns Memory not *Memory
	}

	return memories, nil
}

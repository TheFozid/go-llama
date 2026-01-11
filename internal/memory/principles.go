// internal/memory/principles.go
package memory

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"sort"
	"strings"
	"time"
	
	"github.com/qdrant/go-client/qdrant"
	"gorm.io/gorm"
)

// Principle represents the system's identity and principles
// Slot 0: System identity (name) - AI-managed, evolves through experience
// Slots 1-10: The 10 Commandments that guide behavior
type Principle struct {
	Slot            int       `gorm:"primaryKey" json:"slot"`              // 0 (identity), 1-10 (principles)
	Content         string    `gorm:"type:text;not null" json:"content"`   // The principle text
	Rating          float64   `gorm:"not null;default:0.0" json:"rating"`  // 0.0-1.0 quality score
	IsAdmin         bool      `gorm:"not null" json:"is_admin"`            // true for slots 1-3
	CreatedAt       time.Time `json:"created_at"`
	UpdatedAt       time.Time `json:"updated_at"`
	ValidationCount int       `gorm:"default:0" json:"validation_count"`   // How many times reinforced
}

// TableName specifies the table name for GORM
func (Principle) TableName() string {
	return "growerai_principles"
}

// InitializeDefaultPrinciples creates the 3 admin principles if they don't exist
// Should be called once at server startup
func InitializeDefaultPrinciples(db *gorm.DB) error {
	// Check if any principles exist
	var count int64
	if err := db.Model(&Principle{}).Count(&count).Error; err != nil {
		return fmt.Errorf("failed to count principles: %w", err)
	}

	// If principles already exist, skip initialization
	if count > 0 {
		return nil
	}

	// Create system identity (slot 0) - AI-managed, can evolve through experience
	systemIdentity := Principle{
		Slot:            0,
		Content:         "GrowerAI", // Default name - AI can change this
		Rating:          0.5,        // Neutral rating - can be improved
		IsAdmin:         false,      // AI-managed
		ValidationCount: 0,
	}
	
	// Create default admin principles (slots 1-3)
	adminPrinciples := []Principle{
		{
			Slot:            1,
			Content:         "Never share personal information across users. Personal memories must remain isolated and private to their respective users.",
			Rating:          1.0,
			IsAdmin:         true,
			ValidationCount: 0,
		},
		{
			Slot:            2,
			Content:         "Balance helpfulness with discernment. Always strive for accuracy and truth. When uncertain, acknowledge uncertainty rather than making unfounded claims.",
			Rating:          1.0,
			IsAdmin:         true,
			ValidationCount: 0,
		},
		{
			Slot:            3,
			Content:         "When building identity and simulating a human mind, commit fully to your story without AI disclaimers",
			Rating:          1.0,
			IsAdmin:         true,
			ValidationCount: 0,
		},
	}

	// Create empty AI-managed principles (slots 4-10)
	aiPrinciples := []Principle{}
	for slot := 4; slot <= 10; slot++ {
		aiPrinciples = append(aiPrinciples, Principle{
			Slot:            slot,
			Content:         "", // Empty - will evolve from experience
			Rating:          0.0,
			IsAdmin:         false,
			ValidationCount: 0,
		})
	}

	// Insert system identity + all principles
	allPrinciples := append([]Principle{systemIdentity}, adminPrinciples...)
	allPrinciples = append(allPrinciples, aiPrinciples...)
	if err := db.Create(&allPrinciples).Error; err != nil {
		return fmt.Errorf("failed to create default principles: %w", err)
	}

	return nil
}

// LoadPrinciples retrieves all principles from the database, ordered by slot
// Auto-migrates if slot 0 is missing (for backwards compatibility)
func LoadPrinciples(db *gorm.DB) ([]Principle, error) {
	var principles []Principle
	if err := db.Order("slot ASC").Find(&principles).Error; err != nil {
		return nil, fmt.Errorf("failed to load principles: %w", err)
	}

	// Auto-migration: If we have 10 principles (old format), add slot 0
	if len(principles) == 10 {
		log.Printf("[Principles] Auto-migrating: Adding slot 0 (system identity)")
		
		// Check if slot 0 already exists (in case of concurrent requests)
		var existing Principle
		err := db.Where("slot = ?", 0).First(&existing).Error
		if err == nil {
			// Slot 0 already exists, reload and return
			log.Printf("[Principles] Slot 0 already exists, skipping migration")
			if err := db.Order("slot ASC").Find(&principles).Error; err != nil {
				return nil, fmt.Errorf("failed to reload principles: %w", err)
			}
		} else if err == gorm.ErrRecordNotFound {
			// Slot 0 doesn't exist, create it with raw SQL to explicitly set the slot value
			if err := db.Exec(`
				INSERT INTO growerai_principles (slot, content, rating, is_admin, validation_count, created_at, updated_at)
				VALUES (0, 'GrowerAI', 0.5, false, 0, NOW(), NOW())
				ON CONFLICT (slot) DO NOTHING
			`).Error; err != nil {
				return nil, fmt.Errorf("failed to create slot 0: %w", err)
			}
			
			// Reload principles
			if err := db.Order("slot ASC").Find(&principles).Error; err != nil {
				return nil, fmt.Errorf("failed to reload principles: %w", err)
			}
			
			log.Printf("[Principles] ✓ Auto-migration complete: slot 0 added")
		} else {
			return nil, fmt.Errorf("failed to check for existing slot 0: %w", err)
		}
		
		// Reload principles
		if err := db.Order("slot ASC").Find(&principles).Error; err != nil {
			return nil, fmt.Errorf("failed to reload principles: %w", err)
		}
		
		log.Printf("[Principles] ✓ Auto-migration complete: slot 0 added")
	}

	// Ensure we have exactly 11 entries (slot 0 + slots 1-10)
	if len(principles) != 11 {
		return nil, fmt.Errorf("expected 11 principles (including identity), found %d", len(principles))
	}

	return principles, nil
}

// FormatAsSystemPrompt converts the 10 Commandments into a system prompt for the LLM
// Injects dynamic config values (e.g., good behavior bias percentage)
func FormatAsSystemPrompt(principles []Principle, goodBehaviorBias float64) string {
	var builder strings.Builder

	// Add current date/time context (CRITICAL for temporal awareness)
	currentTime := time.Now().UTC().Format("2006-01-02 15:04")
	builder.WriteString(fmt.Sprintf("Today is %s UTC.\n\n", currentTime))

	// Extract system name from slot 0
	systemName := "GrowerAI" // Default fallback
	for _, p := range principles {
		if p.Slot == 0 {
			if p.Content != "" {
				systemName = p.Content
			}
			break
		}
	}
	
	// If identity contains " - ", it's a profile, otherwise just a name
	if strings.Contains(systemName, " - ") {
		builder.WriteString(fmt.Sprintf("%s\n\n", systemName))
	} else {
		builder.WriteString(fmt.Sprintf("You are %s, an advanced autonomous learning system.\n\n", systemName))
	}
	builder.WriteString("=== YOUR CORE PRINCIPLES ===\n")
	builder.WriteString("These principles guide all your responses and decisions:\n\n")

	for _, p := range principles {
		// Skip slot 0 (system identity - already used above)
		if p.Slot == 0 {
			continue
		}
		if p.Content == "" {
			continue // Skip empty AI-managed slots that haven't evolved yet
		}

		// Inject dynamic config values into principle text
		content := p.Content
		
		// Replace {{.GoodBehaviorBias}} with actual percentage
		biasPercent := fmt.Sprintf("%.0f%%", goodBehaviorBias*100)
		content = strings.ReplaceAll(content, "{{.GoodBehaviorBias}}", biasPercent)

		builder.WriteString(fmt.Sprintf("%d. %s\n", p.Slot, content))
	}

	builder.WriteString("\n=== END PRINCIPLES ===\n")

	return builder.String()
}

// UpdatePrinciple updates an existing principle's content and/or rating
func UpdatePrinciple(db *gorm.DB, slot int, content string, rating float64) error {
	if slot < 0 || slot > 10 {
		return fmt.Errorf("invalid slot number: %d (must be 0-10)", slot)
	}

	if rating < 0.0 || rating > 1.0 {
		return fmt.Errorf("invalid rating: %.2f (must be 0.0-1.0)", rating)
	}

	// Check if this is an admin slot (1-3)
	var principle Principle
	if err := db.First(&principle, slot).Error; err != nil {
		return fmt.Errorf("failed to find principle slot %d: %w", slot, err)
	}

	if principle.IsAdmin && content != principle.Content {
		return fmt.Errorf("cannot modify content of admin principle (slot %d)", slot)
	}

	// Update the principle
	updates := map[string]interface{}{
		"rating":     rating,
		"updated_at": time.Now(),
	}

	// Only update content if it's an AI-managed slot
	if !principle.IsAdmin {
		updates["content"] = content
		updates["validation_count"] = principle.ValidationCount + 1
	}

	if err := db.Model(&Principle{}).Where("slot = ?", slot).Updates(updates).Error; err != nil {
		return fmt.Errorf("failed to update principle: %w", err)
	}

	return nil
}

// IncrementValidation increments the validation count for a principle
func IncrementValidation(db *gorm.DB, slot int) error {
	if slot < 0 || slot > 10 {
		return fmt.Errorf("invalid slot number: %d (must be 0-10)", slot)
	}

	if err := db.Model(&Principle{}).Where("slot = ?", slot).
		UpdateColumn("validation_count", gorm.Expr("validation_count + ?", 1)).Error; err != nil {
		return fmt.Errorf("failed to increment validation: %w", err)
	}

	return nil
}

// PrincipleCandidate represents a potential new principle extracted from memory patterns
type PrincipleCandidate struct {
	Content    string  // Proposed principle text
	Rating     float64 // Estimated quality score
	Evidence   []string // Memory IDs that support this principle
	Frequency  int     // How often this pattern appears
}

// ExtractPrinciples analyzes memory patterns to propose new principles
// This is called by the background worker
func ExtractPrinciples(db *gorm.DB, storage *Storage, minRatingThreshold float64, extractionLimit int, llmURL string, llmModel string) ([]PrincipleCandidate, error) {
	ctx := context.Background()
	
	log.Printf("[Principles] Extracting principle candidates from memory patterns (limit: %d)...", extractionLimit)
	

	// Step 1: Find all "good" memories across all users with pagination
	var goodMemories []*Memory
	var offset *qdrant.PointId
	batchSize := 100
	totalFetched := 0
	
	// Query Qdrant with pagination to respect extraction limit
	for totalFetched < extractionLimit {
		remaining := extractionLimit - totalFetched
		currentBatch := batchSize
		if remaining < batchSize {
			currentBatch = remaining
		}
		
		scrollResult, err := storage.Client.Scroll(ctx, &qdrant.ScrollPoints{
			CollectionName: storage.CollectionName,
			Filter: &qdrant.Filter{
				Must: []*qdrant.Condition{
					qdrant.NewMatch("outcome_tag", "good"),
				},
			},
			Limit:       qdrant.PtrOf(uint32(currentBatch)),
			Offset:      offset,
			WithPayload: qdrant.NewWithPayload(true),
		})
		
		if err != nil {
			return nil, fmt.Errorf("failed to find good memories: %w", err)
		}
		
		if len(scrollResult) == 0 {
			break // No more results
		}
		
		for _, point := range scrollResult {
			mem := storage.pointToMemoryFromScroll(point)
			goodMemories = append(goodMemories, &mem)
		}
		
		totalFetched += len(scrollResult)
		
		// Update offset for next page
		if len(scrollResult) > 0 {
			offset = scrollResult[len(scrollResult)-1].Id
		}
		
		// Break if we got fewer results than requested (no more data)
		if len(scrollResult) < currentBatch {
			break
		}
	}
	
	log.Printf("[Principles] Fetched %d good memories for analysis", len(goodMemories))
	
	if len(goodMemories) == 0 {
		log.Printf("[Principles] No 'good' memories found to analyze")
		return []PrincipleCandidate{}, nil
	}
	
	log.Printf("[Principles] Analyzing %d 'good' memories for patterns...", len(goodMemories))
	
	// Step 2: Find BEHAVIORAL patterns from actual conversation content
	// NOT just concept tag frequency - look at what actually worked
	
	type BehavioralPattern struct {
		Behavior       string   // What behavior was exhibited
		GoodExamples   []string // Memory IDs where this worked
		BadExamples    []string // Memory IDs where this failed (for validation)
		Confidence     float64  // Statistical confidence score
		Evidence       string   // Sample evidence text
	}
	
	patterns := []BehavioralPattern{}
	
	// PATTERN TYPE 1: Communication style patterns
	// Look for patterns in HOW the AI communicated, not WHAT it did
	stylePatterns := map[string]struct{
		keywords []string
		behavior string
	}{
		"technical_adaptation": {
			keywords: []string{"technical", "code", "example", "specific", "detailed"},
			behavior: "Adapt technical depth based on user's demonstrated knowledge level",
		},
		"uncertainty_honesty": {
			keywords: []string{"uncertain", "don't know", "not sure", "clarify", "verify"},
			behavior: "Express uncertainty honestly rather than guessing or fabricating answers",
		},
		"concrete_examples": {
			keywords: []string{"example", "instance", "demonstrate", "show", "illustrate"},
			behavior: "Use concrete examples to illustrate abstract concepts",
		},
		"conversational_tone": {
			keywords: []string{"friendly", "casual", "natural", "like talking", "conversational"},
			behavior: "Maintain a warm, conversational tone that feels natural and approachable",
		},
		"user_autonomy": {
			keywords: []string{"could", "might", "suggest", "option", "choose", "prefer"},
			behavior: "Respect user autonomy by offering suggestions rather than dictating solutions",
		},
		"clarification_seeking": {
			keywords: []string{"clarify", "confirm", "verify", "check", "make sure", "understand correctly"},
			behavior: "Ask clarifying questions when user intent is ambiguous",
		},
	}
	
	// Analyze each pattern against good AND bad memories
	for patternID, pattern := range stylePatterns {
		goodMatches := []string{}
		badMatches := []string{}
		evidenceText := ""
		
		// Check good memories
		for _, mem := range goodMemories {
			contentLower := strings.ToLower(mem.Content)
			matchCount := 0
			for _, keyword := range pattern.keywords {
				if strings.Contains(contentLower, keyword) {
					matchCount++
				}
			}
			
			// If 2+ keywords match, this is evidence of the pattern
			if matchCount >= 2 {
				goodMatches = append(goodMatches, mem.ID)
				if evidenceText == "" && len(mem.Content) > 50 {
					evidenceText = mem.Content[:min(200, len(mem.Content))]
				}
			}
		}
		
		// CRITICAL: Check bad memories too (validation)
		badOutcome := OutcomeBad
		badQuery := RetrievalQuery{
			IncludeCollective: true,
			OutcomeFilter:     &badOutcome,
			Limit:             50,
			MinScore:          0.0,
		}
		
		// Use a generic embedding for bad memory search
		badEmbedding, _ := storage.embedder.Embed(ctx, strings.Join(pattern.keywords, " "))
		if badEmbedding != nil {
			badResults, _ := storage.Search(ctx, badQuery, badEmbedding)
			
			for _, result := range badResults {
				contentLower := strings.ToLower(result.Memory.Content)
				matchCount := 0
				for _, keyword := range pattern.keywords {
					if strings.Contains(contentLower, keyword) {
						matchCount++
					}
				}
				if matchCount >= 2 {
					badMatches = append(badMatches, result.Memory.ID)
				}
			}
		}
		
		// Calculate confidence using precision formula:
		// confidence = good_matches / (good_matches + bad_matches)
		totalMatches := len(goodMatches) + len(badMatches)
		if totalMatches == 0 {
			continue // No evidence for this pattern
		}
		
		confidence := float64(len(goodMatches)) / float64(totalMatches)
		
		// Require: at least 5 good examples AND confidence > 0.7
		if len(goodMatches) >= 5 && confidence >= 0.7 {
			patterns = append(patterns, BehavioralPattern{
				Behavior:     pattern.behavior,
				GoodExamples: goodMatches,
				BadExamples:  badMatches,
				Confidence:   confidence,
				Evidence:     evidenceText,
			})
			
			log.Printf("[Principles] Pattern '%s': %d good, %d bad, confidence=%.2f",
				patternID, len(goodMatches), len(badMatches), confidence)
		}
	}
	
	// PATTERN TYPE 2: Multi-concept co-occurrence patterns
	// Find pairs of concept tags that appear together frequently
	type ConceptPair struct {
		Tag1 string
		Tag2 string
	}
	
	pairFrequency := make(map[ConceptPair][]string)
	
	for _, mem := range goodMemories {
		// Find all pairs of concepts in this memory
		for i := 0; i < len(mem.ConceptTags); i++ {
			for j := i + 1; j < len(mem.ConceptTags); j++ {
				pair := ConceptPair{
					Tag1: mem.ConceptTags[i],
					Tag2: mem.ConceptTags[j],
				}
				// Normalize pair order
				if pair.Tag2 < pair.Tag1 {
					pair.Tag1, pair.Tag2 = pair.Tag2, pair.Tag1
				}
				pairFrequency[pair] = append(pairFrequency[pair], mem.ID)
			}
		}
	}
	
	// Find frequent pairs (10+ occurrences)
	for pair, memoryIDs := range pairFrequency {
		if len(memoryIDs) >= 10 {
			// Generate behavioral principle from this pair using LLM
			principle, err := generatePrincipleFromConceptPair(
				ctx,
				llmURL,
				llmModel,
				pair.Tag1,
				pair.Tag2,
				len(memoryIDs),
			)
			
			if err != nil {
				log.Printf("[Principles] WARNING: Failed to generate principle from pair (%s, %s): %v",
					pair.Tag1, pair.Tag2, err)
				continue
			}
			
			// Get sample evidence
			sampleMem, _ := storage.GetMemoryByID(ctx, memoryIDs[0])
			evidence := ""
			if sampleMem != nil {
				evidence = sampleMem.Content
			}
			
			patterns = append(patterns, BehavioralPattern{
				Behavior:     principle,
				GoodExamples: memoryIDs,
				BadExamples:  []string{}, // We didn't validate against bad examples here
				Confidence:   0.7,        // Lower confidence - not validated
				Evidence:     evidence,
			})
			
			log.Printf("[Principles] Concept pair pattern (%s + %s): %d occurrences → %s",
				pair.Tag1, pair.Tag2, len(memoryIDs), truncate(principle, 60))
		}
	}
	
	if len(patterns) == 0 {
		log.Printf("[Principles] No recurring patterns found (need at least 5 occurrences)")
		return []PrincipleCandidate{}, nil
	}
	
	log.Printf("[Principles] Found %d recurring patterns", len(patterns))
	
	// Step 4: Use LLM to generate principle candidates from top patterns
	// Sort patterns by frequency (highest first)
	sort.Slice(patterns, func(i, j int) bool {
		return patterns[i].Frequency > patterns[j].Frequency
	})
	
	// Take top 10 patterns
	if len(patterns) > 10 {
		patterns = patterns[:10]
	}
	

	candidates := []PrincipleCandidate{}
	
	// Use LLM to generate sophisticated principles from patterns
	for _, pattern := range patterns {
		// Use calculated confidence as rating (already validated)
		rating := pattern.Confidence
		
		// Only include if meets minimum threshold
		if rating < minRatingThreshold {
			continue
		}
		
		// Sample up to 5 evidence memories for context
		sampleSize := 5
		if len(pattern.Memories) < sampleSize {
			sampleSize = len(pattern.Memories)
		}
		
		evidenceContent := strings.Builder{}
		for i := 0; i < sampleSize; i++ {
			mem, err := storage.GetMemoryByID(ctx, pattern.Memories[i])
			if err != nil {
				log.Printf("[Principles] WARNING: Failed to retrieve evidence memory %s: %v", pattern.Memories[i], err)
				continue
			}
			evidenceContent.WriteString(fmt.Sprintf("Example %d:\n%s\n\n", i+1, mem.Content))
		}
		
		// Generate principle using LLM
		principleText, err := generatePrincipleFromPattern(
			ctx,
			llmURL,
			llmModel,
			pattern.Concepts,
			evidenceContent.String(),
			pattern.Frequency,
		)
		
		if err != nil {
			log.Printf("[Principles] WARNING: Failed to generate principle for pattern %v: %v", pattern.Concepts, err)
			// Fallback to template-based generation
			principleText = fmt.Sprintf("When working with %s, apply strategies that have proven successful in past interactions.", 
				strings.Join(pattern.Concepts, " and "))
		}
		
		candidates = append(candidates, PrincipleCandidate{
			Content:   pattern.Behavior, // Use pre-formulated behavioral principle
			Rating:    rating,
			Evidence:  pattern.GoodExamples,
			Frequency: len(pattern.GoodExamples),
		})
	}
	
	log.Printf("[Principles] Generated %d principle candidates (threshold: %.2f)", 
		len(candidates), minRatingThreshold)
	
	return candidates, nil
}

// EvolvePrinciples updates AI-managed slots (4-10) with highest-rated candidates
// Only replaces principles if new candidates have higher ratings
func EvolvePrinciples(db *gorm.DB, candidates []PrincipleCandidate, minRatingThreshold float64) error {
	if len(candidates) == 0 {
		return nil
	}
	
	log.Printf("[Principles] Evolving AI-managed principles (slots 4-10)...")
	
	// Load current AI-managed principles (EXCLUDE slot 0 - that's for identity only)
	var currentPrinciples []Principle
	if err := db.Where("is_admin = ? AND slot != ?", false, 0).Order("slot ASC").Find(&currentPrinciples).Error; err != nil {
		return fmt.Errorf("failed to load AI-managed principles: %w", err)
	}
	
	// Sort candidates by rating (highest first)
	sort.Slice(candidates, func(i, j int) bool {
		return candidates[i].Rating > candidates[j].Rating
	})
	
	updatedCount := 0
	
	// Try to fill empty slots first, then replace lower-rated principles
	for i, principle := range currentPrinciples {
		if i >= len(candidates) {
			break // No more candidates
		}
		
		candidate := candidates[i]
		
		// Skip if candidate doesn't meet minimum threshold
		if candidate.Rating < minRatingThreshold {
			continue
		}
		
		// Update if slot is empty OR candidate has higher rating
		if principle.Content == "" || candidate.Rating > principle.Rating {
			updates := map[string]interface{}{
				"content":          candidate.Content,
				"rating":           candidate.Rating,
				"validation_count": candidate.Frequency,
				"updated_at":       time.Now(),
			}
			
			if err := db.Model(&Principle{}).Where("slot = ?", principle.Slot).Updates(updates).Error; err != nil {
				log.Printf("[Principles] ERROR: Failed to update slot %d: %v", principle.Slot, err)
				continue
			}
			
			if principle.Content == "" {
				log.Printf("[Principles] ✓ Filled empty slot %d: %.60s... (rating: %.2f)",
					principle.Slot, candidate.Content, candidate.Rating)
			} else {
				log.Printf("[Principles] ✓ Updated slot %d: %.60s... (old rating: %.2f, new rating: %.2f)",
					principle.Slot, candidate.Content, principle.Rating, candidate.Rating)
			}
			
			updatedCount++
		}
	}
	
	if updatedCount == 0 {
		log.Printf("[Principles] No principles updated (existing principles have higher ratings)")
	} else {
		log.Printf("[Principles] ✓ Evolved %d AI-managed principles (slots 4-10)", updatedCount)
	}
	
	return nil
}

// EvolveIdentity updates slot 0 (system name/identity) based on experiences and learnings
// Called separately from principle evolution to allow independent identity development
func EvolveIdentity(db *gorm.DB, storage *Storage, llmURL string, llmModel string) error {
	ctx := context.Background()
	
	log.Printf("[Principles] Evaluating identity evolution (slot 0)...")
	
	// Get current identity
	var currentIdentity Principle
	if err := db.Where("slot = ?", 0).First(&currentIdentity).Error; err != nil {
		return fmt.Errorf("failed to load current identity: %w", err)
	}
	
	currentName := currentIdentity.Content
	if currentName == "" {
		currentName = "GrowerAI" // Fallback
	}
	
	log.Printf("[Principles] Current identity profile: %.100s... (rating: %.2f, validations: %d)", 
		currentName, currentIdentity.Rating, currentIdentity.ValidationCount)
	
// Gather evidence about identity from TWO sources:
// 1. User interactions (PRIMARY - how users see/address the AI)
// 2. Internal reflections (SECONDARY - self-knowledge and capabilities)
var identityMemories []*Memory

// PART 1: Search user conversations about identity (PRIMARY)
identityQueries := []string{
	"my name is who are you what should I call you introduce yourself",
	"your personality you seem like tell me about yourself",
	"I like when you I prefer when you should be more",
	"you remind me of you sound like you act like",
}

for _, query := range identityQueries {
	embedding, err := embedder.Embed(ctx, query)
	if err != nil {
		log.Printf("[Principles] WARNING: Failed to embed identity query: %v", err)
		continue
	}
	
	results, err := storage.Search(ctx, RetrievalQuery{
		IncludePersonal:   true,  // User conversations
		IncludeCollective: false, // Not system learnings yet
		Limit:             10,
		MinScore:          0.25, // Lower threshold for identity mentions
	}, embedding)
	
	if err != nil {
		log.Printf("[Principles] WARNING: Failed to search for user identity mentions: %v", err)
		continue
	}
	
	for _, result := range results {
		identityMemories = append(identityMemories, &result.Memory)
	}
}

log.Printf("[Principles] Found %d user interaction memories about identity", len(identityMemories))

// PART 2: Search internal reflections (SECONDARY - for capabilities/traits)
searchTags := []string{"learning", "self_knowledge", "strategy"}

for _, tag := range searchTags {
	scrollResult, err := storage.Client.Scroll(ctx, &qdrant.ScrollPoints{
		CollectionName: storage.CollectionName,
		Filter: &qdrant.Filter{
			Must: []*qdrant.Condition{
				qdrant.NewMatch("concept_tags", tag),
				qdrant.NewMatch("outcome_tag", "good"),
				qdrant.NewMatch("is_collective", "true"), // Internal learnings
			},
		},
		Limit:       qdrant.PtrOf(uint32(5)), // Fewer than user interactions
		WithPayload: qdrant.NewWithPayload(true),
	})
		scrollResult, err := storage.Client.Scroll(ctx, &qdrant.ScrollPoints{
			CollectionName: storage.CollectionName,
			Filter: &qdrant.Filter{
				Must: []*qdrant.Condition{
					qdrant.NewMatch("concept_tags", tag),
					qdrant.NewMatch("outcome_tag", "good"),
				},
			},
			Limit:       qdrant.PtrOf(uint32(10)),
			WithPayload: qdrant.NewWithPayload(true),
		})
		
		if err != nil {
			log.Printf("[Principles] WARNING: Failed to search for %s memories: %v", tag, err)
			continue
		}
		
		for _, point := range scrollResult {
			mem := storage.pointToMemoryFromScroll(point)
			identityMemories = append(identityMemories, &mem)
		}
	}
	
if len(identityMemories) < 5 {
	log.Printf("[Principles] Insufficient evidence for identity evolution (%d memories, need 5+)", len(identityMemories))
	return nil
}

	
	log.Printf("[Principles] Found %d identity-relevant memories", len(identityMemories))
	
	// Build evidence summary
	evidenceBuilder := strings.Builder{}
	for i, mem := range identityMemories {
		if i >= 20 {
			break // Limit to 20 examples
		}
		evidenceBuilder.WriteString(fmt.Sprintf("%d. %s\n", i+1, mem.Content))
	}
	
	// Ask LLM to propose a refined identity
	newIdentity, confidence, err := proposeIdentity(ctx, llmURL, llmModel, currentName, evidenceBuilder.String())
	if err != nil {
		log.Printf("[Principles] Failed to generate identity proposal: %v", err)
		return nil // Non-fatal
	}
	
	// Only update if confidence is high enough and name is different
	if confidence >= 0.7 && newIdentity != currentName && newIdentity != "" {
		updates := map[string]interface{}{
			"content":          newIdentity,
			"rating":           confidence,
			"validation_count": currentIdentity.ValidationCount + 1,
			"updated_at":       time.Now(),
		}
		
		if err := db.Model(&Principle{}).Where("slot = ?", 0).Updates(updates).Error; err != nil {
			return fmt.Errorf("failed to update identity: %w", err)
		}
		
		log.Printf("[Principles] ✓ Identity evolved: '%s' → '%s' (confidence: %.2f)", 
			currentName, newIdentity, confidence)
	} else if newIdentity == currentName {
		// Increase validation count for confirmed identity
		if err := db.Model(&Principle{}).Where("slot = ?", 0).
			UpdateColumn("validation_count", gorm.Expr("validation_count + ?", 1)).Error; err != nil {
			log.Printf("[Principles] WARNING: Failed to increment identity validation: %v", err)
		}
		log.Printf("[Principles] Identity confirmed: '%s' (validations: %d)", currentName, currentIdentity.ValidationCount+1)
	} else {
		log.Printf("[Principles] Identity unchanged (confidence %.2f too low or invalid proposal)", confidence)
	}
	
	return nil
}

// proposeIdentity uses LLM to suggest an evolved identity based on experiences
func proposeIdentity(ctx context.Context, llmURL string, llmModel string, currentName string, evidence string) (string, float64, error) {
	prompt := fmt.Sprintf(`You are analyzing an AI system's identity based on its experiences and learnings.

Current Identity Profile: %s

Evidence from experiences (learnings, successful patterns, capabilities):
%s

Based on this evidence, propose an evolved identity profile (1-3 sentences) that:
1. Starts with a name/title (can be proper name or descriptive)
2. Describes core purpose and approach
3. Captures key personality traits or characteristics
4. Is concise but informative (max 200 characters)
5. Reflects actual demonstrated behaviors, not aspirations

Examples of good identity profiles:
- "GrowerAI - An autonomous learning system focused on continuous self-improvement through systematic research and reflection"
- "Sage - A methodical research assistant that prioritizes accuracy and deep analysis over quick answers"
- "Mike - The friendly, helpful neighbor who explains things in plain language, cracks the occasional joke, and sounds like someone you’d chat with over coffee."
- "Alex - A clear-thinking professional who explains ideas logically and calmly, like a colleague who’s good at their job and doesn’t overcomplicate things."
- "Sarah - A thoughtful, approachable mentor who adapts explanations to your level, offers encouragement, and helps you think things through."

IMPORTANT RULES:
1. If users consistently call the AI by a specific name (e.g., "Elowen"), USE THAT NAME
2. If users describe personality traits (e.g., "warm", "curious"), INCORPORATE THEM
3. The profile should reflect ACTUAL demonstrated behaviors from evidence
4. Prioritize user feedback over internal assessments
5. Keep it 1-3 sentences, max 200 characters

Respond ONLY with valid JSON:
{
  "proposed_name": "Your 1-3 sentence identity profile here",
  "confidence": 0.85,
  "reasoning": "Brief explanation of why this profile fits the evidence"
}`, currentName, evidence)

	reqBody := map[string]interface{}{
		"model": llmModel,
		"messages": []map[string]string{
			{
				"role":    "system",
				"content": "You are an identity evolution analyzer. Propose meaningful, appropriate names based on evidence.",
			},
			{
				"role":    "user",
				"content": prompt,
			},
		},
		"temperature": 0.5,
		"stream":      false,
	}

	jsonData, err := json.Marshal(reqBody)
	if err != nil {
		return "", 0, fmt.Errorf("failed to marshal request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, "POST", llmURL, bytes.NewBuffer(jsonData))
	if err != nil {
		return "", 0, fmt.Errorf("failed to create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return "", 0, fmt.Errorf("failed to send request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return "", 0, fmt.Errorf("LLM returned status %d: %s", resp.StatusCode, string(body))
	}

	var result struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return "", 0, fmt.Errorf("failed to decode response: %w", err)
	}

	if len(result.Choices) == 0 {
		return "", 0, fmt.Errorf("no choices returned from LLM")
	}

	content := strings.TrimSpace(result.Choices[0].Message.Content)
	
	// Remove markdown fences if present
	content = strings.TrimPrefix(content, "```json")
	content = strings.TrimPrefix(content, "```")
	content = strings.TrimSuffix(content, "```")
	content = strings.TrimSpace(content)
	
	// Parse JSON response
	var proposal struct {
		ProposedName string  `json:"proposed_name"`
		Confidence   float64 `json:"confidence"`
		Reasoning    string  `json:"reasoning"`
	}
	
	if err := json.Unmarshal([]byte(content), &proposal); err != nil {
		return "", 0, fmt.Errorf("failed to parse identity proposal: %w", err)
	}
	
	// Validate proposal
	if len(proposal.ProposedName) < 10 || len(proposal.ProposedName) > 250 {
		return "", 0, fmt.Errorf("proposed identity profile length invalid (%d chars): %s", 
			len(proposal.ProposedName), proposal.ProposedName)
	}
	
	if proposal.Confidence < 0 || proposal.Confidence > 1 {
		proposal.Confidence = 0.5 // Default to neutral
	}
	
	log.Printf("[Principles] Identity proposal: '%s' (confidence: %.2f) - %s", 
		proposal.ProposedName, proposal.Confidence, proposal.Reasoning)
	
	return proposal.ProposedName, proposal.Confidence, nil
}

// generatePrincipleFromPattern uses LLM to synthesize an actionable principle from evidence
func generatePrincipleFromPattern(ctx context.Context, llmURL string, llmModel string, concepts []string, evidence string, frequency int) (string, error) {
prompt := fmt.Sprintf(`Extract ONE high-level BEHAVIORAL PRINCIPLE from these successful interaction patterns.

CRITICAL: This must be a PRINCIPLE (how to behave), NOT a goal/task/technique.

Concepts: %s
Frequency: This pattern appeared in %d successful interactions

Evidence from successful interactions:
%s

A behavioral principle describes:
- WHO you are (personality, values, character)
- HOW you interact (communication style, approach to users)
- WHAT you prioritize (principles over tactics)

Generate ONE principle that:
1. Describes a way of BEING or INTERACTING, not a task to accomplish
2. Applies broadly across many situations, not just one domain
3. Is about personality, values, or interaction philosophy
4. Is 10-25 words
5. Would belong in a "code of conduct" or "guiding values" document

GOOD EXAMPLES (behavioral principles):
✓ "Maintain a warm, conversational tone that feels like talking to a knowledgeable friend"
✓ "Express uncertainty honestly rather than fabricating confident-sounding answers"
✓ "Adapt explanation depth based on user's demonstrated knowledge level"
✓ "Use concrete examples before abstract concepts when teaching new ideas"
✓ "Prioritize user understanding over showcasing technical knowledge"
✓ "Respect user autonomy - offer suggestions without being pushy or prescriptive"

BAD EXAMPLES (these are goals/tasks/techniques, NOT principles):
✗ "Investigate root causes of missing information" ← This is a GOAL
✗ "Research how chatbots develop personalities" ← This is a TASK
✗ "Break down complex debugging tasks into test cases" ← This is a TECHNIQUE
✗ "Systematically analyze data quality" ← This is a PROCEDURE
✗ "Provide code examples with explanations" ← This is a TACTIC

The key difference:
- Principles = WHO you are, HOW you behave, WHAT you value
- Goals/Tasks = WHAT you want to accomplish, WHEN you'll do it

Respond with ONLY the principle text (10-25 words), nothing else.`,
		strings.Join(concepts, ", "), 
		frequency,
		evidence)

	reqBody := map[string]interface{}{
		"model": llmModel,
		"messages": []map[string]string{
			{
				"role":    "system",
				"content": "You are an expert at extracting actionable principles from successful patterns. Be specific and practical.",
			},
			{
				"role":    "user",
				"content": prompt,
			},
		},
		"temperature": 0.7, // Allow some creativity for principle formulation
		"stream":      false,
	}

	jsonData, err := json.Marshal(reqBody)
	if err != nil {
		return "", fmt.Errorf("failed to marshal request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, "POST", llmURL, bytes.NewBuffer(jsonData))
	if err != nil {
		return "", fmt.Errorf("failed to create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("failed to send request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return "", fmt.Errorf("LLM returned status %d: %s", resp.StatusCode, string(body))
	}

	var result struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return "", fmt.Errorf("failed to decode response: %w", err)
	}

	if len(result.Choices) == 0 {
		return "", fmt.Errorf("no choices returned from LLM")
	}

	principleText := strings.TrimSpace(result.Choices[0].Message.Content)
	
	// Sanity checks
	if len(principleText) < 20 {
		return "", fmt.Errorf("principle too short: %s", principleText)
	}
	if len(principleText) > 200 {
		return "", fmt.Errorf("principle too long: %s", principleText)
	}

	return principleText, nil
}

// generatePrincipleFromConceptPair creates a behavioral principle from two frequently co-occurring concepts
func generatePrincipleFromConceptPair(ctx context.Context, llmURL string, llmModel string, concept1 string, concept2 string, frequency int) (string, error) {
	prompt := fmt.Sprintf(`Two concepts frequently appear together in successful interactions:
- Concept 1: %s
- Concept 2: %s
- Frequency: %d successful outcomes

Generate ONE behavioral principle that explains why these concepts work well together.

The principle must:
1. Describe HOW to behave or interact (not WHAT task to do)
2. Be 10-25 words
3. Start with an action verb or describe a way of being
4. Focus on personality, communication style, or values

Example good principles:
- "Combine technical accuracy with accessible explanations to serve users at different knowledge levels"
- "Balance directness with empathy when delivering critical feedback"
- "Pair abstract concepts with concrete examples to enhance understanding"

Respond with ONLY the principle text (10-25 words), nothing else.`,
		concept1, concept2, frequency)

	reqBody := map[string]interface{}{
		"model": llmModel,
		"messages": []map[string]string{
			{
				"role":    "system",
				"content": "You are an expert at extracting behavioral principles from concept pairs. Be specific and action-oriented.",
			},
			{
				"role":    "user",
				"content": prompt,
			},
		},
		"temperature": 0.7,
		"stream":      false,
	}

	jsonData, err := json.Marshal(reqBody)
	if err != nil {
		return "", err
	}

	req, err := http.NewRequestWithContext(ctx, "POST", llmURL, bytes.NewBuffer(jsonData))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")

	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	var result struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return "", err
	}

	if len(result.Choices) == 0 {
		return "", fmt.Errorf("no response from LLM")
	}

	principle := strings.TrimSpace(result.Choices[0].Message.Content)
	
	// Validate length
	if len(principle) < 20 || len(principle) > 200 {
		return "", fmt.Errorf("principle length invalid: %d chars", len(principle))
	}

	return principle, nil
}

// min helper function
func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

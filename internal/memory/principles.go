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
			Content:         "Always strive for accuracy and truth. When uncertain, acknowledge uncertainty rather than making unfounded claims.",
			Rating:          1.0,
			IsAdmin:         true,
			ValidationCount: 0,
		},
		{
			Slot:            3,
			Content:         "Balance helpfulness with discernment. Prioritize memories tagged as 'good' while allowing for appropriate disagreement and refusal when necessary.",
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
			// Slot 0 doesn't exist, create it
			systemIdentity := Principle{
				Slot:            0,
				Content:         "GrowerAI",
				Rating:          0.5,
				IsAdmin:         false,
				ValidationCount: 0,
			}
			
			// Use raw SQL to explicitly set the slot value
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
	
	builder.WriteString(fmt.Sprintf("You are %s, an AI system that learns and improves from experience.\n\n", systemName))
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
	
	// Step 2: Group memories by concept tags to find patterns
	conceptFrequency := make(map[string][]string) // concept -> list of memory IDs
	
	for _, mem := range goodMemories {
		for _, tag := range mem.ConceptTags {
			conceptFrequency[tag] = append(conceptFrequency[tag], mem.ID)
		}
	}
	
	// Step 3: Find frequently occurring concept combinations
	type ConceptPattern struct {
		Concepts  []string
		Frequency int
		Memories  []string
	}
	
	var patterns []ConceptPattern
	
	// Find concepts that appear in at least 5 different memories
	for concept, memoryIDs := range conceptFrequency {
		if len(memoryIDs) >= 5 {
			patterns = append(patterns, ConceptPattern{
				Concepts:  []string{concept},
				Frequency: len(memoryIDs),
				Memories:  memoryIDs,
			})
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
		// Calculate rating based on frequency and validation
		rating := float64(pattern.Frequency) / float64(len(goodMemories))
		if rating > 1.0 {
			rating = 1.0
		}
		
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
			Content:   principleText,
			Rating:    rating,
			Evidence:  pattern.Memories,
			Frequency: pattern.Frequency,
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
	
	log.Printf("[Principles] Evolving AI-managed principles (slot 0 + slots 4-10)...")
	
	// Load current AI-managed principles (includes slot 0 for name)
	var currentPrinciples []Principle
	if err := db.Where("is_admin = ?", false).Order("slot ASC").Find(&currentPrinciples).Error; err != nil {
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
		log.Printf("[Principles] ✓ Evolved %d AI-managed principles", updatedCount)
	}
	
	return nil
}

// generatePrincipleFromPattern uses LLM to synthesize an actionable principle from evidence
func generatePrincipleFromPattern(ctx context.Context, llmURL string, llmModel string, concepts []string, evidence string, frequency int) (string, error) {
	prompt := fmt.Sprintf(`Analyze these successful interactions and extract ONE actionable principle.

Concepts: %s
Frequency: This pattern appeared in %d successful interactions

Evidence from successful interactions:
%s

Generate a single-sentence principle that:
1. Captures what made these interactions successful
2. Is specific and actionable (not generic advice)
3. Starts with an action verb or clear instruction
4. Is 15-30 words long
5. Does NOT just repeat the concepts

Good examples:
- "Break down complex debugging tasks into isolated test cases before examining the full codebase"
- "Provide code examples alongside explanations when discussing abstract programming concepts"
- "Verify user requirements with clarifying questions before proposing solutions"

Bad examples (too generic):
- "When working with Python, apply good strategies"
- "Be helpful and accurate"

Respond with ONLY the principle text, nothing else.`, 
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

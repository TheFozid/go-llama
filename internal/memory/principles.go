// internal/memory/principles.go
package memory

import (
	"fmt"
	"strings"
	"time"

	"gorm.io/gorm"
)

// Principle represents one of the 10 Commandments that guide GrowerAI's behavior
type Principle struct {
	Slot            int       `gorm:"primaryKey" json:"slot"`              // 1-10
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

	// Insert all principles
	allPrinciples := append(adminPrinciples, aiPrinciples...)
	if err := db.Create(&allPrinciples).Error; err != nil {
		return fmt.Errorf("failed to create default principles: %w", err)
	}

	return nil
}

// LoadPrinciples retrieves all 10 principles from the database, ordered by slot
func LoadPrinciples(db *gorm.DB) ([]Principle, error) {
	var principles []Principle
	if err := db.Order("slot ASC").Find(&principles).Error; err != nil {
		return nil, fmt.Errorf("failed to load principles: %w", err)
	}

	// Ensure we have exactly 10 principles
	if len(principles) != 10 {
		return nil, fmt.Errorf("expected 10 principles, found %d", len(principles))
	}

	return principles, nil
}

// FormatAsSystemPrompt converts the 10 Commandments into a system prompt for the LLM
// Injects dynamic config values (e.g., good behavior bias percentage)
func FormatAsSystemPrompt(principles []Principle, goodBehaviorBias float64) string {
	var builder strings.Builder

	builder.WriteString("You are GrowerAI, an AI system that learns and improves from experience.\n\n")
	builder.WriteString("=== YOUR CORE PRINCIPLES ===\n")
	builder.WriteString("These principles guide all your responses and decisions:\n\n")

	for _, p := range principles {
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
	if slot < 1 || slot > 10 {
		return fmt.Errorf("invalid slot number: %d (must be 1-10)", slot)
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
	if slot < 1 || slot > 10 {
		return fmt.Errorf("invalid slot number: %d (must be 1-10)", slot)
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
// This is called by the background worker (future implementation)
func ExtractPrinciples(db *gorm.DB, storage *Storage, minRatingThreshold float64) ([]PrincipleCandidate, error) {
	// TODO: Phase 4C - Background worker implementation
	// This function will:
	// 1. Analyze memories tagged as "good" across all users
	// 2. Identify repeated patterns and successful strategies
	// 3. Propose new principles based on validated patterns
	// 4. Return candidates sorted by rating (highest first)
	
	// For now, return empty - will implement in worker phase
	return []PrincipleCandidate{}, nil
}

// EvolvePrinciples updates AI-managed slots (4-10) with highest-rated candidates
// Only replaces principles if new candidates have higher ratings
func EvolvePrinciples(db *gorm.DB, candidates []PrincipleCandidate, minRatingThreshold float64) error {
	// TODO: Phase 4C - Background worker implementation
	// This function will:
	// 1. Load current AI-managed principles (slots 4-10)
	// 2. Compare with candidates
	// 3. Replace lower-rated principles with higher-rated candidates
	// 4. Ensure rating >= minRatingThreshold
	
	// For now, no-op - will implement in worker phase
	return nil
}

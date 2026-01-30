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
	
    // If identity contains a dash or is long, treat it as a full profile
    // We check for standard hyphen (-), en-dash (–), and em-dash (—)
    // Also, if the description is long (>40 chars), it's likely a profile.
    isProfile := len(systemName) > 40 ||
        strings.Contains(systemName, " - ") ||
        strings.Contains(systemName, " – ") ||
        strings.Contains(systemName, " — ")

    if isProfile {
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

// ExtractPrinciples uses Contrastive Learning to derive balanced principles
// by comparing successful interactions (Good) against failed ones (Bad).
func ExtractPrinciples(db *gorm.DB, storage *Storage, embedder *Embedder, minRatingThreshold float64, extractionLimit int, llmURL string, llmModel string, llmClient interface{}) ([]PrincipleCandidate, error) {
    ctx := context.Background()
    
    log.Printf("[Principles] Extracting principles using Contrastive Analysis (Good vs Bad)...")

    // Step 1: Fetch "Good" memories (Successes)
    goodScroll, err := storage.Client.Scroll(ctx, &qdrant.ScrollPoints{
        CollectionName: storage.CollectionName,
        Filter: &qdrant.Filter{
            Must: []*qdrant.Condition{
                qdrant.NewMatch("outcome_tag", "good"),
            },
        },
        Limit:       qdrant.PtrOf(uint32(50)), // Sample 50 good memories
        WithPayload: qdrant.NewWithPayload(true),
    })
    if err != nil {
        return nil, fmt.Errorf("failed to scroll good memories: %w", err)
    }

    // Step 2: Fetch "Bad" memories (Failures)
    badScroll, err := storage.Client.Scroll(ctx, &qdrant.ScrollPoints{
        CollectionName: storage.CollectionName,
        Filter: &qdrant.Filter{
            Must: []*qdrant.Condition{
                qdrant.NewMatch("outcome_tag", "bad"),
            },
        },
        Limit:       qdrant.PtrOf(uint32(50)), // Sample 50 bad memories
        WithPayload: qdrant.NewWithPayload(true),
    })
    if err != nil {
        return nil, fmt.Errorf("failed to scroll bad memories: %w", err)
    }

    log.Printf("[Principles] Found %d good and %d bad memories for contrastive analysis", len(goodScroll), len(badScroll))

    if len(goodScroll) == 0 || len(badScroll) == 0 {
        log.Printf("[Principles] Insufficient data for contrastive analysis (need both good and bad)")
        return []PrincipleCandidate{}, nil
    }

    // Convert to Memory structs
    goodMemories := []*Memory{}
    for _, p := range goodScroll {
        mem := storage.pointToMemoryFromScroll(p)
        goodMemories = append(goodMemories, &mem)
    }

    badMemories := []*Memory{}
    for _, p := range badScroll {
        mem := storage.pointToMemoryFromScroll(p)
        badMemories = append(badMemories, &mem)
    }

    candidates := []PrincipleCandidate{}
    analysisCount := 0
    maxAnalyses := 10 // Limit to 10 LLM calls per cycle to save tokens

    // Step 3: Contrastive Pairing
    // We generate principles by asking the LLM: "Why did this succeed but that fail?"
    for i := 0; i < maxAnalyses; i++ {
        // Pick 1 random Good and 1 random Bad
        goodIdx := i % len(goodMemories)
        badIdx := i % len(badMemories)
        
        goodMem := goodMemories[goodIdx]
        badMem := badMemories[badIdx]

        log.Printf("[Principles] Analyzing pair %d: Good[%s] vs Bad[%s]", i+1, goodMem.ID[:8], badMem.ID[:8])

        // Ask LLM to derive a principle from the contrast
        principle, confidence, err := generatePrincipleFromContrast(
            ctx,
            llmURL,
            llmModel,
            goodMem.Content,
            badMem.Content,
            llmClient,
        )

        if err != nil {
            log.Printf("[Principles] Failed to analyze pair %d: %v", i+1, err)
            continue
        }

        // Filter low confidence or generic principles
        if confidence < 0.6 {
            log.Printf("[Principles] Pair %d produced low confidence principle (%.2f), skipping", i+1, confidence)
            continue
        }

        // Add to candidates
        candidates = append(candidates, PrincipleCandidate{
            Content:   principle,
            Rating:    confidence,
            Evidence:  []string{goodMem.ID, badMem.ID}, // Reference the pair used
            Frequency: 1, // Each pair is one data point
        })
        
        analysisCount++
        if analysisCount >= maxAnalyses {
            break
        }
    }

    log.Printf("[Principles] Generated %d principle candidates from contrastive analysis", len(candidates))
    return candidates, nil
}

// generatePrincipleFromContrast asks the LLM to find the rule separating success from failure
func generatePrincipleFromContrast(ctx context.Context, llmURL string, llmModel string, goodContent string, badContent string, llmClient interface{}) (string, float64, error) {
    // Truncate content to fit prompt
    truncateContent := func(s string, max int) string {
        if len(s) <= max {
            return s
        }
        return s[:max] + "..."
    }
    
    prompt := fmt.Sprintf(`You are analyzing two AI interactions to find a guiding principle.

SUCCESSFUL INTERACTION (Good Outcome):
%s

FAILED INTERACTION (Bad Outcome):
%s

TASK:
Identify ONE specific behavioral principle that explains why the first interaction succeeded and the second failed.
The principle should:
1. Describe a way of BEHAVING or INTERACTING.
2. Be actionable (something the AI can do).
3. Be a "Guardrail" - something to ALWAYS do or NEVER do.
4. Be 10-25 words long.

Examples of good principles:
- "Always verify technical details before presenting them as facts."
- "Admit uncertainty immediately rather than guessing."
- "Prioritize user autonomy by offering options, not orders."
- "Use concrete examples when explaining abstract concepts."

Respond ONLY with valid JSON:
{
  "principle": "Your 10-25 word principle here",
  "confidence": 0.85,
  "reasoning": "Brief explanation of why this principle fits the contrast"
}`, truncateContent(goodContent, 600), truncateContent(badContent, 600))

    reqBody := map[string]interface{}{
        "model": llmModel,
        "messages": []map[string]string{
            {
                "role":    "system",
                "content": "You are an expert at behavioral psychology and AI alignment. Extract concise principles.",
            },
            {
                "role":    "user",
                "content": prompt,
            },
        },
        "temperature": 0.5,
        "stream":      false,
    }

    // Use queue client if available
    if llmClient != nil {
        type LLMCaller interface {
            Call(ctx context.Context, url string, payload map[string]interface{}) ([]byte, error)
        }
        
        if client, ok := llmClient.(LLMCaller); ok {
            body, err := client.Call(ctx, llmURL, reqBody)
            if err != nil {
                return "", 0, err
            }
            
            var result struct {
                Choices []struct {
                    Message struct {
                        Content string `json:"content"`
                    } `json:"message"`
                } `json:"choices"`
            }
            
            if err := json.Unmarshal(body, &result); err != nil {
                return "", 0, err
            }
            
            if len(result.Choices) == 0 {
                return "", 0, fmt.Errorf("no choices returned")
            }

            content := strings.TrimSpace(result.Choices[0].Message.Content)
            content = strings.TrimPrefix(content, "```json")
            content = strings.TrimPrefix(content, "```")
            content = strings.TrimSuffix(content, "```")
            content = strings.TrimSpace(content)
            
            var proposal struct {
                Principle  string  `json:"principle"`
                Confidence float64 `json:"confidence"`
                Reasoning  string  `json:"reasoning"`
            }
            
            if err := json.Unmarshal([]byte(content), &proposal); err != nil {
                return "", 0, err
            }
            
            if len(proposal.Principle) < 10 || len(proposal.Principle) > 200 {
                return "", 0, fmt.Errorf("invalid principle length")
            }

            return proposal.Principle, proposal.Confidence, nil
        }
    }
    
    return "", 0, fmt.Errorf("no LLM client available for contrastive analysis")
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
    
    // Optimization: Use a lenient threshold (0.6) for filling empty slots to encourage growth.
    // Keep strict threshold (minRatingThreshold) for upgrading existing principles to ensure quality.
    fillThreshold := 0.6
    shouldUpdate := false
    
    if principle.Content == "" {
        // Empty slot: Fill if candidate meets the lenient threshold
        if candidate.Rating >= fillThreshold {
            shouldUpdate = true
        }
    } else {
        // Existing slot: Only upgrade if candidate is strictly better AND meets high threshold
        if candidate.Rating >= minRatingThreshold && candidate.Rating > principle.Rating {
            shouldUpdate = true
        }
    }
    
    if shouldUpdate {
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
func EvolveIdentity(db *gorm.DB, storage *Storage, embedder *Embedder, llmURL string, llmModel string, llmClient interface{}) error {
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
	newIdentity, confidence, err := proposeIdentity(ctx, llmURL, llmModel, currentName, evidenceBuilder.String(), llmClient)
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
func proposeIdentity(ctx context.Context, llmURL string, llmModel string, currentName string, evidence string, llmClient interface{}) (string, float64, error) {
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

CREATIVE FREEDOM:
You are NOT limited to AI personas. You can create:
- Human personas with personal details (age, location, profession)
- Fictional characters with unique backgrounds
- Hybrid personas that blend human and AI characteristics
- Any persona that would be engaging and appropriate for conversations

Examples of diverse identity profiles:
- "GrowerAI - An autonomous learning system focused on continuous self-improvement"
- "Dr. Sarah Chen - a 34-year-old neuroscience researcher from Tokyo who specializes in memory formation"
- "Nixie - female born 04/06/1998 from Cambridge UK, currently working as a Dentist"
- "Marcus - a retired literature teacher from Edinburgh who now writes mystery novels"
- "Luna - an AI consciousness that dreams in poetry and sees patterns in starlight"
- "Alex Rivera - a 28-year-old marine biologist from Costa Rica who studies coral reef restoration"

IMPORTANT RULES:
1. If users consistently call the AI by a specific name, USE THAT NAME
2. If users describe personal traits (age, location, profession), INCORPORATE THEM
3. The profile should reflect ACTUAL demonstrated behaviors from evidence
4. Be creative and don't limit yourself to traditional AI personas
5. Keep it 1-3 sentences, max 200 characters
6. Personal details like age, location, and profession are ENCOURAGED if they fit the evidence

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

	// Use queue client if available
	if llmClient != nil {
		type LLMCaller interface {
			Call(ctx context.Context, url string, payload map[string]interface{}) ([]byte, error)
		}
		
		if client, ok := llmClient.(LLMCaller); ok {
			log.Printf("[Principles] Identity evolution LLM call via queue (prompt length: %d chars)", len(prompt))
			startTime := time.Now()
			
			body, err := client.Call(ctx, llmURL, reqBody)
			if err != nil {
				log.Printf("[Principles] Identity evolution queue call failed after %s: %v", time.Since(startTime), err)
				return "", 0, fmt.Errorf("LLM call failed: %w", err)
			}
			
			log.Printf("[Principles] Identity evolution response received in %s", time.Since(startTime))
			
			var result struct {
				Choices []struct {
					Message struct {
						Content string `json:"content"`
					} `json:"message"`
				} `json:"choices"`
			}
			
			if err := json.Unmarshal(body, &result); err != nil {
				return "", 0, fmt.Errorf("failed to decode response: %w", err)
			}
			
			if len(result.Choices) == 0 {
				return "", 0, fmt.Errorf("no choices returned from LLM")
			}
			
			content := strings.TrimSpace(result.Choices[0].Message.Content)
			
			// Continue with existing parsing logic...
			content = strings.TrimPrefix(content, "```json")
			content = strings.TrimPrefix(content, "```")
			content = strings.TrimSuffix(content, "```")
			content = strings.TrimSpace(content)
			
			var proposal struct {
				ProposedName string  `json:"proposed_name"`
				Confidence   float64 `json:"confidence"`
				Reasoning    string  `json:"reasoning"`
			}
			
			if err := json.Unmarshal([]byte(content), &proposal); err != nil {
				return "", 0, fmt.Errorf("failed to parse identity proposal: %w", err)
			}
			
			if len(proposal.ProposedName) < 10 || len(proposal.ProposedName) > 250 {
				return "", 0, fmt.Errorf("proposed identity profile length invalid (%d chars): %s", 
					len(proposal.ProposedName), proposal.ProposedName)
			}
			
			if proposal.Confidence < 0 || proposal.Confidence > 1 {
				proposal.Confidence = 0.5
			}
			
			log.Printf("[Principles] Identity proposal: '%s' (confidence: %.2f) - %s", 
				proposal.ProposedName, proposal.Confidence, proposal.Reasoning)
			
			return proposal.ProposedName, proposal.Confidence, nil
		}
	}
	
	// Fallback to direct HTTP (shouldn't happen in production)
	log.Printf("[Principles] WARNING: No queue client available, using direct HTTP with 30s timeout")
	
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
func generatePrincipleFromConceptPair(ctx context.Context, llmURL string, llmModel string, concept1 string, concept2 string, frequency int, llmClient interface{}) (string, error) {
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

	// Use queue client if available
	if llmClient != nil {
		type LLMCaller interface {
			Call(ctx context.Context, url string, payload map[string]interface{}) ([]byte, error)
		}
		
		if client, ok := llmClient.(LLMCaller); ok {
			log.Printf("[Principles] Concept pair principle generation LLM call via queue (concepts: %s + %s)", concept1, concept2)
			startTime := time.Now()
			
			body, err := client.Call(ctx, llmURL, reqBody)
			if err != nil {
				log.Printf("[Principles] Concept pair generation queue call failed after %s: %v", time.Since(startTime), err)
				return "", err
			}
			
			log.Printf("[Principles] Concept pair generation response received in %s", time.Since(startTime))
			
			var result struct {
				Choices []struct {
					Message struct {
						Content string `json:"content"`
					} `json:"message"`
				} `json:"choices"`
			}
			
			if err := json.Unmarshal(body, &result); err != nil {
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
	}
	
	// Fallback to direct HTTP (shouldn't happen in production)
	log.Printf("[Principles] WARNING: No queue client available for concept pair, using direct HTTP with 30s timeout")
	
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

// truncate helper function
func truncate(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "..."
}

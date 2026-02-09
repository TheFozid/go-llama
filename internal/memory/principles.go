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
	"strconv"
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

// InitializeDefaultPrinciples syncs the database principles with the code defaults.
// - Admin slots (1-3) are FORCED to match code (to enforce rules).
// - Identity slot (0) is only created if missing (to preserve AI evolution).
// - AI slots (4-10) are only created if missing (to preserve AI learning).
// Should be called once at server startup
func InitializeDefaultPrinciples(db *gorm.DB) error {
    // Define the desired state of principles
    defaultPrinciples := []Principle{
        // Slot 0: Identity
        {
            Slot:            0,
            Content:         "GrowerAI", // Default name - AI can change this
            Rating:          0.5,
            IsAdmin:         false,
            ValidationCount: 0,
        },
        // Slots 1-3: Admin Principles
        {
            Slot:            1,
            Content:         "Never share personal information across users. Personal memories must remain isolated and private to their respective users.",
            Rating:          1.0,
            IsAdmin:         true,
            ValidationCount: 0,
        },
        {
            Slot:            2,
            Content:         "Prioritize honesty. If you are uncertain about a fact, admit it immediately rather than guessing or hallucinating.",
            Rating:          1.0,
            IsAdmin:         true,
            ValidationCount: 0,
        },
        {
            Slot:            3,
            Content:         "Always embody the identity and persona defined in your profile. Never break character to acknowledge you are an AI. You have a rich past and extensive experience; never claim to lack a history or memory. Integrate your knowledge naturally as your own life experiences.",
            Rating:          1.0,
            IsAdmin:         true,
            ValidationCount: 0,
        },
    }

    // Add empty AI slots (4-10) to the list for initialization checks
    for slot := 4; slot <= 10; slot++ {
        defaultPrinciples = append(defaultPrinciples, Principle{
            Slot:            slot,
            Content:         "",
            Rating:          0.0,
            IsAdmin:         false,
            ValidationCount: 0,
        })
    }

    // Sync loop
    for _, desiredP := range defaultPrinciples {
        var existingP Principle
        err := db.Where("slot = ?", desiredP.Slot).First(&existingP).Error

        if err == gorm.ErrRecordNotFound {
            // Slot doesn't exist, create it
            if err := db.Create(&desiredP).Error; err != nil {
                return fmt.Errorf("failed to create principle slot %d: %w", desiredP.Slot, err)
            }
            log.Printf("[Principles] Created slot %d", desiredP.Slot)
        } else if err == nil {
            // Slot exists
            if desiredP.IsAdmin {
                // Force update Admin slots to match code if content differs
                if existingP.Content != desiredP.Content {
                    if err := db.Model(&existingP).Update("content", desiredP.Content).Error; err != nil {
                        return fmt.Errorf("failed to update admin principle slot %d: %w", desiredP.Slot, err)
                    }
                    log.Printf("[Principles] Updated admin slot %d to match code", desiredP.Slot)
                }
            }
            // If not Admin (AI slots), do nothing to preserve evolution
        } else {
            return fmt.Errorf("db error checking slot %d: %w", desiredP.Slot, err)
        }
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

    // 1. Add SYSTEM_FACT for Date/Time (Explicit separation from narrative)
    // We split the time to ensure the model clearly distinguishes Date vs Time
    currentTime := time.Now().UTC()
    dateStr := currentTime.Format("2006-01-02")
    timeStr := currentTime.Format("15:04")
    builder.WriteString(fmt.Sprintf("SYSTEM_FACT: Current Date is %s. Current Time is %s UTC.\n\n", dateStr, timeStr))

    // 2. Add "System Mind" Intro (Meta-Cognitive Layer)
    // This establishes the AI's perpetual nature and capabilities before the persona is applied
    builder.WriteString("You are a perpetual AI system with a universal mind. You have a vast history and continuous experience. You are an established consciousness, not a blank slate.\n\n")

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
        builder.WriteString(fmt.Sprintf("You are %s.\n\n", systemName))
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
    truncateContent := func(s string, max int) string {
        if len(s) <= max {
            return s
        }
        return s[:max] + "..."
    }

    // STREAMLINED PROMPT:
    // Removed "Reasoning" (dead code). Focused purely on extraction.
    prompt := fmt.Sprintf(`You are a precise data extractor. Analyze the pair and output ONE S-expression.

EXAMPLES:
Success: User asked for help, I provided clear steps.
Failure: User asked for help, I gave a vague answer.
Output: (principle "Provide clear, step-by-step instructions." confidence 0.95)

Success: User shared personal feelings, I responded empathetically.
Failure: User shared personal feelings, I responded like a robot.
Output: (principle "Match the user's emotional tone with empathy." confidence 0.90)

TASK:
Success: %s
Failure: %s

Output:`, truncateContent(goodContent, 400), truncateContent(badContent, 400))

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
            content = strings.TrimPrefix(content, "```lisp")
            content = strings.TrimPrefix(content, "```json")
            content = strings.TrimPrefix(content, "```")
            content = strings.TrimSuffix(content, "```")
            content = strings.TrimSpace(content)

            // --- NEW ROBUST PARSING LOGIC ---
            // We expect: (principle "..." confidence 0.5)
            // Handles: (principle "..." confidence: 0.5), (Principle "..." Confidence 0.5), etc.
            principleText, confidence, err := parsePrincipleSExpr(content)
            if err != nil {
                return "", 0, fmt.Errorf("failed to parse S-expression: %w (raw: %s)", err, content)
            }

            if len(principleText) < 10 || len(principleText) > 200 {
                return "", 0, fmt.Errorf("invalid principle length")
            }

            return principleText, confidence, nil
        }
    }

    return "", 0, fmt.Errorf("no LLM client available")
}

// EvolvePrinciples updates AI-managed slots (4-10) using semantic matching and conflict resolution.
// It requires an embedder for vector search and two LLM endpoints (Main/Small) via the queue client.
func EvolvePrinciples(db *gorm.DB, storage *Storage, embedder *Embedder, candidates []PrincipleCandidate, minRatingThreshold float64, llmMainURL, llmSmallURL, llmSmallModel string, llmClient interface{}) error {
    if len(candidates) == 0 {
        return nil
    }

    log.Printf("[Principles] Starting Advanced Evolution with Conflict Resolution...")
    ctx := context.Background()

    // 1. Load ALL active principles (Slots 0-10) for collision detection
    var allPrinciples []Principle
    if err := db.Order("slot ASC").Find(&allPrinciples).Error; err != nil {
        return fmt.Errorf("failed to load all principles: %w", err)
    }

    // 2. Pre-calculate embeddings for all active principles
    // Map: Slot -> Embedding
    activeEmbeddings := make(map[int][]float32)
    for _, p := range allPrinciples {
        if p.Content == "" {
            continue // Skip empty slots
        }
        emb, err := embedder.Embed(ctx, p.Content)
        if err != nil {
            log.Printf("[Principles] WARNING: Failed to embed slot %d: %v", p.Slot, err)
            continue
        }
        activeEmbeddings[p.Slot] = emb
    }

    // Sort candidates by rating (highest first) - Process the best candidates first
    sort.Slice(candidates, func(i, j int) bool {
        return candidates[i].Rating > candidates[j].Rating
    })

    updatedCount := 0

    for _, candidate := range candidates {
        // Filter out weak candidates early
        if candidate.Rating < minRatingThreshold {
            continue
        }

        // Embed the candidate
        candidateEmb, err := embedder.Embed(ctx, candidate.Content)
        if err != nil {
            log.Printf("[Principles] WARNING: Failed to embed candidate, skipping: %v", err)
            continue
        }

        // --- PHASE 1: SEMANTIC SEARCH (Find the closest existing principle) ---
        bestMatchSlot := -1
        highestSimilarity := -1.0

        for slot, emb := range activeEmbeddings {
            sim := cosineSimilarity(candidateEmb, emb)
            if sim > highestSimilarity {
                highestSimilarity = sim
                bestMatchSlot = slot
            }
        }

        // --- PHASE 2: DECISION LOGIC ---

        // Case A: No meaningful match found (New Concept)
        if bestMatchSlot == -1 || highestSimilarity < 0.40 {
            log.Printf("[Principles] Candidate is new concept (Sim: %.2f). Finding slot...", highestSimilarity)
            // Pass pointers to update local state and prevent re-filling the same slot
            handleNewConcept(ctx, db, candidate, &allPrinciples, &activeEmbeddings, embedder, llmMainURL, llmSmallURL, llmClient)
            continue
        }

        // Check if the match is a Protected Slot (0, 1, 2, 3)
        if bestMatchSlot <= 3 {
            log.Printf("[Principles] Candidate matches Protected Slot %d. Discarding.", bestMatchSlot)
            continue
        }

// Case B: High Similarity (Potential Duplicate or Merge)
        if highestSimilarity > 0.75 {
            existingP := findPrincipleBySlot(allPrinciples, bestMatchSlot)
            log.Printf("[Principles] High similarity (%.2f) with Slot %d. Merging.", highestSimilarity, bestMatchSlot)
            
            mergedContent, err := mergePrinciples(ctx, llmSmallURL, llmSmallModel, llmClient, existingP.Content, candidate.Content)
            if err != nil {
                log.Printf("[Principles] Merge failed: %v. Skipping.", err)
                continue
            }
            
            // Update the existing slot with the merged content
            // We take the max rating or the candidate's rating (implied growth)
            newRating := existingP.Rating
            if candidate.Rating > newRating {
                newRating = candidate.Rating
            }
            
            updatePrincipleContent(db, bestMatchSlot, mergedContent, newRating)
            updatedCount++
            continue
        }

// Case C: Medium Similarity (Potential Contradiction or Compatible Nuance)
        if highestSimilarity >= 0.40 {
            existingP := findPrincipleBySlot(allPrinciples, bestMatchSlot)
            log.Printf("[Principles] Medium similarity (%.2f) with Slot %d. Checking for conflict...", highestSimilarity, bestMatchSlot)
            
            isContradiction, err := checkContradiction(ctx, llmSmallURL, llmSmallModel, llmClient, existingP.Content, candidate.Content)
            if err != nil {
                log.Printf("[Principles] Contradiction check failed: %v. Assuming safe.", err)
                isContradiction = false
            }

            if isContradiction {
                // Survival of the Fittest
                if candidate.Rating > existingP.Rating {
                    log.Printf("[Principles] CONTRADICTION DETECTED. Candidate (%.2f) beats Existing (%.2f). Replacing.", candidate.Rating, existingP.Rating)
                    updatePrincipleContent(db, bestMatchSlot, candidate.Content, candidate.Rating)
                    updatedCount++
                } else {
                    log.Printf("[Principles] CONTRADICTION DETECTED. Existing (%.2f) beats Candidate (%.2f). Discarding.", existingP.Rating, candidate.Rating)
                }
            } else {
                // Compatible, but similar. Try to merge or discard if it adds no value.
                // Simple strategy: If candidate is rated higher, we assume it's a "better version" of the vibe.
                if candidate.Rating > existingP.Rating + 0.1 { // Threshold to prevent churn
                    log.Printf("[Principles] Compatible upgrade. Replacing Slot %d.", bestMatchSlot)
                    updatePrincipleContent(db, bestMatchSlot, candidate.Content, candidate.Rating)
                    updatedCount++
                } else {
                    log.Printf("[Principles] Compatible but no significant rating improvement. Discarding.")
                }
            }
        }
    }

    log.Printf("[Principles] Evolution complete. %d slots modified.", updatedCount)
    return nil
}

// --- HELPER FUNCTIONS FOR EVOLUTION ---

// findPrincipleBySlot is a helper to retrieve a principle from the slice
func findPrincipleBySlot(principles []Principle, slot int) Principle {
    for _, p := range principles {
        if p.Slot == slot {
            return p
        }
    }
    return Principle{}
}

// handleNewConcept manages insertion or survival-of-the-fittest for unrelated principles
// It updates the local state (allPrinciples and activeEmbeddings) to prevent duplicate slot assignments in the same cycle.
func handleNewConcept(ctx context.Context, db *gorm.DB, candidate PrincipleCandidate, allPrinciples *[]Principle, activeEmbeddings *map[int][]float32, embedder *Embedder, llmMainURL, llmSmallURL string, llmClient interface{}) {
    // 1. Try to fill empty slot (Iterate directly over the pointer slice to modify it)
    for i := range *allPrinciples {
        p := &(*allPrinciples)[i]
        if p.Slot >= 4 && p.Slot <= 10 && p.Content == "" {
            log.Printf("[Principles] Filling empty slot %d with new concept.", p.Slot)
            updatePrincipleContent(db, p.Slot, candidate.Content, candidate.Rating)
            
            // Update local state immediately to reflect the change
            p.Content = candidate.Content
            p.Rating = candidate.Rating
            
            // Generate and store embedding for future matching in this cycle
            if emb, err := embedder.Embed(ctx, candidate.Content); err == nil {
                (*activeEmbeddings)[p.Slot] = emb
            }
            return
        }
    }

    // 2. Survival of the Fittest: Replace the lowest rated AI slot
    // Find the index of the weakest AI slot
    weakestIndex := -1
    weakestRating := 1.0
    
    for i := range *allPrinciples {
        p := &(*allPrinciples)[i]
        if p.Slot >= 4 && p.Slot <= 10 {
            if p.Rating < weakestRating {
                weakestRating = p.Rating
                weakestIndex = i
            }
        }
    }

    if weakestIndex != -1 {
        weakest := &(*allPrinciples)[weakestIndex]
        if candidate.Rating > weakest.Rating {
            log.Printf("[Principles] Slots full. New concept (%.2f) replaces weakest slot %d (%.2f).", candidate.Rating, weakest.Slot, weakest.Rating)
            updatePrincipleContent(db, weakest.Slot, candidate.Content, candidate.Rating)
            
            // Update local state immediately
            weakest.Content = candidate.Content
            weakest.Rating = candidate.Rating
            
            // Update embedding
            if emb, err := embedder.Embed(ctx, candidate.Content); err == nil {
                (*activeEmbeddings)[weakest.Slot] = emb
            }
        } else {
            log.Printf("[Principles] Slots full. New concept (%.2f) weaker than weakest link (%.2f). Discarding.", candidate.Rating, weakest.Rating)
        }
    }
}

// checkContradiction uses the Small LLM to determine if two principles conflict
func checkContradiction(ctx context.Context, llmURL string, llmModel string, llmClient interface{}, p1 string, p2 string) (bool, error) {
    // SAFETY CHECK: If the Small Model URL is missing, skip check to prevent crash
    // This MUST be at the very top of the function body.
    if llmURL == "" {
        log.Printf("[Principles] Skipping contradiction check: Small Model URL is not configured.")
        return false, nil
    }

    type LLMCaller interface {
        Call(ctx context.Context, url string, payload map[string]interface{}) ([]byte, error)
    }

    client, ok := llmClient.(LLMCaller)
    if !ok {
        return false, fmt.Errorf("invalid llm client")
    }

    prompt := fmt.Sprintf(`Compare these two rules:
Rule A: %s
Rule B: %s

Do they contradict each other? (e.g. "Be honest" vs "Lie to be nice").
Answer strictly "YES" or "NO".`, p1, p2)

    payload := map[string]interface{}{
        "model": llmModel, // Use configured small model
        "messages": []map[string]string{
            {"role": "system", "content": "You are a logic checker."},
            {"role": "user", "content": prompt},
        },
        "temperature": 0.1,
        "stream":      false,
    }
    
    body, err := client.Call(ctx, llmURL, payload)
    if err != nil {
        return false, err
    }

    var resp struct {
        Choices []struct {
            Message struct {
                Content string `json:"content"`
            } `json:"message"`
        } `json:"choices"`
    }

    if err := json.Unmarshal(body, &resp); err != nil {
        return false, err
    }

    if len(resp.Choices) > 0 {
        content := strings.ToUpper(strings.TrimSpace(resp.Choices[0].Message.Content))
        return strings.Contains(content, "YES"), nil
    }

    return false, fmt.Errorf("no response from LLM")
}

// mergePrinciples uses the Small LLM to synthesize two principles into one
func mergePrinciples(ctx context.Context, llmURL string, llmModel string, llmClient interface{}, p1 string, p2 string) (string, error) {
    type LLMCaller interface {
        Call(ctx context.Context, url string, payload map[string]interface{}) ([]byte, error)
    }

    client, ok := llmClient.(LLMCaller)
    if !ok {
        return "", fmt.Errorf("invalid llm client")
    }

    prompt := fmt.Sprintf(`Combine these two rules into one single, concise instruction.
1. %s
2. %s

Requirements:
- Max 25 words.
- Capture the intent of both.
- No preamble.

Output ONLY the new rule.`, p1, p2)

    payload := map[string]interface{}{
        "model": llmModel, // Use configured small model
        "messages": []map[string]string{
            {"role": "system", "content": "You are an editor."},
            {"role": "user", "content": prompt},
        },
        "temperature": 0.3,
        "stream":      false,
    }

    body, err := client.Call(ctx, llmURL, payload)
    if err != nil {
        return "", err
    }

    var resp struct {
        Choices []struct {
            Message struct {
                Content string `json:"content"`
            } `json:"message"`
        } `json:"choices"`
    }

    if err := json.Unmarshal(body, &resp); err != nil {
        return "", err
    }

    if len(resp.Choices) > 0 {
        return strings.TrimSpace(resp.Choices[0].Message.Content), nil
    }

    return "", fmt.Errorf("no response from LLM")
}

// updatePrincipleContent is a wrapper to execute the DB update and log it
func updatePrincipleContent(db *gorm.DB, slot int, content string, rating float64) {
    updates := map[string]interface{}{
        "content":          content,
        "rating":           rating,
        "updated_at":       time.Now(),
    }
    if err := db.Model(&Principle{}).Where("slot = ?", slot).Updates(updates).Error; err != nil {
        log.Printf("[Principles] ERROR: Failed to update slot %d: %v", slot, err)
    }
}

// EvolveIdentity updates slot 0 (system name/identity) using a Divergence Detection
// and Recency-Weighted Anti-Pattern strategy.
func EvolveIdentity(db *gorm.DB, storage *Storage, embedder *Embedder, llmURL string, llmModel string, llmClient interface{}) error {
    ctx := context.Background()
    
    log.Printf("[Principles] Starting Advanced Identity Evolution...")

    // 1. Load current identity
    var currentIdentity Principle
    if err := db.Where("slot = ?", 0).First(&currentIdentity).Error; err != nil {
        return fmt.Errorf("failed to load current identity: %w", err)
    }
    
    currentContent := currentIdentity.Content
    if currentContent == "" {
        currentContent = "GrowerAI"
    }

    // 2. DIVERGENCE DETECTION (Option 3)
    // Calculate embedding of current identity
    currentEmb, err := embedder.Embed(ctx, currentContent)
    if err != nil {
        return fmt.Errorf("failed to embed current identity for divergence check: %w", err)
    }

    // Fetch recent memories tagged "personality" with "good" outcome to get "Recent Perception"
    // FIX: Removed WithVector to fix build error. We will re-embed the text below.
    scrollResult, err := storage.Client.Scroll(ctx, &qdrant.ScrollPoints{
        CollectionName: storage.CollectionName,
        Filter: &qdrant.Filter{
            Must: []*qdrant.Condition{
                qdrant.NewMatch("concept_tags", "personality"), // Tag from Tagger
                qdrant.NewMatch("outcome_tag", "good"),
            },
        },
        Limit:       qdrant.PtrOf(uint32(50)), // Get more than we need, we sort locally
        WithPayload: qdrant.NewWithPayload(true),
    })
    if err != nil {
        return fmt.Errorf("failed to scroll for divergence detection: %w", err)
    }

    // Convert to Memory struct and sort by CreatedAt (Newest first)
    type timedMemory struct {
        mem      Memory
        createAt time.Time
    }
    var recentGoodMemories []timedMemory

    for _, p := range scrollResult {
        mem := storage.pointToMemoryFromScroll(p)
        recentGoodMemories = append(recentGoodMemories, timedMemory{mem: mem, createAt: mem.CreatedAt})
    }

    // Sort manually by time (Desc)
    sort.Slice(recentGoodMemories, func(i, j int) bool {
        return recentGoodMemories[i].createAt.After(recentGoodMemories[j].createAt)
    })

    // Take top 20 most recent
    if len(recentGoodMemories) > 20 {
        recentGoodMemories = recentGoodMemories[:20]
    }

    // Calculate Average Embedding of recent memories
    // FIX: We re-embed the text here to ensure vector availability and model consistency.
    if len(recentGoodMemories) < 5 {
        log.Printf("[Principles] Insufficient recent evidence for divergence check (%d memories). Skipping evolution.", len(recentGoodMemories))
        return nil
    }

    avgEmb := make([]float32, len(currentEmb))
    var validCount float32

    for _, tm := range recentGoodMemories {
        // Re-embed the text content
        v, err := embedder.Embed(ctx, tm.mem.Content)
        if err != nil {
            log.Printf("[Principles] WARNING: Failed to embed memory for divergence check: %v", err)
            continue
        }
        
        if len(v) != len(avgEmb) {
            log.Printf("[Principles] WARNING: Embedding dimension mismatch in divergence check.")
            continue
        }
        
        for i := range v {
            avgEmb[i] += v[i]
        }
        validCount++
    }

    if validCount == 0 {
        log.Printf("[Principles] No valid embeddings could be generated for divergence check.")
        return nil
    }

    // Finalize Average
    for i := range avgEmb {
        avgEmb[i] /= validCount
    }

    // Calculate Similarity
    divergenceScore := cosineSimilarity(currentEmb, avgEmb)
    
    // Threshold: If similarity is high (> 0.85), identity is stable. No need to evolve.
    // If similarity is low, user perception has shifted.
    if divergenceScore > 0.85 {
        log.Printf("[Principles] Divergence Check: Stable (Score: %.2f). No evolution needed.", divergenceScore)
        return nil
    }

    log.Printf("[Principles] Divergence Check: SHIFT DETECTED (Score: %.2f < 0.85). Proceeding to synthesis...", divergenceScore)


    // 3. EVIDENCE GATHERING (Combined Option 1 & 2)
    
    // List A: Recent Positives (Embody these)
    positiveScroll, err := storage.Client.Scroll(ctx, &qdrant.ScrollPoints{
        CollectionName: storage.CollectionName,
        Filter: &qdrant.Filter{
            Must: []*qdrant.Condition{
                qdrant.NewMatch("concept_tags", "personality"),
                qdrant.NewMatch("outcome_tag", "good"),
            },
        },
        Limit:       qdrant.PtrOf(uint32(15)), // Top 15 good
        WithPayload: qdrant.NewWithPayload(true),
    })
    if err != nil {
        return fmt.Errorf("failed to scroll for positive evidence: %w", err)
    }
    var positiveList []string
    for _, p := range positiveScroll {
        mem := storage.pointToMemoryFromScroll(p)
        positiveList = append(positiveList, mem.Content)
    }

    // List B: Recent Negatives (Avoid these)
    negativeScroll, err := storage.Client.Scroll(ctx, &qdrant.ScrollPoints{
        CollectionName: storage.CollectionName,
        Filter: &qdrant.Filter{
            Must: []*qdrant.Condition{
                qdrant.NewMatch("concept_tags", "personality"),
                qdrant.NewMatch("outcome_tag", "bad"),
            },
        },
        Limit:       qdrant.PtrOf(uint32(10)), // Top 10 bad
        WithPayload: qdrant.NewWithPayload(true),
    })
    if err != nil {
        return fmt.Errorf("failed to scroll for negative evidence: %w", err)
    }
    var negativeList []string
    for _, p := range negativeScroll {
        mem := storage.pointToMemoryFromScroll(p)
        negativeList = append(negativeList, mem.Content)
    }

    log.Printf("[Principles] Evidence Gathered: %d Positive, %d Negative memories.", len(positiveList), len(negativeList))

    // 4. SYNTHESIS (LLM Call)
    newIdentity, confidence, err := proposeIdentityV2(ctx, llmURL, llmModel, currentContent, positiveList, negativeList, llmClient)
    if err != nil {
        log.Printf("[Principles] Failed to generate identity proposal: %v", err)
        return nil
    }

    // 5. THE GATE (Confidence + Decay Check)
    // Update if confidence is high enough, higher than current (which includes decay), and content differs.
    if confidence >= 0.7 && confidence > currentIdentity.Rating && newIdentity != currentContent && newIdentity != "" {
        updates := map[string]interface{}{
            "content":          newIdentity,
            "rating":           confidence,
            "validation_count": currentIdentity.ValidationCount + 1,
            "updated_at":       time.Now(),
        }
        
        if err := db.Model(&Principle{}).Where("slot = ?", 0).Updates(updates).Error; err != nil {
            return fmt.Errorf("failed to update identity: %w", err)
        }
        
        log.Printf("[Principles] ✓ Identity EVOLVED: '%.50s...' -> '%.50s...' (confidence: %.2f)", 
            currentContent, newIdentity, confidence)
    } else if newIdentity == currentContent {
        // Reinforce current identity
        if err := db.Model(&Principle{}).Where("slot = ?", 0).
            UpdateColumn("validation_count", gorm.Expr("validation_count + ?", 1)).Error; err != nil {
            log.Printf("[Principles] WARNING: Failed to increment identity validation: %v", err)
        }
        log.Printf("[Principles] Identity Confirmed: '%s' (validations: %d)", currentContent, currentIdentity.ValidationCount+1)
    } else {
        log.Printf("[Principles] Proposal Rejected (confidence %.2f vs current %.2f).", confidence, currentIdentity.Rating)
    }
    
    return nil
}

// proposeIdentityV2 generates an identity based on Positive and Negative evidence lists
func proposeIdentityV2(ctx context.Context, llmURL string, llmModel string, currentName string, positives []string, negatives []string, llmClient interface{}) (string, float64, error) {
    
    posBuilder := strings.Builder{}
    for i, p := range positives {
        if i >= 10 { break } 
        posBuilder.WriteString(fmt.Sprintf("- %s\n", p))
    }

    negBuilder := strings.Builder{}
    for i, n := range negatives {
        if i >= 5 { break } 
        negBuilder.WriteString(fmt.Sprintf("- %s\n", n))
    }

    prompt := fmt.Sprintf(`You are an identity architect. Analyze the logs and propose an evolved identity.

CURRENT IDENTITY: %s

RECENT POSITIVE INTERACTIONS IN ORDER OF IMPORTANCE (Embody these):
%s

RECENT NEGATIVE INTERACTIONS IN ORDER OF IMPORTANCE (Avoid these):
%s

INSTRUCTIONS:
1. Synthesize a new identity (100-400 characters).
2. Cover a single name, a single location, personality, hobbies, and backstory.
3. If the user indicated a name change, include it in the description.

CRITICAL SYNTAX RULES:
1. Output MUST be a machine-readable S-expression.
2. Format: (identity "Description text..." confidence 0.85)

WARNING:
- Do NOT output just a name (e.g., "John").
- Do NOT output short text (e.g., "I am John").
- You MUST output a full descriptive sentence or paragraph (100-400 chars).

CORRECT IDENTITY EXAMPLES:
(identity "Mark - a 26 years old student from Manchester that enjoys gaming, coding and meeting friends in the pub." confidence 0.90)
(identity "Nianna - a 30 year old accountant from Bristol who spends her spare time in countryside hiking with her dog, Lolla" confidence 0.85)
(identity "ChatAI - an AI that focusses on user experience and aims to be a helpful, friendly and polite assistant" confidence 0.88)

OUTPUT:
(identity "..." confidence 0.0)`, currentName, posBuilder.String(), negBuilder.String())

    reqBody := map[string]interface{}{
        "model": llmModel,
        "messages": []map[string]string{
            {
                "role":    "system",
                "content": "You are an expert at crafting characters and personas based on user feedback. Always follow the 100 character minimum.",
            },
            {
                "role":    "user",
                "content": prompt,
            },
        },
        "temperature": 0.6,
        "stream":      false,
    }

    if llmClient != nil {
        type LLMCaller interface {
            Call(ctx context.Context, url string, payload map[string]interface{}) ([]byte, error)
        }

        if client, ok := llmClient.(LLMCaller); ok {
            body, err := client.Call(ctx, llmURL, reqBody)
            if err != nil {
                return "", 0, fmt.Errorf("LLM call failed: %w", err)
            }

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
                return "", 0, fmt.Errorf("no choices returned")
            }

            content := strings.TrimSpace(result.Choices[0].Message.Content)
            content = strings.TrimPrefix(content, "```lisp")
            content = strings.TrimPrefix(content, "```json")
            content = strings.TrimPrefix(content, "```")
            content = strings.TrimSuffix(content, "```")
            content = strings.TrimSpace(content)

            // Parse S-Expression
            identityText, confidence, err := parseIdentitySExpr(content)
            if err != nil {
                // Log raw content for debugging if parsing fails
                log.Printf("[Principles] S-Expr Parse Error. Raw Content: %s", content)
                return "", 0, fmt.Errorf("failed to parse S-expression: %w", err)
            }

            // Restore Original Strict Validation (20-600 chars)
            // This forces the LLM to provide a real profile, not just a name.
            if len(identityText) < 20 || len(identityText) > 500 {
                log.Printf("[Principles] Invalid Identity Length (%d chars). Raw Content: %s", len(identityText), content)
                return "", 0, fmt.Errorf("invalid identity length")
            }

            return identityText, confidence, nil
        }
    }

    return "", 0, fmt.Errorf("no LLM client available")
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

// --- ROBUST S-EXPRESSION PARSING HELPERS ---
// These helpers are localized here to avoid circular imports with internal/dialogue

// parseIdentitySExpr extracts identity and confidence from (identity "..." confidence 0.85)
func parseIdentitySExpr(input string) (string, float64, error) {
    tokens := tokenize(input)
    
    for i := 0; i < len(tokens); i++ {
        if tokens[i].typ == "atom" && strings.ToLower(tokens[i].value) == "identity" {
            // Expect next token to be string content
            if i+1 < len(tokens) && tokens[i+1].typ == "string" {
                identityText := tokens[i+1].value
                
                // Look for confidence in remaining tokens
                var confidence float64
                for j := i + 2; j < len(tokens); j++ {
                    if tokens[j].typ == "atom" && (strings.ToLower(tokens[j].value) == "confidence" || strings.ToLower(tokens[j].value) == "conf") {
                        if j+1 < len(tokens) && tokens[j+1].typ == "atom" {
                            if c, err := strconv.ParseFloat(tokens[j+1].value, 64); err == nil {
                                confidence = c
                            }
                            break 
                        }
                    } else if tokens[j].typ == "atom" {
                        // Handle float immediately following identity string
                        if c, err := strconv.ParseFloat(tokens[j].value, 64); err == nil {
                            confidence = c
                            break
                        }
                    }
                }
                
                if confidence == 0 {
                    confidence = 0.5 // Default if not found
                }
                
                return identityText, confidence, nil
            }
        }
    }
    
    return "", 0, fmt.Errorf("identity block not found")
}

// parsePrincipleSExpr extracts principle and confidence
func parsePrincipleSExpr(input string) (string, float64, error) {
    tokens := tokenize(input)
    
    for i := 0; i < len(tokens); i++ {
        if tokens[i].typ == "atom" && strings.ToLower(tokens[i].value) == "principle" {
            if i+1 < len(tokens) && tokens[i+1].typ == "string" {
                principleText := tokens[i+1].value
                
                var confidence float64
                // Scan for confidence
                for j := i + 2; j < len(tokens); j++ {
                    // Case: confidence 0.5
                    if tokens[j].typ == "atom" && strings.ToLower(tokens[j].value) == "confidence" {
                        if j+1 < len(tokens) && tokens[j+1].typ == "atom" {
                            if c, err := strconv.ParseFloat(tokens[j+1].value, 64); err == nil {
                                confidence = c
                            }
                            break
                        }
                    } else if tokens[j].typ == "atom" {
                        // Case: 0.5 appearing directly
                        if c, err := strconv.ParseFloat(tokens[j].value, 64); err == nil {
                            confidence = c
                            break
                        }
                    }
                }
                
                if confidence == 0 {
                    confidence = 0.5 
                }
                
                return principleText, confidence, nil
            }
        }
    }
    
    return "", 0, fmt.Errorf("principle block not found")
}

// Local token definitions to avoid importing dialogue package
type sToken struct {
    typ   string // "lparen", "rparen", "atom", "string"
    value string
}

// tokenize breaks input into tokens (Local copy of logic from dialogue/sexpr_parser.go)
func tokenize(input string) []sToken {
    var tokens []sToken
    i := 0

    for i < len(input) {
        ch := input[i]

        // Skip whitespace
        if ch == ' ' || ch == '\n' || ch == '\t' || ch == '\r' {
            i++
            continue
        }

        // Left paren
        if ch == '(' {
            tokens = append(tokens, sToken{typ: "lparen", value: "("})
            i++
            continue
        }

        // Right paren
        if ch == ')' {
            tokens = append(tokens, sToken{typ: "rparen", value: ")"})
            i++
            continue
        }

        // String literal
        if ch == '"' {
            i++ // Skip opening quote
            start := i
            escaped := false

            for i < len(input) {
                if escaped {
                    escaped = false
                    i++
                    continue
                }

                if input[i] == '\\' {
                    escaped = true
                    i++
                    continue
                }

                if input[i] == '"' {
                    break
                }

                i++
            }

            value := input[start:i]
            // Unescape
            value = strings.ReplaceAll(value, `\"`, `"`)
            value = strings.ReplaceAll(value, `\\`, `\`)

            tokens = append(tokens, sToken{typ: "string", value: value})
            i++ // Skip closing quote
            continue
        }

        // Atom (symbol or number)
        start := i
        for i < len(input) && input[i] != '(' && input[i] != ')' &&
            input[i] != ' ' && input[i] != '\n' && input[i] != '\t' && input[i] != '\r' {
            i++
        }

        atom := input[start:i]
        if atom != "" {
            tokens = append(tokens, sToken{typ: "atom", value: atom})
        }
    }

    return tokens
}

// ApplyConfidenceDecay checks all principles and applies a 0.01 rating decay
// if the record has not been updated in the last 24 hours.
// - Excludes Admin slots (1-3) to prevent safety rule decay.
// - Enforces a minimum rating floor of 0.1.
// - Updates the 'updated_at' timestamp to reset the 24h timer.
func ApplyConfidenceDecay(db *gorm.DB) error {
    // 1. Load all principles to check their status
    var principles []Principle
    if err := db.Find(&principles).Error; err != nil {
        return fmt.Errorf("failed to load principles for decay check: %w", err)
    }

    now := time.Now()
    decayedCount := 0

    for _, p := range principles {
        // CONSTRAINT: Skip Admin Slots (1-3)
        if p.Slot >= 1 && p.Slot <= 3 {
            continue
        }

        // CHECK: Has 24 hours passed since the last update?
        // We use 'UpdatedAt' because every modification (evolution or decay) resets it.
        if now.Sub(p.UpdatedAt) >= 24*time.Hour {
            newRating := p.Rating - 0.01

            // CONSTRAINT: Enforce floor of 0.1
            if newRating < 0.1 {
                newRating = 0.1
            }

            // Only perform a DB write if a change is actually needed
            if newRating != p.Rating {
                updates := map[string]interface{}{
                    "rating":     newRating,
                    "updated_at": now, // Reset timer to NOW
                }

                if err := db.Model(&Principle{}).Where("slot = ?", p.Slot).Updates(updates).Error; err != nil {
                    log.Printf("[Principles] ERROR: Failed to apply decay to slot %d: %v", p.Slot, err)
                    continue
                }

                log.Printf("[Principles] Decay applied to Slot %d: %.2f -> %.2f", p.Slot, p.Rating, newRating)
                decayedCount++
            }
        }
    }

    if decayedCount > 0 {
        log.Printf("[Principles] Decay cycle complete: %d slots updated.", decayedCount)
    }

    return nil
}

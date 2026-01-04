// internal/api/ws_growerai_handler.go
package api

import (
	"context"
	"fmt"
	"log"
	"strings"
	"time"

	"go-llama/internal/chat"
	"go-llama/internal/config"
	"go-llama/internal/db"
	"go-llama/internal/memory"
)

// handleGrowerAIWebSocket processes GrowerAI messages via WebSocket with streaming
func handleGrowerAIWebSocket(conn *safeWSConn, cfg *config.Config, chatInst *chat.Chat, content string, userID uint) {
	log.Printf("[GrowerAI-WS] Processing message from user %d in chat %d", userID, chatInst.ID)

	if cfg.GrowerAI.ReasoningModel.URL == "" {
		conn.WriteJSON(map[string]string{"error": "GrowerAI not configured"})
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()

	// Initialize memory components
	log.Printf("[GrowerAI-WS] Initializing embedder: %s", cfg.GrowerAI.EmbeddingModel.URL)
	embedder := memory.NewEmbedder(cfg.GrowerAI.EmbeddingModel.URL)

	log.Printf("[GrowerAI-WS] Initializing storage: %s/%s", cfg.GrowerAI.Qdrant.URL, cfg.GrowerAI.Qdrant.Collection)
	storage, err := memory.NewStorage(
		cfg.GrowerAI.Qdrant.URL,
		cfg.GrowerAI.Qdrant.Collection,
		cfg.GrowerAI.Qdrant.APIKey,
	)
	if err != nil {
		log.Printf("[GrowerAI-WS] ERROR: Failed to initialize storage: %v", err)
		conn.WriteJSON(map[string]string{"error": "memory system unavailable"})
		return
	}

	// Initialize linker for co-occurrence tracking
	linker := memory.NewLinker(
		storage,
		cfg.GrowerAI.Linking.SimilarityThreshold,
		cfg.GrowerAI.Linking.MaxLinksPerMemory,
	)

	// Load 10 Commandments (Principles) - REPLACES static system prompt
	log.Printf("[GrowerAI-WS] Loading principles...")
	principles, err := memory.LoadPrinciples(db.DB)
	if err != nil {
		log.Printf("[GrowerAI-WS] ERROR: Failed to load principles: %v", err)
		conn.WriteJSON(map[string]string{"error": "principles system unavailable"})
		return
	}

	// Generate embedding for user's message
	log.Printf("[GrowerAI-WS] Generating embedding for query: %s", truncate(content, 50))
	queryEmbedding, err := embedder.Embed(ctx, content)
	if err != nil {
		log.Printf("[GrowerAI-WS] ERROR: Failed to generate embedding: %v", err)
		conn.WriteJSON(map[string]string{"error": "embedding generation failed"})
		return
	}
	log.Printf("[GrowerAI-WS] ✓ Generated %d-dimensional embedding", len(queryEmbedding))

	// Search memory for relevant context
	userIDStr := fmt.Sprintf("%d", userID)
	query := memory.RetrievalQuery{
		Query:             content,
		UserID:            &userIDStr,
		IncludePersonal:   true,
		IncludeCollective: false,
		Limit:             cfg.GrowerAI.Retrieval.MaxMemories,
		MinScore:          cfg.GrowerAI.Retrieval.MinScore,
	}

	log.Printf("[GrowerAI-WS] Searching memory (user=%s, limit=%d, min_score=%.2f)...", 
		userIDStr, cfg.GrowerAI.Retrieval.MaxMemories, cfg.GrowerAI.Retrieval.MinScore)
	results, err := storage.Search(ctx, query, queryEmbedding)
	if err != nil {
		log.Printf("[GrowerAI-WS] WARNING: Memory search failed: %v", err)
		results = []memory.RetrievalResult{}
	}
	log.Printf("[GrowerAI-WS] ✓ Found %d relevant memories", len(results))

// Phase 4D: Traverse links to find additional relevant memories (BATCH OPTIMIZED)
linkedMemories := []memory.RetrievalResult{}
linkedIDs := make(map[string]bool) // Track to avoid duplicates
maxLinked := cfg.GrowerAI.Retrieval.MaxLinkedMemories

// Mark primary memories to avoid duplicates
for _, result := range results {
	linkedIDs[result.Memory.ID] = true
}

// Collect all unique linked IDs from primary results
allLinkedIDs := []string{}
for _, result := range results {
	for _, linkedID := range result.Memory.RelatedMemories {
		if !linkedIDs[linkedID] {
			allLinkedIDs = append(allLinkedIDs, linkedID)
			linkedIDs[linkedID] = true // Mark as seen
			
			if len(allLinkedIDs) >= maxLinked {
				break
			}
		}
	}
	
	if len(allLinkedIDs) >= maxLinked {
		break
	}
}

// Batch retrieve all linked memories in ONE query
if len(allLinkedIDs) > 0 {
	log.Printf("[GrowerAI-WS] Batch retrieving %d linked memories...", len(allLinkedIDs))
	
	linkedMemsMap, err := storage.GetMemoriesByIDs(ctx, allLinkedIDs)
	if err != nil {
		log.Printf("[GrowerAI-WS] WARNING: Batch link retrieval failed: %v", err)
	} else {
		// Convert map to results list
		for id, linkedMem := range linkedMemsMap {
			linkedMemories = append(linkedMemories, memory.RetrievalResult{
				Memory: *linkedMem,
				Score:  0.5, // Base score for linked memories
			})
			
			log.Printf("[GrowerAI-WS]   ↳ Retrieved linked memory: %s (tier=%s, age=%s)",
				id[:8], linkedMem.Tier, time.Since(linkedMem.CreatedAt).Round(time.Minute))
		}
		
		// Track retrieval stats
		retrieved := len(linkedMemsMap)
		requested := len(allLinkedIDs)
		failed := requested - retrieved
		
		if failed > 0 {
			failureRate := float64(failed) / float64(requested)
			log.Printf("[GrowerAI-WS] Link traversal stats: %d/%d successful (%.1f%% failure rate)",
				retrieved, requested, failureRate*100)
			
			if failureRate > 0.5 {
				log.Printf("[GrowerAI-WS] ⚠️  HIGH LINK FAILURE RATE: %.1f%% of links failed to resolve. Memory IDs may be stale or memories were deleted.",
					failureRate*100)
			}
		} else {
			log.Printf("[GrowerAI-WS] ✓ Successfully retrieved all %d linked memories", retrieved)
		}
	}
}

	// Combine primary results with linked memories
	allResults := append(results, linkedMemories...)
	
	log.Printf("[GrowerAI-WS] Total memories (including links): %d", len(allResults))

	// Phase 4D: Track co-occurrence for retrieved memories
	if len(allResults) > 1 {
		retrievedMems := make([]memory.Memory, len(allResults))
		for i, res := range allResults {
			retrievedMems[i] = res.Memory
		}
		
		if err := linker.TrackCoOccurrence(ctx, retrievedMems); err != nil {
			log.Printf("[GrowerAI-WS] WARNING: Failed to track co-occurrence: %v", err)
		} else {
			log.Printf("[GrowerAI-WS] ✓ Tracked co-occurrence for %d memories", len(retrievedMems))
		}
	}

	// Update access metadata for retrieved memories
	for _, result := range allResults {
		if err := storage.UpdateAccessMetadata(ctx, result.Memory.ID); err != nil {
			log.Printf("[GrowerAI-WS] WARNING: Failed to update access metadata for memory %s: %v",
				result.Memory.ID, err)
		}
	}

	// Fetch recent conversation history from database
	var messages []chat.Message
	if err := db.DB.Where("chat_id = ?", chatInst.ID).
		Order("created_at ASC").
		Limit(20). // Last 20 messages for context
		Find(&messages).Error; err != nil {
		log.Printf("[GrowerAI-WS] WARNING: Failed to fetch chat history: %v", err)
		messages = []chat.Message{}
	}
	
	// Apply sliding window to respect context size
	messages = chat.BuildSlidingWindow(messages, cfg.GrowerAI.ReasoningModel.ContextSize)
	
	log.Printf("[GrowerAI-WS] ✓ Loaded %d messages from conversation history", len(messages))

	// Build system prompt from principles + memories
	systemPrompt := memory.FormatAsSystemPrompt(principles, cfg.GrowerAI.Personality.GoodBehaviorBias)
	
	var contextBuilder strings.Builder
	contextBuilder.WriteString(systemPrompt)
	contextBuilder.WriteString("\n\n")

	if len(allResults) > 0 {
		contextBuilder.WriteString("=== RELEVANT MEMORIES ===\n")
		for i, result := range allResults {
			log.Printf("[GrowerAI-WS]   Memory %d: score=%.3f, tier=%s, age=%s, outcome=%s",
				i+1, result.Score, result.Memory.Tier,
				time.Since(result.Memory.CreatedAt).Round(time.Minute),
				result.Memory.OutcomeTag)
			
			// Show link info if memory was retrieved via link
			linkInfo := ""
			isLinked := false
			for _, primaryRes := range results {
				if result.Memory.ID == primaryRes.Memory.ID {
					break // This is a primary result
				}
				for _, linkedID := range primaryRes.Memory.RelatedMemories {
					if linkedID == result.Memory.ID {
						isLinked = true
						break
					}
				}
				if isLinked {
					break
				}
			}
			if isLinked {
				linkInfo = " [linked]"
			}
			
			contextBuilder.WriteString(fmt.Sprintf("[Memory %d - %.0f%% relevant - from %s ago - outcome: %s%s]\n%s\n\n",
				i+1,
				result.Score*100,
				time.Since(result.Memory.CreatedAt).Round(time.Minute),
				result.Memory.OutcomeTag,
				linkInfo,
				result.Memory.Content))
		}
		contextBuilder.WriteString("=== END MEMORIES ===\n\n")
	} else {
		log.Printf("[GrowerAI-WS]   No relevant memories found")
	}

	// Call LLM with enhanced context (streaming)
	// Structure: system prompt (with memories) + conversation history + current message
	llmMessages := []map[string]string{
		{
			"role":    "system",
			"content": contextBuilder.String(),
		},
	}
	
	// Add conversation history (excluding the current user message which we'll add separately)
	for _, msg := range messages {
		role := "user"
		if msg.Sender == "bot" {
			role = "assistant"
		}
		
		// Clean up bot messages (remove tokens/sec footer)
		msgContent := msg.Content
		if msg.Sender == "bot" {
			// Remove "_Tokens/sec: X.XX_" footer from bot messages
			lines := strings.Split(msgContent, "\n")
			var cleanedLines []string
			for _, line := range lines {
				if !strings.HasPrefix(strings.TrimSpace(line), "_Tokens/sec:") {
					cleanedLines = append(cleanedLines, line)
				}
			}
			msgContent = strings.Join(cleanedLines, "\n")
			msgContent = strings.TrimSpace(msgContent)
		}
		
		llmMessages = append(llmMessages, map[string]string{
			"role":    role,
			"content": msgContent,
		})
	}
	
	// Add current user message
	llmMessages = append(llmMessages, map[string]string{
		"role":    "user",
		"content": content,
	})
	
	log.Printf("[GrowerAI-WS] ✓ Built conversation context: %d messages (%d history + 1 current)",
		len(llmMessages)-1, len(messages)) // -1 for system message

	payload := map[string]interface{}{
		"model":    cfg.GrowerAI.ReasoningModel.Name,
		"messages": llmMessages,
		"stream":   true,
	}

	log.Printf("[GrowerAI-WS] Calling LLM with streaming: %s", cfg.GrowerAI.ReasoningModel.URL)

	var botResponse string
	var toksPerSec float64
	err = streamLLMResponseWS(conn, conn.conn, cfg.GrowerAI.ReasoningModel.URL, payload, &botResponse, &toksPerSec)
	if err != nil {
		log.Printf("[GrowerAI-WS] ERROR: LLM streaming failed: %v", err)
		conn.WriteJSON(map[string]string{"error": "llm streaming failed"})
		return
	}

	log.Printf("[GrowerAI-WS] ✓ LLM response received (%d chars, %.1f tok/s)", len(botResponse), toksPerSec)

	// Phase 4D: Increment validation count for retrieved memories (they helped produce this response)
	// We'll tag this interaction's outcome in the background, but we can assume retrieval = useful
	if len(allResults) > 0 {
		log.Printf("[GrowerAI-WS] Incrementing validation count for %d retrieved memories", len(allResults))
		
		// Use separate context with longer timeout for batch operations
		validationCtx, validationCancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer validationCancel()
		
		successCount := 0
		for _, result := range allResults {
			if err := storage.IncrementValidationCount(validationCtx, result.Memory.ID); err != nil {
				log.Printf("[GrowerAI-WS] WARNING: Failed to increment validation for memory %s: %v",
					result.Memory.ID, err)
			} else {
				successCount++
			}
		}
		
		if successCount > 0 {
			log.Printf("[GrowerAI-WS] ✓ Successfully incremented validation for %d/%d memories", successCount, len(allResults))
		}
	}

	// Evaluate what to store in memory
	shouldStore := len(content) > 20 && len(botResponse) > 20

	if shouldStore {
		log.Printf("[GrowerAI-WS] Evaluating memory storage...")

		memoryContent := fmt.Sprintf("User asked: %s\nAssistant responded: %s",
			content, truncate(botResponse, 200))

		memEmbedding, err := embedder.Embed(ctx, memoryContent)
		if err != nil {
			log.Printf("[GrowerAI-WS] WARNING: Failed to generate memory embedding (attempt 1): %v", err)
			
			// Retry once with fresh context
			retryCtx, retryCancel := context.WithTimeout(context.Background(), 20*time.Second)
			memEmbedding, err = embedder.Embed(retryCtx, memoryContent)
			retryCancel()
			
			if err != nil {
				log.Printf("[GrowerAI-WS] ERROR: Failed to generate memory embedding after retry: %v", err)
			} else {
				log.Printf("[GrowerAI-WS] ✓ Memory embedding generated on retry")
			}
		}
		
		if err == nil {
			importanceScore := 0.5
			if len(content) > 100 {
				importanceScore += 0.2
			}
			if len(allResults) > 0 {
				importanceScore += 0.1
			}

		// Create new memory with full timestamp precision
		now := time.Now()

		mem := &memory.Memory{
			Content:            memoryContent,
			Tier:               memory.TierRecent,
			UserID:             &userIDStr,
			IsCollective:       false,
			CreatedAt:          now,
			LastAccessedAt:     now,
			AccessCount:        0,
			ImportanceScore:    importanceScore,
			Embedding:          memEmbedding,
				Metadata: map[string]interface{}{
					"chat_id": chatInst.ID,
				},
			}

			if err := storage.Store(ctx, mem); err != nil {
				log.Printf("[GrowerAI-WS] WARNING: Failed to store memory: %v", err)
			} else {
			log.Printf("[GrowerAI-WS] ✓ Stored memory (id=%s, importance=%.2f, created=%s)",
				mem.ID, mem.ImportanceScore, mem.CreatedAt.Format("2006-01-02 15:04"))
		}
		}
	} else {
		log.Printf("[GrowerAI-WS] Skipping memory storage (message too short)")
	}

	// Save bot message to database
	botResponseWithStats := botResponse + "\n\n_Tokens/sec: " + fmt.Sprintf("%.2f", toksPerSec) + "_"
	botMsg := chat.Message{
		ChatID:    chatInst.ID,
		Sender:    "bot",
		Content:   botResponseWithStats,
		CreatedAt: time.Now(),
	}
	if err := db.DB.Create(&botMsg).Error; err != nil {
		log.Printf("[GrowerAI-WS] WARNING: Failed to save bot message: %v", err)
	}

	log.Printf("[GrowerAI-WS] ✓ Message processing complete")
}

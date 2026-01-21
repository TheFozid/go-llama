// internal/api/ws_llm_handler.go
package api

import (
    "bytes"
    "context"
    "encoding/json"
    "fmt"
    "io"
    "log"
    "net/http"
    "net/url"
    "strconv"
    "strings"
    "time"

    "github.com/gorilla/websocket"
    "go-llama/internal/chat"
    "go-llama/internal/config"
    "go-llama/internal/db"
    "go-llama/internal/llm"
    "go-llama/internal/memory"
)

// handleStandardLLMWebSocket processes standard LLM messages via WebSocket with streaming
func handleStandardLLMWebSocket(conn *safeWSConn, cfg *config.Config, chatInst *chat.Chat, req WSChatPrompt, userID uint, llmManager interface{}, discoveryService *llm.DiscoveryService) {
	// Save user message
	userMsg := chat.Message{
		ChatID:    chatInst.ID,
		Sender:    "user",
		Content:   req.Prompt,
		CreatedAt: time.Now(),
	}
	if err := db.DB.Create(&userMsg).Error; err != nil {
		conn.WriteJSON(map[string]string{"error": "failed to save message"})
		return
	}

    // Find the model endpoint
    endpoint := discoveryService.FindEndpointForModel(chatInst.ModelName)
    modelMigrated := false
    if endpoint == nil {
        // Model was removed, migrate to first available
        chatModels := discoveryService.GetChatModels()
        if len(chatModels) == 0 {
            conn.WriteJSON(map[string]string{"error": "no models available"})
            return
        }
        chatInst.ModelName = chatModels[0].Name
        chatInst.LlmSessionID = ""
        modelMigrated = true
        endpoint = discoveryService.FindEndpointForModel(chatInst.ModelName)
    }

	// Fetch chat history
	var allMessages []chat.Message
	if err := db.DB.Where("chat_id = ?", chatInst.ID).Order("created_at asc").Find(&allMessages).Error; err != nil {
		conn.WriteJSON(map[string]string{"error": "failed to fetch chat history"})
		return
	}

    // Get context size from model info or use default
    models, _ := discoveryService.GetModels(endpoint.BaseURL)
    contextSize := 2048 // Default fallback
    for _, m := range models {
        if m.Name == chatInst.ModelName {
            // For now, we'll use a default context size
            // In the future, we could parse this from model name or endpoint
            contextSize = 2048
            break
        }
    }

	// Determine if web search will happen (call shouldAutoSearch only once)
	autoSearch := false
	if !req.WebSearch {
		autoSearch = shouldAutoSearch(cfg, req.Prompt)
	}
	willSearch := req.WebSearch || autoSearch

	// Calculate effective context size accounting for web search
	effectiveContextSize := contextSize
	if willSearch {
		// Estimate web context size before we have actual sources
		estimatedResults := cfg.SearxNG.MaxResults / 2 // We keep top 50%
		if estimatedResults < 1 {
			estimatedResults = 1
		}
		webContextSize := (estimatedResults * 50) + 50
		effectiveContextSize = contextSize - webContextSize
		if effectiveContextSize < 512 {
			effectiveContextSize = 512 // Minimum context for history
		}
	}

	// Build sliding window with correct size
	messages := chat.BuildSlidingWindow(allMessages, effectiveContextSize)

	var llmMessages []map[string]string
	for _, m := range messages {
		role := "user"
		if m.Sender == "bot" {
			role = "assistant"
		}
		llmMessages = append(llmMessages, map[string]string{
			"role":    role,
			"content": m.Content,
		})
	}

	// Build context-aware system instruction
	var systemInstruction string
	currentTime := time.Now().UTC().Format("2006-01-02 15:04")

	if willSearch {
		systemInstruction = fmt.Sprintf(
			"Today is %s UTC. You are a helpful assistant with access to current web search results retrieved today. The search results below contain the facts you need. Answer using ONLY the provided information and cite sources as [1], [2]. Do not say you cannot access information that is explicitly provided.",
			currentTime,
		)
	} else {
		systemInstruction = fmt.Sprintf("Today is %s UTC. Be direct and helpful.", currentTime)
	}

	llmMessages = append([]map[string]string{
		{"role": "system", "content": systemInstruction},
	}, llmMessages...)

	// Handle web search if requested
	var sources []map[string]string
	if req.Prompt == "" {
		conn.WriteJSON(map[string]string{"error": "missing prompt"})
		return
	}

	// Notify UI if auto triggered
	if autoSearch {
		conn.WriteJSON(map[string]string{"auto_search": "true"})
	}

	if req.WebSearch || autoSearch {
		sources = performWebSearch(cfg, allMessages, conn)

		// Inject web context as USER message RIGHT AFTER system prompt if we have sources
		if len(sources) > 0 {
			var webContextBuilder strings.Builder
			currentDate := time.Now().UTC().Format("2006-01-02")
			webContextBuilder.WriteString("Here are search results retrieved on ")
			webContextBuilder.WriteString(currentDate)
			webContextBuilder.WriteString(":\n\n")

			for i, src := range sources {
				webContextBuilder.WriteString("[")
				webContextBuilder.WriteString(strconv.Itoa(i+1))
				webContextBuilder.WriteString("] ")
				webContextBuilder.WriteString(src["snippet"])
				webContextBuilder.WriteString("\n\n")
			}

			webContextBuilder.WriteString("Use these results to answer my question. Cite sources as [1], [2].")

			webContext := webContextBuilder.String()

			// Insert as USER message RIGHT AFTER system message (position 1)
			webContextMsg := map[string]string{
				"role":    "user",
				"content": webContext,
			}
			llmMessages = append(llmMessages[:1], append([]map[string]string{webContextMsg}, llmMessages[1:]...)...)
		}

		// Graceful fallback if no results
		if (req.WebSearch || autoSearch) && len(sources) == 0 {
			fallbackMsg := "Web search returned no results."
			llmMessages = append(llmMessages, map[string]string{
				"role":    "system",
				"content": fallbackMsg,
			})
		}
	}

    // Prepare LLM payload
    payload := map[string]interface{}{
        "model":    chatInst.ModelName,
        "messages": llmMessages,
        "stream":   true,
    }
    if chatInst.LlmSessionID != "" && !modelMigrated {
        payload["session"] = chatInst.LlmSessionID
    }

	// Stream LLM response
	var botResponse string
	var toksPerSec float64
	var err error
	
	// Use queue if available (critical priority for user messages)
	if llmManager != nil {
		if mgr, ok := llmManager.(*llm.Manager); ok && cfg.GrowerAI.LLMQueue.Enabled {
			llmClient := llm.NewClient(
				mgr,
				llm.PriorityCritical,
				time.Duration(cfg.GrowerAI.LLMQueue.CriticalTimeoutSeconds)*time.Second,
			)
			
			log.Printf("[LLM-WS] Using LLM queue (priority: CRITICAL, timeout: %ds)", 
				cfg.GrowerAI.LLMQueue.CriticalTimeoutSeconds)
			
			// Create context for this request
			ctx := context.Background()
			
            // Get streaming HTTP response from queue
            httpResp, queueErr := llmClient.CallStreaming(ctx, chatInst.ModelName, payload)
			if queueErr != nil {
				conn.WriteJSON(map[string]string{"error": "llm streaming failed", "detail": queueErr.Error()})
				return
			}
			
			// Use the streamLLMResponseFromHTTP helper from ws_growerai_handler.go
			// Note: This requires the helper function to be accessible or duplicated
			err = streamLLMResponseFromHTTP(conn, conn.conn, httpResp, &botResponse, &toksPerSec)
        } else {
            log.Printf("[LLM-WS] Using legacy direct LLM call")
            llmURL := endpoint.BaseURL + "/v1/chat/completions"
            err = streamLLMResponseWS(conn, conn.conn, llmURL, payload, &botResponse, &toksPerSec)
        }
    } else {
        log.Printf("[LLM-WS] Using legacy direct LLM call (no queue manager)")
        llmURL := endpoint.BaseURL + "/v1/chat/completions"
        err = streamLLMResponseWS(conn, conn.conn, llmURL, payload, &botResponse, &toksPerSec)
    }
	
	if err != nil {
		conn.WriteJSON(map[string]string{"error": "llm streaming failed", "detail": err.Error()})
		return
	}

	// Save bot response
	if strings.TrimSpace(botResponse) != "" {
		botResponseWithStats := botResponse + "\n\n_Tokens/sec: " + fmt.Sprintf("%.2f", toksPerSec) + "_"

		// Append sources if web search was used (collapsible)
		if (req.WebSearch || autoSearch) && len(sources) > 0 {
			botResponseWithStats += "\n<details><summary><strong>Sources</strong></summary>\n"
			for i, src := range sources {
				botResponseWithStats += fmt.Sprintf(
					"%d. <a href=\"%s\" target=\"_blank\" rel=\"noopener noreferrer\">%s</a>\n",
					i+1, src["url"], src["title"],
				)
			}
			botResponseWithStats += "</details>"
		}

		botMsg := chat.Message{
			ChatID:    chatInst.ID,
			Sender:    "bot",
			Content:   botResponseWithStats,
			CreatedAt: time.Now(),
		}
		if err := db.DB.Create(&botMsg).Error; err != nil {
			log.Printf("failed to save bot message: %v", err)
		}
	}
}

// performWebSearch executes web search and returns ranked sources
func performWebSearch(cfg *config.Config, allMessages []chat.Message, conn *safeWSConn) []map[string]string {
	searxngURL := cfg.SearxNG.URL
	if searxngURL == "" {
		searxngURL = "http://localhost:8888/search"
	}

	// Build combined search context (last up to 3 user messages from full history)
	var userPrompts []string
	combinedLength := 0
	const maxCombinedChars = 500
	for i := len(allMessages) - 1; i >= 0 && len(userPrompts) < 3; i-- {
		if allMessages[i].Sender == "user" {
			if combinedLength+len(allMessages[i].Content) > maxCombinedChars {
				break
			}
			userPrompts = append([]string{allMessages[i].Content}, userPrompts...)
			combinedLength += len(allMessages[i].Content)
		}
	}
	combinedPrompt := strings.Join(userPrompts, " ")

	// Extract site filter (URL or text-based site reference)
	searchQuery, siteFilter := extractSiteFilter(combinedPrompt)

	// Compress long queries
	if len(strings.Fields(searchQuery)) > 20 {
		searchQuery = compressForSearch(searchQuery)
	}

	// Prepend site filter if found
	if siteFilter != "" {
		searchQuery = "site:" + siteFilter + " " + searchQuery
	}

	httpResp, err := http.Get(searxngURL + "?q=" + url.QueryEscape(searchQuery) + "&format=json")
	if err != nil {
		log.Printf("SearxNG request failed: %v", err)
		return []map[string]string{}
	}
	defer httpResp.Body.Close()

	if httpResp.StatusCode != 200 {
		log.Printf("SearxNG returned status %d", httpResp.StatusCode)
		return []map[string]string{}
	}

	var searxResp struct {
		Results []struct {
			Title   string `json:"title"`
			URL     string `json:"url"`
			Content string `json:"content"`
		} `json:"results"`
	}

	if err := json.NewDecoder(httpResp.Body).Decode(&searxResp); err != nil {
		log.Printf("SearxNG decode error: %v", err)
		return []map[string]string{}
	}

	raw := searxResp.Results
	if len(raw) == 0 {
		return []map[string]string{}
	}

	// Determine how many raw results we care about
	limit := cfg.SearxNG.MaxResults
	if limit <= 0 || limit > len(raw) {
		limit = len(raw)
	}

	tmpResults := make([]SearxResult, 0, limit)
	for i := 0; i < limit; i++ {
		r := raw[i]
		tmpResults = append(tmpResults, SearxResult{
			Title:   r.Title,
			URL:     r.URL,
			Content: r.Content,
		})
	}

	// Rank those (returns top 80%, removing junk)
	ranked := rankAndFilterResults(combinedPrompt, tmpResults)

	var sources []map[string]string
	for _, r := range ranked {
		if r.Title != "" && r.URL != "" {
			sources = append(sources, map[string]string{
				"title":   r.Title,
				"url":     r.URL,
				"snippet": r.Content,
			})
			// Debug: Log what we're sending to LLM
			log.Printf("📄 Source [%s]: %s", r.Title, r.Content[:min(len(r.Content), 100)])
		}
	}

	return sources
}

// handleGrowerAIWebSocket processes GrowerAI messages via WebSocket with streaming
func handleGrowerAIWebSocket(conn *safeWSConn, cfg *config.Config, chatInst *chat.Chat, prompt string, userID uint, llmManager interface{}, discoveryService *llm.DiscoveryService) {
    // Find reasoning and embedding models
    reasoningModels := discoveryService.GetChatModels()
    embeddingModels := discoveryService.GetEmbeddingModels()
    
    if len(reasoningModels) == 0 {
        conn.WriteJSON(map[string]string{"error": "No reasoning models available for GrowerAI"})
        return
    }
    if len(embeddingModels) == 0 {
        conn.WriteJSON(map[string]string{"error": "No embedding models available for GrowerAI"})
        return
    }

    // Initialize memory components
    reasoningModel := reasoningModels[0]
    embeddingModel := embeddingModels[0]
    reasoningEndpoint := discoveryService.FindEndpointForModel(reasoningModel.Name)
    embeddingEndpoint := discoveryService.FindEndpointForModel(embeddingModel.Name)
    
    log.Printf("[GrowerAI-WS] Initializing embedder: %s", embeddingModel.Name)
    embedder := memory.NewEmbedder(embeddingEndpoint.BaseURL + "/v1/embeddings")
    
    // Initialize storage
    storage, err := memory.NewStorage(
        cfg.GrowerAI.Qdrant.URL,
        cfg.GrowerAI.Qdrant.Collection,
        cfg.GrowerAI.Qdrant.APIKey,
    )
    if err != nil {
        conn.WriteJSON(map[string]string{"error": "memory system unavailable"})
        return
    }

    // Generate embedding for user's message
    ctx := context.Background()
    queryEmbedding, err := embedder.Embed(ctx, prompt)
    if err != nil {
        conn.WriteJSON(map[string]string{"error": "embedding generation failed"})
        return
    }

    // Search memory for relevant context
    userIDStr := fmt.Sprintf("%d", userID)
    query := memory.RetrievalQuery{
        Query:             prompt,
        UserID:            &userIDStr,
        IncludePersonal:   true,
        IncludeCollective: true,
        Limit:             5,
        MinScore:          0.5,
    }

    results, err := storage.Search(ctx, query, queryEmbedding)
    if err != nil {
        log.Printf("[GrowerAI-WS] WARNING: Memory search failed: %v", err)
        results = []memory.RetrievalResult{}
    }

    // Build context with retrieved memories
    var contextBuilder strings.Builder
    contextBuilder.WriteString("You are GrowerAI, an AI system that learns and improves from conversations.\n\n")
    
    if len(results) > 0 {
        contextBuilder.WriteString("=== RELEVANT MEMORIES ===\n")
        for i, result := range results {
            contextBuilder.WriteString(fmt.Sprintf("[Memory %d - %.0f%% relevant - from %s ago]\n%s\n\n",
                i+1,
                result.Score*100,
                time.Since(result.Memory.CreatedAt).Round(time.Minute),
                result.Memory.Content))
        }
        contextBuilder.WriteString("=== END MEMORIES ===\n\n")
    }

    contextBuilder.WriteString(fmt.Sprintf("User's current message: %s\n\n", prompt))
    contextBuilder.WriteString("Respond naturally, incorporating relevant context from memories if available.")

    // Prepare LLM messages
    llmMessages := []map[string]string{
        {
            "role":    "system",
            "content": contextBuilder.String(),
        },
    }

    // Prepare LLM payload
    payload := map[string]interface{}{
        "model":    reasoningModel.Name,
        "messages": llmMessages,
        "stream":   true,
    }

    // Stream LLM response
    var botResponse string
    var toksPerSec float64
    
    // Use queue if available (critical priority for user messages)
    if llmManager != nil {
        if mgr, ok := llmManager.(*llm.Manager); ok && cfg.GrowerAI.LLMQueue.Enabled {
            llmClient := llm.NewClient(
                mgr,
                llm.PriorityCritical,
                time.Duration(cfg.GrowerAI.LLMQueue.CriticalTimeoutSeconds)*time.Second,
                discoveryService,
            )
            
            // Create context for this request
            ctx := context.Background()
            
            // Get streaming HTTP response from queue
            httpResp, queueErr := llmClient.CallStreaming(ctx, reasoningModel.Name, payload)
            if queueErr != nil {
                conn.WriteJSON(map[string]string{"error": "llm streaming failed", "detail": queueErr.Error()})
                return
            }
            
            // Use the streamLLMResponseFromHTTP helper
            err = streamLLMResponseFromHTTP(conn, conn.conn, httpResp, &botResponse, &toksPerSec)
        } else {
            llmURL := reasoningEndpoint.BaseURL + "/v1/chat/completions"
            err = streamLLMResponseWS(conn, conn.conn, llmURL, payload, &botResponse, &toksPerSec)
        }
    } else {
        llmURL := reasoningEndpoint.BaseURL + "/v1/chat/completions"
        err = streamLLMResponseWS(conn, conn.conn, llmURL, payload, &botResponse, &toksPerSec)
    }
    
    if err != nil {
        conn.WriteJSON(map[string]string{"error": "llm streaming failed", "detail": err.Error()})
        return
    }

    // Save user message
    userMsg := chat.Message{
        ChatID:    chatInst.ID,
        Sender:    "user",
        Content:   prompt,
        CreatedAt: time.Now(),
    }
    if err := db.DB.Create(&userMsg).Error; err != nil {
        log.Printf("failed to save user message: %v", err)
    }

    // Save bot response
    if strings.TrimSpace(botResponse) != "" {
        botResponseWithStats := botResponse + "\n\n_Tokens/sec: " + fmt.Sprintf("%.2f", toksPerSec) + "_"

        botMsg := chat.Message{
            ChatID:    chatInst.ID,
            Sender:    "bot",
            Content:   botResponseWithStats,
            CreatedAt: time.Now(),
        }
        if err := db.DB.Create(&botMsg).Error; err != nil {
            log.Printf("failed to save bot message: %v", err)
        }
    }
}

// streamLLMResponseFromHTTP streams an HTTP response to a WebSocket connection
func streamLLMResponseFromHTTP(conn *safeWSConn, wsConn *websocket.Conn, httpResp *http.Response, response *string, toksPerSec *float64) error {
    defer httpResp.Body.Close()
    
    decoder := json.NewDecoder(httpResp.Body)
    startTime := time.Now()
    tokenCount := 0
    
    for {
        var chunk struct {
            Choices []struct {
                Delta struct {
                    Content string `json:"content"`
                } `json:"delta"`
                FinishReason string `json:"finish_reason"`
            } `json:"choices"`
        }
        
        if err := decoder.Decode(&chunk); err == io.EOF {
            break
        } else if err != nil {
            return err
        }
        
        if len(chunk.Choices) > 0 && chunk.Choices[0].Delta.Content != "" {
            content := chunk.Choices[0].Delta.Content
            *response += content
            tokenCount += len(strings.Fields(content))
            
            // Send token to WebSocket
            if err := conn.WriteJSON(map[string]string{
                "token": content,
            }); err != nil {
                return err
            }
        }
        
        if len(chunk.Choices) > 0 && chunk.Choices[0].FinishReason == "stop" {
            break
        }
    }
    
    // Calculate tokens per second
    elapsed := time.Since(startTime).Seconds()
    if elapsed > 0 {
        *toksPerSec = float64(tokenCount) / elapsed
    }
    
    // Send completion signal
    if err := conn.WriteJSON(map[string]string{
        "done": "true",
    }); err != nil {
        return err
    }
    
    return nil
}

// streamLLMResponseWS streams a response directly to a WebSocket connection
func streamLLMResponseWS(conn *safeWSConn, wsConn *websocket.Conn, url string, payload map[string]interface{}, response *string, toksPerSec *float64) error {
    jsonData, _ := json.Marshal(payload)
    req, _ := http.NewRequest("POST", url, bytes.NewBuffer(jsonData))
    req.Header.Set("Content-Type", "application/json")
    
    client := &http.Client{Timeout: 120 * time.Second}
    httpResp, err := client.Do(req)
    if err != nil {
        return err
    }
    defer httpResp.Body.Close()
    
    if httpResp.StatusCode != http.StatusOK {
        return fmt.Errorf("HTTP %d", httpResp.StatusCode)
    }
    
    return streamLLMResponseFromHTTP(conn, wsConn, httpResp, response, toksPerSec)
}

package api

import (
    "bufio"
    "context"
    "encoding/json"
    "fmt"
    "log"
    "net/http"
    "net/url"
    "strconv"
    "strings"
    "time"

    "go-llama/internal/chat"
    "go-llama/internal/config"
    "go-llama/internal/memory"
    "go-llama/internal/db"
)

// handleStandardLLMWebSocket processes standard LLM messages via WebSocket with streaming
func handleStandardLLMWebSocket(conn *safeWSConn, cfg *config.Config, chatInst *chat.Chat, req WSChatPrompt, userID uint, llmClient interface{}) {
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

    // Find model configuration
    var modelConfig *config.LLMConfig
    for i := range cfg.LLMs {
        if cfg.LLMs[i].Name == chatInst.ModelName {
            modelConfig = &cfg.LLMs[i]
            break
        }
    }

    // Handle model migration if needed
    modelMigrated := false
    if modelConfig == nil && len(cfg.LLMs) > 0 {
        modelConfig = &cfg.LLMs[0]
        chatInst.ModelName = modelConfig.Name
        chatInst.LlmSessionID = ""
        modelMigrated = true
    }
    if modelConfig == nil {
        conn.WriteJSON(map[string]string{"error": "no models available"})
        return
    }

    // Fetch chat history
    var allMessages []chat.Message
    if err := db.DB.Where("chat_id = ?", chatInst.ID).Order("created_at asc").Find(&allMessages).Error; err != nil {
        conn.WriteJSON(map[string]string{"error": "failed to fetch chat history"})
        return
    }

    // Determine context size
    contextSize := modelConfig.ContextSize
    if contextSize == 0 {
        contextSize = 2048 // Fallback default
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

    // Build System Prompt with Principles
    principles, err := memory.LoadPrinciples(db.DB)
    if err != nil {
        log.Printf("[WS-Chat] WARNING: Failed to load principles: %v", err)
        // Fallback to generic prompt if loading fails
        principles = []memory.Principle{}
    }

    // Get the bias for dynamic config values
    goodBehaviorBias := cfg.GrowerAI.Personality.GoodBehaviorBias

    // Generate system prompt (includes Date, Identity, and Principles)
    systemInstruction := memory.FormatAsSystemPrompt(principles, goodBehaviorBias)

    // If web search is active, append specific instructions to the principles
    if willSearch {
        systemInstruction += "\n\n=== ADDITIONAL INSTRUCTIONS ===\n"
        systemInstruction += "You have access to current web search results retrieved today. "
        systemInstruction += "The search results contain facts you need. Answer using ONLY provided information and cite sources as [1], [2]. "
        systemInstruction += "Do not say you cannot access information that is explicitly provided."
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
        "model":    modelConfig.Name,
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

    // Use injected critical client
    if llmClient != nil {
        log.Printf("[LLM-WS] Using critical LLM client for streaming")

        // Create context for this request
        ctx := context.Background()

        // Type assert for streaming client
        type LLMStreamCaller interface {
            CallStreaming(ctx context.Context, url string, payload map[string]interface{}) (*http.Response, error)
        }

        streamClient, ok := llmClient.(LLMStreamCaller)
        if !ok {
            conn.WriteJSON(map[string]string{"error": "invalid LLM client type for streaming"})
            return
        }

        // Get streaming HTTP response from queue
        llmURL := config.GetChatURL(modelConfig.URL)
        httpResp, queueErr := streamClient.CallStreaming(ctx, llmURL, payload)
        if queueErr != nil {
            conn.WriteJSON(map[string]string{"error": "llm streaming failed", "detail": queueErr.Error()})
            return
        }
        defer httpResp.Body.Close()

        if httpResp.StatusCode != http.StatusOK {
            conn.WriteJSON(map[string]string{"error": "llm returned error", "detail": fmt.Sprintf("%d", httpResp.StatusCode)})
            return
        }

        // --- Direct Streaming Implementation ---
        // Since queue returns an active http.Response, we must read the stream
        // and write to WebSocket directly here.
        reader := bufio.NewReader(httpResp.Body)
        index := 0
        var responseBuilder strings.Builder
        var startTime time.Time
        firstToken := true
        inReasoning := false

        for {
            line, err := reader.ReadString('\n')
            if err != nil {
                break
            }
            line = strings.TrimSpace(line)
            if len(line) < 7 || !strings.HasPrefix(line, "data: ") {
                continue
            }
            data := line[6:]
            if data == "[DONE]" {
                break
            }

            var chunk struct {
                Choices []struct {
                    Delta struct {
                        Content          string `json:"content"`
                        ReasoningContent string `json:"reasoning_content"`
                    } `json:"delta"`
                } `json:"choices"`
                FinishReason string `json:"finish_reason"`
            }

            if err := json.Unmarshal([]byte(data), &chunk); err != nil {
                log.Printf("stream decode error: %v", err)
                continue
            }

            if len(chunk.Choices) > 0 {
                delta := chunk.Choices[0].Delta

                // Handle reasoning_content (thinking process)
                if delta.ReasoningContent != "" {
                    token := delta.ReasoningContent

                    // Start timer when we receive first token of any kind
                    if firstToken {
                        startTime = time.Now()
                        firstToken = false
                    }

                    // If this is the first reasoning chunk, open <think> block
                    if !inReasoning {
                        inReasoning = true
                        token = "<think>" + token
                    }

                    // Append reasoning to the accumulated response
                    responseBuilder.WriteString(token)

                    // Stream to frontend
                    conn.WriteJSON(WSChatToken{Token: token, Index: index})
                    index++
                }

                // Handle normal content (the actual answer)
                if delta.Content != "" {
                    token := delta.Content

                    // Start timer if we never got any reasoning
                    if firstToken {
                        startTime = time.Now()
                        firstToken = false
                    }

                    // If we were in a reasoning section, close <think> block before answer
                    if inReasoning {
                        inReasoning = false
                        // Close the think block in both saved content and streamed tokens
                        responseBuilder.WriteString("</think>")
                        conn.WriteJSON(WSChatToken{Token: "</think>", Index: index})
                        index++
                    }

                    // Detect end tokens ONLY when stream is truly ending
                    endTokens := []string{
                        "<|end_of_text|>",
                        "<|end|>",
                        "<|im_end|>",
                        "<|eot_id|>",
                        "<|im_end|>",
                        "[|endofturn|]",
                    }

                    isEndToken := false
                    for _, t := range endTokens {
                        if token == t {
                            isEndToken = true
                            break
                        }
                    }
                    if isEndToken {
                        continue
                    }

                    // Normal answer token
                    responseBuilder.WriteString(token)
                    conn.WriteJSON(WSChatToken{Token: token, Index: index})
                    index++
                }
            }

            if chunk.FinishReason != "" {
                break
            }
        }

        // Close think if stream ended during reasoning with no final answer
        if inReasoning {
            responseBuilder.WriteString("</think>")
        }

        if !startTime.IsZero() {
            duration := time.Since(startTime).Seconds()
            if duration > 0 {
                toksPerSec = float64(index) / duration
            }
        }

        conn.WriteJSON(map[string]interface{}{
            "event":          "end",
            "tokens_per_sec": toksPerSec,
        })
        botResponse = responseBuilder.String()

    } else {
        log.Printf("[LLM-WS] ERROR: Critical LLM client not available")
        conn.WriteJSON(map[string]string{"error": "server misconfiguration: queue client missing"})
        return
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

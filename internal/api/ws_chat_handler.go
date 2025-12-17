package api

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"
	"context"
	"github.com/gin-gonic/gin"
	"github.com/gorilla/websocket"
	"go-llama/internal/auth"
	"go-llama/internal/chat"
	"go-llama/internal/memory"
	"go-llama/internal/config"
	"go-llama/internal/db"
)


// extractSiteFilter extracts a single site filter from the prompt for search optimization.
// Priority: actual URL > text-based site reference (e.g., "on reddit")
// Returns: (cleanedPrompt, siteHost)
func extractSiteFilter(prompt string) (string, string) {
	// Step 1: Check for actual URLs first (highest priority)
	urlPattern := regexp.MustCompile(`https?://[^\s]+|(?:www\.)?[a-zA-Z0-9-]+\.[a-zA-Z]{2,}[^\s]*`)
	urls := urlPattern.FindAllString(prompt, -1)
	
	if len(urls) > 0 {
		// Extract host from first URL found
		parsed, err := url.Parse(urls[0])
		if err == nil && parsed.Host != "" {
			// Remove URL from prompt and return
			cleanedPrompt := urlPattern.ReplaceAllString(prompt, " ")
			cleanedPrompt = strings.TrimSpace(regexp.MustCompile(`\s+`).ReplaceAllString(cleanedPrompt, " "))
			return cleanedPrompt, parsed.Host
		}
	}
	
	// Step 2: No URL found, check for text-based site references
	sitePatterns := map[*regexp.Regexp]string{
		regexp.MustCompile(`(?i)\bon reddit\b`):           "reddit.com",
		regexp.MustCompile(`(?i)\bfrom reddit\b`):         "reddit.com",
		regexp.MustCompile(`(?i)\bin reddit\b`):           "reddit.com",
		regexp.MustCompile(`(?i)\bon stackoverflow\b`):    "stackoverflow.com",
		regexp.MustCompile(`(?i)\bfrom stackoverflow\b`):  "stackoverflow.com",
		regexp.MustCompile(`(?i)\bon stack overflow\b`):   "stackoverflow.com",
		regexp.MustCompile(`(?i)\bon github\b`):           "github.com",
		regexp.MustCompile(`(?i)\bfrom github\b`):         "github.com",
		regexp.MustCompile(`(?i)\bon twitter\b`):          "twitter.com",
		regexp.MustCompile(`(?i)\bfrom twitter\b`):        "twitter.com",
		regexp.MustCompile(`(?i)\bon youtube\b`):          "youtube.com",
		regexp.MustCompile(`(?i)\bfrom youtube\b`):        "youtube.com",
		regexp.MustCompile(`(?i)\bon wikipedia\b`):        "wikipedia.org",
		regexp.MustCompile(`(?i)\bfrom wikipedia\b`):      "wikipedia.org",
		regexp.MustCompile(`(?i)\bon hackernews\b`):       "news.ycombinator.com",
		regexp.MustCompile(`(?i)\bon hacker news\b`):      "news.ycombinator.com",
		regexp.MustCompile(`(?i)\bsite:([a-zA-Z0-9.-]+)`): "$1", // Explicit site: operator
	}
	
	cleanedPrompt := prompt
	for pattern, domain := range sitePatterns {
		if pattern.MatchString(cleanedPrompt) {
			// Handle site: operator specially (capture group)
			if strings.Contains(pattern.String(), "site:") {
				matches := pattern.FindStringSubmatch(cleanedPrompt)
				if len(matches) > 1 {
					cleanedPrompt = pattern.ReplaceAllString(cleanedPrompt, "")
					cleanedPrompt = strings.TrimSpace(regexp.MustCompile(`\s+`).ReplaceAllString(cleanedPrompt, " "))
					return cleanedPrompt, matches[1]
				}
			} else {
				// Remove the site reference phrase
				cleanedPrompt = pattern.ReplaceAllString(cleanedPrompt, "")
				cleanedPrompt = strings.TrimSpace(regexp.MustCompile(`\s+`).ReplaceAllString(cleanedPrompt, " "))
				return cleanedPrompt, domain
			}
		}
	}
	
	// No site filter found
	return prompt, ""
}

// --- Auto Web Search Logic ---
func shouldAutoSearch(cfg *config.Config, prompt string) bool {
// Disable auto web search entirely if max results set to 0 or less
if cfg.SearxNG.MaxResults <= 0 {
    return false
}
	p := strings.ToLower(prompt)
	score := 0

	// User override: don't search
	denyList := []string{
	    "don't search",
	    "do not search",
	    "dont search",
	    "dont web search",
	    "do not web search",
	}

	for _, phrase := range denyList {
	    if strings.Contains(p, phrase) {
	        return false
	    }
	}

explicitSearchPhrases := []string{
    // Direct commands
    "search the web",
    "search online",
    "search the internet",
    "search web",
    "search internet",

    // Imperatives phrased differently
    "look it up online",
    "look online",
    "look this up online",
    "look it up on the web",

    // Requests based on web sources
    "use the web",
    "use internet",
    "use the internet",
    "use online sources",
    "use online information",
    "use web results",

    // Declarative forms
    "get info from the web",
    "get information from the web",
    "get online information",
    "find this online",
    "find information online",
    "verify this online",

    // Short common expressions
    "google it",
    "bing it",
    "check online",
    "check the web",
    "check the internet",
    "web search",
}

for _, phrase := range explicitSearchPhrases {
	if strings.Contains(p, phrase) {
		return true
	}
}


	// Recent years
	if strings.Contains(p, "2024") || strings.Contains(p, "2025") {
		score += 2
	}

	// Freshness words
	fresh := []string{"latest", "current", "today", "now"}
	for _, w := range fresh {
		if strings.Contains(p, w) {
			score += 2
			break
		}
	}

	// Price / rates
	priceWords := []string{"price", "rate", "convert", "exchange", "worth"}
	for _, w := range priceWords {
		if strings.Contains(p, w) {
			score += 2
			break
		}
	}

	// News / live info
	newsWords := []string{"news", "update", "trending", "live", "results"}
	for _, w := range newsWords {
		if strings.Contains(p, w) {
			score += 2
			break
		}
	}

	// Tickers
	if strings.Contains(p, "btc") || strings.Contains(p, "eth") ||
		strings.Contains(p, "aapl") || strings.Contains(p, "tsla") ||
		strings.Contains(p, "nvda") {
		score += 3
	}

	// Question pattern boost
	questionWords := []string{"who", "when", "where", "what", "will", "did", "does"}
	parts := strings.Fields(p)
	if len(parts) > 3 {
		for _, q := range questionWords {
			if strings.HasPrefix(p, q) {
				score++
				break
			}
		}
	}

// Extra positive search signals
if strings.HasSuffix(p, "?") {
    score++
}
if strings.Contains(p, " vs ") || strings.Contains(p, "compare") {
    score++
}
currencyHints := []string{"£", "$", "€", "price of", "how much is"}
for _, w := range currencyHints {
    if strings.Contains(p, w) {
        score++
        break
    }
}
recentPhrases := []string{"breaking", "just announced", "results today"}
for _, w := range recentPhrases {
    if strings.Contains(p, w) {
        score++
        break
    }
}

// Negative signals — reduce search likelihood
negativeWords := []string{"explain", "tutorial", "guide", "overview", "story", "fiction"}
for _, w := range negativeWords {
    if strings.Contains(p, w) {
        score -= 2
        break
    }
}


// Adjust search threshold based on prompt length
words := strings.Fields(p)
dynamicThreshold := 3 + (len(words) / 80) // +1 point per 80 words

return score >= dynamicThreshold
}

// WebSocket message format
type WSChatPrompt struct {
	ChatID    int    `json:"chatId"`
	Prompt    string `json:"prompt"`
	WebSearch bool   `json:"web_search"`
}

// WebSocket streaming token format
type WSChatToken struct {
	Token string `json:"token"`
	Index int    `json:"index"`
}

var wsUpgrader = websocket.Upgrader{
	CheckOrigin: func(r *http.Request) bool { return true },
}

// WebSocket connection wrapper with mutex for thread-safe writes
type safeWSConn struct {
	conn *websocket.Conn
	mu   sync.Mutex
}

func (s *safeWSConn) WriteJSON(v interface{}) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.conn.WriteJSON(v)
}

func (s *safeWSConn) ReadMessage() (int, []byte, error) {
	return s.conn.ReadMessage()
}

func (s *safeWSConn) Close() error {
	return s.conn.Close()
}

func WSChatHandler(cfg *config.Config) gin.HandlerFunc {
	return func(c *gin.Context) {
		token := c.GetHeader("Authorization")
		if token == "" {
			token = c.Query("token")
		}
		if token == "" {
			c.JSON(http.StatusUnauthorized, gin.H{"error": "missing JWT"})
			return
		}
		token = strings.TrimPrefix(token, "Bearer ")
		claims, err := auth.ParseJWT(cfg.Server.JWTSecret, token)
		if err != nil {
			c.JSON(http.StatusUnauthorized, gin.H{"error": "invalid JWT"})
			return
		}
		c.Set("userId", claims.UserID)

		rawConn, err := wsUpgrader.Upgrade(c.Writer, c.Request, nil)
		if err != nil {
			log.Println("WebSocket upgrade failed:", err)
			return
		}
		conn := &safeWSConn{conn: rawConn}
		defer conn.Close()

		_, msg, err := conn.ReadMessage()

		if err != nil {
			conn.WriteJSON(map[string]string{"error": "invalid initial payload"})
			return
		}
		var req WSChatPrompt
		if err := json.Unmarshal(msg, &req); err != nil {
			conn.WriteJSON(map[string]string{"error": "invalid JSON"})
			return
		}

		userID, ok := getUserIDFromContext(c)
		if !ok {
			conn.WriteJSON(map[string]string{"error": "unauthorized"})
			return
		}
		var chatInst chat.Chat
		if err := db.DB.Where("id = ? AND user_id = ?", req.ChatID, userID).First(&chatInst).Error; err != nil {
			conn.WriteJSON(map[string]string{"error": "chat not found"})
			return
		}

// Check if this is a GrowerAI chat
if chatInst.UseGrowerAI {
    // Handle GrowerAI via WebSocket
    handleGrowerAIWebSocket(conn, cfg, &chatInst, req.Prompt, userID)
    return
}

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
		var modelConfig *config.LLMConfig
		for i := range cfg.LLMs {
			if cfg.LLMs[i].Name == chatInst.ModelName {
				modelConfig = &cfg.LLMs[i]
				break
			}
		}
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

var allMessages []chat.Message
if err := db.DB.Where("chat_id = ?", chatInst.ID).Order("created_at asc").Find(&allMessages).Error; err != nil {
	conn.WriteJSON(map[string]string{"error": "failed to fetch chat history"})
	return
}
contextSize := modelConfig.ContextSize
if contextSize == 0 {
	contextSize = 2048 // Fallback default
}

// Determine if web search will happen FIRST (call shouldAutoSearch only once)
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

// Build sliding window ONCE with correct size
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
			searxngURL := cfg.SearxNG.URL
			if searxngURL == "" {
				searxngURL = "http://localhost:8888/search"
			}


// Build search query only if web search is triggered
var searchQuery string
var siteFilter string

// Build combined search context (last up to 3 user messages from full history)
var userPrompts []string
combinedLength := 0
const maxCombinedChars = 500
for i := len(allMessages) - 1; i >= 0 && len(userPrompts) < 3; i-- {
    if allMessages[i].Sender == "user" {
        if combinedLength + len(allMessages[i].Content) > maxCombinedChars {
            break
        }
        userPrompts = append([]string{allMessages[i].Content}, userPrompts...)
        combinedLength += len(allMessages[i].Content)
    }
}
combinedPrompt := strings.Join(userPrompts, " ")

// Extract site filter (URL or text-based site reference)
searchQuery, siteFilter = extractSiteFilter(combinedPrompt)

// Compress long queries
if len(strings.Fields(searchQuery)) > 20 {
    searchQuery = compressForSearch(searchQuery)
}

// Prepend site filter if found
if siteFilter != "" {
    searchQuery = "site:" + siteFilter + " " + searchQuery
}

httpResp, err := http.Get(searxngURL + "?q=" + url.QueryEscape(searchQuery) + "&format=json")
			if err == nil && httpResp.StatusCode == 200 {
				defer httpResp.Body.Close()
				var searxResp struct {
					Results []struct {
						Title   string `json:"title"`
						URL     string `json:"url"`
						Content string `json:"content"`
					} `json:"results"`
				}
if err := json.NewDecoder(httpResp.Body).Decode(&searxResp); err == nil {
	raw := searxResp.Results
	if len(raw) > 0 {
		// 1) Determine how many raw results we care about
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

		// 2) Rank those (returns top 80%, removing junk)
		ranked := rankAndFilterResults(combinedPrompt, tmpResults)

// Use SearxNG snippets directly (already ranked and filtered to top 80%)
for _, r := range ranked {
    if r.Title != "" && r.URL != "" {
        sources = append(sources, map[string]string{
            "title":   r.Title,
            "url":     r.URL,
            "snippet": r.Content, // Use SearxNG's original snippet
        })
        // Debug: Log what we're sending to LLM
        log.Printf("📄 Source [%s]: %s", r.Title, r.Content[:min(len(r.Content), 100)])
    }
}
			}
		}
			}
		
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
				"role":    "user",  // ← Changed from "system" to "user"
				"content": webContext,
			}
			llmMessages = append(llmMessages[:1], append([]map[string]string{webContextMsg}, llmMessages[1:]...)...)
		}
		
		// 🟡 Graceful fallback if no results
		if (req.WebSearch || autoSearch) && len(sources) == 0 {
			fallbackMsg := "Web search returned no results."
			llmMessages = append(llmMessages, map[string]string{
				"role":    "system",
				"content": fallbackMsg,
			})
		}
	}

		payload := map[string]interface{}{
			"model":    modelConfig.Name,
			"messages": llmMessages,
			"stream":   true,
		}
		if chatInst.LlmSessionID != "" && !modelMigrated {
			payload["session"] = chatInst.LlmSessionID
		}

		var botResponse string
		var toksPerSec float64
		err = streamLLMResponseWS(conn, rawConn, modelConfig.URL, payload, &botResponse, &toksPerSec)
		if err != nil {
			conn.WriteJSON(map[string]string{"error": "llm streaming failed", "detail": err.Error()})
			return
		}

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
}

// --- Streaming function ---
func streamLLMResponseWS(safeConn *safeWSConn, rawConn *websocket.Conn, llmURL string, payload map[string]interface{}, respOut *string, toksPerSecOut *float64) error {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go func() {
		for {
			_, msg, err := rawConn.ReadMessage()
			if err != nil {
				cancel() // WS closed
				return
			}
			var req map[string]interface{}
			if json.Unmarshal(msg, &req) == nil && req["event"] == "stop" {
				cancel() // Explicit stop message
				return
			}
		}
	}()

	body, _ := json.Marshal(payload)
	req, _ := http.NewRequestWithContext(ctx, "POST", llmURL, bytes.NewBuffer(body))
	req.Header.Set("Content-Type", "application/json")
	client := http.Client{Timeout: 0}
	resp, err := client.Do(req)
	if err != nil {
		log.Printf("LLM HTTP request failed: %v", err)
		return err
	}
	defer resp.Body.Close()

	reader := bufio.NewReader(resp.Body)
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
					Content string `json:"content"`
					ReasoningContent string `json:"reasoning_content"`
				} `json:"delta"`
			} `json:"choices"`
			FinishReason string `json:"finish_reason"`
		}
		if err := json.Unmarshal([]byte(data), &chunk); err != nil {
			log.Printf("stream decode error: %v", err)
			continue
		}
		log.Printf("WS LLM chunk: %+v", chunk)



if len(chunk.Choices) > 0 {
    delta := chunk.Choices[0].Delta

    // 1) Handle reasoning_content (new standard)
    if delta.ReasoningContent != "" {
        token := delta.ReasoningContent

        // Start timer when we receive first token of any kind
        if firstToken {
            startTime = time.Now()
            firstToken = false
        }

        // If this is the first reasoning chunk, open <think>
        if !inReasoning {
            inReasoning = true
            token = "<think>" + token
        }

        // Append reasoning to the accumulated response (to match old behaviour)
        responseBuilder.WriteString(token)

        // Stream to frontend
		safeConn.WriteJSON(WSChatToken{Token: token, Index: index})
        index++
    }

    // 2) Handle normal content (the actual answer)
    if delta.Content != "" {
        token := delta.Content

        // Start timer if we never got any reasoning
        if firstToken {
            startTime = time.Now()
            firstToken = false
        }

        // If we were in a reasoning section, close </think> before answer
        if inReasoning {
            inReasoning = false
            // Close the think block in both saved content and streamed tokens
            responseBuilder.WriteString("</think>")
            safeConn.WriteJSON(WSChatToken{Token: "</think>", Index: index})
            index++
        }

        // Detect end tokens ONLY when stream is truly ending
        endTokens := []string{
            "<|end_of_text|>",
            "<|end|>",
            "<|assistant|>",
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
		safeConn.WriteJSON(WSChatToken{Token: token, Index: index})
        index++
    }
}


		if chunk.FinishReason != "" {
			break
		}
	}

	if inReasoning {
    // Close think if stream ended during reasoning with no final answer
    responseBuilder.WriteString("</think>")
	}

	var toksPerSec float64
	if !startTime.IsZero() {
	    duration := time.Since(startTime).Seconds()
	    if duration > 0 {
	        toksPerSec = float64(index) / duration
	    }
	}

	safeConn.WriteJSON(map[string]interface{}{
		"event":          "end",
		"tokens_per_sec": toksPerSec,
	})
	*respOut = responseBuilder.String()
	*toksPerSecOut = toksPerSec
	return nil
}
// Known geographic & political keywords to preserve during search compression
var geoTokens = map[string]bool{
    "uk":true, "united kingdom":true, "britain":true, "british":true, "england":true, "scotland":true, "wales":true, "london":true,
    "us":true, "usa":true, "united states":true, "american":true, "america":true, "washington":true,
    "canada":true, "canadian":true, "ottawa":true,
    "australia":true, "australian":true, "sydney":true,
    "europe":true, "eu":true, "european":true,
    "france":true, "french":true, "paris":true,
    "germany":true, "german":true, "berlin":true,
    "italy":true, "italian":true, "rome":true,
    "spain":true, "spanish":true, "madrid":true,
    "japan":true, "japanese":true, "tokyo":true,
    "china":true, "chinese":true, "beijing":true,
    "india":true, "indian":true, "delhi":true,
    "russia":true, "russian":true, "moscow":true,
    "ukraine":true, "ukrainian":true, "kyiv":true,
    "brazil":true, "brazilian":true, "brasilia":true,
}

// handleGrowerAIWebSocket processes GrowerAI messages via WebSocket with streaming
func handleGrowerAIWebSocket(conn *safeWSConn, cfg *config.Config, chatInst *chat.Chat, content string, userID uint) {
	log.Printf("[GrowerAI-WS] Processing message from user %d in chat %d", userID, chatInst.ID)
	
	if cfg.GrowerAI.ReasoningModel.URL == "" {
		conn.WriteJSON(map[string]string{"error": "GrowerAI not configured"})
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
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
		IncludeCollective: true,
		Limit:             5,
		MinScore:          0.5,
	}

	log.Printf("[GrowerAI-WS] Searching memory (user=%s, min_score=0.5)...", userIDStr)
	results, err := storage.Search(ctx, query, queryEmbedding)
	if err != nil {
		log.Printf("[GrowerAI-WS] WARNING: Memory search failed: %v", err)
		results = []memory.RetrievalResult{}
	}
	log.Printf("[GrowerAI-WS] ✓ Found %d relevant memories", len(results))

	// Build context with retrieved memories
	var contextBuilder strings.Builder
	contextBuilder.WriteString("You are GrowerAI, an AI system that learns and improves from conversations.\n\n")
	
	if len(results) > 0 {
		contextBuilder.WriteString("=== RELEVANT MEMORIES ===\n")
		for i, result := range results {
			log.Printf("[GrowerAI-WS]   Memory %d: score=%.3f, tier=%s, age=%s", 
				i+1, result.Score, result.Memory.Tier, 
				time.Since(result.Memory.CreatedAt).Round(time.Minute))
			contextBuilder.WriteString(fmt.Sprintf("[Memory %d - %.0f%% relevant - from %s ago]\n%s\n\n",
				i+1,
				result.Score*100,
				time.Since(result.Memory.CreatedAt).Round(time.Minute),
				result.Memory.Content))
		}
		contextBuilder.WriteString("=== END MEMORIES ===\n\n")
	} else {
		log.Printf("[GrowerAI-WS]   No relevant memories found")
	}

	contextBuilder.WriteString(fmt.Sprintf("User's current message: %s\n\n", content))
	contextBuilder.WriteString("Respond naturally, incorporating relevant context from memories if available.")

	// Call LLM with enhanced context (streaming)
	llmMessages := []map[string]string{
		{
			"role":    "system",
			"content": contextBuilder.String(),
		},
	}

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

	// Evaluate what to store in memory
	shouldStore := len(content) > 20 && len(botResponse) > 20
	
	if shouldStore {
		log.Printf("[GrowerAI-WS] Evaluating memory storage...")
		
		memoryContent := fmt.Sprintf("User asked: %s\nAssistant responded: %s", 
			content, truncate(botResponse, 200))
		
		memEmbedding, err := embedder.Embed(ctx, memoryContent)
		if err != nil {
			log.Printf("[GrowerAI-WS] WARNING: Failed to generate memory embedding: %v", err)
		} else {
			importanceScore := 0.5
			if len(content) > 100 {
				importanceScore += 0.2
			}
			if len(results) > 0 {
				importanceScore += 0.1
			}
			
			mem := &memory.Memory{
				Content:         memoryContent,
				Tier:            memory.TierRecent,
				UserID:          &userIDStr,
				IsCollective:    false,
				CreatedAt:       time.Now(),
				LastAccessedAt:  time.Now(),
				AccessCount:     0,
				ImportanceScore: importanceScore,
				Embedding:       memEmbedding,
				Metadata: map[string]interface{}{
					"chat_id": chatInst.ID,
				},
			}
			
			if err := storage.Store(ctx, mem); err != nil {
				log.Printf("[GrowerAI-WS] WARNING: Failed to store memory: %v", err)
			} else {
				log.Printf("[GrowerAI-WS] ✓ Stored memory (id=%s, importance=%.2f)", 
					mem.ID, mem.ImportanceScore)
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

// Compress long prompts into clean search queries
func compressForSearch(prompt string) string {
    words := strings.Fields(prompt)
    if len(words) <= 20 {
        return prompt
    }

    p := strings.ToLower(prompt)

    stopwords := map[string]bool{
        "a":true,"an":true,"the":true,"of":true,"for":true,"to":true,"in":true,"on":true,"with":true,"and":true,"or":true,
        "by":true,"be":true,"is":true,"are":true,"that":true,"which":true,"this":true,"those":true,"about":true,"can":true,
        "you":true,"could":true,"would":true,"please":true,"tell":true,"me":true,"explain":true,"give":true,"i":true,"want":true,
    }

    tokens := strings.Fields(p)
    var keep []string

    for _, t := range tokens {
        if stopwords[t] {
            continue
        }
        // keep numbers and years
        if strings.ContainsAny(t, "0123456789") {
            keep = append(keep, t)
            continue
        }
        // known tickers
        if t == "btc" || t == "eth" || t == "aapl" || t == "tsla" || t == "nvda" {
            keep = append(keep, t)
            continue
        }
// keep geo tokens even if short (e.g., "uk", "us", "eu")
if len(t) <= 2 && !geoTokens[t] {
    continue
}

        keep = append(keep, t)
    }

    // limit to 12 keywords max
    if len(keep) > 12 {
        keep = keep[:12]
    }

    if len(keep) < 3 {
        return prompt
    }
// Ensure the original geo context is not lost
for key := range geoTokens {
    if strings.Contains(p, key) {
        found := false
        for _, k := range keep {
            if k == key {
                found = true
                break
            }
        }
        if !found {
            keep = append(keep, key)
        }
    }
}

    return strings.Join(keep, " ")
}

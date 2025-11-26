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
	"go-llama/internal/config"
	"go-llama/internal/db"
)

// extractURLFromPrompt finds and removes URLs from prompt, returning (cleanedPrompt, extractedURL)
func extractURLFromPrompt(prompt string) (string, string) {
	urlPattern := regexp.MustCompile(`https?://[^\s]+|(?:www\.)?[a-zA-Z0-9-]+\.[a-zA-Z]{2,}[^\s]*`)
	urls := urlPattern.FindAllString(prompt, -1)
	cleanedPrompt := urlPattern.ReplaceAllString(prompt, " ")
	cleanedPrompt = strings.TrimSpace(regexp.MustCompile(`\s+`).ReplaceAllString(cleanedPrompt, " "))
	
	if len(urls) > 0 {
		return cleanedPrompt, urls[0]
	}
	return cleanedPrompt, ""
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
		
		// We'll adjust context size after we know if web search is happening
		// For now, build with full context and adjust later if needed
		messages := chat.BuildSlidingWindow(allMessages, contextSize)

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

		// Estimate web context size if search will happen
		// Calculate based on actual results for better space utilization
		webContextSize := 0
		willSearch := req.WebSearch || shouldAutoSearch(cfg, req.Prompt)
		if willSearch {
			// Initial rough estimate before we have actual sources
			estimatedResults := cfg.SearxNG.MaxResults / 2 // We keep top 50%
			if estimatedResults < 1 {
				estimatedResults = 1
			}
			// Estimate: ~50 tokens per result (title + snippet + URL) + overhead
			webContextSize = (estimatedResults * 50) + 50
		}
		
		// Rebuild sliding window if we need to account for web context
		if webContextSize > 0 {
			adjustedContextSize := contextSize - webContextSize
			if adjustedContextSize < 512 {
				adjustedContextSize = 512 // Minimum context for history
			}
			messages = chat.BuildSlidingWindow(allMessages, adjustedContextSize)
			
			// Rebuild llmMessages with adjusted history
			llmMessages = []map[string]string{}
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
		}

// Single, concise system instruction optimized for small models (1B-10B)
currentTime := time.Now().UTC().Format("2006-01-02 15:04")
systemInstruction := fmt.Sprintf("Today is %s UTC. Be direct and helpful.", currentTime)

llmMessages = append([]map[string]string{
    {"role": "system", "content": systemInstruction},
}, llmMessages...)

		var sources []map[string]string
if req.Prompt == "" {
	conn.WriteJSON(map[string]string{"error": "missing prompt"})
	return
}

// --- AUTO WEB SEARCH DECISION ---
autoSearch := false

if !req.WebSearch {
if shouldAutoSearch(cfg, req.Prompt) {
        autoSearch = true
    }
}


// Notify UI if auto triggered
if autoSearch && !req.WebSearch {
	conn.WriteJSON(map[string]string{"auto_search": "true"})
}


		if req.WebSearch || autoSearch {
			searxngURL := cfg.SearxNG.URL
			if searxngURL == "" {
				searxngURL = "http://localhost:8888/search"
			}

// Build combined search context (last up to 3 user messages)
// with character limit to avoid processing huge concatenations
var userPrompts []string
combinedLength := 0
const maxCombinedChars = 500

for i := len(messages) - 1; i >= 0 && len(userPrompts) < 3; i-- {
    if messages[i].Sender == "user" {
        if combinedLength + len(messages[i].Content) > maxCombinedChars {
            break
        }
        userPrompts = append([]string{messages[i].Content}, userPrompts...)
        combinedLength += len(messages[i].Content)
    }
}

combinedPrompt := strings.Join(userPrompts, " ")

// --- Extract URL from prompt first ---
cleanedFromURL, extractedURL := extractURLFromPrompt(combinedPrompt)

// --- Then detect site-specific search phrases ---
cleanPrompt, siteDomain := extractSiteQuery(cleanedFromURL)
searchQuery := cleanPrompt
if len(strings.Fields(searchQuery)) > 20 {
    searchQuery = compressForSearch(searchQuery)
}

// Apply site filter: prioritize extracted URL, then detected domain
if extractedURL != "" {
    parsed, err := url.Parse(extractedURL)
    if err == nil && parsed.Host != "" {
        searchQuery = "site:" + parsed.Host + " " + searchQuery
    }
} else if siteDomain != "" {
    searchQuery = "site:" + siteDomain + " " + searchQuery
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

		// 2) Rank those and keep top 50% of *limit*
		ranked := rankAndFilterResults(combinedPrompt, tmpResults)

		keepTop := limit / 2
		if keepTop < 1 {
			keepTop = 1
		}
		if keepTop > len(ranked) {
			keepTop = len(ranked)
		}

		// Parallel enrichment for better performance
		type enrichResult struct {
			index   int
			title   string
			url     string
			snippet string
		}

		enrichChan := make(chan enrichResult, keepTop)
		var wg sync.WaitGroup

		for i := 0; i < keepTop; i++ {
			wg.Add(1)
			go func(idx int, r SearxResult) {
				defer wg.Done()
				enrichedSnippet := enrichAndSummarize(r.URL, r.Content, searchQuery)
				enrichChan <- enrichResult{
					index:   idx,
					title:   r.Title,
					url:     r.URL,
					snippet: enrichedSnippet,
				}
			}(i, ranked[i])
		}

		go func() {
			wg.Wait()
			close(enrichChan)
		}()

		// Collect results in original order
		enriched := make([]enrichResult, keepTop)
		for res := range enrichChan {
			enriched[res.index] = res
		}

		for _, e := range enriched {
			if e.title != "" && e.url != "" {
				sources = append(sources, map[string]string{
					"title":   e.title,
					"url":     e.url,
					"snippet": e.snippet,
				})
			}
		}
	}
}


			}

			// Recalculate actual web context size now that we have real sources
			if len(sources) > 0 {
				// Calculate actual token usage from sources
				actualWebSize := 0
				for _, src := range sources {
					// Rough estimate: title + snippet + URL ≈ 4 chars per token
					actualWebSize += (len(src["title"]) + len(src["snippet"]) + len(src["url"])) / 4
				}
				actualWebSize += 50 // Overhead for formatting
				
				// If actual size is very different from estimate, rebuild sliding window
				if actualWebSize < webContextSize-200 || actualWebSize > webContextSize+200 {
					adjustedContextSize := contextSize - 50 - actualWebSize - 100 // system msg + web + safety
					if adjustedContextSize < 512 {
						adjustedContextSize = 512
					}
					
					messages = chat.BuildSlidingWindow(allMessages, adjustedContextSize)
					
					// Rebuild llmMessages with better-fitted history
					llmMessages = []map[string]string{}
					currentTime := time.Now().UTC().Format("2006-01-02 15:04")
					systemInstruction := fmt.Sprintf("Today is %s UTC. Be direct and helpful.", currentTime)
					llmMessages = append(llmMessages, map[string]string{
						"role":    "system",
						"content": systemInstruction,
					})
					
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
				}
			}
			
			// 🟡 Graceful fallback if no results
			if (req.WebSearch || autoSearch) && len(sources) == 0 {
				fallbackMsg := "Web search returned no results."
				llmMessages = append(llmMessages, map[string]string{
					"role":    "system",
					"content": fallbackMsg,
				})
			}
			

			if len(sources) > 0 {
				var webContextBuilder strings.Builder
				webContextBuilder.WriteString("Search results:\n")
				for i, src := range sources {
					webContextBuilder.WriteString("[")
					webContextBuilder.WriteString(strconv.Itoa(i+1))
					webContextBuilder.WriteString("] ")
					webContextBuilder.WriteString(src["title"])
					webContextBuilder.WriteString(": ")
					webContextBuilder.WriteString(src["snippet"])
					webContextBuilder.WriteString(" (")
					webContextBuilder.WriteString(src["url"])
					webContextBuilder.WriteString(")\n")
				}
				webContextBuilder.WriteString("\nCite sources as [1], [2].")
				
				webContext := webContextBuilder.String()

				llmMessages = append(llmMessages, map[string]string{
					"role":    "system",
					"content": webContext,
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

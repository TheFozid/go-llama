package api

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
	"context"
	"github.com/gin-gonic/gin"
	"github.com/gorilla/websocket"
	"go-llama/internal/auth"
	"go-llama/internal/chat"
	"go-llama/internal/config"
	"go-llama/internal/db"
)

// --- Auto Web Search Logic ---
func shouldAutoSearch(prompt string) bool {
	p := strings.ToLower(prompt)
	score := 0

	// User override: don't search
	if strings.Contains(p, "don't search") || strings.Contains(p, "do not search") {
		return false
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

	return score >= 3
}

func tinyModelThinksWebNeeded(modelURL, prompt string) bool {
	payload := map[string]interface{}{
		"model": "", // filled later by handler's first model
		"messages": []map[string]string{
			{
				"role": "system",
				"content": "Answer only yes or no. Does this query require up-to-date external information from the web?",
			},
			{
				"role": "user",
				"content": prompt,
			},
		},
		"max_tokens": 2,
	}

	body, _ := json.Marshal(payload)
	req, _ := http.NewRequest("POST", modelURL, bytes.NewBuffer(body))
	req.Header.Set("Content-Type", "application/json")
	client := http.Client{Timeout: 2 * time.Second}
	res, err := client.Do(req)
	if err != nil {
		return false
	}
	defer res.Body.Close()

	var resp struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
	}
	_ = json.NewDecoder(res.Body).Decode(&resp)

	if len(resp.Choices) > 0 {
		txt := strings.ToLower(strings.TrimSpace(resp.Choices[0].Message.Content))
		return strings.HasPrefix(txt, "y")
	}
	return false
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

		conn, err := wsUpgrader.Upgrade(c.Writer, c.Request, nil)
		if err != nil {
			log.Println("WebSocket upgrade failed:", err)
			return
		}
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

mdInstruction := `
Write clearly and naturally.
Use light Markdown formatting only when it improves clarity.

Rules:
- Short direct answers should be plain text.
- For longer or structured answers, use minimal Markdown:
  - Bullet points for lists
  - Bold for emphasis when helpful
  - Headings sparingly (only if they genuinely help)
  - Code blocks only for code or technical output
- Do not format just for decoration or style. Clarity first, formatting second.
`

		llmMessages = append([]map[string]string{
			{"role": "system", "content": mdInstruction},
		}, llmMessages...)

// Per-turn light markdown reminder
llmMessages = append([]map[string]string{
    {"role": "system", "content": "(Formatting note: Use light Markdown only if it improves clarity.)"},
}, llmMessages...)


		var sources []map[string]string
		maxResults := 4 // default
		if cfg.SearxNG.MaxResults > 0 {
			maxResults = cfg.SearxNG.MaxResults
		}
if req.Prompt == "" {
	conn.WriteJSON(map[string]string{"error": "missing prompt"})
	return
}

// --- AUTO WEB SEARCH DECISION ---
autoSearch := false

if !req.WebSearch { // only if user didn't manually enable
	if shouldAutoSearch(req.Prompt) {
		autoSearch = true
	} else {
		tinyModel := cfg.LLMs[0].URL
		if tinyModelThinksWebNeeded(tinyModel, req.Prompt) {
			autoSearch = true
		}
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
			httpResp, err := http.Get(searxngURL + "?q=" + url.QueryEscape(req.Prompt) + "&format=json")
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
					for _, r := range searxResp.Results {
						sources = append(sources, map[string]string{
							"title":   r.Title,
							"url":     r.URL,
							"snippet": r.Content,
						})
						if len(sources) >= maxResults {
							break
						}
					}
				}
			}
			if len(sources) > 0 {
webContext := "Web search results:\n"
for i, src := range sources {
    webContext += "[" + strconv.Itoa(i+1) + "] " + src["title"] + ": " + src["snippet"] + " -> URL: " + src["url"] + "\n"
}
webContext += `

Use your own knowledge and the information above to answer the user's question.

If you use a web result, cite it inline like [1].
If you choose to add a hyperlink, use: [1](matching URL).

Do not include a list of references and do not repeat URLs at the end.
Format the answer in Markdown when helpful, but keep the message natural.

Question: ` + req.Prompt + "\n"

				llmMessages = append([]map[string]string{
					{"role": "user", "content": webContext},
				}, llmMessages[1:]...)
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
		err = streamLLMResponseWS(conn, modelConfig.URL, payload, &botResponse, &toksPerSec)
		if err != nil {
			conn.WriteJSON(map[string]string{"error": "llm streaming failed", "detail": err.Error()})
			return
		}

		if strings.TrimSpace(botResponse) != "" {
			botResponseWithStats := botResponse + "\n\n_Tokens/sec: " + fmt.Sprintf("%.2f", toksPerSec) + "_"

	// Append sources if web search was used
if (req.WebSearch || autoSearch) && len(sources) > 0 {
	    botResponseWithStats += "\n\n**Sources:**\n"
	    for i, src := range sources {
	        botResponseWithStats += fmt.Sprintf("%d. [%s](%s)\n", i+1, src["title"], src["url"])
	    }
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
func streamLLMResponseWS(conn *websocket.Conn, llmURL string, payload map[string]interface{}, respOut *string, toksPerSecOut *float64) error {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go func() {
		for {
			_, msg, err := conn.ReadMessage()
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
				} `json:"delta"`
			} `json:"choices"`
			FinishReason string `json:"finish_reason"`
		}
		if err := json.Unmarshal([]byte(data), &chunk); err != nil {
			log.Printf("stream decode error: %v", err)
			continue
		}
		log.Printf("WS LLM chunk: %+v", chunk)

if len(chunk.Choices) > 0 && chunk.Choices[0].Delta.Content != "" {
    token := chunk.Choices[0].Delta.Content

    // Start timer when we receive first token
    if firstToken {
        startTime = time.Now()
        firstToken = false
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

    // Soft stop — skip outputting the end token
    if isEndToken {
        continue
    }

    // Normal token streaming
    responseBuilder.WriteString(token)
    conn.WriteJSON(WSChatToken{Token: token, Index: index})
    index++
}


		if chunk.FinishReason != "" {
			break
		}
	}
	var toksPerSec float64
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
	*respOut = responseBuilder.String()
	*toksPerSecOut = toksPerSec
	return nil
}

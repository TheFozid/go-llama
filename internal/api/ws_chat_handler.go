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
		if req.Prompt == "" {
			conn.WriteJSON(map[string]string{"error": "missing prompt"})
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
Please format your response in valid Markdown. Use headings, lists, and code blocks where appropriate.
Do not change the meaning, tone, or structure of the content.
`
		llmMessages = append([]map[string]string{
			{"role": "system", "content": mdInstruction},
		}, llmMessages...)

		var sources []map[string]string
		maxResults := 4 // default
		if cfg.SearxNG.MaxResults > 0 {
			maxResults = cfg.SearxNG.MaxResults
		}
		if req.WebSearch {
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
					webContext += "[" + strconv.Itoa(i+1) + "] \"" + src["title"] + "\": " + src["snippet"] + " (" + src["url"] + ")\n"
				}
				webContext += `
Using only the above web results and your own knowledge, please answer the following user question clearly and accurately.
Format your response in valid Markdown. Use headings, lists, and code blocks where appropriate.
Do not change the meaning, tone, or structure of the content.
Include citations or references for any referenced sources in markdown format.

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
	startTime := time.Now()

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
			responseBuilder.WriteString(token)
			conn.WriteJSON(WSChatToken{Token: token, Index: index})
			index++
		}
		if chunk.FinishReason != "" {
			break
		}
	}
	duration := time.Since(startTime).Seconds()
	toksPerSec := 0.0
	if duration > 0 {
		toksPerSec = float64(index) / duration
	}
	conn.WriteJSON(map[string]interface{}{
		"event":          "end",
		"tokens_per_sec": toksPerSec,
	})
	*respOut = responseBuilder.String()
	*toksPerSecOut = toksPerSec
	return nil
}

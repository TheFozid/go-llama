package api

import (
	"bytes"
	"encoding/json"
	"io"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"
	"strconv"

	"github.com/gin-gonic/gin"
	"go-llama/internal/chat"
	"go-llama/internal/config"
	"go-llama/internal/db"
	"gorm.io/gorm"
)

// Helper to extract user ID from context
func getUserIDFromContext(c *gin.Context) (uint, bool) {
	idVal, exists := c.Get("userId")
	if !exists {
		return 0, false
	}
	switch v := idVal.(type) {
	case uint:
		return v, true
	case int:
		return uint(v), true
	case float64:
		return uint(v), true
	default:
		return 0, false
	}
}

// List available LLM models
func ListLLMsHandler(cfg *config.Config) gin.HandlerFunc {
	return func(c *gin.Context) {
		models := make([]map[string]string, len(cfg.LLMs))
		for i, model := range cfg.LLMs {
			models[i] = map[string]string{
				"name": model.Name,
				"url":  model.URL,
			}
		}
		c.JSON(http.StatusOK, models)
	}
}

// Create a new chat, allow model selection
func CreateChatHandler(cfg *config.Config) gin.HandlerFunc {
	return func(c *gin.Context) {
		userID, ok := getUserIDFromContext(c)
		if !ok {
			c.JSON(http.StatusUnauthorized, gin.H{"error": "unauthorized"})
			return
		}
		var req struct {
			Title     string `json:"title"`
			ModelName string `json:"model_name"`
		}
		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request"})
			return
		}
		modelName := req.ModelName
		if modelName == "" && len(cfg.LLMs) > 0 {
			modelName = cfg.LLMs[0].Name
		}
		// Check model exists
		modelExists := false
		for _, m := range cfg.LLMs {
			if m.Name == modelName {
				modelExists = true
				break
			}
		}
		if !modelExists {
			c.JSON(http.StatusBadRequest, gin.H{"error": "model not available"})
			return
		}
		chatInst := chat.Chat{
			Title:        req.Title,
			UserID:       userID,
			ModelName:    modelName,
			LlmSessionID: "",
			CreatedAt:    time.Now(),
		}
		if err := db.DB.Create(&chatInst).Error; err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to create chat"})
			return
		}
		c.JSON(http.StatusCreated, gin.H{
			"id":        chatInst.ID,
			"title":     chatInst.Title,
			"model":     chatInst.ModelName,
			"createdAt": chatInst.CreatedAt,
		})
	}
}

// List all chats for the current user (only chats with at least one user message)
func ListChatsHandler() gin.HandlerFunc {
	return func(c *gin.Context) {
		userID, ok := getUserIDFromContext(c)
		if !ok {
			c.JSON(http.StatusUnauthorized, gin.H{"error": "unauthorized"})
			return
		}
		var chats []chat.Chat
		// Only include chats with at least one user message (prompt)
		if err := db.DB.
			Where("user_id = ?", userID).
			Where("id IN (SELECT chat_id FROM messages WHERE sender = 'user')").
			Order("created_at desc").
			Find(&chats).Error; err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to fetch chats"})
			return
		}
		c.JSON(http.StatusOK, chats)
	}
}

// Edit chat title (fixed: parse id to uint)
func EditChatTitleHandler() gin.HandlerFunc {
	return func(c *gin.Context) {
		userID, ok := getUserIDFromContext(c)
		if !ok {
			c.JSON(http.StatusUnauthorized, gin.H{"error": "unauthorized"})
			return
		}
		idStr := c.Param("id")
		idUint, err := strconv.ParseUint(idStr, 10, 64)
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid chat id"})
			return
		}
		var req struct {
			Title string `json:"title"`
		}
		if err := c.ShouldBindJSON(&req); err != nil || req.Title == "" {
			c.JSON(http.StatusBadRequest, gin.H{"error": "missing title"})
			return
		}
		var chatInst chat.Chat
		if err := db.DB.Where("id = ? AND user_id = ?", idUint, userID).First(&chatInst).Error; err != nil {
			c.JSON(http.StatusNotFound, gin.H{"error": "chat not found"})
			return
		}
		chatInst.Title = req.Title
		if err := db.DB.Save(&chatInst).Error; err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to update title"})
			return
		}
		c.JSON(http.StatusOK, gin.H{"id": chatInst.ID, "title": chatInst.Title})
	}
}

// Delete chat (fixed: parse id to uint)
func DeleteChatHandler() gin.HandlerFunc {
	return func(c *gin.Context) {
		userID, ok := getUserIDFromContext(c)
		if !ok {
			c.JSON(http.StatusUnauthorized, gin.H{"error": "unauthorized"})
			return
		}
		idStr := c.Param("id")
		idUint, err := strconv.ParseUint(idStr, 10, 64)
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid chat id"})
			return
		}
		var chatInst chat.Chat
		if err := db.DB.Where("id = ? AND user_id = ?", idUint, userID).First(&chatInst).Error; err != nil {
			c.JSON(http.StatusNotFound, gin.H{"error": "chat not found"})
			return
		}
		if err := db.DB.Where("chat_id = ?", chatInst.ID).Delete(&chat.Message{}).Error; err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to delete messages"})
			return
		}
		if err := db.DB.Delete(&chatInst).Error; err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to delete chat"})
			return
		}
		c.JSON(http.StatusOK, gin.H{"deleted": true})
	}
}

// Get a single chat by ID for the current user (fixed: parse id to uint)
func GetChatHandler() gin.HandlerFunc {
	return func(c *gin.Context) {
		userID, ok := getUserIDFromContext(c)
		if !ok {
			c.JSON(http.StatusUnauthorized, gin.H{"error": "unauthorized"})
			return
		}
		idStr := c.Param("id")
		idUint, err := strconv.ParseUint(idStr, 10, 64)
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid chat id"})
			return
		}
		var chatInst chat.Chat
		if err := db.DB.Where("id = ? AND user_id = ?", idUint, userID).First(&chatInst).Error; err != nil {
			if err == gorm.ErrRecordNotFound {
				c.JSON(http.StatusNotFound, gin.H{"error": "chat not found"})
			} else {
				c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to fetch chat"})
			}
			return
		}
		c.JSON(http.StatusOK, chatInst)
	}
}

// List all messages in a chat (fixed: parse id to uint)
func ListMessagesHandler() gin.HandlerFunc {
	return func(c *gin.Context) {
		userID, ok := getUserIDFromContext(c)
		if !ok {
			c.JSON(http.StatusUnauthorized, gin.H{"error": "unauthorized"})
			return
		}
		idStr := c.Param("id")
		idUint, err := strconv.ParseUint(idStr, 10, 64)
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid chat id"})
			return
		}
		var chatInst chat.Chat
		if err := db.DB.Where("id = ? AND user_id = ?", idUint, userID).First(&chatInst).Error; err != nil {
			c.JSON(http.StatusNotFound, gin.H{"error": "chat not found"})
			return
		}
		var messages []chat.Message
		if err := db.DB.Where("chat_id = ?", chatInst.ID).Order("created_at asc").Find(&messages).Error; err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to fetch messages"})
			return
		}
		c.JSON(http.StatusOK, messages)
	}
}
// Send a message in a chat (calls LLM, supports optional web search)
func SendMessageHandler(cfg *config.Config) gin.HandlerFunc {
	return func(c *gin.Context) {
		userID, ok := getUserIDFromContext(c)
		if !ok {
			c.JSON(http.StatusUnauthorized, gin.H{"error": "unauthorized"})
			return
		}
		idStr := c.Param("id")
		idUint, err := strconv.ParseUint(idStr, 10, 64)
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid chat id"})
			return
		}
		var chatInst chat.Chat
		if err := db.DB.Where("id = ? AND user_id = ?", idUint, userID).First(&chatInst).Error; err != nil {
			c.JSON(http.StatusNotFound, gin.H{"error": "chat not found"})
			return
		}

		var req struct {
			Content   string `json:"content"`
			WebSearch bool   `json:"web_search"`
		}
		if err := c.ShouldBindJSON(&req); err != nil || req.Content == "" {
			c.JSON(http.StatusBadRequest, gin.H{"error": "missing content"})
			return
		}

		// Save user's message
		userMsg := chat.Message{
			ChatID:    chatInst.ID,
			Sender:    "user",
			Content:   req.Content,
			CreatedAt: time.Now(),
		}
		if err := db.DB.Create(&userMsg).Error; err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to save message"})
			return
		}

		// Find the configured model
		var modelConfig *config.LLMConfig
		for i := range cfg.LLMs {
			if cfg.LLMs[i].Name == chatInst.ModelName {
				modelConfig = &cfg.LLMs[i]
				break
			}
		}
		modelMigrated := false
		oldModel := chatInst.ModelName
		if modelConfig == nil && len(cfg.LLMs) > 0 {
			// Model was removed, migrate to first available
			modelConfig = &cfg.LLMs[0]
			chatInst.ModelName = modelConfig.Name
			chatInst.LlmSessionID = ""
			modelMigrated = true
		}
		if modelConfig == nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "no models available"})
			return
		}

		// Build messages history for LLM if needed (on session migration or model migration)
		var messages []chat.Message
		if modelMigrated || chatInst.LlmSessionID == "" {
			// Re-feed entire history
			if err := db.DB.Where("chat_id = ?", chatInst.ID).Order("created_at asc").Find(&messages).Error; err != nil {
				c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to fetch chat history"})
				return
			}
		} else {
			// Only need the latest user message
			messages = []chat.Message{userMsg}
		}

		// Prepare OpenAI API-compatible messages array
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

		// --- NEW: Handle optional web search ---
		var sources []map[string]string
		if req.WebSearch {
			// Call SearxNG for search results
			searxngURL := cfg.SearxNG.URL
			if searxngURL == "" {
				searxngURL = "http://localhost:8888/search"
			}
			httpResp, err := http.Get(fmt.Sprintf("%s?q=%s&format=json", searxngURL, url.QueryEscape(req.Content)))
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
						if len(sources) >= 4 {
							break
						}
					}
				}
			}
			// Prepend a formatted context to the prompt if results exist
			if len(sources) > 0 {
				webContext := "Web search results:\n"
				for i, src := range sources {
					webContext += fmt.Sprintf("[%d] \"%s\": %s (%s)\n", i+1, src["title"], src["snippet"], src["url"])
				}
				webContext += "\nUsing only the above web results and your own knowledge, answer the following question. Cite [n] where you use web results.\n"
				// Insert as the first user message (preserve any real user messages after it)
				llmMessages = append([]map[string]string{
					{"role": "user", "content": webContext + req.Content},
				}, llmMessages[1:]...)
			}
		}

		// Prepare LLM API payload
		payload := map[string]interface{}{
			"model":    modelConfig.Name,
			"messages": llmMessages,
		}
		if chatInst.LlmSessionID != "" && !modelMigrated {
			payload["session"] = chatInst.LlmSessionID
		}

		// Call LLM API and handle possible session errors
		llmResp, sessionErr := CallLLM(modelConfig.URL, payload)

		// If session error, re-feed entire history as new session
		if sessionErr == ErrLLMSession {
			// Clear session and re-feed full history
			payload["session"] = nil
			if err := db.DB.Where("chat_id = ?", chatInst.ID).Order("created_at asc").Find(&messages).Error; err != nil {
				c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to fetch chat history"})
				return
			}
			llmMessages = llmMessages[:0]
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
			payload["messages"] = llmMessages
			llmResp, sessionErr = CallLLM(modelConfig.URL, payload)
			modelMigrated = modelMigrated || sessionErr == nil // treat as migration for frontend
		}
		if sessionErr != nil {
			c.JSON(http.StatusBadGateway, gin.H{"error": "llm failure", "detail": sessionErr.Error()})
			return
		}

		// Parse LLM response
		botReply := llmResp.Reply
		tokens := llmResp.Tokens
		tokensPerSec := llmResp.TokensPerSec
		sessionID := llmResp.SessionID

		// Save bot message
		botMsg := chat.Message{
			ChatID:    chatInst.ID,
			Sender:    "bot",
			Content:   botReply,
			CreatedAt: time.Now(),
		}
		if err := db.DB.Create(&botMsg).Error; err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to save bot message"})
			return
		}
		// Update chat with new session id/model (if needed)
		if chatInst.LlmSessionID != sessionID || modelMigrated {
			db.DB.Model(&chatInst).Updates(map[string]interface{}{
				"llm_session_id": sessionID,
				"model_name":     chatInst.ModelName,
			})
		}

		resp := gin.H{
			"reply": map[string]interface{}{
				"id":                botMsg.ID,
				"sender":            "bot",
				"content":           botReply,
				"createdAt":         botMsg.CreatedAt,
				"tokens":            tokens,
				"tokens_per_second": tokensPerSec,
			},
		}
		if modelMigrated {
			resp["model_migrated"] = true
			resp["old_model"] = oldModel
			resp["new_model"] = chatInst.ModelName
		}
		// Add sources only if web_search was enabled and results were present
		if req.WebSearch && len(sources) > 0 {
			resp["sources"] = sources
		}
		c.JSON(http.StatusOK, resp)
	}
}

// --- LLM API call logic (exported for testing) ---

var ErrLLMSession = errors.New("llm session error")

type LLMResponse struct {
	Reply         string
	Tokens        int
	TokensPerSec  float64
	SessionID     string
}

// CallLLM is exported for testing
var CallLLM = func(url string, payload map[string]interface{}) (LLMResponse, error) {
	var respStruct struct {
		Choices []struct {
			Message struct {
				Role    string `json:"role"`
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
		Usage struct {
			CompletionTokens int `json:"completion_tokens"`
			PromptTokens     int `json:"prompt_tokens"`
			TotalTokens      int `json:"total_tokens"`
		} `json:"usage"`
		Timings struct {
			PredictedN         int     `json:"predicted_n"`
			PredictedMs        float64 `json:"predicted_ms"`
			PredictedPerSecond float64 `json:"predicted_per_second"`
		} `json:"timings"`
		ID string `json:"id"`
	}
	body, _ := json.Marshal(payload)
	req, _ := http.NewRequest("POST", url, bytes.NewBuffer(body))
	req.Header.Set("Content-Type", "application/json")
	client := http.Client{Timeout: 120 * time.Second}
	res, err := client.Do(req)
	if err != nil {
		return LLMResponse{}, err
	}
	defer res.Body.Close()
	if res.StatusCode == 400 || res.StatusCode == 422 {
		// Check for session/token error in response
		var respMap map[string]interface{}
		_ = json.NewDecoder(res.Body).Decode(&respMap)
		if respMap["error"] != nil && (containsSessionError(respMap["error"].(string))) {
			return LLMResponse{}, ErrLLMSession
		}
		return LLMResponse{}, errors.New(respMap["error"].(string))
	}
	if res.StatusCode > 299 {
		b, _ := io.ReadAll(res.Body)
		return LLMResponse{}, errors.New(string(b))
	}
	_ = json.NewDecoder(res.Body).Decode(&respStruct)
	reply := ""
	if len(respStruct.Choices) > 0 {
		reply = respStruct.Choices[0].Message.Content
	}
	tokens := respStruct.Usage.CompletionTokens
	tokensPerSec := respStruct.Timings.PredictedPerSecond
	if tokensPerSec == 0 && respStruct.Timings.PredictedMs > 0 && respStruct.Timings.PredictedN > 0 {
		tokensPerSec = float64(respStruct.Timings.PredictedN) / (respStruct.Timings.PredictedMs / 1000)
	}
	return LLMResponse{
		Reply:        reply,
		Tokens:       tokens,
		TokensPerSec: tokensPerSec,
		SessionID:    respStruct.ID,
	}, nil
}

func containsSessionError(msg string) bool {
	return (msg != "" && (contains(msg, "session") || contains(msg, "token")))
}

func contains(s, substr string) bool {
	return strings.Contains(s, substr)
}

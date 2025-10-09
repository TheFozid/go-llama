package api

import (
	"bytes"
	"encoding/json"
	"testing"
	"time"

	"go-llama/internal/chat"
	"go-llama/internal/config"
	"go-llama/internal/db"

	"github.com/gin-gonic/gin"
	"github.com/gorilla/websocket"
	"net/http/httptest"
)

func TestWSChatHandler_MissingPrompt(t *testing.T) {
	// setupUserDB(t) should migrate ALL models, including user, chat, and message.
	setupUserDB(t)      // defined in setup_handler_test.go
	resetUserTable(t)   // defined in setup_handler_test.go
	u := seedUser(t, "wsuser", "user") // defined in user_crud_handlers_test.go

	// Ensure chat.Chat table exists!
	c := chat.Chat{UserID: u.ID, ModelName: "test-model", CreatedAt: time.Now()}
	if err := db.DB.Create(&c).Error; err != nil {
		t.Fatalf("failed to seed chat: %v", err)
	}

	gin.SetMode(gin.TestMode)
	cfg := &config.Config{
		LLMs: []config.LLMConfig{{Name: "test-model", URL: "http://localhost:12345/llm"}},
	}

	r := gin.New()
	r.Use(func(cxt *gin.Context) {
		cxt.Set("userId", u.ID)
		cxt.Next()
	})
	r.GET("/ws/chat", WSChatHandler(cfg))

	s := httptest.NewServer(r)
	defer s.Close()

	wsURL := "ws" + s.URL[4:] + "/ws/chat"
	ws, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatalf("WebSocket dial failed: %v", err)
	}
	defer ws.Close()

	payload := WSChatPrompt{ChatID: int(c.ID), Prompt: "", WebSearch: false}
	b, _ := json.Marshal(payload)
	if err := ws.WriteMessage(websocket.TextMessage, b); err != nil {
		t.Fatalf("WebSocket write failed: %v", err)
	}
	_, resp, err := ws.ReadMessage()
	if err != nil {
		t.Fatalf("WebSocket read failed: %v", err)
	}
	if !bytes.Contains(resp, []byte("missing prompt")) {
		t.Errorf("expected missing prompt error, got: %s", string(resp))
	}
}

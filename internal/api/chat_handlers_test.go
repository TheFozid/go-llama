package api

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
	"fmt"
	"errors"

	"go-llama/internal/chat"
	"go-llama/internal/config"
	"go-llama/internal/db"

	"github.com/gin-gonic/gin"
)

func TestListLLMsHandler_ReturnsModels(t *testing.T) {
	cfg := &config.Config{
		LLMs: []config.LLMConfig{
			{Name: "foo", URL: "http://llm1"},
			{Name: "bar", URL: "http://llm2"},
		},
	}
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.GET("/models", ListLLMsHandler(cfg))

	w := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/models", nil)
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200 OK, got %d: %s", w.Code, w.Body.String())
	}
	var list []map[string]string
	if err := json.Unmarshal(w.Body.Bytes(), &list); err != nil {
		t.Fatalf("failed to decode JSON: %v", err)
	}
	if len(list) != 2 || list[0]["name"] != "foo" || list[1]["name"] != "bar" {
		t.Errorf("unexpected models: %+v", list)
	}
}

func TestCreateChatHandler_Unauthorized(t *testing.T) {
	cfg := &config.Config{
		LLMs: []config.LLMConfig{{Name: "foo", URL: "http://llm1"}},
	}
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.POST("/chats", CreateChatHandler(cfg))
	w := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/chats", bytes.NewReader([]byte(`{"title":"Chat1","model_name":"foo"}`)))
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(w, req)
	if w.Code != http.StatusUnauthorized {
		t.Errorf("expected 401 Unauthorized, got %d", w.Code)
	}
}

func TestCreateChatHandler_CreatesChat(t *testing.T) {
	setupUserDB(t)
	resetUserTable(t)
	cfg := &config.Config{
		LLMs: []config.LLMConfig{{Name: "foo", URL: "http://llm1"}},
	}
	u := seedUser(t, "chatuser", "user")
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(func(c *gin.Context) {
		c.Set("userId", u.ID)
		c.Next()
	})
	r.POST("/chats", CreateChatHandler(cfg))
	payload := map[string]string{"title": "A chat", "model_name": "foo"}
	b, _ := json.Marshal(payload)
	w := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/chats", bytes.NewReader(b))
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(w, req)
	if w.Code != http.StatusCreated {
		t.Fatalf("expected 201 Created, got %d: %s", w.Code, w.Body.String())
	}
}

func TestCreateChatHandler_ModelNotAvailable(t *testing.T) {
	setupUserDB(t)
	resetUserTable(t)
	cfg := &config.Config{
		LLMs: []config.LLMConfig{{Name: "foo", URL: "http://llm1"}},
	}
	u := seedUser(t, "chatuser", "user")
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(func(c *gin.Context) {
		c.Set("userId", u.ID)
		c.Next()
	})
	r.POST("/chats", CreateChatHandler(cfg))
	payload := map[string]string{"title": "A chat", "model_name": "not_real"}
	b, _ := json.Marshal(payload)
	w := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/chats", bytes.NewReader(b))
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(w, req)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 Bad Request, got %d: %s", w.Code, w.Body.String())
	}
}

func TestListChatsHandler_ReturnsChats(t *testing.T) {
	setupUserDB(t)
	resetUserTable(t)
	u := seedUser(t, "listuser", "user")
	chat1 := chat.Chat{Title: "Chat1", UserID: u.ID, ModelName: "foo", CreatedAt: time.Now()}
	chat2 := chat.Chat{Title: "Chat2", UserID: u.ID, ModelName: "foo", CreatedAt: time.Now().Add(-1 * time.Hour)}
	db.DB.Create(&chat1)
	db.DB.Create(&chat2)

	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(func(c *gin.Context) {
		c.Set("userId", u.ID)
		c.Next()
	})
	r.GET("/chats", ListChatsHandler())
	w := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/chats", nil)
	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200 OK, got %d: %s", w.Code, w.Body.String())
	}
	var chats []chat.Chat
	if err := json.Unmarshal(w.Body.Bytes(), &chats); err != nil {
		t.Fatalf("failed to decode chats: %v", err)
	}
	if len(chats) != 2 {
		t.Errorf("expected 2 chats, got %d", len(chats))
	}
}

func TestGetChatHandler_NotFound(t *testing.T) {
	setupUserDB(t)
	resetUserTable(t)
	u := seedUser(t, "getchatuser", "user")
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(func(c *gin.Context) {
		c.Set("userId", u.ID)
		c.Next()
	})
	r.GET("/chats/:id", GetChatHandler())
	w := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/chats/999", nil)
	r.ServeHTTP(w, req)
	if w.Code != http.StatusNotFound {
		t.Fatalf("expected 404 Not Found, got %d: %s", w.Code, w.Body.String())
	}
}

func TestListMessagesHandler_NotFound(t *testing.T) {
	setupUserDB(t)
	resetUserTable(t)
	u := seedUser(t, "listmsguser", "user")
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(func(c *gin.Context) {
		c.Set("userId", u.ID)
		c.Next()
	})
	r.GET("/chats/:id/messages", ListMessagesHandler())
	w := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/chats/999/messages", nil)
	r.ServeHTTP(w, req)
	if w.Code != http.StatusNotFound {
		t.Fatalf("expected 404 Not Found, got %d: %s", w.Code, w.Body.String())
	}
}

func TestListMessagesHandler_ReturnsMessages(t *testing.T) {
	setupUserDB(t)
	resetUserTable(t)
	u := seedUser(t, "listmsguser2", "user")
	c := chat.Chat{Title: "Chat1", UserID: u.ID, ModelName: "foo", CreatedAt: time.Now()}
	db.DB.Create(&c)
	m1 := chat.Message{ChatID: c.ID, Sender: "user", Content: "hello", CreatedAt: time.Now()}
	m2 := chat.Message{ChatID: c.ID, Sender: "bot", Content: "world", CreatedAt: time.Now()}
	db.DB.Create(&m1)
	db.DB.Create(&m2)

	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(func(c *gin.Context) {
		c.Set("userId", u.ID)
		c.Next()
	})
	r.GET("/chats/:id/messages", ListMessagesHandler())
	w := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/chats/"+fmt.Sprintf("%d", c.ID)+"/messages", nil)
	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200 OK, got %d: %s", w.Code, w.Body.String())
	}
	var messages []chat.Message
	if err := json.Unmarshal(w.Body.Bytes(), &messages); err != nil {
		t.Fatalf("failed to decode messages: %v", err)
	}
	if len(messages) != 2 {
		t.Errorf("expected 2 messages, got %d", len(messages))
	}
}

func TestSendMessageHandler_Unauthorized(t *testing.T) {
	cfg := &config.Config{
		LLMs: []config.LLMConfig{{Name: "foo", URL: "http://llm1"}},
	}
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.POST("/chats/:id/send", SendMessageHandler(cfg))
	w := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/chats/1/send", bytes.NewReader([]byte(`{"content":"hello"}`)))
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(w, req)
	if w.Code != http.StatusUnauthorized {
		t.Errorf("expected 401 Unauthorized, got %d", w.Code)
	}
}

func TestSendMessageHandler_ChatNotFound(t *testing.T) {
	setupUserDB(t)
	resetUserTable(t)
	cfg := &config.Config{
		LLMs: []config.LLMConfig{{Name: "foo", URL: "http://llm1"}},
	}
	u := seedUser(t, "sendmsguser", "user")
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(func(c *gin.Context) {
		c.Set("userId", u.ID)
		c.Next()
	})
	r.POST("/chats/:id/send", SendMessageHandler(cfg))
	w := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/chats/999/send", bytes.NewReader([]byte(`{"content":"hello"}`)))
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(w, req)
	if w.Code != http.StatusNotFound {
		t.Errorf("expected 404 Not Found, got %d", w.Code)
	}
}

func TestSendMessageHandler_MissingContent(t *testing.T) {
	setupUserDB(t)
	resetUserTable(t)
	cfg := &config.Config{
		LLMs: []config.LLMConfig{{Name: "foo", URL: "http://llm1"}},
	}
	u := seedUser(t, "sendmsguser2", "user")
	c := chat.Chat{Title: "Chat1", UserID: u.ID, ModelName: "foo", CreatedAt: time.Now()}
	db.DB.Create(&c)

	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(func(c *gin.Context) {
		c.Set("userId", u.ID)
		c.Next()
	})
	r.POST("/chats/:id/send", SendMessageHandler(cfg))
	w := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/chats/"+fmt.Sprintf("%d", c.ID)+"/send", bytes.NewReader([]byte(`{"content":""}`)))
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(w, req)
	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400 Bad Request, got %d: %s", w.Code, w.Body.String())
	}
}

func TestSendMessageHandler_LLMFailure(t *testing.T) {
	setupUserDB(t)
	resetUserTable(t)
	cfg := &config.Config{
		LLMs: []config.LLMConfig{{Name: "foo", URL: "http://llm1"}},
	}
	// Replace CallLLM with a stub that always returns error
	origCallLLM := CallLLM
	CallLLM = func(url string, payload map[string]interface{}) (LLMResponse, error) {
		return LLMResponse{}, errors.New("llm fail")
	}
	defer func() { CallLLM = origCallLLM }()

	u := seedUser(t, "failuser", "user")
	c := chat.Chat{Title: "Chat1", UserID: u.ID, ModelName: "foo", CreatedAt: time.Now()}
	db.DB.Create(&c)

	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(func(c *gin.Context) {
		c.Set("userId", u.ID)
		c.Next()
	})
	r.POST("/chats/:id/send", SendMessageHandler(cfg))
	payload := map[string]interface{}{"content": "hello"}
	b, _ := json.Marshal(payload)
	w := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/chats/"+fmt.Sprintf("%d", c.ID)+"/send", bytes.NewReader(b))
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(w, req)
	if w.Code != http.StatusBadGateway {
		t.Errorf("expected 502 Bad Gateway, got %d: %s", w.Code, w.Body.String())
	}
}

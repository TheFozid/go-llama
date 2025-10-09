package api

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"go-llama/internal/config"
	"github.com/gin-gonic/gin"
)

func TestHealthHandler_ReturnsOk(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.GET("/health", healthHandler)

	w := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/health", nil)
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200 OK, got %d: %s", w.Code, w.Body.String())
	}
	if !contains(w.Body.String(), "ok") {
		t.Errorf("expected response to contain 'ok', got: %s", w.Body.String())
	}
}

func TestConfigHandler_ReturnsConfig(t *testing.T) {
	// Only set the LLMs slice, skip Server and SearxNG if their types are not exported
	cfg := &config.Config{
		LLMs: []config.LLMConfig{
			{Name: "llm1", URL: "http://llm1"},
			{Name: "llm2", URL: "http://llm2"},
		},
	}
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.GET("/config", configHandler(cfg))

	w := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/config", nil)
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200 OK, got %d: %s", w.Code, w.Body.String())
	}
	// This will only check for the LLMs
	if !contains(w.Body.String(), "\"llm1\"") {
		t.Errorf("expected response to contain LLM config fields, got: %s", w.Body.String())
	}
}

package api

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"go-llama/internal/config"
	"github.com/gin-gonic/gin"
)

func TestSearxNGSearchHandler_BadRequest(t *testing.T) {
	gin.SetMode(gin.TestMode)
	cfg := &config.Config{}
	r := gin.New()
	r.POST("/search", SearxNGSearchHandler(cfg))

	w := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/search", bytes.NewReader([]byte(`{}`)))
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(w, req)
	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400 Bad Request, got %d: %s", w.Code, w.Body.String())
	}
}

func TestSearxNGSearchHandler_SearxNGUnavailable(t *testing.T) {
	gin.SetMode(gin.TestMode)
	// Fix: set URL directly as string or use correct struct if available
	cfg := &config.Config{}
	cfg.SearxNG.URL = "http://localhost:9999/search"
	r := gin.New()
	r.POST("/search", SearxNGSearchHandler(cfg))

	payload := map[string]string{"prompt": "test prompt"}
	b, _ := json.Marshal(payload)
	w := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/search", bytes.NewReader(b))
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(w, req)
	if w.Code != http.StatusBadGateway {
		t.Errorf("expected 502 Bad Gateway, got %d: %s", w.Code, w.Body.String())
	}
}

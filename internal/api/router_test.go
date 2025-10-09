package api

import (
	"testing"
	"github.com/gin-gonic/gin"
	"go-llama/internal/config"
	"net/http"
	"net/http/httptest"
)

func TestSetupRouter_BasicRoutes(t *testing.T) {
	gin.SetMode(gin.TestMode)

	cfg := &config.Config{}
	r := SetupRouter(cfg, nil)

	// Health route should exist and return 200
	w := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/health", nil)
	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Errorf("GET /health should return 200, got %d", w.Code)
	}

	// Config route should exist and return 200
	w2 := httptest.NewRecorder()
	req2 := httptest.NewRequest("GET", "/config", nil)
	r.ServeHTTP(w2, req2)
	if w2.Code != http.StatusOK {
		t.Errorf("GET /config should return 200, got %d", w2.Code)
	}
}

func TestSetupRouter_Subpath(t *testing.T) {
	gin.SetMode(gin.TestMode)

	cfg := &config.Config{}
	cfg.Server.Subpath = "/api"
	r := SetupRouter(cfg, nil)

	// Should correctly prefix routes with subpath
	w := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/api/health", nil)
	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Errorf("GET /api/health should return 200, got %d", w.Code)
	}
}

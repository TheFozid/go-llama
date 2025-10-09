package auth

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"go-llama/internal/config"
	"go-llama/internal/user"
	redisdb "go-llama/internal/redis"
	"github.com/redis/go-redis/v9"

	"github.com/gin-gonic/gin"
)

func setupTestJWT(secret string, userId uint, username, role string, exp time.Duration) string {
	token, _ := GenerateJWT(secret, userId, username, role, exp)
	return token
}

func setupTestRedis() *redis.Client {
	cfg := &config.Config{}
	cfg.Redis.Addr = "localhost:6379"
	cfg.Redis.Password = ""
	cfg.Redis.DB = 15
	return redisdb.NewClient(cfg)
}

func TestAuthMiddleware_MissingHeader(t *testing.T) {
	cfg := &config.Config{}
	cfg.Server.JWTSecret = "secret"
	rdb := setupTestRedis()
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(AuthMiddleware(cfg, rdb, false))
	r.GET("/test", func(c *gin.Context) {
		c.String(200, "OK")
	})
	w := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/test", nil)
	r.ServeHTTP(w, req)
	if w.Code != http.StatusUnauthorized {
		t.Errorf("expected 401, got %d", w.Code)
	}
}

func TestAuthMiddleware_InvalidToken(t *testing.T) {
	cfg := &config.Config{}
	cfg.Server.JWTSecret = "secret"
	rdb := setupTestRedis()
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(AuthMiddleware(cfg, rdb, false))
	r.GET("/test", func(c *gin.Context) {
		c.String(200, "OK")
	})
	w := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/test", nil)
	req.Header.Set("Authorization", "Bearer not.a.valid.jwt")
	r.ServeHTTP(w, req)
	if w.Code != http.StatusUnauthorized {
		t.Errorf("expected 401 for invalid JWT, got %d", w.Code)
	}
}

func TestAuthMiddleware_SessionInvalid(t *testing.T) {
	cfg := &config.Config{}
	cfg.Server.JWTSecret = "secret"
	rdb := setupTestRedis()
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(AuthMiddleware(cfg, rdb, false))
	r.GET("/test", func(c *gin.Context) {
		c.String(200, "OK")
	})
	token := setupTestJWT(cfg.Server.JWTSecret, 123, "user", "user", time.Minute)
	w := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/test", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	r.ServeHTTP(w, req)
	// No session in Redis, should be session error
	if w.Code != http.StatusUnauthorized {
		t.Errorf("expected 401 for session error, got %d", w.Code)
	}
}

func TestAuthMiddleware_NonAdminForbidden(t *testing.T) {
	cfg := &config.Config{}
	cfg.Server.JWTSecret = "secret"
	rdb := setupTestRedis()
	userId := uint(123)
	token := setupTestJWT(cfg.Server.JWTSecret, userId, "normaluser", "user", time.Minute)
	_ = SetSession(rdb, userId, token, time.Minute)
	defer DeleteSession(rdb, userId)
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(AuthMiddleware(cfg, rdb, true)) // requireAdmin = true
	r.GET("/test", func(c *gin.Context) {
		c.String(200, "OK")
	})
	w := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/test", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	r.ServeHTTP(w, req)
	if w.Code != http.StatusForbidden {
		t.Errorf("expected 403 for non-admin, got %d", w.Code)
	}
}

func TestAuthMiddleware_AdminAllowed(t *testing.T) {
	cfg := &config.Config{}
	cfg.Server.JWTSecret = "secret"
	rdb := setupTestRedis()
	userId := uint(222)
	token := setupTestJWT(cfg.Server.JWTSecret, userId, "adminuser", string(user.RoleAdmin), time.Minute)
	_ = SetSession(rdb, userId, token, time.Minute)
	defer DeleteSession(rdb, userId)
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(AuthMiddleware(cfg, rdb, true)) // requireAdmin = true
	r.GET("/test", func(c *gin.Context) {
		c.String(200, "OK")
	})
	w := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/test", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Errorf("expected 200 for admin, got %d", w.Code)
	}
}

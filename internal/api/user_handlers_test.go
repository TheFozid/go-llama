package api

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"go-llama/internal/config"
	"go-llama/internal/db"
	"go-llama/internal/user"

	"github.com/gin-gonic/gin"
	"github.com/redis/go-redis/v9"
)

func setupRedis() *redis.Client {
	// Use redis.NewClient with a dummy config, but do NOT rely on real Redis for handler tests.
	return redis.NewClient(&redis.Options{Addr: "localhost:6379", DB: 15})
}

func TestLoginHandler_NeedSetup(t *testing.T) {
	setupUserDB(t)
	resetUserTable(t)
	cfg := &config.Config{}
	cfg.Server.JWTSecret = "secret"
	rdb := setupRedis()

	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.POST("/login", LoginHandler(cfg, rdb))
	w := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/login", bytes.NewReader([]byte(`{"username":"a","password":"b"}`)))
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(w, req)
	if w.Code != http.StatusForbidden {
		t.Errorf("expected 403 Forbidden for initial setup required, got %d: %s", w.Code, w.Body.String())
	}
}

func TestLoginHandler_InvalidRequest(t *testing.T) {
	setupUserDB(t)
	resetUserTable(t)
	seedUser(t, "loginuser", "user")
	cfg := &config.Config{}
	cfg.Server.JWTSecret = "secret"
	rdb := setupRedis()

	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.POST("/login", LoginHandler(cfg, rdb))
	w := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/login", bytes.NewReader([]byte(`{"username":""}`)))
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(w, req)
	if w.Code != http.StatusUnauthorized { // <-- expect 401
		t.Errorf("expected 401 Unauthorized for invalid login body, got %d: %s", w.Code, w.Body.String())
	}
}

func TestLoginHandler_InvalidUser(t *testing.T) {
	setupUserDB(t)
	resetUserTable(t)
	seedUser(t, "someone", "user") // <-- seed at least one user!
	cfg := &config.Config{}
	cfg.Server.JWTSecret = "secret"
	rdb := setupRedis()

	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.POST("/login", LoginHandler(cfg, rdb))
	payload := map[string]string{"username": "nobody", "password": "wrongpw"}
	b, _ := json.Marshal(payload)
	w := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/login", bytes.NewReader(b))
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(w, req)
	if w.Code != http.StatusUnauthorized {
		t.Errorf("expected 401 Unauthorized for bad user, got %d: %s", w.Code, w.Body.String())
	}
}

func TestLoginHandler_InvalidPassword(t *testing.T) {
	setupUserDB(t)
	resetUserTable(t)
	u := seedUser(t, "loginuser2", "user")
	cfg := &config.Config{}
	cfg.Server.JWTSecret = "secret"
	rdb := setupRedis()

	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.POST("/login", LoginHandler(cfg, rdb))
	payload := map[string]string{"username": u.Username, "password": "wrongpw"}
	b, _ := json.Marshal(payload)
	w := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/login", bytes.NewReader(b))
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(w, req)
	if w.Code != http.StatusUnauthorized {
		t.Errorf("expected 401 Unauthorized for bad password, got %d: %s", w.Code, w.Body.String())
	}
}

func TestLoginHandler_Success(t *testing.T) {
	setupUserDB(t)
	resetUserTable(t)
	cfg := &config.Config{}
	cfg.Server.JWTSecret = "secret"
	rdb := setupRedis()
	pw := "mypw"
	hash, _ := user.HashPassword(pw)
	u := user.User{Username: "gooduser", PasswordHash: hash, Role: user.RoleUser, CreatedAt: time.Now()}
	db.DB.Create(&u)

	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.POST("/login", LoginHandler(cfg, rdb))
	payload := map[string]string{"username": u.Username, "password": pw}
	b, _ := json.Marshal(payload)
	w := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/login", bytes.NewReader(b))
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200 OK for valid login, got %d: %s", w.Code, w.Body.String())
	}
	if !contains(w.Body.String(), u.Username) {
		t.Errorf("expected response to contain username, got: %s", w.Body.String())
	}
}

func TestLogoutHandler_Unauthorized(t *testing.T) {
	rdb := setupRedis()
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.POST("/logout", LogoutHandler(rdb))
	w := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/logout", nil)
	r.ServeHTTP(w, req)
	if w.Code != http.StatusUnauthorized {
		t.Errorf("expected 401 Unauthorized for logout, got %d: %s", w.Code, w.Body.String())
	}
}

func TestLogoutHandler_Success(t *testing.T) {
	rdb := setupRedis()
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(func(c *gin.Context) {
		c.Set("userId", uint(123))
		c.Next()
	})
	r.POST("/logout", LogoutHandler(rdb))
	w := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/logout", nil)
	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Errorf("expected 200 OK for logout, got %d: %s", w.Code, w.Body.String())
	}
	if !contains(w.Body.String(), "Logged out") {
		t.Errorf("expected response to contain 'Logged out', got: %s", w.Body.String())
	}
}

func TestMeHandler_UserNotFound(t *testing.T) {
	setupUserDB(t)
	resetUserTable(t)
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(func(c *gin.Context) {
		// Use a non-existent user ID
		c.Set("userId", uint(99999))
		c.Next()
	})
	r.GET("/me", MeHandler())
	w := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/me", nil)
	r.ServeHTTP(w, req)
	if w.Code != http.StatusInternalServerError {
		t.Errorf("expected 500 Internal Server Error, got %d: %s", w.Code, w.Body.String())
	}
}

func TestMeHandler_Success(t *testing.T) {
	setupUserDB(t)
	resetUserTable(t)
	u := seedUser(t, "meuser", "user")
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(func(c *gin.Context) {
		c.Set("userId", u.ID)
		c.Next()
	})
	r.GET("/me", MeHandler())
	w := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/me", nil)
	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200 OK, got %d: %s", w.Code, w.Body.String())
	}
	if !contains(w.Body.String(), u.Username) {
		t.Errorf("expected response to contain username, got: %s", w.Body.String())
	}
}

func TestCreateUserHandler_InvalidRequest(t *testing.T) {
	setupUserDB(t)
	resetUserTable(t)
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.POST("/users", CreateUserHandler())
	w := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/users", bytes.NewReader([]byte(`{"username":""}`)))
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(w, req)
	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400 Bad Request for invalid body, got %d: %s", w.Code, w.Body.String())
	}
}

func TestCreateUserHandler_MissingOrInvalidFields(t *testing.T) {
	setupUserDB(t)
	resetUserTable(t)
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.POST("/users", CreateUserHandler())
	payload := map[string]string{"username": "u", "password": "p", "role": "badrole"}
	b, _ := json.Marshal(payload)
	w := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/users", bytes.NewReader(b))
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(w, req)
	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400 Bad Request for invalid role, got %d: %s", w.Code, w.Body.String())
	}
}

func TestCreateUserHandler_UsernameExists(t *testing.T) {
	setupUserDB(t)
	resetUserTable(t)
	u := seedUser(t, "dupeuser", "user")
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.POST("/users", CreateUserHandler())
	payload := map[string]string{"username": u.Username, "password": "pw", "role": "user"}
	b, _ := json.Marshal(payload)
	w := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/users", bytes.NewReader(b))
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(w, req)
	if w.Code != http.StatusBadRequest && w.Code != http.StatusInternalServerError {
		t.Errorf("expected 400 Bad Request or 500 Internal Server Error for duplicate username, got %d: %s", w.Code, w.Body.String())
	}
	// Accept both "exists" and "constraint" as valid error markers
	if !contains(w.Body.String(), "exists") && !contains(w.Body.String(), "constraint") {
		t.Errorf("expected response to mention 'exists' or 'constraint', got: %s", w.Body.String())
	}
}

func TestCreateUserHandler_Success(t *testing.T) {
	setupUserDB(t)
	resetUserTable(t)
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.POST("/users", CreateUserHandler())
	payload := map[string]string{"username": "newuser", "password": "pw", "role": "user"}
	b, _ := json.Marshal(payload)
	w := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/users", bytes.NewReader(b))
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(w, req)
	if w.Code != http.StatusCreated {
		t.Fatalf("expected 201 Created, got %d: %s", w.Code, w.Body.String())
	}
	if !contains(w.Body.String(), "newuser") {
		t.Errorf("expected response to contain username, got: %s", w.Body.String())
	}
}

func TestListUsersHandler_ReturnsUsers(t *testing.T) {
	setupUserDB(t)
	resetUserTable(t)
	u1 := seedUser(t, "listuser1", "user")
	u2 := seedUser(t, "listuser2", "admin")
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.GET("/users", ListUsersHandler())
	w := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/users", nil)
	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200 OK for list users, got %d: %s", w.Code, w.Body.String())
	}
	if !contains(w.Body.String(), u1.Username) || !contains(w.Body.String(), u2.Username) {
		t.Errorf("expected response to contain both usernames, got: %s", w.Body.String())
	}
}

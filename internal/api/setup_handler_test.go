package api

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"go-llama/internal/db"
	"go-llama/internal/user"
	"go-llama/internal/chat"

	"github.com/gin-gonic/gin"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func setupUserDB(t *testing.T) *gorm.DB {
	dbConn, err := gorm.Open(sqlite.Open("file::memory:?cache=shared"), &gorm.Config{})
	if err != nil {
		t.Fatalf("failed to open sqlite: %v", err)
	}
	// MIGRATE ALL MODELS USED IN TESTS!
	if err := dbConn.AutoMigrate(
		&user.User{},
		&chat.Chat{},
		&chat.Message{},
	); err != nil {
		t.Fatalf("failed to migrate: %v", err)
	}
	db.DB = dbConn
	return dbConn
}

func resetUserTable(t *testing.T) {
	if err := db.DB.Exec("DELETE FROM users").Error; err != nil {
		t.Fatalf("failed to reset users table: %v", err)
	}
	if err := db.DB.Exec("DELETE FROM chats").Error; err != nil {
		t.Fatalf("failed to reset chats table: %v", err)
	}
	if err := db.DB.Exec("DELETE FROM messages").Error; err != nil {
		t.Fatalf("failed to reset messages table: %v", err)
	}
}

func TestSetupHandler_AllowsInitialSetup(t *testing.T) {
	setupUserDB(t)
	resetUserTable(t)
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.POST("/setup", SetupHandler())
	payload := SetupRequest{Username: "admin1", Password: "pw1"}
	b, _ := json.Marshal(payload)
	w := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/setup", bytes.NewReader(b))
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(w, req)
	if w.Code != http.StatusCreated {
		t.Fatalf("expected 201 Created, got %d: %s", w.Code, w.Body.String())
	}
	if !contains(w.Body.String(), "setup_complete") {
		t.Errorf("setup response should indicate completion, got: %s", w.Body.String())
	}
}

func TestSetupHandler_ForbiddenIfUserExists(t *testing.T) {
	setupUserDB(t)
	resetUserTable(t)
	// Seed one user to block setup
	u := user.User{Username: "existing", PasswordHash: "hash", Role: user.RoleAdmin, CreatedAt: time.Now()}
	if err := db.DB.Create(&u).Error; err != nil {
		t.Fatalf("failed to seed user: %v", err)
	}
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.POST("/setup", SetupHandler())
	payload := SetupRequest{Username: "admin2", Password: "pw2"}
	b, _ := json.Marshal(payload)
	w := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/setup", bytes.NewReader(b))
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(w, req)
	if w.Code != http.StatusForbidden {
		t.Fatalf("expected 403 Forbidden, got %d: %s", w.Code, w.Body.String())
	}
	if !contains(w.Body.String(), "Setup not allowed") {
		t.Errorf("should block setup if user exists, got: %s", w.Body.String())
	}
}

func TestSetupHandler_RejectsBadInput(t *testing.T) {
	setupUserDB(t)
	resetUserTable(t)
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.POST("/setup", SetupHandler())
	// Missing username
	payload := SetupRequest{Password: "pw3"}
	b, _ := json.Marshal(payload)
	w := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/setup", bytes.NewReader(b))
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(w, req)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 Bad Request for missing username, got %d: %s", w.Code, w.Body.String())
	}
	// Missing password
	payload2 := SetupRequest{Username: "admin3"}
	b2, _ := json.Marshal(payload2)
	w2 := httptest.NewRecorder()
	req2 := httptest.NewRequest("POST", "/setup", bytes.NewReader(b2))
	req2.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(w2, req2)
	if w2.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 Bad Request for missing password, got %d: %s", w2.Code, w2.Body.String())
	}
}

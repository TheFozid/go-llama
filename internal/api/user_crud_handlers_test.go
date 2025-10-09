package api

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
	"fmt"

	"go-llama/internal/db"
	"go-llama/internal/user"

	"github.com/gin-gonic/gin"
)

func seedUser(t *testing.T, username string, role string) user.User {
	u := user.User{Username: username, PasswordHash: "hash", Role: user.Role(role), CreatedAt: time.Now()}
	if err := db.DB.Create(&u).Error; err != nil {
		t.Fatalf("failed to seed user: %v", err)
	}
	return u
}

// GET /users/me
func TestGetMeHandler(t *testing.T) {
	resetUserTable(t)
	u := seedUser(t, "testuser", "user")
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(func(c *gin.Context) {
		c.Set("userId", u.ID)
		c.Next()
	})
	r.GET("/users/me", GetMeHandler())
	w := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/users/me", nil)
	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200 OK, got %d: %s", w.Code, w.Body.String())
	}
	if !contains(w.Body.String(), "testuser") {
		t.Errorf("expected username in response, got: %s", w.Body.String())
	}
}

// PUT /users/me
func TestUpdateMeHandler(t *testing.T) {
	resetUserTable(t)
	u := seedUser(t, "testuser", "user")
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(func(c *gin.Context) {
		c.Set("userId", u.ID)
		c.Next()
	})
	r.PUT("/users/me", UpdateMeHandler())
	payload := UpdateMeRequest{Password: "newpw"}
	b, _ := json.Marshal(payload)
	w := httptest.NewRecorder()
	req := httptest.NewRequest("PUT", "/users/me", bytes.NewReader(b))
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200 OK, got %d: %s", w.Code, w.Body.String())
	}
	var u2 user.User
	if err := db.DB.First(&u2, u.ID).Error; err != nil {
		t.Fatalf("couldn't fetch updated user: %v", err)
	}
	if err := user.CheckPassword(u2.PasswordHash, "newpw"); err != nil {
		t.Errorf("password was not updated: %v", err)
	}
}

// DELETE /users/me
func TestDeleteMeHandler(t *testing.T) {
	resetUserTable(t)
	u := seedUser(t, "todelete", "user")
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(func(c *gin.Context) {
		c.Set("userId", u.ID)
		c.Next()
	})
	r.DELETE("/users/me", DeleteMeHandler())
	w := httptest.NewRecorder()
	req := httptest.NewRequest("DELETE", "/users/me", nil)
	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200 OK, got %d: %s", w.Code, w.Body.String())
	}
	var count int64
	db.DB.Model(&user.User{}).Where("id = ?", u.ID).Count(&count)
	if count != 0 {
		t.Error("user was not deleted")
	}
}

// GET /users/:id [admin only]
func TestGetUserByIdHandler_Admin(t *testing.T) {
	resetUserTable(t)
	_ = seedUser(t, "admin", "admin")
	target := seedUser(t, "target", "user")
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(func(c *gin.Context) {
		c.Set("userRole", "admin")
		c.Next()
	})
	r.GET("/users/:id", GetUserByIdHandler())
	w := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/users/"+toStrUint(target.ID), nil)
	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200 OK, got %d: %s", w.Code, w.Body.String())
	}
	if !contains(w.Body.String(), "target") {
		t.Errorf("expected username in response, got: %s", w.Body.String())
	}
}

// GET /users/:id [forbidden if not admin]
func TestGetUserByIdHandler_Forbidden(t *testing.T) {
	resetUserTable(t)
	target := seedUser(t, "target", "user")
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(func(c *gin.Context) {
		c.Set("userRole", "user")
		c.Next()
	})
	r.GET("/users/:id", GetUserByIdHandler())
	w := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/users/"+toStrUint(target.ID), nil)
	r.ServeHTTP(w, req)
	if w.Code != http.StatusForbidden {
		t.Errorf("expected 403 Forbidden, got %d: %s", w.Code, w.Body.String())
	}
}

// PUT /users/:id [admin only]
func TestUpdateUserByIdHandler_Admin(t *testing.T) {
	resetUserTable(t)
	_ = seedUser(t, "admin", "admin")
	target := seedUser(t, "target", "user")
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(func(c *gin.Context) {
		c.Set("userRole", "admin")
		c.Next()
	})
	r.PUT("/users/:id", UpdateUserByIdHandler())
	payload := UpdateUserRequest{Password: "adminpw", Role: "admin"}
	b, _ := json.Marshal(payload)
	w := httptest.NewRecorder()
	req := httptest.NewRequest("PUT", "/users/"+toStrUint(target.ID), bytes.NewReader(b))
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200 OK, got %d: %s", w.Code, w.Body.String())
	}
	var u2 user.User
	if err := db.DB.First(&u2, target.ID).Error; err != nil {
		t.Fatalf("couldn't fetch updated user: %v", err)
	}
	if err := user.CheckPassword(u2.PasswordHash, "adminpw"); err != nil {
		t.Errorf("password was not updated: %v", err)
	}
	if u2.Role != "admin" {
		t.Errorf("role was not updated to admin, got: %s", u2.Role)
	}
}

// PUT /users/:id [forbidden if not admin]
func TestUpdateUserByIdHandler_Forbidden(t *testing.T) {
	resetUserTable(t)
	target := seedUser(t, "target", "user")
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(func(c *gin.Context) {
		c.Set("userRole", "user")
		c.Next()
	})
	r.PUT("/users/:id", UpdateUserByIdHandler())
	payload := UpdateUserRequest{Password: "hackerpw", Role: "admin"}
	b, _ := json.Marshal(payload)
	w := httptest.NewRecorder()
	req := httptest.NewRequest("PUT", "/users/"+toStrUint(target.ID), bytes.NewReader(b))
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(w, req)
	if w.Code != http.StatusForbidden {
		t.Errorf("expected 403 Forbidden, got %d: %s", w.Code, w.Body.String())
	}
}

// DELETE /users/:id [admin only]
func TestDeleteUserByIdHandler_Admin(t *testing.T) {
	resetUserTable(t)
	_ = seedUser(t, "admin", "admin")
	target := seedUser(t, "target", "user")
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(func(c *gin.Context) {
		c.Set("userRole", "admin")
		c.Next()
	})
	r.DELETE("/users/:id", DeleteUserByIdHandler())
	w := httptest.NewRecorder()
	req := httptest.NewRequest("DELETE", "/users/"+toStrUint(target.ID), nil)
	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200 OK, got %d: %s", w.Code, w.Body.String())
	}
	var count int64
	db.DB.Model(&user.User{}).Where("id = ?", target.ID).Count(&count)
	if count != 0 {
		t.Error("user was not deleted by admin")
	}
}

// DELETE /users/:id [forbidden if not admin]
func TestDeleteUserByIdHandler_Forbidden(t *testing.T) {
	resetUserTable(t)
	target := seedUser(t, "target", "user")
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(func(c *gin.Context) {
		c.Set("userRole", "user")
		c.Next()
	})
	r.DELETE("/users/:id", DeleteUserByIdHandler())
	w := httptest.NewRecorder()
	req := httptest.NewRequest("DELETE", "/users/"+toStrUint(target.ID), nil)
	r.ServeHTTP(w, req)
	if w.Code != http.StatusForbidden {
		t.Errorf("expected 403 Forbidden, got %d: %s", w.Code, w.Body.String())
	}
}

// Helper: uint to string
func toStrUint(x uint) string {
	return fmt.Sprintf("%d", x)
}

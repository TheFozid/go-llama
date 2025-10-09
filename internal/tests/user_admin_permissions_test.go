package main

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"go-llama/internal/api"
	"go-llama/internal/db"
	"go-llama/internal/user"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func setupAdminPermTestDB(t *testing.T) *gorm.DB {
	dbConn, err := gorm.Open(sqlite.Open("file::memory:?cache=shared"), &gorm.Config{})
	if err != nil {
		t.Fatalf("failed to open in-memory sqlite: %v", err)
	}
	if err := dbConn.AutoMigrate(&user.User{}); err != nil {
		t.Fatalf("failed to migrate: %v", err)
	}
	db.DB = dbConn
	return dbConn
}

func resetAdminUserTable(t *testing.T) {
	if err := db.DB.Exec("DELETE FROM users").Error; err != nil {
		t.Fatalf("failed to reset users table: %v", err)
	}
}

// Simulate middleware that sets userId and userRole
func withUser(id uint, role string) gin.HandlerFunc {
	return func(c *gin.Context) {
		c.Set("userId", id)
		c.Set("userRole", role)
		c.Next()
	}
}

func TestAdminCanUpdateAndDeleteAnyUser(t *testing.T) {
	gin.SetMode(gin.TestMode)
	db := setupAdminPermTestDB(t)
	resetAdminUserTable(t)

	// Create admin and regular user
	admin := user.User{Username: "admin", PasswordHash: "hash", Role: user.Role("admin")}
	regular := user.User{Username: "regular", PasswordHash: "hash", Role: user.Role("user")}
	if err := db.Create(&admin).Error; err != nil {
		t.Fatalf("failed to create admin: %v", err)
	}
	if err := db.Create(&regular).Error; err != nil {
		t.Fatalf("failed to create regular user: %v", err)
	}

	// Setup router as admin
	r := gin.New()
	r.Use(withUser(admin.ID, "admin"))
	r.PUT("/users/:id", api.UpdateUserByIdHandler())
	r.DELETE("/users/:id", api.DeleteUserByIdHandler())

	// Admin updates regular's password and role
	updateBody := `{"password":"newpass","role":"admin"}`
	w := httptest.NewRecorder()
	req := httptest.NewRequest("PUT", "/users/"+toStrUint(regular.ID), strings.NewReader(updateBody))
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("admin should be able to update any user, got %d: %s", w.Code, w.Body.String())
	}
	// Confirm password/role changed
	var updated user.User
	if err := db.First(&updated, regular.ID).Error; err != nil {
		t.Fatalf("couldn't fetch updated user: %v", err)
	}
	if updated.Role != "admin" {
		t.Errorf("expected role to be changed to admin, got %s", updated.Role)
	}
	if err := user.CheckPassword(updated.PasswordHash, "newpass"); err != nil {
		t.Errorf("password wasn't updated for user: %v", err)
	}

	// Admin deletes user
	w2 := httptest.NewRecorder()
	req2 := httptest.NewRequest("DELETE", "/users/"+toStrUint(regular.ID), nil)
	r.ServeHTTP(w2, req2)
	if w2.Code != http.StatusOK {
		t.Fatalf("admin should be able to delete any user, got %d: %s", w2.Code, w2.Body.String())
	}
	var count int64
	db.Model(&user.User{}).Where("id = ?", regular.ID).Count(&count)
	if count != 0 {
		t.Error("user was not deleted by admin")
	}
}

func TestUserCannotUpdateOrDeleteOtherUsers(t *testing.T) {
	gin.SetMode(gin.TestMode)
	db := setupAdminPermTestDB(t)
	resetAdminUserTable(t)

	// Create two regular users
	user1 := user.User{Username: "user1", PasswordHash: "hash", Role: user.Role("user")}
	user2 := user.User{Username: "user2", PasswordHash: "hash", Role: user.Role("user")}
	if err := db.Create(&user1).Error; err != nil {
		t.Fatalf("failed to create user1: %v", err)
	}
	if err := db.Create(&user2).Error; err != nil {
		t.Fatalf("failed to create user2: %v", err)
	}

	// Setup router as user1
	r := gin.New()
	r.Use(withUser(user1.ID, "user"))
	r.PUT("/users/:id", api.UpdateUserByIdHandler())
	r.DELETE("/users/:id", api.DeleteUserByIdHandler())

	// Try to update user2 (should fail, expect 403 or 401)
	updateBody := `{"password":"hacked","role":"admin"}`
	w := httptest.NewRecorder()
	req := httptest.NewRequest("PUT", "/users/"+toStrUint(user2.ID), strings.NewReader(updateBody))
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(w, req)
	if w.Code == http.StatusOK {
		t.Fatalf("regular user should NOT be able to update another user (got 200 OK)")
	}
	if w.Code != http.StatusForbidden && w.Code != http.StatusUnauthorized {
		t.Errorf("expected 403 Forbidden or 401 Unauthorized, got %d: %s", w.Code, w.Body.String())
	}

	// Try to delete user2 (should fail)
	w2 := httptest.NewRecorder()
	req2 := httptest.NewRequest("DELETE", "/users/"+toStrUint(user2.ID), nil)
	r.ServeHTTP(w2, req2)
	if w2.Code == http.StatusOK {
		t.Fatalf("regular user should NOT be able to delete another user (got 200 OK)")
	}
	if w2.Code != http.StatusForbidden && w2.Code != http.StatusUnauthorized {
		t.Errorf("expected 403 Forbidden or 401 Unauthorized, got %d: %s", w2.Code, w2.Body.String())
	}
	// Confirm user2 not deleted
	var count int64
	db.Model(&user.User{}).Where("id = ?", user2.ID).Count(&count)
	if count == 0 {
		t.Error("user2 was deleted by unauthorized user")
	}
}

func TestUserCannotEscalateOwnRole(t *testing.T) {
	gin.SetMode(gin.TestMode)
	db := setupAdminPermTestDB(t)
	resetAdminUserTable(t)

	// Create a regular user
	user1 := user.User{Username: "user1esc", PasswordHash: "hash", Role: user.Role("user")}
	if err := db.Create(&user1).Error; err != nil {
		t.Fatalf("failed to create user1: %v", err)
	}

	r := gin.New()
	r.Use(withUser(user1.ID, "user"))
	r.PUT("/users/:id", api.UpdateUserByIdHandler())

	// Try to escalate self to admin
	updateBody := `{"role":"admin"}`
	w := httptest.NewRecorder()
	req := httptest.NewRequest("PUT", "/users/"+toStrUint(user1.ID), strings.NewReader(updateBody))
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(w, req)
	if w.Code == http.StatusOK {
		// Confirm role did NOT change
		var updated user.User
		if err := db.First(&updated, user1.ID).Error; err != nil {
			t.Fatalf("couldn't fetch updated user: %v", err)
		}
		if updated.Role == "admin" {
			t.Errorf("regular user should NOT be able to self-escalate to admin!")
		}
	} else if w.Code != http.StatusForbidden && w.Code != http.StatusUnauthorized {
		t.Errorf("expected 403 Forbidden or 401 Unauthorized, got %d: %s", w.Code, w.Body.String())
	}
}

// Helper: uint to string
func toStrUint(x uint) string {
	return fmt.Sprintf("%d", x)
}

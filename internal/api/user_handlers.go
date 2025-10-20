package api

import (
	"net/http"
	"time"

	"go-llama/internal/auth"
	"go-llama/internal/config"
	"go-llama/internal/db"
	"go-llama/internal/user"
	"github.com/redis/go-redis/v9"

	"github.com/gin-gonic/gin"
)

type LoginRequest struct {
	Username string `json:"username"`
	Password string `json:"password"`
}
type LoginResponse struct {
	Token    string `json:"token"`
	UserID   uint   `json:"userId"`
	Username string `json:"username"`
	Role     string `json:"role"`
}

func LoginHandler(cfg *config.Config, rdb *redis.Client) gin.HandlerFunc {
	return func(c *gin.Context) {
		// If no users exist, indicate need for setup
		var count int64
		if err := db.DB.Model(&user.User{}).Count(&count).Error; err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": gin.H{"message": "DB error"}})
			return
		}
		if count == 0 {
			c.JSON(http.StatusForbidden, gin.H{"error": gin.H{"message": "Initial setup required", "need_setup": true}})
			return
		}
		var req LoginRequest
		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": gin.H{"message": "Invalid request"}})
			return
		}
		var u user.User
		if err := db.DB.Where("username = ?", req.Username).First(&u).Error; err != nil {
			c.JSON(http.StatusUnauthorized, gin.H{"error": gin.H{"message": "Invalid username or password"}})
			return
		}
		if err := user.CheckPassword(u.PasswordHash, req.Password); err != nil {
			c.JSON(http.StatusUnauthorized, gin.H{"error": gin.H{"message": "Invalid username or password"}})
			return
		}
		token, err := auth.GenerateJWT(cfg.Server.JWTSecret, u.ID, u.Username, string(u.Role), 7*24*time.Hour)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": gin.H{"message": "Failed to generate token"}})
			return
		}
		_ = auth.SetSession(rdb, u.ID, token, 7*24*time.Hour)
		c.JSON(http.StatusOK, LoginResponse{
			Token:    token,
			UserID:   u.ID,
			Username: u.Username,
			Role:     string(u.Role),
		})
	}
}

func LogoutHandler(rdb *redis.Client) gin.HandlerFunc {
	return func(c *gin.Context) {
		userId, exists := c.Get("userId")
		if !exists {
			c.JSON(http.StatusUnauthorized, gin.H{"error": gin.H{"message": "Not authenticated"}})
			return
		}
		_ = auth.DeleteSession(rdb, userId.(uint))
		c.JSON(http.StatusOK, gin.H{"message": "Logged out"})
	}
}

func MeHandler() gin.HandlerFunc {
	return func(c *gin.Context) {
		userId, _ := c.Get("userId")
		var u user.User
		if err := db.DB.First(&u, userId.(uint)).Error; err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": gin.H{"message": "User not found"}})
			return
		}
		c.JSON(http.StatusOK, gin.H{
			"id":        u.ID,
			"username":  u.Username,
			"role":      u.Role,
			"createdAt": u.CreatedAt,
		})
	}
}

// OnlineUserCountHandler returns the number of unique online users.
func OnlineUserCountHandler(rdb *redis.Client) gin.HandlerFunc {
	return func(c *gin.Context) {
		count, err := auth.OnlineUserCount(rdb)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": gin.H{"message": "Failed to count online users"}})
			return
		}
		c.JSON(http.StatusOK, gin.H{"online": count})
	}
}

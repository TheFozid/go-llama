package api

import (
	"net/http"
	"go-llama/internal/user"
	"go-llama/internal/db"
	"strings"

	"github.com/gin-gonic/gin"
)

type SetupRequest struct {
	Username string `json:"username"`
	Password string `json:"password"`
}

func SetupHandler() gin.HandlerFunc {
	return func(c *gin.Context) {
		// Only allow if no users exist
		var count int64
		if err := db.DB.Model(&user.User{}).Count(&count).Error; err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": gin.H{"message": "DB error"}})
			return
		}
		if count != 0 {
			c.JSON(http.StatusForbidden, gin.H{"error": gin.H{"message": "Setup not allowed; users already exist"}})
			return
		}
		var req SetupRequest
		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": gin.H{"message": "Invalid request"}})
			return
		}
		if req.Username == "" || req.Password == "" {
			c.JSON(http.StatusBadRequest, gin.H{"error": gin.H{"message": "Username and password required"}})
			return
		}
		pwHash, err := user.HashPassword(req.Password)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": gin.H{"message": "Password hash failed"}})
			return
		}
		u := user.User{
			Username:     req.Username,
			PasswordHash: pwHash,
			Role:         user.RoleAdmin,
		}
		if err := db.DB.Create(&u).Error; err != nil {
			if strings.Contains(err.Error(), "unique") {
				c.JSON(http.StatusBadRequest, gin.H{"error": gin.H{"message": "Username already exists"}})
				return
			}
			c.JSON(http.StatusInternalServerError, gin.H{"error": gin.H{"message": "DB error"}})
			return
		}
		c.JSON(http.StatusCreated, gin.H{
			"id":            u.ID,
			"username":      u.Username,
			"role":          u.Role,
			"createdAt":     u.CreatedAt,
			"setup_complete": true,
		})
	}
}

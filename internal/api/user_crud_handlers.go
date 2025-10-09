package api

import (
	"net/http"

	"go-llama/internal/user"
	"go-llama/internal/db"

	"github.com/gin-gonic/gin"
)

// GET /users  [admin only]
func ListUsersHandler() gin.HandlerFunc {
	return func(c *gin.Context) {
		role, _ := c.Get("userRole")
		if role != "admin" {
			c.JSON(http.StatusForbidden, gin.H{"error": gin.H{"message": "Forbidden"}})
			return
		}
		var users []user.User
		if err := db.DB.Find(&users).Error; err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": gin.H{"message": "List error"}})
			return
		}
		var result []gin.H
		for _, u := range users {
			result = append(result, gin.H{
				"id":        u.ID,
				"username":  u.Username,
				"role":      u.Role,
				"createdAt": u.CreatedAt,
			})
		}
		c.JSON(http.StatusOK, result)
	}
}

// POST /users  [admin only]
func CreateUserHandler() gin.HandlerFunc {
	return func(c *gin.Context) {
		role, _ := c.Get("userRole")
		if role != "admin" {
			c.JSON(http.StatusForbidden, gin.H{"error": gin.H{"message": "Forbidden"}})
			return
		}
		var req struct {
			Username string `json:"username"`
			Password string `json:"password"`
		}
		if err := c.ShouldBindJSON(&req); err != nil || req.Username == "" || req.Password == "" {
			c.JSON(http.StatusBadRequest, gin.H{"error": gin.H{"message": "Missing username or password"}})
			return
		}
		pwHash, err := user.HashPassword(req.Password)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": gin.H{"message": "Password hash failed"}})
			return
		}
		newUser := user.User{
			Username:     req.Username,
			PasswordHash: pwHash,
			Role:         "user",
		}
		if err := db.DB.Create(&newUser).Error; err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": gin.H{"message": "Create error"}})
			return
		}
		c.JSON(http.StatusCreated, gin.H{
			"id":        newUser.ID,
			"username":  newUser.Username,
			"role":      newUser.Role,
			"createdAt": newUser.CreatedAt,
		})
	}
}

// GET /users/me
func GetMeHandler() gin.HandlerFunc {
	return func(c *gin.Context) {
		userId, _ := c.Get("userId")
		var u user.User
		if err := db.DB.First(&u, userId.(uint)).Error; err != nil {
			c.JSON(http.StatusNotFound, gin.H{"error": gin.H{"message": "User not found"}})
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

type UpdateMeRequest struct {
	Password string `json:"password,omitempty"`
}

// PUT /users/me
func UpdateMeHandler() gin.HandlerFunc {
	return func(c *gin.Context) {
		userId, _ := c.Get("userId")
		var req UpdateMeRequest
		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": gin.H{"message": "Invalid request"}})
			return
		}
		var u user.User
		if err := db.DB.First(&u, userId.(uint)).Error; err != nil {
			c.JSON(http.StatusNotFound, gin.H{"error": gin.H{"message": "User not found"}})
			return
		}
		if req.Password != "" {
			pwHash, err := user.HashPassword(req.Password)
			if err != nil {
				c.JSON(http.StatusInternalServerError, gin.H{"error": gin.H{"message": "Password hash failed"}})
				return
			}
			u.PasswordHash = pwHash
		}
		if err := db.DB.Save(&u).Error; err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": gin.H{"message": "Update error"}})
			return
		}
		c.JSON(http.StatusOK, gin.H{"message": "User updated"})
	}
}

// DELETE /users/me
func DeleteMeHandler() gin.HandlerFunc {
	return func(c *gin.Context) {
		userId, _ := c.Get("userId")
		if err := db.DB.Delete(&user.User{}, userId.(uint)).Error; err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": gin.H{"message": "Delete error"}})
			return
		}
		c.JSON(http.StatusOK, gin.H{"message": "User deleted"})
	}
}

// GET /users/:id  [admin only]
func GetUserByIdHandler() gin.HandlerFunc {
	return func(c *gin.Context) {
		role, _ := c.Get("userRole")
		if role != "admin" {
			c.JSON(http.StatusForbidden, gin.H{"error": gin.H{"message": "Forbidden"}})
			return
		}
		id := c.Param("id")
		var u user.User
		if err := db.DB.First(&u, id).Error; err != nil {
			c.JSON(http.StatusNotFound, gin.H{"error": gin.H{"message": "User not found"}})
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

type UpdateUserRequest struct {
	Password string `json:"password,omitempty"`
	Role     string `json:"role,omitempty"`
}

// PUT /users/:id  [admin only]
func UpdateUserByIdHandler() gin.HandlerFunc {
	return func(c *gin.Context) {
		role, _ := c.Get("userRole")
		if role != "admin" {
			c.JSON(http.StatusForbidden, gin.H{"error": gin.H{"message": "Forbidden"}})
			return
		}
		id := c.Param("id")
		var req UpdateUserRequest
		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": gin.H{"message": "Invalid request"}})
			return
		}
		var u user.User
		if err := db.DB.First(&u, id).Error; err != nil {
			c.JSON(http.StatusNotFound, gin.H{"error": gin.H{"message": "User not found"}})
			return
		}
		if req.Password != "" {
			pwHash, err := user.HashPassword(req.Password)
			if err != nil {
				c.JSON(http.StatusInternalServerError, gin.H{"error": gin.H{"message": "Password hash failed"}})
				return
			}
			u.PasswordHash = pwHash
		}
		if req.Role != "" && (req.Role == "admin" || req.Role == "user") {
			u.Role = user.Role(req.Role)
		}
		if err := db.DB.Save(&u).Error; err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": gin.H{"message": "Update error"}})
			return
		}
		c.JSON(http.StatusOK, gin.H{"message": "User updated"})
	}
}

// DELETE /users/:id  [admin only]
func DeleteUserByIdHandler() gin.HandlerFunc {
	return func(c *gin.Context) {
		role, _ := c.Get("userRole")
		if role != "admin" {
			c.JSON(http.StatusForbidden, gin.H{"error": gin.H{"message": "Forbidden"}})
			return
		}
		id := c.Param("id")
		if err := db.DB.Delete(&user.User{}, id).Error; err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": gin.H{"message": "Delete error"}})
			return
		}
		c.JSON(http.StatusOK, gin.H{"message": "User deleted"})
	}
}

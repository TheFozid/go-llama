package auth

import (
	"net/http"
	"strings"
	"time"

	"go-llama/internal/config"
	"go-llama/internal/user"

	"github.com/gin-gonic/gin"
	"github.com/redis/go-redis/v9"
)

func AuthMiddleware(cfg *config.Config, rdb *redis.Client, requireAdmin bool) gin.HandlerFunc {
	return func(c *gin.Context) {
		authHeader := c.GetHeader("Authorization")
		if authHeader == "" || !strings.HasPrefix(authHeader, "Bearer ") {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": gin.H{"message": "Missing or invalid Authorization header"}})
			return
		}
		tokenStr := strings.TrimPrefix(authHeader, "Bearer ")
		claims, err := ParseJWT(cfg.Server.JWTSecret, tokenStr)
		if err != nil {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": gin.H{"message": "Invalid or expired token"}})
			return
		}
		// Check session in Redis
		sessionToken, err := GetSession(rdb, claims.UserID)
		if err != nil || sessionToken != tokenStr {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": gin.H{"message": "Session expired or invalid"}})
			return
		}
		// Enforce inactivity timeout (refresh expiry)
		_ = SetSession(rdb, claims.UserID, tokenStr, 30*time.Minute)

		// Attach user info to context
		c.Set("userId", claims.UserID)
		c.Set("username", claims.Username)
		c.Set("role", claims.Role)
		c.Set("userRole", claims.Role) // <-- Add this line

		if requireAdmin && claims.Role != string(user.RoleAdmin) {
			c.AbortWithStatusJSON(http.StatusForbidden, gin.H{"error": gin.H{"message": "Admin only"}})
			return
		}
		c.Next()
	}
}

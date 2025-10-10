package auth

import (
	"net/http"
	"strings"

	"go-llama/internal/config"
	"go-llama/internal/user"

	"github.com/gin-gonic/gin"
)

// Stateless JWT Auth Middleware (no Redis session dependency)
// Only checks JWT signature and expiry. This eliminates premature logout caused by Redis session expiry.
func AuthMiddleware(cfg *config.Config, rdb interface{}, requireAdmin bool) gin.HandlerFunc {
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
		// REMOVED REDIS SESSION CHECKS

		// Attach user info to context
		c.Set("userId", claims.UserID)
		c.Set("username", claims.Username)
		c.Set("role", claims.Role)
		c.Set("userRole", claims.Role)

		if requireAdmin && claims.Role != string(user.RoleAdmin) {
			c.AbortWithStatusJSON(http.StatusForbidden, gin.H{"error": gin.H{"message": "Admin only"}})
			return
		}
		c.Next()
	}
}

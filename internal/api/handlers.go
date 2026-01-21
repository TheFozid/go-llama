package api

import (
	"net/http"
	"go-llama/internal/config"
	"github.com/gin-gonic/gin"
)

// GET /health
func healthHandler(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{
		"status": "ok",
	})
}

// GET /config
func configHandler(cfg *config.Config) gin.HandlerFunc {
	return func(c *gin.Context) {
		// Only return non-sensitive config fields
		c.JSON(http.StatusOK, gin.H{
			"server": gin.H{
				"host":    cfg.Server.Host,
				"port":    cfg.Server.Port,
				"subpath": cfg.Server.Subpath,
			},
			"llms":     cfg.LLMs,
			"searxng":  cfg.SearxNG,
		})
	}
}

// ModelStatusHandler returns the status of all LLM endpoints
func ModelStatusHandler(discoveryService *llm.DiscoveryService) gin.HandlerFunc {
    return func(c *gin.Context) {
        endpoints := discoveryService.GetAllEndpoints()
        c.JSON(http.StatusOK, gin.H{
            "endpoints": endpoints,
            "chat_models": discoveryService.GetChatModels(),
            "embedding_models": discoveryService.GetEmbeddingModels(),
        })
    }
}

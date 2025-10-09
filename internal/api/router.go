package api

import (
	"github.com/gin-gonic/gin"
	"go-llama/internal/config"
	"go-llama/internal/auth"
	"go-llama/internal/db"
	"go-llama/internal/user"
	"github.com/redis/go-redis/v9"
	"net/http"
	"path"
)

func usersExist() bool {
	var count int64
	if db.DB == nil {
		return false
	}
	db.DB.Model(&user.User{}).Count(&count)
	return count > 0
}

func SetupRouter(cfg *config.Config, rdb *redis.Client) *gin.Engine {
	r := gin.Default()
	subpath := cfg.Server.Subpath // e.g. "/go-llama" or any custom path, always starts with '/'

	// Load HTML templates
	r.LoadHTMLFiles("./frontend/index.html", "./frontend/login.html", "./frontend/setup.html")

	// Serve frontend static assets
	r.Static(path.Join(subpath, "static"), "./static")
	r.Static(path.Join(subpath, "css"), "./frontend/css")
	r.Static(path.Join(subpath, "js"), "./frontend/js")

	// Pretty HTML routes with dynamic subpath injection and user existence check
	r.GET(subpath, func(c *gin.Context) {
		if !usersExist() {
			c.Redirect(http.StatusFound, path.Join(subpath, "setup"))
			return
		}
		c.HTML(http.StatusOK, "index.html", gin.H{"subpath": subpath})
	})
	r.GET(path.Join(subpath, "login"), func(c *gin.Context) {
		if !usersExist() {
			c.Redirect(http.StatusFound, path.Join(subpath, "setup"))
			return
		}
		c.HTML(http.StatusOK, "login.html", gin.H{"subpath": subpath})
	})
	r.GET(path.Join(subpath, "setup"), func(c *gin.Context) {
		c.HTML(http.StatusOK, "setup.html", gin.H{"subpath": subpath})
	})
	r.GET(path.Join(subpath, "favicon.ico"), func(c *gin.Context) {
		c.File("./static/favicon.ico")
	})
	// Redirect /subpath/ to /subpath (no duplicate panic)
	r.GET(subpath+"/", func(c *gin.Context) {
		c.Redirect(http.StatusMovedPermanently, subpath)
	})

	// API routes
	group := r.Group(subpath)
	{
		group.GET("/health", healthHandler)
		group.GET("/config", configHandler(cfg))

		// Setup: only if no users
		group.POST("/setup", SetupHandler())

		// Auth
		group.POST("/auth/login", LoginHandler(cfg, rdb))
		group.POST("/auth/logout", auth.AuthMiddleware(cfg, rdb, false), LogoutHandler(rdb))
		group.GET("/auth/me", auth.AuthMiddleware(cfg, rdb, false), MeHandler())

		// Admin: users
		group.GET("/users", auth.AuthMiddleware(cfg, rdb, true), ListUsersHandler())
		group.POST("/users", auth.AuthMiddleware(cfg, rdb, true), CreateUserHandler())

		// User self-service
		group.GET("/users/me", auth.AuthMiddleware(cfg, rdb, false), GetMeHandler())
		group.PUT("/users/me", auth.AuthMiddleware(cfg, rdb, false), UpdateMeHandler())
		group.DELETE("/users/me", auth.AuthMiddleware(cfg, rdb, false), DeleteMeHandler())

		// Admin: user by id
		group.GET("/users/:id", auth.AuthMiddleware(cfg, rdb, true), GetUserByIdHandler())
		group.PUT("/users/:id", auth.AuthMiddleware(cfg, rdb, true), UpdateUserByIdHandler())
		group.DELETE("/users/:id", auth.AuthMiddleware(cfg, rdb, true), DeleteUserByIdHandler())

		// --- LLMs ---
		group.GET("/llms", ListLLMsHandler(cfg))

		// --- Chat endpoints ---
		group.POST("/chats", auth.AuthMiddleware(cfg, rdb, false), CreateChatHandler(cfg))
		group.GET("/chats", auth.AuthMiddleware(cfg, rdb, false), ListChatsHandler())
		group.GET("/chats/:id", auth.AuthMiddleware(cfg, rdb, false), GetChatHandler())
		group.GET("/chats/:id/messages", auth.AuthMiddleware(cfg, rdb, false), ListMessagesHandler())
		group.POST("/chats/:id/messages", auth.AuthMiddleware(cfg, rdb, false), SendMessageHandler(cfg))

		// --- Streaming WebSocket endpoint ---
		group.GET("/ws/chat", WSChatHandler(cfg))

		// --- SearxNG-augmented LLM endpoint ---
		group.POST("/search", auth.AuthMiddleware(cfg, rdb, false), SearxNGSearchHandler(cfg))

		// --- Online users count ---
		group.GET("/users/online", OnlineUserCountHandler(rdb))

		// --- New delete and edit ---
		group.PUT("/chats/:id", auth.AuthMiddleware(cfg, rdb, false), EditChatTitleHandler())
		group.DELETE("/chats/:id", auth.AuthMiddleware(cfg, rdb, false), DeleteChatHandler())
	}
	return r
}

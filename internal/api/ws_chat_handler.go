// internal/api/ws_chat_handler.go
package api

import (
	"encoding/json"
	"log"
	"net/http"
	"regexp"
	"strings"
	"sync"

	"github.com/gin-gonic/gin"
	"github.com/gorilla/websocket"
	"go-llama/internal/auth"
	"go-llama/internal/chat"
	"go-llama/internal/config"
	"go-llama/internal/db"
)

// WebSocket message format
type WSChatPrompt struct {
	ChatID    int    `json:"chatId"`
	Prompt    string `json:"prompt"`
	WebSearch bool   `json:"web_search"`
}

// WebSocket streaming token format
type WSChatToken struct {
	Token string `json:"token"`
	Index int    `json:"index"`
}

var wsUpgrader = websocket.Upgrader{
	CheckOrigin: func(r *http.Request) bool { return true },
}

// WebSocket connection wrapper with mutex for thread-safe writes
type safeWSConn struct {
	conn *websocket.Conn
	mu   sync.Mutex
}

func (s *safeWSConn) WriteJSON(v interface{}) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.conn.WriteJSON(v)
}

func (s *safeWSConn) ReadMessage() (int, []byte, error) {
	return s.conn.ReadMessage()
}

func (s *safeWSConn) Close() error {
	return s.conn.Close()
}

// WSChatHandler is the main WebSocket entry point - routes to standard LLM or GrowerAI
func WSChatHandler(cfg *config.Config) gin.HandlerFunc {
	return func(c *gin.Context) {
		// Authenticate
		token := c.GetHeader("Authorization")
		if token == "" {
			token = c.Query("token")
		}
		if token == "" {
			c.JSON(http.StatusUnauthorized, gin.H{"error": "missing JWT"})
			return
		}
		token = strings.TrimPrefix(token, "Bearer ")
		claims, err := auth.ParseJWT(cfg.Server.JWTSecret, token)
		if err != nil {
			c.JSON(http.StatusUnauthorized, gin.H{"error": "invalid JWT"})
			return
		}
		c.Set("userId", claims.UserID)

		// Upgrade to WebSocket
		rawConn, err := wsUpgrader.Upgrade(c.Writer, c.Request, nil)
		if err != nil {
			log.Println("WebSocket upgrade failed:", err)
			return
		}
		conn := &safeWSConn{conn: rawConn}
		defer conn.Close()

		// Read initial message
		_, msg, err := conn.ReadMessage()
		if err != nil {
			conn.WriteJSON(map[string]string{"error": "invalid initial payload"})
			return
		}
		var req WSChatPrompt
		if err := json.Unmarshal(msg, &req); err != nil {
			conn.WriteJSON(map[string]string{"error": "invalid JSON"})
			return
		}

		// Get user ID
		userID, ok := getUserIDFromContext(c)
		if !ok {
			conn.WriteJSON(map[string]string{"error": "unauthorized"})
			return
		}

		// Fetch chat instance
		var chatInst chat.Chat
		if err := db.DB.Where("id = ? AND user_id = ?", req.ChatID, userID).First(&chatInst).Error; err != nil {
			conn.WriteJSON(map[string]string{"error": "chat not found"})
			return
		}

		// Route to appropriate handler
		if chatInst.UseGrowerAI {
			handleGrowerAIWebSocket(conn, cfg, &chatInst, req.Prompt, userID)
		} else {
			handleStandardLLMWebSocket(conn, cfg, &chatInst, req, userID)
		}
	}
}

// --- Helper functions ---

func getUserIDFromContext(c *gin.Context) (uint, bool) {
	val, exists := c.Get("userId")
	if !exists {
		return 0, false
	}
	userID, ok := val.(uint)
	return userID, ok
}

// extractSiteFilter extracts a single site filter from the prompt for search optimization.
// Priority: actual URL > text-based site reference (e.g., "on reddit")
// Returns: (cleanedPrompt, siteHost)
func extractSiteFilter(prompt string) (string, string) {
	// Step 1: Check for actual URLs first (highest priority)
	urlPattern := regexp.MustCompile(`https?://[^\s]+|(?:www\.)?[a-zA-Z0-9-]+\.[a-zA-Z]{2,}[^\s]*`)
	urls := urlPattern.FindAllString(prompt, -1)

	if len(urls) > 0 {
		// Extract host from first URL found
		parsed, err := url.Parse(urls[0])
		if err == nil && parsed.Host != "" {
			// Remove URL from prompt and return
			cleanedPrompt := urlPattern.ReplaceAllString(prompt, " ")
			cleanedPrompt = strings.TrimSpace(regexp.MustCompile(`\s+`).ReplaceAllString(cleanedPrompt, " "))
			return cleanedPrompt, parsed.Host
		}
	}

	// Step 2: No URL found, check for text-based site references
	sitePatterns := map[*regexp.Regexp]string{
		regexp.MustCompile(`(?i)\bon reddit\b`):           "reddit.com",
		regexp.MustCompile(`(?i)\bfrom reddit\b`):         "reddit.com",
		regexp.MustCompile(`(?i)\bin reddit\b`):           "reddit.com",
		regexp.MustCompile(`(?i)\bon stackoverflow\b`):    "stackoverflow.com",
		regexp.MustCompile(`(?i)\bfrom stackoverflow\b`):  "stackoverflow.com",
		regexp.MustCompile(`(?i)\bon stack overflow\b`):   "stackoverflow.com",
		regexp.MustCompile(`(?i)\bon github\b`):           "github.com",
		regexp.MustCompile(`(?i)\bfrom github\b`):         "github.com",
		regexp.MustCompile(`(?i)\bon twitter\b`):          "twitter.com",
		regexp.MustCompile(`(?i)\bfrom twitter\b`):        "twitter.com",
		regexp.MustCompile(`(?i)\bon youtube\b`):          "youtube.com",
		regexp.MustCompile(`(?i)\bfrom youtube\b`):        "youtube.com",
		regexp.MustCompile(`(?i)\bon wikipedia\b`):        "wikipedia.org",
		regexp.MustCompile(`(?i)\bfrom wikipedia\b`):      "wikipedia.org",
		regexp.MustCompile(`(?i)\bon hackernews\b`):       "news.ycombinator.com",
		regexp.MustCompile(`(?i)\bon hacker news\b`):      "news.ycombinator.com",
		regexp.MustCompile(`(?i)\bsite:([a-zA-Z0-9.-]+)`): "$1", // Explicit site: operator
	}

	cleanedPrompt := prompt
	for pattern, domain := range sitePatterns {
		if pattern.MatchString(cleanedPrompt) {
			// Handle site: operator specially (capture group)
			if strings.Contains(pattern.String(), "site:") {
				matches := pattern.FindStringSubmatch(cleanedPrompt)
				if len(matches) > 1 {
					cleanedPrompt = pattern.ReplaceAllString(cleanedPrompt, "")
					cleanedPrompt = strings.TrimSpace(regexp.MustCompile(`\s+`).ReplaceAllString(cleanedPrompt, " "))
					return cleanedPrompt, matches[1]
				}
			} else {
				// Remove the site reference phrase
				cleanedPrompt = pattern.ReplaceAllString(cleanedPrompt, "")
				cleanedPrompt = strings.TrimSpace(regexp.MustCompile(`\s+`).ReplaceAllString(cleanedPrompt, " "))
				return cleanedPrompt, domain
			}
		}
	}

	// No site filter found
	return prompt, ""
}

// shouldAutoSearch determines if web search should be triggered automatically
func shouldAutoSearch(cfg *config.Config, prompt string) bool {
	// Disable auto web search entirely if max results set to 0 or less
	if cfg.SearxNG.MaxResults <= 0 {
		return false
	}
	p := strings.ToLower(prompt)
	score := 0

	// User override: don't search
	denyList := []string{
		"don't search",
		"do not search",
		"dont search",
		"dont web search",
		"do not web search",
	}

	for _, phrase := range denyList {
		if strings.Contains(p, phrase) {
			return false
		}
	}

	explicitSearchPhrases := []string{
		// Direct commands
		"search the web",
		"search online",
		"search the internet",
		"search web",
		"search internet",

		// Imperatives phrased differently
		"look it up online",
		"look online",
		"look this up online",
		"look it up on the web",

		// Requests based on web sources
		"use the web",
		"use internet",
		"use the internet",
		"use online sources",
		"use online information",
		"use web results",

		// Declarative forms
		"get info from the web",
		"get information from the web",
		"get online information",
		"find this online",
		"find information online",
		"verify this online",

		// Short common expressions
		"google it",
		"bing it",
		"check online",
		"check the web",
		"check the internet",
		"web search",
	}

	for _, phrase := range explicitSearchPhrases {
		if strings.Contains(p, phrase) {
			return true
		}
	}

	// Recent years
	if strings.Contains(p, "2024") || strings.Contains(p, "2025") {
		score += 2
	}

	// Freshness words
	fresh := []string{"latest", "current", "today", "now"}
	for _, w := range fresh {
		if strings.Contains(p, w) {
			score += 2
			break
		}
	}

	// Price / rates
	priceWords := []string{"price", "rate", "convert", "exchange", "worth"}
	for _, w := range priceWords {
		if strings.Contains(p, w) {
			score += 2
			break
		}
	}

	// News / live info
	newsWords := []string{"news", "update", "trending", "live", "results"}
	for _, w := range newsWords {
		if strings.Contains(p, w) {
			score += 2
			break
		}
	}

	// Tickers
	if strings.Contains(p, "btc") || strings.Contains(p, "eth") ||
		strings.Contains(p, "aapl") || strings.Contains(p, "tsla") ||
		strings.Contains(p, "nvda") {
		score += 3
	}

	// Question pattern boost
	questionWords := []string{"who", "when", "where", "what", "will", "did", "does"}
	parts := strings.Fields(p)
	if len(parts) > 3 {
		for _, q := range questionWords {
			if strings.HasPrefix(p, q) {
				score++
				break
			}
		}
	}

	// Extra positive search signals
	if strings.HasSuffix(p, "?") {
		score++
	}
	if strings.Contains(p, " vs ") || strings.Contains(p, "compare") {
		score++
	}
	currencyHints := []string{"£", "$", "€", "price of", "how much is"}
	for _, w := range currencyHints {
		if strings.Contains(p, w) {
			score++
			break
		}
	}
	recentPhrases := []string{"breaking", "just announced", "results today"}
	for _, w := range recentPhrases {
		if strings.Contains(p, w) {
			score++
			break
		}
	}

	// Negative signals — reduce search likelihood
	negativeWords := []string{"explain", "tutorial", "guide", "overview", "story", "fiction"}
	for _, w := range negativeWords {
		if strings.Contains(p, w) {
			score -= 2
			break
		}
	}

	// Adjust search threshold based on prompt length
	words := strings.Fields(p)
	dynamicThreshold := 3 + (len(words) / 80) // +1 point per 80 words

	return score >= dynamicThreshold
}

// Known geographic & political keywords to preserve during search compression
var geoTokens = map[string]bool{
	"uk": true, "united kingdom": true, "britain": true, "british": true, "england": true, "scotland": true, "wales": true, "london": true,
	"us": true, "usa": true, "united states": true, "american": true, "america": true, "washington": true,
	"canada": true, "canadian": true, "ottawa": true,
	"australia": true, "australian": true, "sydney": true,
	"europe": true, "eu": true, "european": true,
	"france": true, "french": true, "paris": true,
	"germany": true, "german": true, "berlin": true,
	"italy": true, "italian": true, "rome": true,
	"spain": true, "spanish": true, "madrid": true,
	"japan": true, "japanese": true, "tokyo": true,
	"china": true, "chinese": true, "beijing": true,
	"india": true, "indian": true, "delhi": true,
	"russia": true, "russian": true, "moscow": true,
	"ukraine": true, "ukrainian": true, "kyiv": true,
	"brazil": true, "brazilian": true, "brasilia": true,
}

// compressForSearch compresses long prompts into clean search queries
func compressForSearch(prompt string) string {
	words := strings.Fields(prompt)
	if len(words) <= 20 {
		return prompt
	}

	p := strings.ToLower(prompt)

	stopwords := map[string]bool{
		"a": true, "an": true, "the": true, "of": true, "for": true, "to": true, "in": true, "on": true, "with": true, "and": true, "or": true,
		"by": true, "be": true, "is": true, "are": true, "that": true, "which": true, "this": true, "those": true, "about": true, "can": true,
		"you": true, "could": true, "would": true, "please": true, "tell": true, "me": true, "explain": true, "give": true, "i": true, "want": true,
	}

	tokens := strings.Fields(p)
	var keep []string

	for _, t := range tokens {
		if stopwords[t] {
			continue
		}
		// keep numbers and years
		if strings.ContainsAny(t, "0123456789") {
			keep = append(keep, t)
			continue
		}
		// known tickers
		if t == "btc" || t == "eth" || t == "aapl" || t == "tsla" || t == "nvda" {
			keep = append(keep, t)
			continue
		}
		// keep geo tokens even if short (e.g., "uk", "us", "eu")
		if len(t) <= 2 && !geoTokens[t] {
			continue
		}

		keep = append(keep, t)
	}

	// limit to 12 keywords max
	if len(keep) > 12 {
		keep = keep[:12]
	}

	if len(keep) < 3 {
		return prompt
	}
	// Ensure the original geo context is not lost
	for key := range geoTokens {
		if strings.Contains(p, key) {
			found := false
			for _, k := range keep {
				if k == key {
					found = true
					break
				}
			}
			if !found {
				keep = append(keep, key)
			}
		}
	}

	return strings.Join(keep, " ")
}

package api

import (
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"strings"

	"github.com/gin-gonic/gin"
	"go-llama/internal/config"
)

type SearxNGPromptRequest struct {
	Prompt string `json:"prompt"`
}
type SearxNGSource struct {
	Title   string `json:"title"`
	URL     string `json:"url"`
	Snippet string `json:"snippet"`
}
type SearxNGAnswerResponse struct {
	Answer  string          `json:"answer"`
	Sources []SearxNGSource `json:"sources"`
}

// POST /search
func SearxNGSearchHandler(cfg *config.Config) gin.HandlerFunc {
	return func(c *gin.Context) {
		var req SearxNGPromptRequest
		if err := c.ShouldBindJSON(&req); err != nil || req.Prompt == "" {
			c.JSON(http.StatusBadRequest, gin.H{"error": gin.H{"message": "Invalid prompt"}})
			return
		}

		// --- 1. Query SearxNG for sources ---
		searxngURL := cfg.SearxNG.URL
		if searxngURL == "" {
			searxngURL = "http://localhost:8888/search"
		}
		u, _ := url.Parse(searxngURL)
		q := u.Query()
		searchQuery := cleanForSearch(req.Prompt)
		q.Set("q", searchQuery)
		log.Printf("🔍 Cleaned query: %q → %q", req.Prompt, searchQuery)

		q.Set("format", "json")
		u.RawQuery = q.Encode()

		resp, err := http.Get(u.String())
		if err != nil {
			log.Println("SearxNG error:", err)
			c.JSON(http.StatusBadGateway, gin.H{"error": gin.H{"message": "SearxNG unavailable"}})
			return
		}
		defer resp.Body.Close()
		body, _ := io.ReadAll(resp.Body)

		var searxResults struct {
			Results []struct {
				Title   string `json:"title"`
				URL     string `json:"url"`
				Content string `json:"content"`
			} `json:"results"`
		}
		_ = json.Unmarshal(body, &searxResults)

		// --- Limit to MaxResults, then rank, then keep top 50% ---
		sources := []SearxNGSource{} // ✅ declared once, visible after block

		raw := searxResults.Results
		if len(raw) > 0 {
			// 1) Limit how many raw results we consider based on config
			limit := cfg.SearxNG.MaxResults
			if limit <= 0 || limit > len(raw) {
				limit = len(raw)
			}

			tmpResults := make([]SearxResult, 0, limit)
			for i := 0; i < limit; i++ {
				r := raw[i]
				tmpResults = append(tmpResults, SearxResult{
					Title:   r.Title,
					URL:     r.URL,
					Content: r.Content,
				})
			}

			// 2) Rank those, then keep top 50% of *limit*
			ranked := rankAndFilterResults(req.Prompt, tmpResults)

			keepTop := limit / 2
			if keepTop < 1 {
				keepTop = 1
			}
			if keepTop > len(ranked) {
				keepTop = len(ranked)
			}

			for i := 0; i < keepTop; i++ {
				r := ranked[i]
				if r.Title == "" || r.URL == "" {
					continue
				}
				snippet := enrichAndSummarize(r.URL, r.Content, searchQuery)
				sources = append(sources, SearxNGSource{
					Title:   r.Title,
					URL:     r.URL,
					Snippet: snippet,
				})
			}
		}

		// --- 2. Format context for LLM ---
		webContext := ""
		if len(sources) > 0 {
			webContext = "Web search results:\n\n"
			for i, src := range sources {
				webContext += fmt.Sprintf("[%d] %s\n\n", i+1, src.Snippet)
			}
			webContext += "Use the above information to answer. Cite sources as [1], [2].\n"
		}

		// --- 3. Build LLM payload ---
		var llmMessages []map[string]string
		if webContext != "" {
			llmMessages = append(llmMessages, map[string]string{
				"role":    "user",
				"content": webContext + req.Prompt,
			})
		} else {
			llmMessages = append(llmMessages, map[string]string{
				"role":    "user",
				"content": req.Prompt,
			})
		}

		// Use first model as default for /search
		modelName := "default"
		modelURL := ""
		if len(cfg.LLMs) > 0 {
			modelName = cfg.LLMs[0].Name
			modelURL = cfg.LLMs[0].URL
		}
		payload := map[string]interface{}{
			"model":    modelName,
			"messages": llmMessages,
		}

		llmResp, err := CallLLM(modelURL, payload)
		if err != nil {
			log.Println("LLM error:", err)
			c.JSON(http.StatusBadGateway, gin.H{"error": gin.H{"message": "LLM unavailable", "detail": err.Error()}})
			return
		}

		// --- 4. Respond with real LLM answer and sources ---
		c.JSON(http.StatusOK, SearxNGAnswerResponse{
			Answer:  strings.TrimSpace(llmResp.Reply),
			Sources: sources,
		})
	}
}

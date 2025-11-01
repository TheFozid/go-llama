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
		q.Set("q", req.Prompt)
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

		sources := []SearxNGSource{}
		for _, r := range searxResults.Results {
			if r.Title == "" || r.URL == "" || r.Content == "" {
				continue
			}
			sources = append(sources, SearxNGSource{
				Title:   r.Title,
				URL:     r.URL,
				Snippet: r.Content,
			})
			if len(sources) >= 3 {
				break
			}
		}

		// --- 2. Format context for LLM ---
		webContext := ""
		if len(sources) > 0 {
			webContext = "Web search results:\n"
			for i, src := range sources {
				webContext += fmt.Sprintf("[%d] \"%s\": %s (%s)\n", i+1, src.Title, src.Snippet, src.URL)
			}
webContext += "\nInstructions:\n" +
	"- Use the above results when answering\n" +
	"- Cite sources as [n] at the end of sentences that use them\n" +
	"- Do not generate a \\\"Sources\\\" section\n" +
	"- If unsure whether a sentence needs a citation, include it\n" +
	"- If the answer is not supported by the results, say so\n" +
	"- Format your answer in Markdown\n\n" +
	"User question:\n"

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

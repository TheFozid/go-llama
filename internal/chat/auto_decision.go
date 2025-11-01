// internal/chat/auto_decision.go
package chat

import (
	"bytes"
	"encoding/json"
	"net/http"
	"regexp"
	"strings"
	"time"
)

// -------- Heuristic (cheap) --------

var searchTriggerRegexps = []*regexp.Regexp{
	regexp.MustCompile(`\bwho\s+is\b`),
	regexp.MustCompile(`\bwhat\s+is\b`),
	regexp.MustCompile(`\b(current|today|latest|breaking)\b`),
	regexp.MustCompile(`\bnews\b`),
	regexp.MustCompile(`\b(update|updated|recent|changelog|release)\b`),
	regexp.MustCompile(`\b(price|prices|stock|stocks|rate|rates|exchange|fx)\b`),
	regexp.MustCompile(`\bweather\b`),
	regexp.MustCompile(`\b\d{4}\b`),              // any year mention
	regexp.MustCompile(`\b(\$|£|€)\s*\d+(\.\d+)?`), // currencies
}

// HeuristicNeedsSearch returns true if the prompt likely needs fresh web info.
func HeuristicNeedsSearch(prompt string) bool {
	p := strings.ToLower(prompt)
	for _, re := range searchTriggerRegexps {
		if re.FindStringIndex(p) != nil {
			return true
		}
	}
	return false
}

// -------- Minimal, non-streaming OpenAI-compatible call --------

type llmMsg struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}
type llmReq struct {
	Model    string   `json:"model"`
	Messages []llmMsg `json:"messages"`
	Stream   bool     `json:"stream,omitempty"`
}
type llmResp struct {
	Choices []struct {
		Message struct {
			Content string `json:"content"`
		} `json:"message"`
	} `json:"choices"`
}

func chatOnce(modelURL, modelName string, msgs []llmMsg) (string, error) {
	req := llmReq{
		Model:    modelName,
		Messages: msgs,
		Stream:   false,
	}
	body, _ := json.Marshal(req)

	httpClient := &http.Client{Timeout: 25 * time.Second}
	httpReq, _ := http.NewRequest("POST", modelURL, bytes.NewBuffer(body))
	httpReq.Header.Set("Content-Type", "application/json")

	resp, err := httpClient.Do(httpReq)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	var out llmResp
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return "", err
	}
	if len(out.Choices) == 0 {
		return "", nil
	}
	return strings.TrimSpace(out.Choices[0].Message.Content), nil
}

// -------- Decision: YES/NO + optional rewrite --------

// LLMSearchDecision returns true if the model says web search is needed.
func LLMSearchDecision(modelURL, modelName, userPrompt string) (bool, error) {
	system := "Decide if web search is needed to answer the user's question. " +
		"Reply EXACTLY one word: YES or NO. " +
		"Say YES for current facts, dates, prices, news, weather, software/library versions, world knowledge that changes. " +
		"If unsure, reply YES."
	msgs := []llmMsg{
		{Role: "system", Content: system},
		{Role: "user", Content: userPrompt},
	}
	out, err := chatOnce(modelURL, modelName, msgs)
	if err != nil {
		return false, err
	}
	switch strings.ToUpper(strings.TrimSpace(out)) {
	case "YES":
		return true, nil
	case "NO":
		return false, nil
	default:
		// Bias to safety
		return true, nil
	}
}

// RewriteForSearch asks the model to compress the user's question into a concise search query.
func RewriteForSearch(modelURL, modelName, userPrompt string) (string, error) {
	system := "Rewrite the user's question into a concise web search query. " +
		"Do NOT answer. Return ONLY the query, no quotes, no explanations."
	msgs := []llmMsg{
		{Role: "system", Content: system},
		{Role: "user", Content: userPrompt},
	}
	out, err := chatOnce(modelURL, modelName, msgs)
	if err != nil {
		return "", err
	}
	q := strings.TrimSpace(out)
	q = strings.Trim(q, `"'“”`)
	q = strings.TrimPrefix(q, "SEARCH:")
	return strings.TrimSpace(q), nil
}

// AutoSearchDecision decides whether to search and (optionally) returns a rewritten query.
// It is safe and cheap: heuristic first, then a single YES/NO model call if needed.
// If decision is YES, it tries to rewrite; if rewrite fails, returns the empty string for query.
func AutoSearchDecision(modelURL, modelName, userPrompt string) (shouldSearch bool, rewrittenQuery string) {
	// 1) Heuristic
	if HeuristicNeedsSearch(userPrompt) {
		shouldSearch = true
	} else {
		// 2) Tiny judge
		ok, err := LLMSearchDecision(modelURL, modelName, userPrompt)
		if err != nil {
			// On error, fail-closed: do not auto-search.
			ok = false
		}
		shouldSearch = ok
	}
	if shouldSearch {
		if q, err := RewriteForSearch(modelURL, modelName, userPrompt); err == nil && q != "" {
			return true, q
		}
		return true, ""
	}
	return false, ""
}

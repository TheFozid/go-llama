// internal/api/ws_streaming.go
package api

import (
	"bufio"
	"context"
	"encoding/json"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/gorilla/websocket"
)

// streamLLMResponseWS handles streaming LLM responses over WebSocket
// Returns the complete response text and tokens per second
func streamLLMResponseWS(safeConn *safeWSConn, rawConn *websocket.Conn, llmURL string, payload map[string]interface{}, respOut *string, toksPerSecOut *float64) error {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Listen for stop messages from client
	go func() {
		for {
			_, msg, err := rawConn.ReadMessage()
			if err != nil {
				cancel() // WS closed
				return
			}
			var req map[string]interface{}
			if json.Unmarshal(msg, &req) == nil && req["event"] == "stop" {
				cancel() // Explicit stop message
				return
			}
		}
	}()

	body, _ := json.Marshal(payload)
	req, _ := http.NewRequestWithContext(ctx, "POST", llmURL, strings.NewReader(string(body)))
	req.Header.Set("Content-Type", "application/json")
	client := http.Client{Timeout: 0}
	resp, err := client.Do(req)
	if err != nil {
		log.Printf("LLM HTTP request failed: %v", err)
		return err
	}
	defer resp.Body.Close()

	reader := bufio.NewReader(resp.Body)
	index := 0
	var responseBuilder strings.Builder
	var startTime time.Time
	firstToken := true
	inReasoning := false

	for {
		line, err := reader.ReadString('\n')
		if err != nil {
			break
		}
		line = strings.TrimSpace(line)
		if len(line) < 7 || !strings.HasPrefix(line, "data: ") {
			continue
		}
		data := line[6:]
		if data == "[DONE]" {
			break
		}
		
		var chunk struct {
			Choices []struct {
				Delta struct {
					Content          string `json:"content"`
					ReasoningContent string `json:"reasoning_content"`
				} `json:"delta"`
			} `json:"choices"`
			FinishReason string `json:"finish_reason"`
		}
		
		if err := json.Unmarshal([]byte(data), &chunk); err != nil {
			log.Printf("stream decode error: %v", err)
			continue
		}

		if len(chunk.Choices) > 0 {
			delta := chunk.Choices[0].Delta

			// Handle reasoning_content (thinking process)
			if delta.ReasoningContent != "" {
				token := delta.ReasoningContent

				// Start timer when we receive first token of any kind
				if firstToken {
					startTime = time.Now()
					firstToken = false
				}

				// If this is the first reasoning chunk, open <think>
				if !inReasoning {
					inReasoning = true
					token = "<think>" + token
				}

				// Append reasoning to the accumulated response
				responseBuilder.WriteString(token)

				// Stream to frontend
				safeConn.WriteJSON(WSChatToken{Token: token, Index: index})
				index++
			}

			// Handle normal content (the actual answer)
			if delta.Content != "" {
				token := delta.Content

				// Start timer if we never got any reasoning
				if firstToken {
					startTime = time.Now()
					firstToken = false
				}

				// If we were in a reasoning section, close </think> before answer
				if inReasoning {
					inReasoning = false
					// Close the think block in both saved content and streamed tokens
					responseBuilder.WriteString("</think>")
					safeConn.WriteJSON(WSChatToken{Token: "</think>", Index: index})
					index++
				}

				// Detect end tokens ONLY when stream is truly ending
				endTokens := []string{
					"<|end_of_text|>",
					"<|end|>",
					"<|assistant|>",
					"<|eot_id|>",
					"<|im_end|>",
					"[|endofturn|]",
				}

				isEndToken := false
				for _, t := range endTokens {
					if token == t {
						isEndToken = true
						break
					}
				}
				if isEndToken {
					continue
				}

				// Normal answer token
				responseBuilder.WriteString(token)
				safeConn.WriteJSON(WSChatToken{Token: token, Index: index})
				index++
			}
		}

		if chunk.FinishReason != "" {
			break
		}
	}

	// Close think if stream ended during reasoning with no final answer
	if inReasoning {
		responseBuilder.WriteString("</think>")
	}

	var toksPerSec float64
	if !startTime.IsZero() {
		duration := time.Since(startTime).Seconds()
		if duration > 0 {
			toksPerSec = float64(index) / duration
		}
	}

	safeConn.WriteJSON(map[string]interface{}{
		"event":          "end",
		"tokens_per_sec": toksPerSec,
	})
	*respOut = responseBuilder.String()
	*toksPerSecOut = toksPerSec
	return nil
}

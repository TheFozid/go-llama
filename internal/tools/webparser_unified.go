package tools

import (
	"bytes"
    "context"
    "encoding/json"
    "fmt"
    "io"
    "log"
    "net/http"
    "net/url"
    "strings"
    "time"

    "github.com/go-shiori/go-readability"
	"github.com/ledongthuc/pdf"
    "go-llama/internal/config"
)

// WebParserUnifiedTool provides intelligent web parsing with strategy selection
type WebParserUnifiedTool struct {
    httpClient        *http.Client
    userAgent         string
    maxSizeMB         int
    llmURL            string
    llmModel          string
    llmClient         interface{} // Queue client
    maxContentTokens  int         // Dynamic limit based on LLM context size (typically 2/3 of context)
}

// NewWebParserUnifiedTool creates a new unified parser
func NewWebParserUnifiedTool(userAgent string, llmURL string, llmModel string, maxPageSizeMB int, config ToolConfig, llmClient interface{}, maxContentTokens int) *WebParserUnifiedTool {
    timeout := time.Duration(config.TimeoutIdle) * time.Second
    if timeout == 0 {
        timeout = 90 * time.Second
    }

    // Fallback safety: if calculation resulted in 0 or less, revert to a safe default (6000)
    if maxContentTokens <= 0 {
        maxContentTokens = 6000
    }

    return &WebParserUnifiedTool{
        httpClient: &http.Client{
            Timeout: timeout,
            CheckRedirect: func(req *http.Request, via []*http.Request) error {
                if len(via) >= 10 {
                    return fmt.Errorf("stopped after 10 redirects")
                }
                return nil
            },
        },
        userAgent:        userAgent,
        maxSizeMB:        maxPageSizeMB,
        llmURL:           llmURL,
        llmModel:         llmModel,
        llmClient:        llmClient,
        maxContentTokens: maxContentTokens,
    }
}

// Name returns the tool identifier
func (t *WebParserUnifiedTool) Name() string {
    return "web_parse_unified"
}

// Description returns what the tool does
func (t *WebParserUnifiedTool) Description() string {
    return "Intelligently parse web pages: uses full parse for short pages, or LLM-driven selective chunking for large pages to maximize relevance."
}

// RequiresAuth returns false
func (t *WebParserUnifiedTool) RequiresAuth() bool {
    return false
}

// Execute runs the parser
func (t *WebParserUnifiedTool) Execute(ctx context.Context, params map[string]interface{}) (*ToolResult, error) {
    startTime := time.Now()

    // 1. Validate Params
    urlStr, ok := params["url"].(string)
    if !ok || urlStr == "" {
        return &ToolResult{Success: false, Error: "missing 'url' parameter"}, fmt.Errorf("missing url")
    }

    goal, _ := params["goal"].(string) // Optional, but critical for large pages

    if !strings.HasPrefix(urlStr, "http://") && !strings.HasPrefix(urlStr, "https://") {
        return &ToolResult{Success: false, Error: "invalid URL scheme"}, fmt.Errorf("invalid url")
    }

    // 2. Fetch & Extract
    article, err := t.fetchAndExtract(ctx, urlStr)
    if err != nil {
        return &ToolResult{Success: false, Error: fmt.Sprintf("Fetch failed: %v", err)}, err
    }

    // 3. Token Estimation
    tokens := t.estimateTokens(article.TextContent)
    
    var content string
    var strategy string
    var reasoning string

    // 4. Strategy Selection
    if tokens <= t.maxContentTokens {
        // STRATEGY: FULL
        strategy = "FULL_PARSE"
        reasoning = fmt.Sprintf("Page size (%d tokens) is within threshold (%d). Returning full content.", tokens, t.maxContentTokens)
        content = article.TextContent
        log.Printf("[WebParser] Strategy: FULL (Size: %d tokens)", tokens)
    } else {
        // STRATEGY: SELECTIVE
        strategy = "SELECTIVE_CHUNKING"
        log.Printf("[WebParser] Strategy: SELECTIVE (Size: %d tokens)", tokens)
        
        if goal == "" {
            // Fallback if no goal provided but page is huge
            // Use the dynamic limit instead of hardcoded 4000
            reasoning = fmt.Sprintf("Page size (%d tokens) exceeds threshold, but NO GOAL provided. Returning first %d tokens.", tokens, t.maxContentTokens)
            content = t.truncateText(article.TextContent, t.maxContentTokens)
        } else {
            // LLM Assisted Selection
            selectedContent, selReasoning, err := t.performSelectiveParsing(ctx, article, goal)
            if err != nil {
                // Use dynamic limit for fallback as well (split between metadata and content)
                fallbackLimit := t.maxContentTokens
                reasoning = fmt.Sprintf("Selective parsing failed: %v. Falling back to metadata + first %d tokens.", err, fallbackLimit)
                content = fmt.Sprintf("METADATA:\n%s\n\nTOP CONTENT:\n%s", t.formatMetadata(article), t.truncateText(article.TextContent, fallbackLimit))
            } else {
                content = selectedContent
                reasoning = selReasoning
            }
        }
    }

    // 5. Format Output
    output := fmt.Sprintf("=== WEB PARSER RESULTS ===\nStrategy: %s\nReasoning: %s\n\nSource: %s\n%s\n\nContent:\n%s",
        strategy, reasoning, article.Title, urlStr, content)

    return &ToolResult{
        Success:  true,
        Output:   output,
        Duration: time.Since(startTime),
        Metadata: map[string]interface{}{
            "url":           urlStr,
            "title":         article.Title,
            "strategy":      strategy,
            "final_tokens":  t.estimateTokens(content),
            "original_size": tokens,
        },
    }, nil
}

// fetchAndExtract handles HTTP, Readability, and PDF parsing
func (t *WebParserUnifiedTool) fetchAndExtract(ctx context.Context, urlString string) (*readability.Article, error) {
    parsedURL, err := url.Parse(urlString)
    if err != nil {
        return nil, err
    }

    req, err := http.NewRequestWithContext(ctx, "GET", urlString, nil)
    if err != nil {
        return nil, err
    }
    req.Header.Set("User-Agent", t.userAgent)

    resp, err := t.httpClient.Do(req)
    if err != nil {
        return nil, err
    }
    defer resp.Body.Close()

    if resp.StatusCode != http.StatusOK {
        return nil, fmt.Errorf("HTTP %d", resp.StatusCode)
    }

    maxBytes := int64(t.maxSizeMB * 1024 * 1024)
    limitedReader := io.LimitReader(resp.Body, maxBytes)
    data, err := io.ReadAll(limitedReader)
    if err != nil {
        return nil, err
    }

    // Check Content-Type to determine parsing strategy
    contentType := resp.Header.Get("Content-Type")

    if strings.Contains(contentType, "application/pdf") {
        // --- PDF PARSING LOGIC ---
        log.Printf("[WebParser] Detected PDF, extracting text...")
        
        // Create a reader from the byte data
        pdfReader, err := pdf.NewReader(bytes.NewReader(data), int64(len(data)))
        if err != nil {
            return nil, fmt.Errorf("failed to open PDF: %w", err)
        }

        var pdfTextBuilder strings.Builder
        numPages := pdfReader.NumPage()

        // Limit PDF pages if necessary to prevent massive processing times
        // Optional: You could add a check like if numPages > 50 { return error }
        
        for i := 1; i <= numPages; i++ {
            page := pdfReader.Page(i)
            if page.V.IsNull() {
                continue // Skip empty pages
            }
            
            var txt pdf.Text
            err = page.GetContent(&txt, nil)
            if err != nil {
                log.Printf("[WebParser] Warning: failed to extract text from page %d: %v", i, err)
                continue
            }
            pdfTextBuilder.WriteString(txt.String())
            pdfTextBuilder.WriteString("\n") // Ensure spacing between pages
        }

        // Map PDF content to the Article struct so the rest of the pipeline remains unchanged
        return &readability.Article{
            Title:       "PDF Document: " + parsedURL.Path, // PDFs often lack good titles in metadata
            Content:     pdfTextBuilder.String(),
            TextContent: pdfTextBuilder.String(),
            Length:      pdfTextBuilder.Len(),
        }, nil

    } else {
        // --- HTML PARSING LOGIC (Existing) ---
        article, err := readability.FromReader(strings.NewReader(string(data)), parsedURL)
        if err != nil {
            return nil, err
        }
        return &article, nil
    }
}

// performSelectiveParsing asks the LLM which chunks to read
func (t *WebParserUnifiedTool) performSelectiveParsing(ctx context.Context, article *readability.Article, goal string) (string, string, error) {
    // 1. Create Chunks (approx 500 tokens each)
    chunks := t.createChunks(article.TextContent, 2000) // 2000 chars ~ 500 tokens

    // 2. Generate Chunk Map (Metadata for LLM)
    type ChunkInfo struct {
        Index   int    `json:"index"`
        Preview string `json:"preview"`
    }
    
    chunkInfos := []ChunkInfo{}
    for i, c := range chunks {
        preview := strings.ReplaceAll(c, "\n", " ")
        if len(preview) > 100 {
            preview = preview[:100] + "..."
        }
        chunkInfos = append(chunkInfos, ChunkInfo{Index: i, Preview: preview})
    }

    // 3. Prompt LLM
    mapJSON, _ := json.Marshal(chunkInfos) // Typo fix here
    
    prompt := fmt.Sprintf(`You are a research assistant. Your goal is: "%s".

I have a large web page divided into %d chunks. I cannot read them all. 
Here is a map of the chunks (Index and Preview):

%s

TASK: Identify the chunk indexes (0 to %d) that are MOST LIKELY to contain information relevant to the goal.
- Return ONLY a JSON array of integers. 
- Example: [1, 3, 5]
- If none seem relevant, return [].
- Be precise. Do not guess.

Relevant Chunk Indexes:`,
        goal, len(chunks), string(mapJSON), len(chunks)-1)

    reqBody := map[string]interface{}{
        "model": t.llmModel,
        "messages": []map[string]string{
            {"role": "system", "content": "You are a JSON API for content selection. Output ONLY valid JSON arrays."},
            {"role": "user", "content": prompt},
        },
        "temperature": 0.1,
        "stream":      false,
    }

    if t.llmClient == nil {
        return "", "", fmt.Errorf("LLM client unavailable")
    }

    type LLMCaller interface {
        Call(ctx context.Context, url string, payload map[string]interface{}) ([]byte, error)
    }

    client, ok := t.llmClient.(LLMCaller)
    if !ok {
        return "", "", fmt.Errorf("LLM client type assertion failed")
    }

    llmURL := config.GetChatURL(t.llmURL)
    body, err := client.Call(ctx, llmURL, reqBody)
    if err != nil {
        return "", "", fmt.Errorf("LLM Call failed: %w", err)
    }

    var llmResp struct {
        Choices []struct {
            Message struct {
                Content string `json:"content"`
            } `json:"message"`
        } `json:"choices"`
    }
    
    if err := json.Unmarshal(body, &llmResp); err != nil {
        return "", "", fmt.Errorf("LLM Decode failed: %w", err)
    }

    // 4. Parse Selection
    var selectedIndexes []int
    if err := json.Unmarshal([]byte(llmResp.Choices[0].Message.Content), &selectedIndexes); err != nil {
        return "", "", fmt.Errorf("LLM JSON Parse failed: %w (Content: %s)", err, llmResp.Choices[0].Message.Content)
    }

    // 5. Stitch Content
    var builder strings.Builder
    builder.WriteString(fmt.Sprintf("SELECTIVE EXTRACTION: Goal='%s'. Selected %d/%d chunks.\n\n", goal, len(selectedIndexes), len(chunks)))
    
    for _, idx := range selectedIndexes {
        if idx >= 0 && idx < len(chunks) {
            builder.WriteString(fmt.Sprintf("--- CHUNK %d ---\n%s\n\n", idx, chunks[idx]))
        }
    }

    reasoning := fmt.Sprintf("Page size (%d tokens) required selection. LLM selected %d chunks based on goal '%s'.", t.estimateTokens(article.TextContent), len(selectedIndexes), goal)
    return builder.String(), reasoning, nil
}

// createChunks splits text into roughly equal sized chunks
func (t *WebParserUnifiedTool) createChunks(text string, size int) []string {
    if len(text) == 0 {
        return []string{}
    }
    
    var chunks []string
    for len(text) > 0 {
        if len(text) <= size {
            chunks = append(chunks, text)
            break
        }
        
        // Try to break at a newline or space
        cutPoint := size
        if idx := strings.LastIndex(text[:cutPoint], "\n\n"); idx > size/2 {
            cutPoint = idx + 2
        } else if idx := strings.LastIndex(text[:cutPoint], "\n"); idx > size/2 {
            cutPoint = idx + 1
        } else if idx := strings.LastIndex(text[:cutPoint], " "); idx > size/2 {
            cutPoint = idx + 1
        }
        
        chunks = append(chunks, text[:cutPoint])
        text = text[cutPoint:]
    }
    return chunks
}

// estimateTokens
func (t *WebParserUnifiedTool) estimateTokens(text string) int {
    return int(float64(len(text)) / 4.0)
}

func (t *WebParserUnifiedTool) truncateText(text string, maxChars int) string {
    if len(text) <= maxChars {
        return text
    }
    return text[:maxChars] + "...[truncated]"
}

func (t *WebParserUnifiedTool) formatMetadata(article *readability.Article) string {
    return fmt.Sprintf("Title: %s\nLength: %d chars\nExcerpt: %s", article.Title, len(article.TextContent), article.Excerpt)
}

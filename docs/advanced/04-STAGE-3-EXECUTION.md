# Stage 3: Execution Paths
## Medium & Full Path with Multi-Model Orchestration

**Timeline:** Week 5 (5-7 days)  
**Prerequisites:** Stages 0-2 complete, fast path operational  
**Owner:** Backend Team  
**Status:** Not Started

---

## Objectives

1. Build medium path with multi-model consensus
2. Create full path with web search integration
3. Implement response streaming for real-time feedback
4. Build tool orchestration framework
5. Create fallback mechanisms between paths
6. Integrate with existing SearXNG search system
7. Achieve <1.5s for medium path, <3s for full path

---

## Architecture Overview
```
Query Input
    │
    ├──> Confidence Score ──> Route Decision
    │
    ├──> Fast Path (Stage 2) ──> < 100ms ──> Response
    │
    ├──> Medium Path ──────────────────┐
    │    • Extract entities            │
    │    • Enrich context              │
    │    • Run 2-3 specialist models   │
    │    • Simple voting/consensus     │
    │    └─> 500ms - 1.5s ──> Response │
    │                                   │
    └──> Full Path ────────────────────┘
         • Query rewriting
         • Web search (SearXNG)
         • Content extraction
         • Context building
         • Best model execution
         • Optional verification
         └─> 1s - 3s ──> Response
```

---

## Component 1: Medium Path Executor

### Purpose
Handle moderate-complexity queries using multiple models for validation and consensus.

### Design

**File:** `internal/intelligence/medium_path.go`
```go
package intelligence

import (
    "context"
    "fmt"
    "strings"
    "time"
    
    "github.com/rs/zerolog/log"
)

// MediumPath handles moderate complexity queries with multi-model consensus
type MediumPath struct {
    intelligence *Intelligence
    faqCache     *FAQCache
    models       map[string]*Model // Will be implemented in model.go
}

// NewMediumPath creates a new medium path executor
func NewMediumPath(intel *Intelligence, cache *FAQCache, models map[string]*Model) *MediumPath {
    return &MediumPath{
        intelligence: intel,
        faqCache:     cache,
        models:       models,
    }
}

// Execute executes a query via the medium path
func (mp *MediumPath) Execute(ctx context.Context, query string) (*Response, error) {
    startTime := time.Now()
    
    // Step 1: Extract entities for context
    entities := mp.intelligence.EntityExtractor.Extract(query)
    
    // Step 2: Enrich query with entity context
    enrichedQuery := query
    if len(entities) > 0 {
        enrichedQuery = mp.intelligence.EntityExtractor.EnrichContext(query, entities)
    }
    
    // Step 3: Categorize query to select models
    category := mp.categorizeQuery(query)
    
    // Step 4: Select 2-3 models based on category
    selectedModels := mp.selectModels(category)
    
    if len(selectedModels) == 0 {
        return nil, fmt.Errorf("no suitable models for category: %s", category)
    }
    
    // Step 5: Get responses from selected models
    responses := make([]string, 0, len(selectedModels))
    confidences := make([]float64, 0, len(selectedModels))
    
    for modelName, model := range selectedModels {
        resp, err := model.Generate(ctx, enrichedQuery)
        if err != nil {
            log.Warn().
                Err(err).
                Str("model", modelName).
                Msg("Model generation failed")
            continue
        }
        
        responses = append(responses, resp)
        confidences = append(confidences, 0.8) // Base confidence
        
        log.Debug().
            Str("model", modelName).
            Int("response_len", len(resp)).
            Msg("Model response received")
    }
    
    if len(responses) == 0 {
        return nil, fmt.Errorf("all models failed to generate responses")
    }
    
    // Step 6: Apply consensus logic
    finalResponse, confidence := mp.applyConsensus(responses, confidences)
    
    // Step 7: Cache if high confidence
    if confidence > 0.75 {
        mp.cacheResponse(query, finalResponse, category, confidence)
    }
    
    return &Response{
        Text:       finalResponse,
        Confidence: confidence,
        Source:     fmt.Sprintf("medium_path_%s", category),
        Latency:    time.Since(startTime),
        Cached:     false,
    }, nil
}

// categorizeQuery determines query category for model selection
func (mp *MediumPath) categorizeQuery(query string) string {
    q := strings.ToLower(query)
    
    // Factual queries
    factualIndicators := []string{"who is", "what is", "where is", "when did", "when was"}
    for _, indicator := range factualIndicators {
        if strings.Contains(q, indicator) {
            return "factual"
        }
    }
    
    // Reasoning queries
    reasoningIndicators := []string{"why", "how does", "explain", "how can", "what causes"}
    for _, indicator := range reasoningIndicators {
        if strings.Contains(q, indicator) {
            return "reasoning"
        }
    }
    
    // Comparison queries
    if strings.Contains(q, "compare") || strings.Contains(q, "difference") || 
       strings.Contains(q, "versus") || strings.Contains(q, "vs") {
        return "comparison"
    }
    
    // Creative queries
    creativeIndicators := []string{"write", "create", "generate", "compose", "draft"}
    for _, indicator := range creativeIndicators {
        if strings.Contains(q, indicator) {
            return "creative"
        }
    }
    
    // Default to factual
    return "factual"
}

// selectModels selects appropriate models based on category
func (mp *MediumPath) selectModels(category string) map[string]*Model {
    selected := make(map[string]*Model)
    
    switch category {
    case "factual":
        // Use factual model + one other for validation
        if model, ok := mp.models["factual"]; ok {
            selected["factual"] = model
        }
        if model, ok := mp.models["reasoning"]; ok {
            selected["reasoning"] = model
        }
        
    case "reasoning":
        // Use reasoning model + factual for grounding
        if model, ok := mp.models["reasoning"]; ok {
            selected["reasoning"] = model
        }
        if model, ok := mp.models["factual"]; ok {
            selected["factual"] = model
        }
        
    case "comparison":
        // Use all models for diverse perspectives
        for name, model := range mp.models {
            if name != "creative" {
                selected[name] = model
            }
        }
        
    case "creative":
        // Use creative model only
        if model, ok := mp.models["creative"]; ok {
            selected["creative"] = model
        }
        
    default:
        // Use factual as fallback
        if model, ok := mp.models["factual"]; ok {
            selected["factual"] = model
        }
    }
    
    return selected
}

// applyConsensus determines final answer from multiple model responses
func (mp *MediumPath) applyConsensus(responses []string, confidences []float64) (string, float64) {
    if len(responses) == 0 {
        return "", 0
    }
    
    if len(responses) == 1 {
        return responses[0], confidences[0]
    }
    
    // Simple strategy: Use longest response with highest confidence
    // In production, implement more sophisticated consensus:
    // - Semantic similarity comparison
    // - Fact extraction and validation
    // - Voting on key facts
    
    bestIdx := 0
    bestScore := float64(len(responses[0])) * confidences[0]
    
    for i := 1; i < len(responses); i++ {
        score := float64(len(responses[i])) * confidences[i]
        if score > bestScore {
            bestScore = score
            bestIdx = i
        }
    }
    
    // Average confidence of all models
    avgConfidence := 0.0
    for _, conf := range confidences {
        avgConfidence += conf
    }
    avgConfidence /= float64(len(confidences))
    
    // Boost confidence if responses are similar
    if mp.responsesAreSimilar(responses) {
        avgConfidence = avgConfidence * 1.1
        if avgConfidence > 1.0 {
            avgConfidence = 1.0
        }
    }
    
    return responses[bestIdx], avgConfidence
}

// responsesAreSimilar checks if multiple responses agree
func (mp *MediumPath) responsesAreSimilar(responses []string) bool {
    if len(responses) < 2 {
        return false
    }
    
    // Simple heuristic: check if responses share significant overlap
    // In production, use semantic similarity
    
    words1 := strings.Fields(strings.ToLower(responses[0]))
    words2 := strings.Fields(strings.ToLower(responses[1]))
    
    commonWords := 0
    for _, w1 := range words1 {
        for _, w2 := range words2 {
            if w1 == w2 && len(w1) > 3 { // Ignore short words
                commonWords++
            }
        }
    }
    
    // If >40% overlap, consider similar
    avgLen := (len(words1) + len(words2)) / 2
    return float64(commonWords)/float64(avgLen) > 0.4
}

// cacheResponse stores successful response in cache
func (mp *MediumPath) cacheResponse(query, answer, category string, confidence float64) {
    var confType temporal.ConfidenceType
    var expiresIn time.Duration
    
    switch category {
    case "factual":
        confType = temporal.SlowChange
        expiresIn = 30 * 24 * time.Hour
    case "reasoning":
        confType = temporal.SlowChange
        expiresIn = 7 * 24 * time.Hour
    default:
        confType = temporal.SlowChange
        expiresIn = 7 * 24 * time.Hour
    }
    
    err := mp.faqCache.Store(query, answer, []string{}, confidence, confType, expiresIn)
    if err != nil {
        log.Error().Err(err).Msg("Failed to cache medium path response")
    }
}
```

---

## Component 2: Full Path Executor

### Purpose
Handle complex queries requiring web search, content extraction, and comprehensive reasoning.

### Design

**File:** `internal/intelligence/full_path.go`
```go
package intelligence

import (
    "context"
    "fmt"
    "strings"
    "time"
    
    "github.com/rs/zerolog/log"
)

// FullPath handles complex queries with web search and comprehensive processing
type FullPath struct {
    intelligence *Intelligence
    faqCache     *FAQCache
    models       map[string]*Model
    searchClient *SearchClient // Will integrate with existing SearXNG
    extractor    *ContentExtractor
}

// SearchResult represents a web search result
type SearchResult struct {
    Title   string
    URL     string
    Snippet string
    Score   float64
}

// NewFullPath creates a new full path executor
func NewFullPath(
    intel *Intelligence,
    cache *FAQCache,
    models map[string]*Model,
    searchClient *SearchClient,
    extractor *ContentExtractor,
) *FullPath {
    return &FullPath{
        intelligence: intel,
        faqCache:     cache,
        models:       models,
        searchClient: searchClient,
        extractor:    extractor,
    }
}

// Execute executes a query via the full path
func (fp *FullPath) Execute(ctx context.Context, query string) (*Response, error) {
    startTime := time.Now()
    
    // Step 1: Determine if search is needed
    needsSearch := fp.queryNeedsSearch(query)
    
    var searchContext string
    var sources []string
    
    if needsSearch {
        // Step 2: Perform web search
        results, err := fp.searchClient.Search(ctx, query)
        if err != nil {
            log.Error().Err(err).Msg("Web search failed")
            // Continue without search results
        } else {
            // Step 3: Extract and enrich content
            searchContext, sources = fp.buildSearchContext(ctx, results)
        }
    }
    
    // Step 4: Extract entities for additional context
    entities := fp.intelligence.EntityExtractor.Extract(query)
    entityContext := ""
    if len(entities) > 0 {
        entityContext = fp.intelligence.EntityExtractor.EnrichContext("", entities)
    }
    
    // Step 5: Build comprehensive prompt
    prompt := fp.buildPrompt(query, searchContext, entityContext)
    
    // Step 6: Select best model for this query
    modelName := fp.selectBestModel(query)
    model, ok := fp.models[modelName]
    if !ok {
        return nil, fmt.Errorf("model not found: %s", modelName)
    }
    
    log.Debug().
        Str("model", modelName).
        Bool("has_search", needsSearch).
        Int("entities", len(entities)).
        Msg("Executing full path")
    
    // Step 7: Generate response
    answer, err := model.Generate(ctx, prompt)
    if err != nil {
        return nil, fmt.Errorf("model generation failed: %w", err)
    }
    
    // Step 8: Post-process and validate
    answer = fp.postProcess(answer)
    
    confidence := fp.calculateConfidence(answer, searchContext, entities)
    
    // Step 9: Cache if high confidence
    if confidence > 0.8 {
        fp.cacheResponse(query, answer, sources, confidence)
    }
    
    return &Response{
        Text:       answer,
        Confidence: confidence,
        Source:     "full_path",
        Sources:    sources,
        Latency:    time.Since(startTime),
        Cached:     false,
    }, nil
}

// queryNeedsSearch determines if web search is beneficial
func (fp *FullPath) queryNeedsSearch(query string) bool {
    q := strings.ToLower(query)
    
    // Current events/facts
    currentIndicators := []string{
        "current", "latest", "recent", "today", "now",
        "who is the", "what is the current", "latest news",
    }
    for _, indicator := range currentIndicators {
        if strings.Contains(q, indicator) {
            return true
        }
    }
    
    // Specific factual queries
    factualIndicators := []string{
        "when did", "who invented", "where is",
        "what happened", "how many",
    }
    for _, indicator := range factualIndicators {
        if strings.Contains(q, indicator) {
            return true
        }
    }
    
    // Check if pattern requires search
    match := fp.intelligence.PatternMatcher.Match(query)
    if match != nil && match.Pattern.RequiresSearch {
        return true
    }
    
    // Default: search for queries without clear entity context
    entities := fp.intelligence.EntityExtractor.Extract(query)
    return len(entities) < 2
}

// buildSearchContext extracts and formats search results
func (fp *FullPath) buildSearchContext(ctx context.Context, results []SearchResult) (string, []string) {
    var context strings.Builder
    sources := make([]string, 0, len(results))
    
    context.WriteString("Relevant information from web search:\n\n")
    
    for i, result := range results {
        if i >= 5 { // Limit to top 5 results
            break
        }
        
        // Extract full content if possible
        content := result.Snippet
        if fp.extractor != nil {
            extracted, err := fp.extractor.Extract(ctx, result.URL)
            if err == nil && len(extracted) > len(content) {
                content = extracted
            }
        }
        
        context.WriteString(fmt.Sprintf("[%d] %s\n", i+1, result.Title))
        context.WriteString(fmt.Sprintf("Source: %s\n", result.URL))
        context.WriteString(fmt.Sprintf("%s\n\n", content))
        
        sources = append(sources, result.URL)
    }
    
    return context.String(), sources
}

// buildPrompt constructs comprehensive prompt with all context
func (fp *FullPath) buildPrompt(query, searchContext, entityContext string) string {
    var prompt strings.Builder
    
    if searchContext != "" {
        prompt.WriteString(searchContext)
        prompt.WriteString("\n")
    }
    
    if entityContext != "" {
        prompt.WriteString(entityContext)
        prompt.WriteString("\n")
    }
    
    prompt.WriteString(fmt.Sprintf("Question: %s\n\n", query))
    prompt.WriteString("Please provide a comprehensive, accurate answer based on the information above. ")
    prompt.WriteString("Cite sources when making specific claims.")
    
    return prompt.String()
}

// selectBestModel chooses optimal model for query
func (fp *FullPath) selectBestModel(query string) string {
    complexity := fp.intelligence.ConfidenceScorer.AssessComplexity(query)
    
    switch complexity {
    case "research", "complex":
        return "reasoning" // Use most capable model
    case "medium":
        return "factual"
    default:
        return "factual"
    }
}

// postProcess cleans up and validates the response
func (fp *FullPath) postProcess(answer string) string {
    // Remove markdown artifacts if present
    answer = strings.TrimSpace(answer)
    
    // Remove common prefixes
    prefixes := []string{
        "Based on the search results, ",
        "According to the information provided, ",
        "Here is the answer: ",
    }
    for _, prefix := range prefixes {
        if strings.HasPrefix(answer, prefix) {
            answer = strings.TrimPrefix(answer, prefix)
            break
        }
    }
    
    return answer
}

// calculateConfidence estimates response confidence
func (fp *FullPath) calculateConfidence(answer, searchContext string, entities []*Entity) float64 {
    confidence := 0.5 // Base confidence
    
    // Boost for search-backed answers
    if searchContext != "" {
        confidence += 0.2
    }
    
    // Boost for entity grounding
    if len(entities) > 0 {
        confidence += 0.1 * float64(len(entities))
        if confidence > 0.9 {
            confidence = 0.9
        }
    }
    
    // Penalize very short answers (likely incomplete)
    if len(answer) < 50 {
        confidence -= 0.2
    }
    
    // Boost for longer, detailed answers
    if len(answer) > 200 {
        confidence += 0.1
    }
    
    // Clamp
    if confidence < 0.3 {
        confidence = 0.3
    }
    if confidence > 0.95 {
        confidence = 0.95
    }
    
    return confidence
}

// cacheResponse stores successful response
func (fp *FullPath) cacheResponse(query, answer string, sources []string, confidence float64) {
    // Determine expiry based on query type
    var confType temporal.ConfidenceType
    var expiresIn time.Duration
    
    q := strings.ToLower(query)
    if strings.Contains(q, "current") || strings.Contains(q, "latest") {
        confType = temporal.FastChange
        expiresIn = 24 * time.Hour
    } else if strings.Contains(q, "price") || strings.Contains(q, "weather") {
        confType = temporal.RealTime
        expiresIn = 1 * time.Hour
    } else {
        confType = temporal.SlowChange
        expiresIn = 7 * 24 * time.Hour
    }
    
    err := fp.faqCache.Store(query, answer, sources, confidence, confType, expiresIn)
    if err != nil {
        log.Error().Err(err).Msg("Failed to cache full path response")
    }
}
```

---

## Component 3: Search Client Integration

### Purpose
Integrate with existing SearXNG search infrastructure.

**File:** `internal/intelligence/search_client.go`
```go
package intelligence

import (
    "context"
    "encoding/json"
    "fmt"
    "io"
    "net/http"
    "net/url"
    "time"
    
    "github.com/rs/zerolog/log"
)

// SearchClient handles web search via SearXNG
type SearchClient struct {
    baseURL    string
    httpClient *http.Client
    timeout    time.Duration
}

// NewSearchClient creates a new search client
func NewSearchClient(baseURL string, timeout time.Duration) *SearchClient {
    return &SearchClient{
        baseURL: baseURL,
        httpClient: &http.Client{
            Timeout: timeout,
        },
        timeout: timeout,
    }
}

// Search performs a web search and returns results
func (sc *SearchClient) Search(ctx context.Context, query string) ([]SearchResult, error) {
    // Build request URL
    params := url.Values{}
    params.Set("q", query)
    params.Set("format", "json")
    params.Set("categories", "general")
    
    reqURL := fmt.Sprintf("%s/search?%s", sc.baseURL, params.Encode())
    
    // Create request with context
    req, err := http.NewRequestWithContext(ctx, "GET", reqURL, nil)
    if err != nil {
        return nil, fmt.Errorf("failed to create request: %w", err)
    }
    
    // Execute request
    resp, err := sc.httpClient.Do(req)
    if err != nil {
        return nil, fmt.Errorf("search request failed: %w", err)
    }
    defer resp.Body.Close()
    
    if resp.StatusCode != http.StatusOK {
        return nil, fmt.Errorf("search returned status %d", resp.StatusCode)
    }
    
    // Parse response
    body, err := io.ReadAll(resp.Body)
    if err != nil {
        return nil, fmt.Errorf("failed to read response: %w", err)
    }
    
    var searchResp struct {
        Results []struct {
            Title   string  `json:"title"`
            URL     string  `json:"url"`
            Content string  `json:"content"`
            Score   float64 `json:"score"`
        } `json:"results"`
    }
    
    if err := json.Unmarshal(body, &searchResp); err != nil {
        return nil, fmt.Errorf("failed to parse response: %w", err)
    }
    
    // Convert to SearchResult
    results := make([]SearchResult, 0, len(searchResp.Results))
    for _, r := range searchResp.Results {
        results = append(results, SearchResult{
            Title:   r.Title,
            URL:     r.URL,
            Snippet: r.Content,
            Score:   r.Score,
        })
    }
    
    log.Debug().
        Str("query", query).
        Int("results", len(results)).
        Msg("Search completed")
    
    return results, nil
}
```

---

## Component 4: Content Extractor

### Purpose
Extract full content from web pages for comprehensive context.

**File:** `internal/intelligence/content_extractor.go`
```go
package intelligence

import (
    "context"
    "fmt"
    "io"
    "net/http"
    "strings"
    "time"
    
    "github.com/rs/zerolog/log"
    "golang.org/x/net/html"
)

// ContentExtractor extracts readable content from web pages
type ContentExtractor struct {
    httpClient *http.Client
    timeout    time.Duration
    maxSize    int64 // Maximum content size to extract
}

// NewContentExtractor creates a new content extractor
func NewContentExtractor(timeout time.Duration, maxSize int64) *ContentExtractor {
    return &ContentExtractor{
        httpClient: &http.Client{
            Timeout: timeout,
        },
        timeout: timeout,
        maxSize: maxSize,
    }
}

// Extract fetches and extracts readable content from a URL
func (ce *ContentExtractor) Extract(ctx context.Context, url string) (string, error) {
    // Create request
    req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
    if err != nil {
        return "", fmt.Errorf("failed to create request: %w", err)
    }
    
    req.Header.Set("User-Agent", "Mozilla/5.0 (compatible; IntelligentBot/1.0)")
    
    // Fetch page
    resp, err := ce.httpClient.Do(req)
    if err != nil {
        return "", fmt.Errorf("request failed: %w", err)
    }
    defer resp.Body.Close()
    
    if resp.StatusCode != http.StatusOK {
        return "", fmt.Errorf("unexpected status: %d", resp.StatusCode)
    }
    
    // Limit read size
    limitedReader := io.LimitReader(resp.Body, ce.maxSize)
    body, err := io.ReadAll(limitedReader)
    if err != nil {
        return "", fmt.Errorf("failed to read body: %w", err)
    }
    
    // Parse HTML and extract text
    text := ce.extractText(string(body))
    
    // Clean up
    text = ce.cleanText(text)
    
    log.Debug().
        Str("url", url).
        Int("length", len(text)).
        Msg("Content extracted")
    
    return text, nil
}

// extractText extracts text from HTML
func (ce *ContentExtractor) extractText(htmlContent string) string {
    doc, err := html.Parse(strings.NewReader(htmlContent))
    if err != nil {
        return ""
    }
    
    var text strings.Builder
    var extract func(*html.Node)
    
    extract = func(n *html.Node) {
        if n.Type == html.TextNode {
            text.WriteString(n.Data)
            text.WriteString(" ")
        }
        
        // Skip script and style tags
        if n.Type == html.ElementNode && (n.Data == "script" || n.Data == "style") {
            return
        }
        
        for c := n.FirstChild; c != nil; c = c.NextSibling {
            extract(c)
        }
    }
    
    extract(doc)
    return text.String()
}

// cleanText cleans extracted text
func (ce *ContentExtractor) cleanText(text string) string {
    // Remove extra whitespace
    text = strings.Join(strings.Fields(text), " ")
    
    // Limit length
    if len(text) > 5000 {
        text = text[:5000] + "..."
    }
    
    return strings.TrimSpace(text)
}
```

---

## Component 5: Model Interface

### Purpose
Abstract interface for LLM models.

**File:** `internal/intelligence/model.go`
```go
package intelligence

import (
    "context"
    "fmt"
    "time"
)

// Model represents an LLM model
type Model struct {
    Name       string
    Path       string
    MaxTokens  int
    Temperature float64
    
    // Model-specific implementation (to be filled with actual LLM binding)
    generator ModelGenerator
}

// ModelGenerator is the interface for actual LLM generation
type ModelGenerator interface {
    Generate(ctx context.Context, prompt string) (string, error)
    Close() error
}

// NewModel creates a new model instance
func NewModel(name, path string, generator ModelGenerator) *Model {
    return &Model{
        Name:        name,
        Path:        path,
        MaxTokens:   2048,
        Temperature: 0.7,
        generator:   generator,
    }
}

// Generate generates a response from the model
func (m *Model) Generate(ctx context.Context, prompt string) (string, error) {
    if m.generator == nil {
        return "", fmt.Errorf("model generator not initialized")
    }
    
    // Add timeout protection
    ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
    defer cancel()
    
    return m.generator.Generate(ctx, prompt)
}

// Close releases model resources
func (m *Model) Close() error {
    if m.generator == nil {
        return nil
    }
    return m.generator.Close()
}

// Stub implementation for testing
type StubModelGenerator struct {
    response string
}

func NewStubModelGenerator(response string) *StubModelGenerator {
    return &StubModelGenerator{response: response}
}

func (s *StubModelGenerator) Generate(ctx context.Context, prompt string) (string, error) {
    // Simulate some processing time
    time.Sleep(100 * time.Millisecond)
    return s.response, nil
}

func (s *StubModelGenerator) Close() error {
    return nil
}
```

---

## Component 6: Response Streaming

### Purpose
Stream responses to client in real-time for better UX.

**File:** `internal/intelligence/streaming.go`
```go
package intelligence

import (
    "context"
    "time"
)

// StreamChunk represents a chunk of streamed response
type StreamChunk struct {
    Text       string
    IsFinal    bool
    Confidence float64
    Source     string
    Timestamp  time.Time
}

// StreamWriter writes response chunks
type StreamWriter interface {
    WriteChunk(chunk *StreamChunk) error
    Close() error
}

// StreamingResponse wraps a response for streaming
type StreamingResponse struct {
    writer  StreamWriter
    buffer  []rune
    chunkSize int
}

// NewStreamingResponse creates a new streaming response
func NewStreamingResponse(writer StreamWriter, chunkSize int) *StreamingResponse {
    return &StreamingResponse{
        writer:    writer,
        buffer:    make([]rune, 0),
        chunkSize: chunkSize,
    }
}

// Write adds text to the streaming buffer
func (sr *StreamingResponse) Write(text string) error {
    sr.buffer = append(sr.buffer, []rune(text)...)
    
    // Send chunks when buffer reaches threshold
    for len(sr.buffer) >= sr.chunkSize {
        chunk := string(sr.buffer[:sr.chunkSize])
        sr.buffer = sr.buffer[sr.chunkSize:]
        
        if err := sr.writer.WriteChunk(&StreamChunk{
            Text:      chunk,
            IsFinal:   false,
            Timestamp: time.Now(),
        }); err != nil {
            return err
        }
    }
    
    return nil
}

// Flush sends any remaining buffered content
func (sr *StreamingResponse) Flush(confidence float64, source string) error {
    if len(sr.buffer) > 0 {
        chunk := string(sr.buffer)
        sr.buffer = sr.buffer[:0]
        
        return sr.writer.WriteChunk(&StreamChunk{
Text:       chunk,
IsFinal:    true,
Confidence: confidence,
Source:     source,
Timestamp:  time.Now(),
})
}

return sr.writer.WriteChunk(&StreamChunk{
    Text:       "",
    IsFinal:    true,
    Confidence: confidence,
    Source:     source,
    Timestamp:  time.Now(),
})

}

// Close closes the stream
func (sr *StreamingResponse) Close() error {
return sr.writer.Close()
}


---

## Component 7: Path Router with Fallbacks

### Purpose
Route queries to appropriate path with automatic fallback.

**File:** `internal/intelligence/router.go`
```go
package intelligence

import (
    "context"
    "fmt"
    "time"
    
    "github.com/rs/zerolog/log"
)

// Router routes queries to appropriate execution path
type Router struct {
    intelligence *Intelligence
    fastPath     *FastPath
    mediumPath   *MediumPath
    fullPath     *FullPath
}

// NewRouter creates a new router
func NewRouter(
    intel *Intelligence,
    fastPath *FastPath,
    mediumPath *MediumPath,
    fullPath *FullPath,
) *Router {
    return &Router{
        intelligence: intel,
        fastPath:     fastPath,
        mediumPath:   mediumPath,
        fullPath:     fullPath,
    }
}

// Route determines and executes the appropriate path
func (r *Router) Route(ctx context.Context, query string) (*Response, error) {
    startTime := time.Now()
    
    // Step 1: Rewrite query
    rewritten := r.intelligence.QueryRewriter.Rewrite(query)
    
    // Step 2: Calculate confidence score
    confidence := r.intelligence.ConfidenceScorer.Score(rewritten.Rewritten)
    
    log.Debug().
        Str("query", query).
        Float64("confidence", confidence.Score).
        Str("path", confidence.RecommendedPath).
        Strs("reasons", confidence.Reasons).
        Msg("Query routed")
    
    var response *Response
    var err error
    var path string
    
    // Step 3: Execute appropriate path with fallbacks
    switch confidence.RecommendedPath {
    case "fast":
        path = "fast"
        response, err = r.fastPath.Execute(ctx, rewritten.Rewritten)
        if err != nil {
            // Fallback to medium
            log.Warn().Err(err).Msg("Fast path failed, falling back to medium")
            path = "medium_fallback"
            response, err = r.mediumPath.Execute(ctx, rewritten.Rewritten)
        }
        
    case "medium":
        path = "medium"
        response, err = r.mediumPath.Execute(ctx, rewritten.Rewritten)
        if err != nil {
            // Fallback to full
            log.Warn().Err(err).Msg("Medium path failed, falling back to full")
            path = "full_fallback"
            response, err = r.fullPath.Execute(ctx, rewritten.Rewritten)
        }
        
    case "full":
        path = "full"
        response, err = r.fullPath.Execute(ctx, rewritten.Rewritten)
    }
    
    if err != nil {
        return nil, fmt.Errorf("all paths failed: %w", err)
    }
    
    // Add routing metadata
    response.Latency = time.Since(startTime)
    
    log.Info().
        Str("query", query).
        Str("path", path).
        Float64("confidence", response.Confidence).
        Dur("latency", response.Latency).
        Msg("Query completed")
    
    return response, nil
}
```

---

## Testing Checklist

- [ ] Medium path selects appropriate models
- [ ] Medium path applies consensus correctly
- [ ] Full path integrates with search
- [ ] Content extraction works
- [ ] Search client returns results
- [ ] Router selects correct path
- [ ] Fallbacks work when path fails
- [ ] Response streaming functional
- [ ] All paths meet latency targets

---

## Success Criteria

- [ ] Medium path responds in < 1.5s for 90% of queries
- [ ] Full path responds in < 3s for 90% of queries
- [ ] Search integration returns relevant results
- [ ] Fallback mechanisms prevent failures
- [ ] Router correctly routes 95% of queries
- [ ] All unit tests pass
- [ ] Integration tests with real models work

---

## Performance Targets

| Metric | Target | Stretch Goal |
|--------|--------|--------------|
| Medium Path (p90) | < 1.5s | < 1s |
| Full Path (p90) | < 3s | < 2s |
| Search Latency | < 500ms | < 300ms |
| Content Extraction | < 1s | < 500ms |
| Router Overhead | < 50ms | < 20ms |

---

## Next Stage

Once Stage 3 is complete, proceed to **Stage 4: Self-Learning System** where we'll build:
- Pattern discovery from successful queries
- Entity extraction from web content
- Abbreviation learning
- Temporal guardrails
- Learning logs

---

**Last Updated:** [Current Date]
**Status:** Ready for Implementation
**Estimated Time:** 5-7 days

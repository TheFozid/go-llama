# Stage 2: Caching & Fast Path
## Instant Response Mechanisms for Common Queries

**Timeline:** Week 4 (5-7 days)  
**Prerequisites:** Stage 1 complete, intelligence components operational  
**Owner:** Backend Team  
**Status:** Not Started

---

## Objectives

1. Build FAQ cache with exact query matching
2. Implement time-aware cache expiration and revalidation
3. Create fast path execution engine
4. Build regex-based answer generators (math, conversions, time)
5. Implement system query handlers
6. Achieve <100ms response time for cached queries
7. Target 20%+ cache hit rate within first week

---

## Architecture Overview
```
Query Input
    │
    ├──> Query Normalization (hash generation)
    │
    ├──> FAQ Cache Lookup ──> Hit? ──> Check Freshness ──> Return (10ms)
    │                           │
    │                           └──> Miss
    │
    ├──> Pattern Match ──> Regex Handler ──> Execute ──> Return (50ms)
    │
    └──> No Match ──> Route to Medium/Full Path
```

---

## Component 1: FAQ Cache

### Purpose
Store and retrieve exact matches for common queries with temporal awareness.

### Design

**File:** `internal/intelligence/faq_cache.go`
```go
package intelligence

import (
    "crypto/sha256"
    "database/sql"
    "encoding/hex"
    "encoding/json"
    "fmt"
    "strings"
    "time"
    
    "github.com/rs/zerolog/log"
    "github.com/yourusername/intelligent-llm-orchestration/pkg/temporal"
)

// CachedAnswer represents a cached query answer
type CachedAnswer struct {
    QueryHash       string
    CanonicalQuery  string
    Answer          string
    Sources         []string
    BaseConfidence  float64
    ConfidenceType  temporal.ConfidenceType
    CreatedAt       time.Time
    LastVerified    time.Time
    LastAccessed    time.Time
    HitCount        int
    NeedsRevalidation bool
}

// FAQCache handles exact query matching cache
type FAQCache struct {
    db *sql.DB
}

// NewFAQCache creates a new FAQ cache
func NewFAQCache(db *sql.DB) *FAQCache {
    return &FAQCache{db: db}
}

// Get retrieves a cached answer if available and fresh
func (fc *FAQCache) Get(query string) (*CachedAnswer, bool) {
    // Normalize query and generate hash
    normalized := fc.normalizeQuery(query)
    hash := fc.hashQuery(normalized)
    
    var cached CachedAnswer
    var sourcesJSON sql.NullString
    var confType string
    
    err := fc.db.QueryRow(`
        SELECT 
            query_hash,
            canonical_query,
            answer,
            sources,
            base_confidence,
            confidence_type,
            created_at,
            last_verified,
            last_accessed,
            hit_count,
            needs_revalidation
        FROM faq_cache
        WHERE query_hash = ?
        AND (expires_at IS NULL OR expires_at > ?)
    `, hash, time.Now()).Scan(
        &cached.QueryHash,
        &cached.CanonicalQuery,
        &cached.Answer,
        &sourcesJSON,
        &cached.BaseConfidence,
        &confType,
        &cached.CreatedAt,
        &cached.LastVerified,
        &cached.LastAccessed,
        &cached.HitCount,
        &cached.NeedsRevalidation,
    )
    
    if err == sql.ErrNoRows {
        return nil, false
    }
    
    if err != nil {
        log.Error().Err(err).Str("hash", hash).Msg("Failed to retrieve cached answer")
        return nil, false
    }
    
    // Parse sources
    if sourcesJSON.Valid {
        json.Unmarshal([]byte(sourcesJSON.String), &cached.Sources)
    }
    
    cached.ConfidenceType = temporal.ConfidenceType(confType)
    
    // Check if cache entry is still fresh enough
    tc := temporal.TemporalConfidence{
        BaseConfidence: cached.BaseConfidence,
        Timestamp:      cached.LastVerified,
        Type:           cached.ConfidenceType,
    }
    
    effectiveConf := tc.EffectiveConfidence()
    
    // If confidence dropped too low, treat as miss
    if effectiveConf < 0.5 {
        log.Debug().
            Str("query", normalized).
            Float64("effective_confidence", effectiveConf).
            Msg("Cache hit but confidence too low")
        return nil, false
    }
    
    // Update access time and hit count asynchronously
    go fc.recordHit(hash)
    
    log.Debug().
        Str("query", normalized).
        Float64("confidence", effectiveConf).
        Dur("age", time.Since(cached.LastVerified)).
        Msg("Cache hit")
    
    return &cached, true
}

// Store saves a query-answer pair to cache
func (fc *FAQCache) Store(query, answer string, sources []string, confidence float64, confidenceType temporal.ConfidenceType, expiresIn time.Duration) error {
    normalized := fc.normalizeQuery(query)
    hash := fc.hashQuery(normalized)
    
    sourcesJSON, _ := json.Marshal(sources)
    
    var expiresAt *time.Time
    if expiresIn > 0 {
        exp := time.Now().Add(expiresIn)
        expiresAt = &exp
    }
    
    // Calculate max age based on confidence type
    maxAgeHours := fc.getMaxAgeHours(confidenceType)
    
    _, err := fc.db.Exec(`
        INSERT INTO faq_cache (
            query_hash,
            canonical_query,
            answer,
            sources,
            base_confidence,
            current_confidence,
            confidence_type,
            expires_at,
            max_age_hours,
            created_at,
            last_verified,
            last_accessed
        ) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
        ON CONFLICT(query_hash) DO UPDATE SET
            answer = excluded.answer,
            sources = excluded.sources,
            base_confidence = excluded.base_confidence,
            current_confidence = excluded.current_confidence,
            last_verified = excluded.last_verified,
            needs_revalidation = FALSE
    `, hash, normalized, answer, sourcesJSON, confidence, confidence,
        string(confidenceType), expiresAt, maxAgeHours,
        time.Now(), time.Now(), time.Now())
    
    if err != nil {
        return fmt.Errorf("failed to store cache entry: %w", err)
    }
    
    log.Debug().
        Str("query", normalized).
        Float64("confidence", confidence).
        Msg("Cache entry stored")
    
    return nil
}

// recordHit updates access statistics
func (fc *FAQCache) recordHit(hash string) {
    _, err := fc.db.Exec(`
        UPDATE faq_cache
        SET 
            hit_count = hit_count + 1,
            last_accessed = ?
        WHERE query_hash = ?
    `, time.Now(), hash)
    
    if err != nil {
        log.Error().Err(err).Msg("Failed to record cache hit")
    }
}

// Invalidate removes a cache entry
func (fc *FAQCache) Invalidate(query string) error {
    normalized := fc.normalizeQuery(query)
    hash := fc.hashQuery(normalized)
    
    _, err := fc.db.Exec(`DELETE FROM faq_cache WHERE query_hash = ?`, hash)
    if err != nil {
        return fmt.Errorf("failed to invalidate cache: %w", err)
    }
    
    log.Debug().Str("query", normalized).Msg("Cache invalidated")
    return nil
}

// InvalidatePattern removes cache entries matching a pattern
func (fc *FAQCache) InvalidatePattern(pattern string) (int, error) {
    result, err := fc.db.Exec(`
        DELETE FROM faq_cache
        WHERE canonical_query LIKE ?
    `, "%"+pattern+"%")
    
    if err != nil {
        return 0, fmt.Errorf("failed to invalidate pattern: %w", err)
    }
    
    count, _ := result.RowsAffected()
    log.Info().Int64("count", count).Str("pattern", pattern).Msg("Cache entries invalidated")
    
    return int(count), nil
}

// GetStats returns cache statistics
func (fc *FAQCache) GetStats() (map[string]interface{}, error) {
    var stats struct {
        TotalEntries      int
        FreshEntries      int
        StaleEntries      int
        TotalHits         int64
        AvgConfidence     float64
        OldestEntry       time.Time
        NewestEntry       time.Time
    }
    
    err := fc.db.QueryRow(`
        SELECT 
            COUNT(*) as total,
            SUM(CASE WHEN needs_revalidation = FALSE THEN 1 ELSE 0 END) as fresh,
            SUM(CASE WHEN needs_revalidation = TRUE THEN 1 ELSE 0 END) as stale,
            SUM(hit_count) as total_hits,
            AVG(current_confidence) as avg_confidence,
            MIN(created_at) as oldest,
            MAX(created_at) as newest
        FROM faq_cache
        WHERE expires_at IS NULL OR expires_at > ?
    `, time.Now()).Scan(
        &stats.TotalEntries,
        &stats.FreshEntries,
        &stats.StaleEntries,
        &stats.TotalHits,
        &stats.AvgConfidence,
        &stats.OldestEntry,
        &stats.NewestEntry,
    )
    
    if err != nil {
        return nil, fmt.Errorf("failed to get stats: %w", err)
    }
    
    return map[string]interface{}{
        "total_entries":   stats.TotalEntries,
        "fresh_entries":   stats.FreshEntries,
        "stale_entries":   stats.StaleEntries,
        "total_hits":      stats.TotalHits,
        "avg_confidence":  stats.AvgConfidence,
        "oldest_entry":    stats.OldestEntry,
        "newest_entry":    stats.NewestEntry,
    }, nil
}

// normalizeQuery normalizes a query for consistent hashing
func (fc *FAQCache) normalizeQuery(query string) string {
    // Lowercase
    query = strings.ToLower(query)
    
    // Trim whitespace
    query = strings.TrimSpace(query)
    
    // Remove punctuation at end
    query = strings.TrimRight(query, "?.!")
    
    // Normalize whitespace
    query = strings.Join(strings.Fields(query), " ")
    
    return query
}

// hashQuery generates a SHA-256 hash of a query
func (fc *FAQCache) hashQuery(query string) string {
    hash := sha256.Sum256([]byte(query))
    return hex.EncodeToString(hash[:])
}

// getMaxAgeHours returns maximum age in hours based on confidence type
func (fc *FAQCache) getMaxAgeHours(confType temporal.ConfidenceType) int {
    switch confType {
    case temporal.Static:
        return 8760 // 1 year
    case temporal.SlowChange:
        return 2160 // 90 days
    case temporal.FastChange:
        return 168 // 7 days
    case temporal.RealTime:
        return 24 // 1 day
    default:
        return 720 // 30 days
    }
}
```

---

## Component 2: Fast Path Executor

### Purpose
Execute simple queries using regex handlers and system functions.

### Design

**File:** `internal/intelligence/fast_path.go`
```go
package intelligence

import (
    "context"
    "fmt"
    "strconv"
    "strings"
    "time"
    
    "github.com/rs/zerolog/log"
)

// Response represents a query response
type Response struct {
    Text       string
    Confidence float64
    Source     string
    Sources    []string
    Latency    time.Duration
    Cached     bool
}

// FastPath handles high-confidence queries with minimal processing
type FastPath struct {
    intelligence *Intelligence
    faqCache     *FAQCache
    handlers     map[string]HandlerFunc
}

// HandlerFunc is a function that handles a specific pattern type
type HandlerFunc func(ctx context.Context, match *PatternMatch) (*Response, error)

// NewFastPath creates a new fast path executor
func NewFastPath(intel *Intelligence, cache *FAQCache) *FastPath {
    fp := &FastPath{
        intelligence: intel,
        faqCache:     cache,
        handlers:     make(map[string]HandlerFunc),
    }
    
    // Register handlers
    fp.registerHandlers()
    
    return fp
}

// registerHandlers registers all built-in handlers
func (fp *FastPath) registerHandlers() {
    fp.handlers["calculation"] = fp.handleCalculation
    fp.handlers["system"] = fp.handleSystem
    fp.handlers["db_lookup"] = fp.handleDBLookup
    fp.handlers["conversion"] = fp.handleConversion
}

// Execute executes a query via the fast path
func (fp *FastPath) Execute(ctx context.Context, query string) (*Response, error) {
    startTime := time.Now()
    
    // Step 1: Check FAQ cache
    if cached, found := fp.faqCache.Get(query); found {
        return &Response{
            Text:       cached.Answer,
            Confidence: cached.BaseConfidence,
            Source:     "faq_cache",
            Sources:    cached.Sources,
            Latency:    time.Since(startTime),
            Cached:     true,
        }, nil
    }
    
    // Step 2: Try pattern match
    match := fp.intelligence.PatternMatcher.Match(query)
    if match == nil {
        return nil, fmt.Errorf("no pattern match for fast path")
    }
    
    // Step 3: Execute handler
    handler, exists := fp.handlers[match.Pattern.Handler]
    if !exists {
        return nil, fmt.Errorf("no handler for type: %s", match.Pattern.Handler)
    }
    
    response, err := handler(ctx, match)
    if err != nil {
        fp.intelligence.PatternMatcher.RecordFailure(match.Pattern.ID)
        return nil, fmt.Errorf("handler failed: %w", err)
    }
    
    response.Latency = time.Since(startTime)
    response.Source = fmt.Sprintf("fast_path_%s", match.Pattern.Handler)
    
    // Step 4: Cache successful response
    if response.Confidence > 0.8 {
        fp.cacheResponse(query, response, match.Pattern.Category)
    }
    
    // Record success
    fp.intelligence.PatternMatcher.RecordSuccess(match.Pattern.ID)
    
    log.Debug().
        Str("query", query).
        Str("handler", match.Pattern.Handler).
        Dur("latency", response.Latency).
        Msg("Fast path executed")
    
    return response, nil
}

// handleCalculation handles mathematical calculations
func (fp *FastPath) handleCalculation(ctx context.Context, match *PatternMatch) (*Response, error) {
    // Extract numbers from matches
    if len(match.Matches) < 3 {
        return nil, fmt.Errorf("insufficient matches for calculation")
    }
    
    a, err := strconv.ParseFloat(match.Matches[1], 64)
    if err != nil {
        return nil, fmt.Errorf("invalid first number: %w", err)
    }
    
    b, err := strconv.ParseFloat(match.Matches[2], 64)
    if err != nil {
        return nil, fmt.Errorf("invalid second number: %w", err)
    }
    
    // Determine operation from pattern
    var result float64
    var operation string
    
    if strings.Contains(match.MatchedText, "+") {
        result = a + b
        operation = "addition"
    } else if strings.Contains(match.MatchedText, "-") {
        result = a - b
        operation = "subtraction"
    } else if strings.Contains(match.MatchedText, "*") {
        result = a * b
        operation = "multiplication"
    } else if strings.Contains(match.MatchedText, "/") {
        if b == 0 {
            return nil, fmt.Errorf("division by zero")
        }
        result = a / b
        operation = "division"
    } else if strings.Contains(strings.ToLower(match.MatchedText), "percent") {
        // Percentage calculation: a% of b
        result = (a / 100) * b
        operation = "percentage"
    } else {
        return nil, fmt.Errorf("unknown operation")
    }
    
    // Format result
    var answer string
    if operation == "percentage" {
        answer = fmt.Sprintf("%.2f%% of %.2f is %.2f", a, b, result)
    } else {
        answer = fmt.Sprintf("%.2f", result)
    }
    
    return &Response{
        Text:       answer,
        Confidence: 1.0,
        Source:     "calculation",
    }, nil
}

// handleSystem handles system queries (time, date, etc.)
func (fp *FastPath) handleSystem(ctx context.Context, match *PatternMatch) (*Response, error) {
    query := strings.ToLower(match.MatchedText)
    
    now := time.Now()
    var answer string
    
    if strings.Contains(query, "time") {
        answer = now.Format("The time is 3:04 PM")
    } else if strings.Contains(query, "date") {
        answer = now.Format("Today is Monday, January 2, 2006")
    } else {
        answer = now.Format("It's 3:04 PM on Monday, January 2, 2006")
    }
    
    return &Response{
        Text:       answer,
        Confidence: 1.0,
        Source:     "system",
    }, nil
}

// handleDBLookup looks up information from entity database
func (fp *FastPath) handleDBLookup(ctx context.Context, match *PatternMatch) (*Response, error) {
    // Extract entity from captured groups
    if len(match.Matches) < 2 {
        return nil, fmt.Errorf("no entity captured")
    }
    
    entityName := match.Matches[1]
    
    // Look up entity
    entity := fp.intelligence.EntityExtractor.lookupEntity(entityName)
    if entity == nil {
        return nil, fmt.Errorf("entity not found: %s", entityName)
    }
    
    // Determine what information is being asked
    query := strings.ToLower(match.MatchedText)
    var answer string
    
    if strings.Contains(query, "what is") || strings.Contains(query, "what are") {
        // Definition request
        answer = entity.Description
        
        // Add key facts if available
        if len(entity.Facts) > 0 {
            answer += ". "
            first := true
            for k, v := range entity.Facts {
                if !first {
                    answer += ", "
                }
                answer += fmt.Sprintf("%s: %v", k, v)
                first = false
            }
        }
    } else {
        answer = entity.Description
    }
    
    return &Response{
        Text:       answer,
        Confidence: entity.Confidence,
        Source:     "entity_db",
    }, nil
}

// handleConversion handles unit conversions
func (fp *FastPath) handleConversion(ctx context.Context, match *PatternMatch) (*Response, error) {
    if len(match.Matches) < 4 {
        return nil, fmt.Errorf("insufficient matches for conversion")
    }
    
    value, err := strconv.ParseFloat(match.Matches[1], 64)
    if err != nil {
        return nil, fmt.Errorf("invalid value: %w", err)
    }
    
    fromUnit := strings.ToLower(match.Matches[2])
    toUnit := strings.ToLower(match.Matches[3])
    
    // Simple conversion table (expand as needed)
    conversions := map[string]map[string]float64{
        "km": {
            "miles": 0.621371,
            "m":     1000,
            "feet":  3280.84,
        },
        "miles": {
            "km": 1.60934,
            "m":  1609.34,
        },
        "kg": {
            "lbs":    2.20462,
            "pounds": 2.20462,
            "g":      1000,
        },
        "lbs": {
            "kg": 0.453592,
        },
        "pounds": {
            "kg": 0.453592,
        },
        "c": {
            "f": func(c float64) float64 { return c*9/5 + 32 },
        },
        "f": {
            "c": func(f float64) float64 { return (f - 32) * 5 / 9 },
        },
    }
    
    // Perform conversion
    var result float64
    var found bool
    
    if conv, ok := conversions[fromUnit]; ok {
        if factor, ok := conv[toUnit]; ok {
            if fn, isFunc := factor.(func(float64) float64); isFunc {
                result = fn(value)
            } else if multiplier, isFloat := factor.(float64); isFloat {
                result = value * multiplier
            }
            found = true
        }
    }
    
    if !found {
        return nil, fmt.Errorf("conversion not supported: %s to %s", fromUnit, toUnit)
    }
    
    answer := fmt.Sprintf("%.2f %s = %.2f %s", value, fromUnit, result, toUnit)
    
    return &Response{
        Text:       answer,
        Confidence: 1.0,
        Source:     "conversion",
    }, nil
}

// cacheResponse stores a successful response in cache
func (fp *FastPath) cacheResponse(query string, response *Response, category string) {
    // Determine confidence type based on category
    var confType temporal.ConfidenceType
    switch category {
    case "math", "conversion":
        confType = temporal.Static
    case "time_query":
        confType = temporal.RealTime
    case "definition":
        confType = temporal.SlowChange
    default:
        confType = temporal.SlowChange
    }
    
    // Determine expiry
    var expiresIn time.Duration
    if confType == temporal.RealTime {
        expiresIn = 1 * time.Hour
    } else if confType == temporal.Static {
        expiresIn = 365 * 24 * time.Hour
    } else {
        expiresIn = 30 * 24 * time.Hour
    }
    
    err := fp.faqCache.Store(query, response.Text, response.Sources, response.Confidence, confType, expiresIn)
    if err != nil {
        log.Error().Err(err).Msg("Failed to cache response")
    }
}
```

---

## Component 3: Fast Path Tests

**File:** `internal/intelligence/fast_path_test.go`
```go
package intelligence

import (
    "context"
    "testing"
    "time"
)

func TestFastPath_Calculation(t *testing.T) {
    db := setupTestDB(t)
    defer db.Close()
    
    // Insert test pattern
    _, err := db.Exec(`
        INSERT INTO query_patterns 
        (pattern_id, pattern, category, priority, handler, requires_search, base_confidence, effective_confidence)
        VALUES (1, '(\d+)\s*\+\s*(\d+)', 'math', 100, 'calculation', 0, 1.0, 1.0)
    `)
    if err != nil {
        t.Fatalf("Failed to insert test pattern: %v", err)
    }
    
    intel, err := NewIntelligence(db)
    if err != nil {
        t.Fatalf("Failed to create intelligence: %v", err)
    }
    
    cache := NewFAQCache(db)
    fp := NewFastPath(intel, cache)
    
    tests := []struct {
        name    string
        query   string
        want    string
        wantErr bool
    }{
        {
            name:  "Simple addition",
            query: "5 + 3",
            want:  "8.00",
        },
        {
            name:  "Addition with spaces",
            query: "10 + 25",
            want:  "35.00",
        },
    }
    
    for _, tt := range tests {
        t.Run(tt.name, func(t *testing.T) {
            ctx := context.Background()
            resp, err := fp.Execute(ctx, tt.query)
            
            if (err != nil) != tt.wantErr {
                t.Errorf("Execute() error = %v, wantErr %v", err, tt.wantErr)
                return
            }
            
            if !tt.wantErr && resp.Text != tt.want {
                t.Errorf("Execute() = %v, want %v", resp.Text, tt.want)
            }
            
            if !tt.wantErr && resp.Latency > 100*time.Millisecond {
                t.Errorf("Execute() latency = %v, want < 100ms", resp.Latency)
            }
        })
    }
}

func TestFastPath_SystemQueries(t *testing.T) {
    db := setupTestDB(t)
    defer db.Close()
    
    _, err := db.Exec(`
        INSERT INTO query_patterns 
        (pattern_id, pattern, category, priority, handler, requires_search, base_confidence, effective_confidence)
        VALUES (1, 'what time is it', 'time_query', 100, 'system', 0, 1.0, 1.0)
    `)
    if err != nil {
        t.Fatalf("Failed to insert test pattern: %v", err)
    }
    
    intel, err := NewIntelligence(db)
    if err != nil {
        t.Fatalf("Failed to create intelligence: %v", err)
    }
    
    cache := NewFAQCache(db)
    fp := NewFastPath(intel, cache)
    
    ctx := context.Background()
    resp, err := fp.Execute(ctx, "what time is it")
    
    if err != nil {
        t.Fatalf("Execute() error = %v", err)
    }
    
    if !strings.Contains(resp.Text, "time is") {
        t.Errorf("Expected time response, got: %v", resp.Text)
    }
    
    if resp.Confidence != 1.0 {
        t.Errorf("Expected confidence 1.0, got: %v", resp.Confidence)
    }
}

func TestFAQCache_StoreAndRetrieve(t *testing.T) {
    db := setupTestDB(t)
    defer db.Close()
    
    // Create FAQ cache table
    _, err := db.Exec(`
        CREATE TABLE faq_cache (
            query_hash TEXT PRIMARY KEY,
            canonical_query TEXT NOT NULL,
            answer TEXT NOT NULL,
            sources TEXT,
            base_confidence REAL DEFAULT 1.0,
            current_confidence REAL,
            confidence_type TEXT DEFAULT 'slow_change',
            expires_at TIMESTAMP,
            max_age_hours INTEGER DEFAULT 720,
            created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
            last_verified TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
            last_accessed TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
            hit_count INTEGER DEFAULT 0,
            needs_revalidation BOOLEAN DEFAULT FALSE
        )
    `)
    if err != nil {
        t.Fatalf("Failed to create table: %v", err)
    }
    
    cache := NewFAQCache(db)
    
    // Store
    query := "What is 2 + 2?"
    answer := "4"
    err = cache.Store(query, answer, []string{}, 1.0, temporal.Static, 24*time.Hour)
    if err != nil {
        t.Fatalf("Store() error = %v", err)
    }
    
    // Retrieve
    cached, found := cache.Get(query)
    if !found {
        t.Fatal("Expected cache hit, got miss")
    }
    
    if cached.Answer != answer {
        t.Errorf("Expected answer %q, got %q", answer, cached.Answer)
    }
    
    if cached.BaseConfidence != 1.0 {
        t.Errorf("Expected confidence 1.0, got %v", cached.BaseConfidence)
    }
}

func TestFAQCache_Normalization(t *testing.T) {
    db := setupTestDB(t)
    defer db.Close()
    
    _, err := db.Exec(`
        CREATE TABLE faq_cache (
            query_hash TEXT PRIMARY KEY,
            canonical_query TEXT NOT NULL,
            answer TEXT NOT NULL,
            sources TEXT,
            base_confidence REAL DEFAULT 1.0,
            current_confidence REAL,
            confidence_type TEXT DEFAULT 'slow_change',
            expires_at TIMESTAMP,
            max_age_hours INTEGER DEFAULT 720,
            created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
            last_verified TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
            last_accessed TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
            hit_count INTEGER DEFAULT 0,
            needs_revalidation BOOLEAN DEFAULT FALSE
        )
    `)
    if err != nil {
        t.Fatalf("Failed to create table: %v", err)
    }
    
    cache := NewFAQCache(db)
    
    // Store with one variant
    err = cache.Store("What is 2 + 2?", "4", []string{}, 1.0, temporal.Static, 24*time.Hour)
    if err != nil {
        t.Fatalf("Store() error = %v", err)
    }
    
    // Test different variants should match
    variants := []string{
        "what is 2 + 2?",  // Different case
        "What is 2 + 2",   // No question mark
        "  What is 2 + 2?  ", // Extra spaces
    }
    
    for _, variant := range variants {
        t.Run(variant, func(t *testing.T) {
            cached, found := cache.Get(variant)
            if !found {
                t.Errorf("Expected cache hit for variant %q", variant)
            }
            if cached != nil && cached.Answer != "4" {
                t.Errorf("Expected answer 4, got %q", cached.Answer)
            }
        })
    }
}
```

---

## Integration with Main System

**File:** `internal/api/ws_chat_handler.go` (modifications)
```go
// Add to ChatHandler struct
type ChatHandler struct {
    // ... existing fields ...
    intelligence *intelligence.Intelligence
    faqCache     *intelligence.FAQCache
fastPath     *intelligence.FastPath
}

// Update NewChatHandler
func NewChatHandler(db *sql.DB, config *Config) *ChatHandler {
// Initialize intelligence
intel, err := intelligence.NewIntelligence(db)
if err != nil {
log.Fatal().Err(err).Msg("Failed to initialize intelligence")
}

// Initialize caches
faqCache := intelligence.NewFAQCache(db)

// Initialize fast path
fastPath := intelligence.NewFastPath(intel, faqCache)

return &ChatHandler{
    // ... existing fields ...
    intelligence: intel,
    faqCache:     faqCache,
    fastPath:     fastPath,
}

}

// Update handleChat to try fast path first
func (h *ChatHandler) handleChat(ctx context.Context, msg ChatMessage) {
startTime := time.Now()

// Step 1: Rewrite query
rewritten := h.intelligence.QueryRewriter.Rewrite(msg.Content)

// Step 2: Try fast path first
response, err := h.fastPath.Execute(ctx, rewritten.Rewritten)
if err == nil {
    // Fast path success!
    h.streamResponse(conn, response)
    
    // Log
    h.logQuery(msg.Content, rewritten.Rewritten, "fast", response, time.Since(startTime))
    return
}

// Step 3: Try medium/full path (existing logic)
// ... existing code ...

}


---

## Performance Monitoring

**File:** `internal/intelligence/metrics.go`
```go
package intelligence

import (
    "sync/atomic"
    "time"
)

// Metrics tracks performance metrics
type Metrics struct {
    CacheHits          atomic.Uint64
    CacheMisses        atomic.Uint64
    FastPathExecutions atomic.Uint64
    FastPathFailures   atomic.Uint64
    AvgFastPathLatency atomic.Uint64 // in microseconds
}

// RecordCacheHit records a cache hit
func (m *Metrics) RecordCacheHit() {
    m.CacheHits.Add(1)
}

// RecordCacheMiss records a cache miss
func (m *Metrics) RecordCacheMiss() {
    m.CacheMisses.Add(1)
}

// RecordFastPathExecution records a fast path execution with latency
func (m *Metrics) RecordFastPathExecution(latency time.Duration) {
    m.FastPathExecutions.Add(1)
    
    // Update rolling average
    current := m.AvgFastPathLatency.Load()
    newAvg := (current + uint64(latency.Microseconds())) / 2
    m.AvgFastPathLatency.Store(newAvg)
}

// GetCacheHitRate returns cache hit rate
func (m *Metrics) GetCacheHitRate() float64 {
    hits := m.CacheHits.Load()
    misses := m.CacheMisses.Load()
    total := hits + misses
    
    if total == 0 {
        return 0
    }
    
    return float64(hits) / float64(total)
}

// GetStats returns current metrics
func (m *Metrics) GetStats() map[string]interface{} {
    return map[string]interface{}{
        "cache_hits":            m.CacheHits.Load(),
        "cache_misses":          m.CacheMisses.Load(),
        "cache_hit_rate":        m.GetCacheHitRate(),
        "fast_path_executions":  m.FastPathExecutions.Load(),
        "fast_path_failures":    m.FastPathFailures.Load(),
        "avg_fast_path_latency": time.Duration(m.AvgFastPathLatency.Load()) * time.Microsecond,
    }
}
```

---

## Testing Checklist

- [ ] FAQ cache stores and retrieves correctly
- [ ] Query normalization works for variants
- [ ] Cache expiration honors temporal rules
- [ ] Math calculations accurate
- [ ] System queries return correct time/date
- [ ] Unit conversions work
- [ ] DB lookup finds entities
- [ ] Cache hit rate measured accurately
- [ ] Fast path latency < 100ms
- [ ] Successful responses cached appropriately

---

## Success Criteria

- [ ] Cache hit rate reaches 20% within first week
- [ ] Fast path responds in < 100ms for 95% of requests
- [ ] Math operations 100% accurate
- [ ] System queries always work
- [ ] Cache properly expires stale data
- [ ] Metrics accurately tracked
- [ ] Integration with main system seamless
- [ ] All unit tests pass

---

## Performance Targets

| Metric | Target | Stretch Goal |
|--------|--------|--------------|
| Cache Hit Rate (Week 1) | 20% | 30% |
| Fast Path Latency (p95) | < 100ms | < 50ms |
| Fast Path Latency (p99) | < 150ms | < 100ms |
| Math Accuracy | 100% | 100% |
| Cache Memory Usage | < 50MB | < 30MB |

---

## Next Stage

Once Stage 2 is complete, proceed to **Stage 3: Execution Paths** where we'll build:
- Medium path with model consensus
- Full path with search integration
- Response streaming
- Tool orchestration

---

**Last Updated:** [Current Date]
**Status:** Ready for Implementation
**Estimated Time:** 5-7 days

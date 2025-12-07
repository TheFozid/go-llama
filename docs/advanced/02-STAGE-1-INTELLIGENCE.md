# Stage 1: Core Intelligence Components
## Pattern Matching, Entity Extraction, Query Rewriting & Confidence Scoring

**Timeline:** Weeks 2-3 (10-14 days)  
**Prerequisites:** Stage 0 complete, database operational  
**Owner:** Backend Team  
**Status:** Not Started

---

## Objectives

1. Build pattern matcher with regex engine and database integration
2. Create entity extractor with knowledge base lookup
3. Implement query rewriter with abbreviation expansion and pronoun resolution
4. Build heuristic-based confidence scorer for routing decisions
5. Implement temporal confidence framework
6. Create integration layer for these components

---

## Architecture Overview
```
Query Input
    │
    ├──> Pattern Matcher ──> Match Found? ──> Fast Path
    │                             │
    │                             └──> No Match
    │
    ├──> Entity Extractor ──> Known Entities ──> Context Enrichment
    │
    ├──> Query Rewriter ──> Normalized Query ──> Better Matching
    │
    └──> Confidence Scorer ──> Route Decision ──> fast/medium/full
```

---

## Component 1: Pattern Matcher

### Purpose
Match incoming queries against known regex patterns for instant responses without LLM inference.

### Design

**File:** `internal/intelligence/pattern_matcher.go`
```go
package intelligence

import (
    "database/sql"
    "fmt"
    "regexp"
    "strings"
    "sync"
    "time"
    
    "github.com/rs/zerolog/log"
)

// Pattern represents a compiled regex pattern with metadata
type Pattern struct {
    ID             int
    Regex          *regexp.Regexp
    RawPattern     string
    Category       string
    Priority       int
    Template       string
    Handler        string
    RequiresSearch bool
    Confidence     float64
    Examples       []string
}

// PatternMatch represents a successful pattern match
type PatternMatch struct {
    Pattern     *Pattern
    Matches     []string            // Full match + capture groups
    Captured    map[string]string   // Named capture groups
    MatchedText string
}

// PatternMatcher handles regex-based query matching
type PatternMatcher struct {
    patterns []*Pattern
    db       *sql.DB
    mu       sync.RWMutex
}

// NewPatternMatcher creates a new pattern matcher and loads patterns from DB
func NewPatternMatcher(db *sql.DB) (*PatternMatcher, error) {
    pm := &PatternMatcher{
        db:       db,
        patterns: make([]*Pattern, 0),
    }
    
    if err := pm.LoadPatterns(); err != nil {
        return nil, fmt.Errorf("failed to load patterns: %w", err)
    }
    
    log.Info().Int("count", len(pm.patterns)).Msg("Pattern matcher initialized")
    return pm, nil
}

// LoadPatterns loads patterns from database
func (pm *PatternMatcher) LoadPatterns() error {
    pm.mu.Lock()
    defer pm.mu.Unlock()
    
    rows, err := pm.db.Query(`
        SELECT 
            pattern_id, pattern, category, priority, 
            template, handler, requires_search, 
            base_confidence, examples
        FROM query_patterns
        WHERE effective_confidence > 0.5
        ORDER BY priority DESC, effective_confidence DESC
    `)
    if err != nil {
        return fmt.Errorf("query failed: %w", err)
    }
    defer rows.Close()
    
    pm.patterns = nil
    
    for rows.Next() {
        var p Pattern
        var examplesJSON sql.NullString
        
        err := rows.Scan(
            &p.ID,
            &p.RawPattern,
            &p.Category,
            &p.Priority,
            &p.Template,
            &p.Handler,
            &p.RequiresSearch,
            &p.Confidence,
            &examplesJSON,
        )
        if err != nil {
            log.Warn().Err(err).Msg("Failed to scan pattern row")
            continue
        }
        
        // Compile regex with case-insensitive flag
        p.Regex, err = regexp.Compile("(?i)^" + p.RawPattern + "$")
        if err != nil {
            log.Warn().
                Int("pattern_id", p.ID).
                Str("pattern", p.RawPattern).
                Err(err).
                Msg("Failed to compile pattern")
            continue
        }
        
        // Parse examples JSON (if present)
        if examplesJSON.Valid {
            // TODO: Parse JSON array
            p.Examples = []string{}
        }
        
        pm.patterns = append(pm.patterns, &p)
    }
    
    if err := rows.Err(); err != nil {
        return fmt.Errorf("rows iteration failed: %w", err)
    }
    
    log.Info().Int("loaded", len(pm.patterns)).Msg("Patterns loaded")
    return nil
}

// Match attempts to match a query against known patterns
func (pm *PatternMatcher) Match(query string) *PatternMatch {
    pm.mu.RLock()
    defer pm.mu.RUnlock()
    
    query = normalizeQuery(query)
    
    for _, pattern := range pm.patterns {
        if matches := pattern.Regex.FindStringSubmatch(query); matches != nil {
            // Record hit asynchronously
            go pm.recordHit(pattern.ID)
            
            return &PatternMatch{
                Pattern:     pattern,
                Matches:     matches,
                Captured:    extractCaptureGroups(pattern.Regex, matches),
                MatchedText: matches[0],
            }
        }
    }
    
    return nil
}

// MatchWithContext matches query and returns additional context
func (pm *PatternMatcher) MatchWithContext(query string) (*PatternMatch, map[string]interface{}) {
    match := pm.Match(query)
    if match == nil {
        return nil, nil
    }
    
    context := map[string]interface{}{
        "category":        match.Pattern.Category,
        "handler":         match.Pattern.Handler,
        "requires_search": match.Pattern.RequiresSearch,
        "confidence":      match.Pattern.Confidence,
    }
    
    return match, context
}

// recordHit updates pattern usage statistics
func (pm *PatternMatcher) recordHit(patternID int) {
    _, err := pm.db.Exec(`
        UPDATE query_patterns 
        SET 
            hit_count = hit_count + 1,
            recent_hits = recent_hits + 1,
            last_used = ?
        WHERE pattern_id = ?
    `, time.Now(), patternID)
    
    if err != nil {
        log.Error().
            Err(err).
            Int("pattern_id", patternID).
            Msg("Failed to record pattern hit")
    }
}

// RecordSuccess records a successful pattern match
func (pm *PatternMatcher) RecordSuccess(patternID int) {
    go func() {
        _, err := pm.db.Exec(`
            UPDATE query_patterns
            SET 
                success_count = success_count + 1,
                recent_successes = recent_successes + 1,
                last_success = ?,
                overall_success_rate = CAST(success_count + 1 AS REAL) / CAST(hit_count AS REAL),
                recent_success_rate = CAST(recent_successes + 1 AS REAL) / CAST(recent_hits AS REAL)
            WHERE pattern_id = ?
        `, time.Now(), patternID)
        
        if err != nil {
            log.Error().Err(err).Msg("Failed to record pattern success")
        }
    }()
}

// RecordFailure records a failed pattern match
func (pm *PatternMatcher) RecordFailure(patternID int) {
    go func() {
        _, err := pm.db.Exec(`
            UPDATE query_patterns
            SET 
                failure_count = failure_count + 1,
                recent_failures = recent_failures + 1,
                overall_success_rate = CAST(success_count AS REAL) / CAST(hit_count AS REAL),
                recent_success_rate = CAST(recent_successes AS REAL) / CAST(recent_hits AS REAL)
            WHERE pattern_id = ?
        `, patternID)
        
        if err != nil {
            log.Error().Err(err).Msg("Failed to record pattern failure")
        }
    }()
}

// Reload reloads patterns from database
func (pm *PatternMatcher) Reload() error {
    return pm.LoadPatterns()
}

// Helper functions

func normalizeQuery(query string) string {
    // Trim whitespace
    query = strings.TrimSpace(query)
    
    // Remove multiple spaces
    query = strings.Join(strings.Fields(query), " ")
    
    // Lowercase
    query = strings.ToLower(query)
    
    return query
}

func extractCaptureGroups(re *regexp.Regexp, matches []string) map[string]string {
    result := make(map[string]string)
    
    names := re.SubexpNames()
    for i, name := range names {
        if i > 0 && i < len(matches) && name != "" {
            result[name] = matches[i]
        }
    }
    
    return result
}

// GetPatternStats returns statistics for a pattern
func (pm *PatternMatcher) GetPatternStats(patternID int) (map[string]interface{}, error) {
    var stats struct {
        HitCount          int
        SuccessCount      int
        FailureCount      int
        RecentHits        int
        RecentSuccesses   int
        RecentFailures    int
        OverallSuccessRate float64
        RecentSuccessRate  float64
        LastUsed          sql.NullTime
        LastSuccess       sql.NullTime
    }
    
    err := pm.db.QueryRow(`
        SELECT 
            hit_count, success_count, failure_count,
            recent_hits, recent_successes, recent_failures,
            overall_success_rate, recent_success_rate,
            last_used, last_success
        FROM query_patterns
        WHERE pattern_id = ?
    `, patternID).Scan(
        &stats.HitCount,
        &stats.SuccessCount,
        &stats.FailureCount,
        &stats.RecentHits,
        &stats.RecentSuccesses,
        &stats.RecentFailures,
        &stats.OverallSuccessRate,
        &stats.RecentSuccessRate,
        &stats.LastUsed,
        &stats.LastSuccess,
    )
    
    if err != nil {
        return nil, err
    }
    
    result := map[string]interface{}{
        "hit_count":            stats.HitCount,
        "success_count":        stats.SuccessCount,
        "failure_count":        stats.FailureCount,
        "recent_hits":          stats.RecentHits,
        "recent_successes":     stats.RecentSuccesses,
        "recent_failures":      stats.RecentFailures,
        "overall_success_rate": stats.OverallSuccessRate,
        "recent_success_rate":  stats.RecentSuccessRate,
    }
    
    if stats.LastUsed.Valid {
        result["last_used"] = stats.LastUsed.Time
    }
    if stats.LastSuccess.Valid {
        result["last_success"] = stats.LastSuccess.Time
    }
    
    return result, nil
}
```

### Pattern Matcher Tests

**File:** `internal/intelligence/pattern_matcher_test.go`
```go
package intelligence

import (
    "testing"
    "os"
    "path/filepath"
    
    "github.com/yourusername/intelligent-llm-orchestration/internal/database"
)

func setupTestDB(t *testing.T) *database.DB {
    dbPath := filepath.Join(t.TempDir(), "test.db")
    
    db, err := database.New(dbPath)
    if err != nil {
        t.Fatalf("Failed to create test database: %v", err)
    }
    
    // Create tables (simplified for testing)
    _, err = db.Exec(`
        CREATE TABLE query_patterns (
            pattern_id INTEGER PRIMARY KEY,
            pattern TEXT,
            category TEXT,
            priority INTEGER,
            template TEXT,
            handler TEXT,
            requires_search BOOLEAN,
            base_confidence REAL,
            examples TEXT,
            hit_count INTEGER DEFAULT 0,
            success_count INTEGER DEFAULT 0,
            failure_count INTEGER DEFAULT 0,
            recent_hits INTEGER DEFAULT 0,
            recent_successes INTEGER DEFAULT 0,
            recent_failures INTEGER DEFAULT 0,
            effective_confidence REAL DEFAULT 0.9,
            overall_success_rate REAL DEFAULT 1.0,
            recent_success_rate REAL DEFAULT 1.0,
            last_used TIMESTAMP,
            last_success TIMESTAMP
        )
    `)
    if err != nil {
        t.Fatalf("Failed to create table: %v", err)
    }
    
    return db
}

func TestPatternMatcher_Match(t *testing.T) {
    db := setupTestDB(t)
    defer db.Close()
    
    // Insert test patterns
    _, err := db.Exec(`
        INSERT INTO query_patterns 
        (pattern_id, pattern, category, priority, handler, requires_search, base_confidence)
        VALUES 
        (1, '(\d+)\s*\+\s*(\d+)', 'math', 100, 'calculation', 0, 1.0),
        (2, 'what time is it', 'time_query', 100, 'system', 0, 1.0)
    `)
    if err != nil {
        t.Fatalf("Failed to insert test data: %v", err)
    }
    
    pm, err := NewPatternMatcher(db)
    if err != nil {
        t.Fatalf("Failed to create pattern matcher: %v", err)
    }
    
    tests := []struct {
        name      string
        query     string
        wantMatch bool
        wantCategory string
    }{
        {
            name:      "Simple addition",
            query:     "5 + 3",
            wantMatch: true,
            wantCategory: "math",
        },
        {
            name:      "Time query",
            query:     "what time is it",
            wantMatch: true,
            wantCategory: "time_query",
        },
        {
            name:      "No match",
            query:     "who is the president",
            wantMatch: false,
        },
    }
    
    for _, tt := range tests {
        t.Run(tt.name, func(t *testing.T) {
            match := pm.Match(tt.query)
            
            if tt.wantMatch && match == nil {
                t.Errorf("Expected match but got nil")
            }
            
            if !tt.wantMatch && match != nil {
                t.Errorf("Expected no match but got: %v", match)
            }
            
            if match != nil && match.Pattern.Category != tt.wantCategory {
                t.Errorf("Expected category %s, got %s", tt.wantCategory, match.Pattern.Category)
            }
        })
    }
}

func TestPatternMatcher_RecordHit(t *testing.T) {
    db := setupTestDB(t)
    defer db.Close()
    
    _, err := db.Exec(`
        INSERT INTO query_patterns 
        (pattern_id, pattern, category, priority, handler, requires_search, base_confidence)
        VALUES (1, '(\d+)\s*\+\s*(\d+)', 'math', 100, 'calculation', 0, 1.0)
    `)
    if err != nil {
        t.Fatalf("Failed to insert test data: %v", err)
    }
    
    pm, err := NewPatternMatcher(db)
    if err != nil {
        t.Fatalf("Failed to create pattern matcher: %v", err)
    }
    
    // Match should record hit
    match := pm.Match("5 + 3")
    if match == nil {
        t.Fatal("Expected match")
    }
    
    // Give async update time to complete
    time.Sleep(100 * time.Millisecond)
    
    // Check hit was recorded
    var hitCount int
    err = db.QueryRow("SELECT hit_count FROM query_patterns WHERE pattern_id = 1").Scan(&hitCount)
    if err != nil {
        t.Fatalf("Failed to query hit count: %v", err)
    }
    
    if hitCount != 1 {
        t.Errorf("Expected hit_count = 1, got %d", hitCount)
    }
}
```

---

## Component 2: Entity Extractor

### Purpose
Extract known entities from queries and enrich context with facts from the knowledge base.

### Design

**File:** `internal/intelligence/entity_extractor.go`
```go
package intelligence

import (
    "database/sql"
    "encoding/json"
    "fmt"
    "regexp"
    "strings"
    "sync"
    "time"
    
    "github.com/rs/zerolog/log"
)

// Entity represents a known entity
type Entity struct {
    ID          string
    Type        string
    Name        string
    Aliases     []string
    Facts       map[string]interface{}
    Description string
    Confidence  float64
    LastUpdated time.Time
}

// EntityExtractor finds and enriches entities in queries
type EntityExtractor struct {
    db    *sql.DB
    cache map[string]*Entity
    mu    sync.RWMutex
    
    // Pre-compiled regexes
    capitalizedRe *regexp.Regexp
    numbersRe     *regexp.Regexp
    datesRe       *regexp.Regexp
}

// NewEntityExtractor creates a new entity extractor
func NewEntityExtractor(db *sql.DB) *EntityExtractor {
    return &EntityExtractor{
        db:            db,
        cache:         make(map[string]*Entity),
        capitalizedRe: regexp.MustCompile(`\b[A-Z][a-z]+(?:\s+[A-Z][a-z]+)*\b`),
        numbersRe:     regexp.MustCompile(`\d+(?:\.\d+)?\s*(?:kg|km|miles|dollars|\$|£|€|%)`),
        datesRe:       regexp.MustCompile(`\d{4}|\d{1,2}/\d{1,2}/\d{2,4}|(?:jan|feb|mar|apr|may|jun|jul|aug|sep|oct|nov|dec)\w*\s+\d{1,2}`),
    }
}

// Extract extracts entities from a query
func (ee *EntityExtractor) Extract(query string) []*Entity {
    var entities []*Entity
    
    // Extract capitalized words (potential proper nouns)
    caps := ee.capitalizedRe.FindAllString(query, -1)
    
    // Also check for known abbreviations in lowercase
    words := strings.Fields(strings.ToLower(query))
    
    for _, cap := range caps {
        if entity := ee.lookupEntity(cap); entity != nil {
            entities = append(entities, entity)
        }
    }
    
    for _, word := range words {
        // Skip if already found
        found := false
        for _, e := range entities {
            if strings.EqualFold(e.Name, word) {
                found = true
                break
            }
        }
        if found {
            continue
        }
        
        if entity := ee.lookupEntity(word); entity != nil {
            entities = append(entities, entity)
        }
    }
    
    return entities
}

// lookupEntity finds an entity by name or alias
func (ee *EntityExtractor) lookupEntity(name string) *Entity {
    // Check cache first
    ee.mu.RLock()
    if cached, ok := ee.cache[strings.ToLower(name)]; ok {
        ee.mu.RUnlock()
        return cached
    }
    ee.mu.RUnlock()
    
    // Query database
    var entity Entity
    var aliasesJSON, factsJSON sql.NullString
    
    err := ee.db.QueryRow(`
        SELECT 
            entity_id, entity_type, name, aliases, 
            description, base_confidence, last_updated
        FROM entities
        WHERE name = ? COLLATE NOCASE
           OR aliases LIKE ? COLLATE NOCASE
        LIMIT 1
    `, name, "%"+name+"%").Scan(
        &entity.ID,
        &entity.Type,
        &entity.Name,
        &aliasesJSON,
        &entity.Description,
        &entity.Confidence,
        &entity.LastUpdated,
    )
    
    if err == sql.ErrNoRows {
        // Try full-text search
        err = ee.db.QueryRow(`
            SELECT 
                e.entity_id, e.entity_type, e.name, e.aliases,
                e.description, e.base_confidence, e.last_updated
            FROM entities e
            WHERE e.entity_id IN (
                SELECT rowid FROM entities_fts WHERE entities_fts MATCH ?
            )
            LIMIT 1
        `, name).Scan(
            &entity.ID,
            &entity.Type,
            &entity.Name,
            &aliasesJSON,
            &entity.Description,
            &entity.Confidence,
            &entity.LastUpdated,
        )
        
        if err != nil {
            return nil
        }
    } else if err != nil {
        log.Error().Err(err).Str("name", name).Msg("Failed to lookup entity")
        return nil
    }
    
    // Parse JSON fields
    if aliasesJSON.Valid {
        json.Unmarshal([]byte(aliasesJSON.String), &entity.Aliases)
    }
    
    // Load facts
    entity.Facts = ee.loadEntityFacts(entity.ID)
    
    // Cache the entity
    ee.mu.Lock()
    ee.cache[strings.ToLower(name)] = &entity
    ee.mu.Unlock()
    
    return &entity
}

// loadEntityFacts loads current facts for an entity
func (ee *EntityExtractor) loadEntityFacts(entityID string) map[string]interface{} {
    facts := make(map[string]interface{})
    
    rows, err := ee.db.Query(`
        SELECT fact_key, fact_value
        FROM entity_facts
        WHERE entity_id = ?
        AND is_current = TRUE
        AND (valid_until IS NULL OR valid_until > ?)
    `, entityID, time.Now())
    
    if err != nil {
        log.Error().Err(err).Str("entity_id", entityID).Msg("Failed to load facts")
        return facts
    }
    defer rows.Close()
    
    for rows.Next() {
        var key, value string
        if err := rows.Scan(&key, &value); err != nil {
            continue
        }
        facts[key] = value
    }
    
    return facts
}

// EnrichContext adds entity information to query context
func (ee *EntityExtractor) EnrichContext(query string, entities []*Entity) string {
    if len(entities) == 0 {
        return query
    }
    
    var context strings.Builder
    context.WriteString(query)
    context.WriteString("\n\n[Context:")
    
    for _, entity := range entities {
        context.WriteString(fmt.Sprintf("\n- %s (%s): %s", 
            entity.Name, entity.Type, entity.Description))
        
        // Add key facts
        if len(entity.Facts) > 0 {
            context.WriteString(" [")
            first := true
            for k, v := range entity.Facts {
                if !first {
                    context.WriteString(", ")
                }
                context.WriteString(fmt.Sprintf("%s: %v", k, v))
                first = false
            }
            context.WriteString("]")
        }
    }
    
    context.WriteString("]")
    return context.String()
}

// GetEntityFact retrieves a specific fact about an entity
func (ee *EntityExtractor) GetEntityFact(entityID, factKey string) (string, bool) {
    var value string
    err := ee.db.QueryRow(`
        SELECT fact_value
        FROM entity_facts
        WHERE entity_id = ?
        AND fact_key = ?
        AND is_current = TRUE
        AND (valid_until IS NULL OR valid_until > ?)
        ORDER BY valid_from DESC
        LIMIT 1
    `, entityID, factKey, time.Now()).Scan(&value)
    
    if err != nil {
        return "", false
    }
    
    return value, true
}

// ClearCache clears the entity cache
func (ee *EntityExtractor) ClearCache() {
    ee.mu.Lock()
    defer ee.mu.Unlock()
    ee.cache = make(map[string]*Entity)
    log.Info().Msg("Entity cache cleared")
}

// InvalidateEntity removes a specific entity from cache
func (ee *EntityExtractor) InvalidateEntity(entityID string) {
    ee.mu.Lock()
    defer ee.mu.Unlock()
    
    // Find and remove all cache entries for this entity
    for key, entity := range ee.cache {
        if entity.ID == entityID {
            delete(ee.cache, key)
        }
    }
}
```

---

## Component 3: Query Rewriter

### Purpose
Normalize queries by expanding abbreviations, resolving pronouns, and standardizing phrasing.

### Design

**File:** `internal/intelligence/query_rewriter.go`
```go
package intelligence

import (
    "database/sql"
    "fmt"
    "regexp"
    "strings"
    "sync"
    
    "github.com/rs/zerolog/log"
)

// RewrittenQuery contains the rewritten query and metadata
type RewrittenQuery struct {
    Original   string
    Rewritten  string
    Changes    []string
    Confidence float64
}

// QueryRewriter normalizes and improves queries
type QueryRewriter struct {
    db                   *sql.DB
    abbreviations        map[string]string
    conversationHistory  []string
    maxHistorySize       int
    mu                   sync.RWMutex
}

// NewQueryRewriter creates a new query rewriter
func NewQueryRewriter(db *sql.DB) *QueryRewriter {
    qr := &QueryRewriter{
        db:                  db,
        abbreviations:       make(map[string]string),
        conversationHistory: make([]string, 0, 10),
        maxHistorySize:      10,
    }
    
    // Load abbreviations from database
    qr.loadAbbreviations()
    
    return qr
}

// loadAbbreviations loads abbreviations from database
func (qr *QueryRewriter) loadAbbreviations() error {
    qr.mu.Lock()
    defer qr.mu.Unlock()
    
    rows, err := qr.db.Query(`
        SELECT short_form, full_form
        FROM learned_abbreviations
        WHERE verified = TRUE OR base_confidence > 0.8
        ORDER BY usage_count DESC
    `)
    if err != nil {
        return fmt.Errorf("failed to load abbreviations: %w", err)
    }
    defer rows.Close()
    
    count := 0
    for rows.Next() {
        var short, full string
        if err := rows.Scan(&short, &full); err != nil {
            continue
        }
        qr.abbreviations[short] = full
        count++
    }
    
    log.Info().Int("count", count).Msg("Abbreviations loaded")
    return nil
}

// Rewrite rewrites a query for better processing
func (qr *QueryRewriter) Rewrite(query string) *RewrittenQuery {
    original := query
    changes := []string{}
    
    // Store original for history
    defer qr.AddToHistory(query)
    
    // Step 1: Basic normalization
    query = strings.TrimSpace(query)
    query = strings.Join(strings.Fields(query), " ")
    
    // Step 2: Expand contractions
    query, contractionsExpanded := qr.expandContractions(query)
    if contractionsExpanded > 0 {
        changes = append(changes, fmt.Sprintf("expanded %d contractions", contractionsExpanded))
    }
    
    // Step 3: Expand abbreviations
    qr.mu.RLock()
    abbrevs := qr.abbreviations
    qr.mu.RUnlock()
    
    queryLower := strings.ToLower(query)
    expandedCount := 0
    
    for short, full := range abbrevs {
        // Use word boundaries to avoid partial matches
        pattern := regexp.MustCompile(`\b` + regexp.QuoteMeta(short) + `\b`)
        if pattern.MatchString(queryLower) {
            query = pattern.ReplaceAllString(query, full)
            expandedCount++
        }
    }
    
    if expandedCount > 0 {
        changes = append(changes, fmt.Sprintf("expanded %d abbreviations", expandedCount))
    }
    
    // Step 4: Resolve pronouns
    if containsPronoun(query) && len(qr.conversationHistory) > 0 {
        resolved, didResolve := qr.resolvePronoun(query)
        if didResolve {
            query = resolved
            changes = append(changes, "resolved pronoun")
        }
    }
    
    // Step 5: Remove filler words
    query = qr.removeFiller(query)
    
    // Calculate confidence based on changes made
    confidence := 0.5 + (float64(len(changes)) * 0.1)
    if confidence > 1.0 {
        confidence = 1.0
    }
    
    return &RewrittenQuery{
        Original:   original,
        Rewritten:  query,
        Changes:    changes,
        Confidence: confidence,
    }
}

// expandContractions expands common contractions
func (qr *QueryRewriter) expandContractions(query string) (string, int) {
    contractions := map[string]string{
        "what's":  "what is",
        "who's":   "who is",
        "where's": "where is",
        "when's":  "when is",
        "how's":   "how is",
        "it's":    "it is",
        "that's":  "that is",
        "there's": "there is",
        "here's":  "here is",
        "don't":   "do not",
        "doesn't": "does not",
        "didn't":  "did not",
        "won't":   "will not",
        "can't":   "cannot",
        "isn't":   "is not",
        "aren't":  "are not",
        "wasn't":  "was not",
        "weren't": "were not",
    }
    
    queryLower := strings.ToLower(query)
    expanded := 0
    
    for contraction, expansion := range contractions {
        if strings.Contains(queryLower, contraction) {
            pattern := regexp.MustCompile(`(?i)\b` + regexp.QuoteMeta(contraction) + `\b`)
            query = pattern.ReplaceAllString(query, expansion)
            expanded++
        }
    }
    
    return query, expanded
}

// resolvePronoun attempts to resolve pronouns using conversation history
func (qr *QueryRewriter) resolvePronoun(query string) (string, bool) {
qr.mu.RLock()
defer qr.mu.RUnlock()

if len(qr.conversationHistory) == 0 {
    return query, false
}

lastQuery := qr.conversationHistory[len(qr.conversationHistory)-1]

// Extract named entities from last query (simple capitalized word detection)
re := regexp.MustCompile(`\b[A-Z][a-z]+(?:\s+[A-Z][a-z]+)*\b`)
entities := re.FindAllString(lastQuery, -1)

if len(entities) == 0 {
    return query, false
}

// Replace pronouns with the most recent entity
pronouns := []string{" he ", " she ", " it ", " they ", " him ", " her ", " them ", " his ", " hers ", " their "}
replaced := false

queryLower := " " + strings.ToLower(query) + " "
for _, pronoun := range pronouns {
    if strings.Contains(queryLower, pronoun) {
        query = strings.ReplaceAll(query, pronoun, " "+entities[0]+" ")
        replaced = true
    }
}

return query, replaced

}

// removeFiller removes unnecessary filler words
func (qr *QueryRewriter) removeFiller(query string) string {
fillers := []string{
"um", "uh", "like", "you know", "i mean",
"basically", "actually", "literally",
}

queryLower := strings.ToLower(query)
for _, filler := range fillers {
    pattern := regexp.MustCompile(`\b` + regexp.QuoteMeta(filler) + `\b`)
    if pattern.MatchString(queryLower) {
        query = pattern.ReplaceAllString(query, "")
    }
}

// Clean up extra spaces
query = strings.Join(strings.Fields(query), " ")

return query

}

// AddToHistory adds a query to conversation history
func (qr *QueryRewriter) AddToHistory(query string) {
qr.mu.Lock()
defer qr.mu.Unlock()

qr.conversationHistory = append(qr.conversationHistory, query)
if len(qr.conversationHistory) > qr.maxHistorySize {
    qr.conversationHistory = qr.conversationHistory[1:]
}

}

// ClearHistory clears the conversation history
func (qr *QueryRewriter) ClearHistory() {
qr.mu.Lock()
defer qr.mu.Unlock()
qr.conversationHistory = make([]string, 0, qr.maxHistorySize)
}

// ReloadAbbreviations reloads abbreviations from database
func (qr *QueryRewriter) ReloadAbbreviations() error {
return qr.loadAbbreviations()
}

// Helper function
func containsPronoun(query string) bool {
q := " " + strings.ToLower(query) + " "
pronouns := []string{" he ", " she ", " it ", " they ", " him ", " her ", " them ", " his ", " hers ", " their "}
for _, p := range pronouns {
if strings.Contains(q, p) {
return true
}
}
return false
}


---

## Component 4: Confidence Scorer

### Purpose
Assess query complexity and confidence to determine optimal execution path.

### Design

**File:** `internal/intelligence/confidence_scorer.go`
```go
package intelligence

import (
    "database/sql"
    "fmt"
    "strings"
    
    "github.com/rs/zerolog/log"
)

// Confidence represents confidence assessment for a query
type Confidence struct {
    Score           float64
    RecommendedPath string // "fast", "medium", "full"
    Reasons         []string
    Factors         map[string]float64
}

// ConfidenceScorer assesses query confidence and routes appropriately
type ConfidenceScorer struct {
    db              *sql.DB
    patternMatcher  *PatternMatcher
    entityExtractor *EntityExtractor
}

// NewConfidenceScorer creates a new confidence scorer
func NewConfidenceScorer(db *sql.DB, pm *PatternMatcher, ee *EntityExtractor) *ConfidenceScorer {
    return &ConfidenceScorer{
        db:              db,
        patternMatcher:  pm,
        entityExtractor: ee,
    }
}

// Score calculates confidence score for a query
func (cs *ConfidenceScorer) Score(query string) *Confidence {
    score := 0.0
    reasons := []string{}
    factors := make(map[string]float64)
    
    // Factor 1: Pattern match
    if match := cs.patternMatcher.Match(query); match != nil {
        patternBonus := 0.4
        score += patternBonus
        factors["pattern_match"] = patternBonus
        reasons = append(reasons, fmt.Sprintf("matches known pattern (%s)", match.Pattern.Category))
    }
    
    // Factor 2: Known entities
    entities := cs.entityExtractor.Extract(query)
    if len(entities) > 0 {
        entityBonus := 0.2 * float64(len(entities))
        if entityBonus > 0.3 {
            entityBonus = 0.3 // Cap at 0.3
        }
        score += entityBonus
        factors["entities"] = entityBonus
        reasons = append(reasons, fmt.Sprintf("found %d known entities", len(entities)))
    }
    
    // Factor 3: Query complexity (word count)
    wordCount := len(strings.Fields(query))
    if wordCount <= 5 {
        complexityBonus := 0.2
        score += complexityBonus
        factors["low_complexity"] = complexityBonus
        reasons = append(reasons, "simple query")
    } else if wordCount > 15 {
        complexityPenalty := -0.2
        score += complexityPenalty
        factors["high_complexity"] = complexityPenalty
        reasons = append(reasons, "complex query")
    }
    
    // Factor 4: Ambiguity markers
    ambiguous := []string{"maybe", "possibly", "might", "could", "unclear", "confused", "not sure"}
    for _, word := range ambiguous {
        if strings.Contains(strings.ToLower(query), word) {
            ambiguityPenalty := -0.3
            score += ambiguityPenalty
            factors["ambiguity"] = ambiguityPenalty
            reasons = append(reasons, "ambiguous language detected")
            break
        }
    }
    
    // Factor 5: Reasoning requirements
    reasoning := []string{"why", "how does", "explain", "analyze", "compare", "evaluate", "justify"}
    for _, word := range reasoning {
        if strings.Contains(strings.ToLower(query), word) {
            reasoningPenalty := -0.2
            score += reasoningPenalty
            factors["reasoning_required"] = reasoningPenalty
            reasons = append(reasons, "requires reasoning")
            break
        }
    }
    
    // Factor 6: Historical performance
    avgSuccess := cs.getHistoricalSuccess(query)
    if avgSuccess > 0 {
        historyBonus := avgSuccess * 0.3
        score += historyBonus
        factors["historical_success"] = historyBonus
        reasons = append(reasons, fmt.Sprintf("historical success rate: %.0f%%", avgSuccess*100))
    }
    
    // Clamp score
    if score < 0 {
        score = 0
    }
    if score > 1 {
        score = 1
    }
    
    // Determine recommended path
    var path string
    if score >= 0.8 {
        path = "fast"
    } else if score >= 0.5 {
        path = "medium"
    } else {
        path = "full"
    }
    
    return &Confidence{
        Score:           score,
        RecommendedPath: path,
        Reasons:         reasons,
        Factors:         factors,
    }
}

// getHistoricalSuccess retrieves historical success rate for similar queries
func (cs *ConfidenceScorer) getHistoricalSuccess(query string) float64 {
    var avgSuccess sql.NullFloat64
    
    // Look for similar queries (simplified - using LIKE)
    // In production, you might use more sophisticated similarity
    err := cs.db.QueryRow(`
        SELECT AVG(CASE WHEN success THEN 1.0 ELSE 0.0 END)
        FROM query_log
        WHERE original_query LIKE ?
        AND created_at > datetime('now', '-30 days')
        LIMIT 10
    `, "%"+query+"%").Scan(&avgSuccess)
    
    if err != nil || !avgSuccess.Valid {
        return 0.0
    }
    
    return avgSuccess.Float64
}

// AssessComplexity determines query complexity level
func (cs *ConfidenceScorer) AssessComplexity(query string) string {
    indicators := map[string][]string{
        "simple": {"what is", "who is", "when did", "how many", "where is"},
        "medium": {"explain", "compare", "how does", "why", "describe"},
        "complex": {"analyze", "evaluate", "synthesize", "argue", "critique"},
        "research": {"comprehensive analysis", "in-depth", "all aspects", "thorough investigation"},
    }
    
    q := strings.ToLower(query)
    wordCount := len(strings.Fields(query))
    
    // Check for research-level indicators
    if wordCount > 30 || cs.containsMultiple(q, indicators["research"]) {
        return "research"
    }
    
    // Check for complex reasoning
    if cs.containsAny(q, indicators["complex"]) || wordCount > 20 {
        return "complex"
    }
    
    // Check for medium difficulty
    if cs.containsAny(q, indicators["medium"]) || wordCount > 10 {
        return "medium"
    }
    
    return "simple"
}

// Helper functions
func (cs *ConfidenceScorer) containsAny(text string, patterns []string) bool {
    for _, pattern := range patterns {
        if strings.Contains(text, pattern) {
            return true
        }
    }
    return false
}

func (cs *ConfidenceScorer) containsMultiple(text string, patterns []string) bool {
    count := 0
    for _, pattern := range patterns {
        if strings.Contains(text, pattern) {
            count++
        }
    }
    return count >= 2
}
```

---

## Component 5: Temporal Confidence Framework

### Purpose
Calculate time-weighted confidence for cached data and facts.

### Design

**File:** `pkg/temporal/confidence.go`
```go
package temporal

import (
    "math"
    "time"
)

// ConfidenceType represents how fast confidence decays
type ConfidenceType string

const (
    Static      ConfidenceType = "static"       // Mathematical constants, historical facts
    SlowChange  ConfidenceType = "slow_change"  // Country capitals, company founders
    FastChange  ConfidenceType = "fast_change"  // Current leaders, policies
    RealTime    ConfidenceType = "real_time"    // Stock prices, weather, sports scores
)

// TemporalConfidence represents confidence that decays over time
type TemporalConfidence struct {
    BaseConfidence float64
    Timestamp      time.Time
    Type           ConfidenceType
}

// EffectiveConfidence calculates current confidence based on age
func (tc *TemporalConfidence) EffectiveConfidence() float64 {
    age := time.Since(tc.Timestamp)
    decayRate := tc.getDecayRate()
    
    // Exponential decay: confidence = base * e^(-rate * days)
    daysOld := age.Hours() / 24.0
    effectiveConf := tc.BaseConfidence * math.Exp(-decayRate*daysOld)
    
    // Never go below minimum threshold
    if effectiveConf < 0.1 {
        effectiveConf = 0.1
    }
    
    return effectiveConf
}

// EffectiveConfidenceHalfLife uses half-life based decay (alternative method)
func (tc *TemporalConfidence) EffectiveConfidenceHalfLife() float64 {
    halfLife := tc.getHalfLife()
    age := time.Since(tc.Timestamp)
    
    halfLives := age.Seconds() / halfLife.Seconds()
    effectiveConf := tc.BaseConfidence * math.Pow(0.5, halfLives)
    
    if effectiveConf < 0.1 {
        effectiveConf = 0.1
    }
    
    return effectiveConf
}

// getDecayRate returns decay rate per day based on confidence type
func (tc *TemporalConfidence) getDecayRate() float64 {
    switch tc.Type {
    case Static:
        return 0.001 // 0.1% per day
    case SlowChange:
        return 0.005 // 0.5% per day
    case FastChange:
        return 0.05 // 5% per day
    case RealTime:
        return 0.2 // 20% per day
    default:
        return 0.01 // 1% per day
    }
}

// getHalfLife returns half-life duration based on confidence type
func (tc *TemporalConfidence) getHalfLife() time.Duration {
    switch tc.Type {
    case Static:
        return 365 * 24 * time.Hour // 1 year
    case SlowChange:
        return 90 * 24 * time.Hour // 3 months
    case FastChange:
        return 7 * 24 * time.Hour // 1 week
    case RealTime:
        return 24 * time.Hour // 1 day
    default:
        return 30 * 24 * time.Hour // 1 month
    }
}

// NeedsRevalidation determines if data should be refreshed
func (tc *TemporalConfidence) NeedsRevalidation() bool {
    age := time.Since(tc.Timestamp)
    
    switch tc.Type {
    case Static:
        return age > 365*24*time.Hour
    case SlowChange:
        return age > 90*24*time.Hour
    case FastChange:
        return age > 7*24*time.Hour
    case RealTime:
        return age > 24*time.Hour
    default:
        return age > 30*24*time.Hour
    }
}

// DaysUntilRevalidation returns days until revalidation is needed
func (tc *TemporalConfidence) DaysUntilRevalidation() float64 {
    age := time.Since(tc.Timestamp)
    var threshold time.Duration
    
    switch tc.Type {
    case Static:
        threshold = 365 * 24 * time.Hour
    case SlowChange:
        threshold = 90 * 24 * time.Hour
    case FastChange:
        threshold = 7 * 24 * time.Hour
    case RealTime:
        threshold = 24 * time.Hour
    default:
        threshold = 30 * 24 * time.Hour
    }
    
    remaining := threshold - age
    if remaining < 0 {
        return 0
    }
    
    return remaining.Hours() / 24.0
}
```

---

## Integration Layer

### Purpose
Tie all components together for use by the meta-brain.

**File:** `internal/intelligence/intelligence.go`
```go
package intelligence

import (
    "database/sql"
    "fmt"
    
    "github.com/rs/zerolog/log"
)

// Intelligence combines all intelligence components
type Intelligence struct {
    PatternMatcher  *PatternMatcher
    EntityExtractor *EntityExtractor
    QueryRewriter   *QueryRewriter
    ConfidenceScorer *ConfidenceScorer
}

// NewIntelligence creates a new intelligence system
func NewIntelligence(db *sql.DB) (*Intelligence, error) {
    // Initialize pattern matcher
    pm, err := NewPatternMatcher(db)
    if err != nil {
        return nil, fmt.Errorf("failed to create pattern matcher: %w", err)
    }
    
    // Initialize entity extractor
    ee := NewEntityExtractor(db)
    
    // Initialize query rewriter
    qr := NewQueryRewriter(db)
    
    // Initialize confidence scorer
    cs := NewConfidenceScorer(db, pm, ee)
    
    log.Info().Msg("Intelligence system initialized")
    
    return &Intelligence{
        PatternMatcher:   pm,
        EntityExtractor:  ee,
        QueryRewriter:    qr,
        ConfidenceScorer: cs,
    }, nil
}

// Reload reloads all components from database
func (i *Intelligence) Reload() error {
    if err := i.PatternMatcher.Reload(); err != nil {
        return fmt.Errorf("failed to reload patterns: %w", err)
    }
    
    i.EntityExtractor.ClearCache()
    
    if err := i.QueryRewriter.ReloadAbbreviations(); err != nil {
        return fmt.Errorf("failed to reload abbreviations: %w", err)
    }
    
    log.Info().Msg("Intelligence system reloaded")
    return nil
}
```

---

## Testing Checklist

### Pattern Matcher
- [ ] Loads patterns from database
- [ ] Matches math operations correctly
- [ ] Matches time queries
- [ ] Records hits asynchronously
- [ ] Records success/failure correctly
- [ ] Handles malformed regex gracefully
- [ ] Priority ordering works

### Entity Extractor
- [ ] Finds entities by exact name
- [ ] Finds entities by alias
- [ ] Uses FTS for fuzzy matching
- [ ] Caches entities correctly
- [ ] Loads entity facts
- [ ] Enriches context appropriately
- [ ] Handles missing entities gracefully

### Query Rewriter
- [ ] Expands contractions
- [ ] Expands abbreviations
- [ ] Resolves pronouns with history
- [ ] Removes filler words
- [ ] Maintains conversation history
- [ ] Calculates confidence correctly

### Confidence Scorer
- [ ] Pattern match increases score
- [ ] Known entities increase score
- [ ] Low word count increases score
- [ ] Ambiguity decreases score
- [ ] Reasoning keywords decrease score
- [ ] Historical success increases score
- [ ] Routes to correct path

### Temporal Confidence
- [ ] Calculates decay correctly
- [ ] Different rates for different types
- [ ] Identifies stale data
- [ ] Half-life calculation accurate

---

## Success Criteria

- [ ] All components compile without errors
- [ ] All unit tests pass
- [ ] Pattern matcher identifies 90%+ of test patterns
- [ ] Entity extractor finds all seeded entities
- [ ] Query rewriter handles common cases
- [ ] Confidence scorer produces reasonable scores
- [ ] Integration layer ties everything together
- [ ] Performance: Each component < 50ms latency
- [ ] Memory: Combined < 100MB RAM usage

---

## Performance Targets

| Component | Target Latency | Memory Usage |
|-----------|---------------|--------------|
| Pattern Matcher | < 5ms | < 10MB |
| Entity Extractor | < 20ms | < 30MB |
| Query Rewriter | < 10ms | < 5MB |
| Confidence Scorer | < 15ms | < 5MB |
| **Combined** | **< 50ms** | **< 50MB** |

---

## Next Stage

Once Stage 1 is complete, proceed to **Stage 2: Caching & Fast Path** where we'll build:
- FAQ cache with exact matching
- Time-aware cache expiration
- Fast path execution
- Regex-based answer generation
- System query handlers

---

**Last Updated:** [Current Date]
**Status:** Ready for Implementation
**Estimated Time:** 10-14 days

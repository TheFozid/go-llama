# Stage 4: Self-Learning System
## Automatic Knowledge Discovery & Updates

**Timeline:** Weeks 6-7 (10-14 days)  
**Prerequisites:** Stages 0-3 complete, all execution paths operational  
**Owner:** Backend Team  
**Status:** Not Started

---

## Objectives

1. Build pattern discovery system to identify new query patterns
2. Implement entity extraction from web search results
3. Create abbreviation learning from user interactions
4. Build temporal guardrails for safe automatic updates
5. Implement learning validation and testing
6. Create audit trails for all learning activities
7. Enable system to improve autonomously without manual intervention

---

## Architecture Overview
```
Query Execution
    │
    ├──> Successful Response
    │       │
    │       ├──> Extract Learning Opportunities
    │       │    • New patterns discovered?
    │       │    • New entities mentioned?
    │       │    • New abbreviations used?
    │       │
    │       ├──> Log Learning Candidate
    │       │
    │       └──> Apply Guardrails
    │            • Confidence threshold
    │            • Temporal validation
    │            • Rate limiting
    │            • Conflict checking
    │            │
    │            ├──> Auto-approve (high confidence)
    │            ├──> A/B test (medium confidence)
    │            └──> Queue for review (low confidence)
    │
    └──> System Improves Over Time
```

---

## Component 1: Pattern Discovery Engine

### Purpose
Automatically identify new query patterns from successful interactions.

### Design

**File:** `internal/learning/pattern_discovery.go`
```go
package learning

import (
    "context"
    "database/sql"
    "encoding/json"
    "fmt"
    "regexp"
    "strings"
    "time"
    
    "github.com/rs/zerolog/log"
)

// PatternCandidate represents a potential new pattern
type PatternCandidate struct {
    ID              int
    QueryExample    string
    SuggestedRegex  string
    SuggestedCategory string
    Confidence      float64
    ClusterSize     int
    SuccessRate     float64
    TestResults     []TestResult
    Status          string // pending, testing, approved, rejected
    DiscoveredAt    time.Time
}

// TestResult represents a pattern test outcome
type TestResult struct {
    Query       string
    Matched     bool
    Expected    bool
    Correct     bool
}

// PatternDiscovery handles automatic pattern discovery
type PatternDiscovery struct {
    db              *sql.DB
    minClusterSize  int
    minSuccessRate  float64
    testSampleSize  int
}

// NewPatternDiscovery creates a new pattern discovery engine
func NewPatternDiscovery(db *sql.DB) *PatternDiscovery {
    return &PatternDiscovery{
        db:             db,
        minClusterSize: 10,  // Need at least 10 similar queries
        minSuccessRate: 0.85, // 85% success rate required
        testSampleSize: 20,   // Test against 20 queries
    }
}

// AnalyzeQuery analyzes a successful query for pattern potential
func (pd *PatternDiscovery) AnalyzeQuery(ctx context.Context, query string, response string, success bool) error {
    if !success {
        return nil // Only learn from successes
    }
    
    // Step 1: Normalize query
    normalized := pd.normalizeQuery(query)
    
    // Step 2: Extract structure
    structure := pd.extractStructure(normalized)
    
    // Step 3: Find similar queries
    similarQueries, err := pd.findSimilarQueries(normalized, structure)
    if err != nil {
        return fmt.Errorf("failed to find similar queries: %w", err)
    }
    
    // Step 4: Check if cluster is large enough
    if len(similarQueries) < pd.minClusterSize {
        log.Debug().
            Str("query", query).
            Int("similar", len(similarQueries)).
            Msg("Cluster too small for pattern")
        return nil
    }
    
    // Step 5: Calculate success rate
    successRate := pd.calculateSuccessRate(similarQueries)
    if successRate < pd.minSuccessRate {
        log.Debug().
            Str("query", query).
            Float64("success_rate", successRate).
            Msg("Success rate too low for pattern")
        return nil
    }
    
    // Step 6: Generate regex pattern
    suggestedRegex := pd.generateRegex(normalized, similarQueries)
    suggestedCategory := pd.categorizePattern(normalized, similarQueries)
    
    // Step 7: Check if pattern already exists
    exists, err := pd.patternExists(suggestedRegex)
    if err != nil {
        return err
    }
    if exists {
        return nil // Pattern already known
    }
    
    // Step 8: Create candidate
    candidate := &PatternCandidate{
        QueryExample:      query,
        SuggestedRegex:    suggestedRegex,
        SuggestedCategory: suggestedCategory,
        Confidence:        pd.calculateConfidence(len(similarQueries), successRate),
        ClusterSize:       len(similarQueries),
        SuccessRate:       successRate,
        Status:            "pending",
        DiscoveredAt:      time.Now(),
    }
    
    // Step 9: Store candidate
    if err := pd.storeCandidate(candidate); err != nil {
        return fmt.Errorf("failed to store candidate: %w", err)
    }
    
    log.Info().
        Str("regex", suggestedRegex).
        Str("category", suggestedCategory).
        Int("cluster_size", len(similarQueries)).
        Float64("success_rate", successRate).
        Msg("Pattern candidate discovered")
    
    return nil
}

// normalizeQuery normalizes a query for pattern matching
func (pd *PatternDiscovery) normalizeQuery(query string) string {
    // Lowercase
    q := strings.ToLower(query)
    
    // Replace numbers with placeholder
    numRe := regexp.MustCompile(`\d+(?:\.\d+)?`)
    q = numRe.ReplaceAllString(q, "NUM")
    
    // Replace quoted strings with placeholder
    quoteRe := regexp.MustCompile(`"[^"]+"`)
    q = quoteRe.ReplaceAllString(q, "STR")
    
    // Normalize whitespace
    q = strings.Join(strings.Fields(q), " ")
    
    return q
}

// extractStructure extracts structural elements from query
func (pd *PatternDiscovery) extractStructure(query string) string {
    // Extract question words
    words := strings.Fields(query)
    if len(words) == 0 {
        return ""
    }
    
    structure := words[0] // Question word
    
    // Add key structural elements
    for _, word := range words[1:] {
        if word == "NUM" || word == "STR" {
            structure += " " + word
        } else if len(word) <= 3 { // Short words often structural (is, of, to)
            structure += " " + word
        }
    }
    
    return structure
}

// findSimilarQueries finds queries with similar structure
func (pd *PatternDiscovery) findSimilarQueries(normalized, structure string) ([]string, error) {
    rows, err := pd.db.Query(`
        SELECT DISTINCT original_query
        FROM query_log
        WHERE created_at > datetime('now', '-30 days')
        AND success = TRUE
        LIMIT 100
    `)
    if err != nil {
        return nil, err
    }
    defer rows.Close()
    
    similar := []string{}
    
    for rows.Next() {
        var query string
        if err := rows.Scan(&query); err != nil {
            continue
        }
        
        // Check if structure matches
        queryNorm := pd.normalizeQuery(query)
        queryStruct := pd.extractStructure(queryNorm)
        
        if pd.structuresSimilar(structure, queryStruct) {
            similar = append(similar, query)
        }
    }
    
    return similar, nil
}

// structuresSimilar checks if two structures are similar
func (pd *PatternDiscovery) structuresSimilar(s1, s2 string) bool {
    words1 := strings.Fields(s1)
    words2 := strings.Fields(s2)
    
    // Must have same length
    if len(words1) != len(words2) {
        return false
    }
    
    // Check word-by-word similarity
    matches := 0
    for i := range words1 {
        if words1[i] == words2[i] {
            matches++
        }
    }
    
    // Require 80% match
    return float64(matches)/float64(len(words1)) >= 0.8
}

// calculateSuccessRate calculates success rate for query cluster
func (pd *PatternDiscovery) calculateSuccessRate(queries []string) float64 {
    if len(queries) == 0 {
        return 0
    }
    
    successCount := 0
    for _, query := range queries {
        var success bool
        err := pd.db.QueryRow(`
            SELECT success FROM query_log
            WHERE original_query = ?
            ORDER BY created_at DESC
            LIMIT 1
        `, query).Scan(&success)
        
        if err == nil && success {
            successCount++
        }
    }
    
    return float64(successCount) / float64(len(queries))
}

// generateRegex generates a regex pattern from examples
func (pd *PatternDiscovery) generateRegex(normalized string, examples []string) string {
    // Start with normalized query
    pattern := normalized
    
    // Replace NUM placeholders with number regex
    pattern = strings.ReplaceAll(pattern, "NUM", `(\d+(?:\.\d+)?)`)
    
    // Replace STR placeholders with string regex
    pattern = strings.ReplaceAll(pattern, "STR", `"([^"]+)"`)
    
    // Escape special regex characters in remaining text
    specialChars := []string{".", "+", "*", "?", "^", "$", "(", ")", "[", "]", "{", "}", "|", "\\"}
    for _, char := range specialChars {
        pattern = strings.ReplaceAll(pattern, char, "\\"+char)
    }
    
    // Allow flexible whitespace
    pattern = regexp.MustCompile(`\s+`).ReplaceAllString(pattern, `\s+`)
    
    return pattern
}

// categorizePattern determines pattern category
func (pd *PatternDiscovery) categorizePattern(normalized string, examples []string) string {
    // Check for math patterns
    if strings.Contains(normalized, "NUM") && 
       (strings.Contains(normalized, "+") || strings.Contains(normalized, "-") || 
        strings.Contains(normalized, "*") || strings.Contains(normalized, "/")) {
        return "math"
    }
    
    // Check for definition patterns
    if strings.HasPrefix(normalized, "what is") || strings.HasPrefix(normalized, "define") {
        return "definition"
    }
    
    // Check for current fact patterns
    if strings.Contains(normalized, "current") || strings.Contains(normalized, "latest") {
        return "current_fact"
    }
    
    // Default to factual
    return "factual"
}

// calculateConfidence calculates confidence in pattern
func (pd *PatternDiscovery) calculateConfidence(clusterSize int, successRate float64) float64 {
    // Base confidence from success rate
    confidence := successRate
    
    // Boost for larger clusters
    if clusterSize >= 20 {
        confidence += 0.1
    }
    if clusterSize >= 50 {
        confidence += 0.1
    }
    
    // Clamp
    if confidence > 1.0 {
        confidence = 1.0
    }
    
    return confidence
}

// patternExists checks if pattern already exists
func (pd *PatternDiscovery) patternExists(regex string) (bool, error) {
    var count int
    err := pd.db.QueryRow(`
        SELECT COUNT(*) FROM query_patterns WHERE pattern = ?
    `, regex).Scan(&count)
    
    return count > 0, err
}

// storeCandidate stores a pattern candidate
func (pd *PatternDiscovery) storeCandidate(candidate *PatternCandidate) error {
    _, err := pd.db.Exec(`
        INSERT INTO pattern_discoveries (
            query_example, suggested_regex, suggested_category,
            confidence, approval_status, discovered_at
        ) VALUES (?, ?, ?, ?, ?, ?)
    `, candidate.QueryExample, candidate.SuggestedRegex, candidate.SuggestedCategory,
       candidate.Confidence, candidate.Status, candidate.DiscoveredAt)
    
    return err
}

// TestPattern tests a pattern candidate against historical queries
func (pd *PatternDiscovery) TestPattern(candidateID int) error {
    // Retrieve candidate
    var candidate PatternCandidate
    err := pd.db.QueryRow(`
        SELECT discovery_id, suggested_regex, suggested_category
        FROM pattern_discoveries
        WHERE discovery_id = ?
    `, candidateID).Scan(&candidate.ID, &candidate.SuggestedRegex, &candidate.SuggestedCategory)
    
    if err != nil {
        return fmt.Errorf("candidate not found: %w", err)
    }
    
    // Compile regex
    re, err := regexp.Compile("(?i)^" + candidate.SuggestedRegex + "$")
    if err != nil {
        return fmt.Errorf("invalid regex: %w", err)
    }
    
    // Get test queries
    testQueries, err := pd.getTestQueries(candidate.SuggestedCategory)
    if err != nil {
        return fmt.Errorf("failed to get test queries: %w", err)
    }
    
    // Test pattern
    results := make([]TestResult, 0, len(testQueries))
    correctCount := 0
    
    for _, testQuery := range testQueries {
        matched := re.MatchString(strings.ToLower(testQuery.Query))
        correct := matched == testQuery.ShouldMatch
        
        if correct {
            correctCount++
        }
        
        results = append(results, TestResult{
            Query:    testQuery.Query,
            Matched:  matched,
            Expected: testQuery.ShouldMatch,
            Correct:  correct,
        })
    }
    
    accuracy := float64(correctCount) / float64(len(testQueries))
    
    // Store test results
    resultsJSON, _ := json.Marshal(results)
    _, err = pd.db.Exec(`
        UPDATE pattern_discoveries
        SET test_success_rate = ?,
            test_count = ?,
            test_results = ?,
            approval_status = CASE 
                WHEN ? >= 0.85 THEN 'approved'
                WHEN ? >= 0.7 THEN 'testing'
                ELSE 'rejected'
            END
        WHERE discovery_id = ?
    `, accuracy, len(testQueries), resultsJSON, accuracy, accuracy, candidateID)
    
    if err != nil {
        return err
    }
    
    log.Info().
        Int("candidate_id", candidateID).
        Float64("accuracy", accuracy).
        Int("correct", correctCount).
        Int("total", len(testQueries)).
        Msg("Pattern test completed")
    
    // If approved, promote to production
    if accuracy >= 0.85 {
        return pd.promoteToProduction(candidateID)
    }
    
    return nil
}

// TestQuery represents a query for testing
type TestQuery struct {
    Query       string
    ShouldMatch bool
}

// getTestQueries retrieves queries for testing a pattern
func (pd *PatternDiscovery) getTestQueries(category string) ([]TestQuery, error) {
    // Get positive examples (should match)
    positiveRows, err := pd.db.Query(`
        SELECT original_query FROM query_log
        WHERE execution_path = 'fast'
        AND success = TRUE
        ORDER BY RANDOM()
        LIMIT ?
    `, pd.testSampleSize/2)
    
    if err != nil {
        return nil, err
    }
    defer positiveRows.Close()
    
    queries := make([]TestQuery, 0, pd.testSampleSize)
    
    for positiveRows.Next() {
        var query string
        if err := positiveRows.Scan(&query); err != nil {
            continue
        }
        queries = append(queries, TestQuery{Query: query, ShouldMatch: true})
    }
    
    // Get negative examples (should not match)
    negativeRows, err := pd.db.Query(`
        SELECT original_query FROM query_log
        WHERE execution_path != 'fast'
        ORDER BY RANDOM()
        LIMIT ?
    `, pd.testSampleSize/2)
    
    if err != nil {
        return queries, nil // Return what we have
    }
    defer negativeRows.Close()
    
    for negativeRows.Next() {
        var query string
        if err := negativeRows.Scan(&query); err != nil {
            continue
        }
        queries = append(queries, TestQuery{Query: query, ShouldMatch: false})
    }
    
    return queries, nil
}

// promoteToProduction promotes an approved pattern to production
func (pd *PatternDiscovery) promoteToProduction(candidateID int) error {
    // Get candidate details
    var regex, category string
    var confidence float64
    
    err := pd.db.QueryRow(`
        SELECT suggested_regex, suggested_category, confidence
        FROM pattern_discoveries
        WHERE discovery_id = ? AND approval_status = 'approved'
    `, candidateID).Scan(&regex, &category, &confidence)
    
    if err != nil {
        return fmt.Errorf("candidate not found or not approved: %w", err)
    }
    
    // Insert into production patterns
    _, err = pd.db.Exec(`
        INSERT INTO query_patterns (
            pattern, category, priority, handler,
            base_confidence, confidence_type, requires_search,
            created_at
        ) VALUES (?, ?, ?, ?, ?, ?, ?, ?)
    `, regex, category, 50, "llm", confidence, "slow_change", false, time.Now())
    
    if err != nil {
        return fmt.Errorf("failed to insert pattern: %w", err)
    }
    
    log.Info().
        Int("candidate_id", candidateID).
        Str("regex", regex).
        Str("category", category).
        Msg("Pattern promoted to production")
    
    return nil
}
```

---

## Component 2: Entity Learning Engine

### Purpose
Extract and store new entities from web search results and conversations.

### Design

**File:** `internal/learning/entity_learning.go`
```go
package learning

import (
    "context"
    "database/sql"
    "encoding/json"
    "fmt"
    "strings"
    "time"
    
    "github.com/rs/zerolog/log"
    "github.com/yourusername/intelligent-llm-orchestration/pkg/temporal"
)

// EntityLearner extracts and learns new entities
type EntityLearner struct {
    db              *sql.DB
    minConfidence   float64
    requireMultipleSources bool
}

// DiscoveredEntity represents a newly discovered entity
type DiscoveredEntity struct {
    Name        string
    Type        string
    Aliases     []string
    Facts       map[string]string
    Description string
    Source      string
    SourceURL   string
    Confidence  float64
}

// NewEntityLearner creates a new entity learner
func NewEntityLearner(db *sql.DB) *EntityLearner {
    return &EntityLearner{
        db:                    db,
        minConfidence:         0.7,
        requireMultipleSources: true,
    }
}

// ExtractFromContent extracts entities from web content
func (el *EntityLearner) ExtractFromContent(ctx context.Context, content string, sourceURL string) error {
    // This would typically use an LLM to extract structured entities
    // For now, simplified placeholder implementation
    
    // Look for capitalized proper nouns
    entities := el.extractProperNouns(content)
    
    for _, entity := range entities {
        // Store as learning candidate
        if err := el.storeCandidate(entity, sourceURL); err != nil {
            log.Error().Err(err).Str("entity", entity.Name).Msg("Failed to store entity candidate")
        }
    }
    
    return nil
}

// extractProperNouns extracts potential entities from text
func (el *EntityLearner) extractProperNouns(content string) []DiscoveredEntity {
    // Simplified extraction - in production, use NER or LLM
    entities := make([]DiscoveredEntity, 0)
    
    // Split into sentences
    sentences := strings.Split(content, ".")
    
    for _, sentence := range sentences {
        words := strings.Fields(sentence)
        
        for i, word := range words {
            // Look for capitalized words (potential proper nouns)
            if len(word) > 0 && word[0] >= 'A' && word[0] <= 'Z' {
                // Extract context (surrounding words)
                context := el.extractContext(words, i)
                
                entity := DiscoveredEntity{
                    Name:        word,
                    Type:        el.guessEntityType(word, context),
                    Description: context,
                    Confidence:  0.6, // Low initial confidence
                }
                
                entities = append(entities, entity)
            }
        }
    }
    
    return entities
}

// extractContext extracts surrounding words for context
func (el *EntityLearner) extractContext(words []string, index int) string {
    start := index - 5
    if start < 0 {
        start = 0
    }
    
    end := index + 5
    if end > len(words) {
        end = len(words)
    }
    
    return strings.Join(words[start:end], " ")
}

// guessEntityType guesses entity type from context
func (el *EntityLearner) guessEntityType(name, context string) string {
    contextLower := strings.ToLower(context)
    
    // Person indicators
    personIndicators := []string{"mr", "mrs", "dr", "president", "ceo", "said", "told"}
    for _, indicator := range personIndicators {
        if strings.Contains(contextLower, indicator) {
            return "person"
        }
    }
    
    // Place indicators
    placeIndicators := []string{"city", "country", "located", "capital", "region"}
    for _, indicator := range placeIndicators {
        if strings.Contains(contextLower, indicator) {
            return "place"
        }
    }
    
    // Organization indicators
    orgIndicators := []string{"company", "corporation", "inc", "ltd", "organization"}
    for _, indicator := range orgIndicators {
        if strings.Contains(contextLower, indicator) {
            return "organization"
        }
    }
    
    return "unknown"
}

// storeCandidate stores an entity candidate
func (el *EntityLearner) storeCandidate(entity DiscoveredEntity, sourceURL string) error {
    // Check if entity already exists
    exists, err := el.entityExists(entity.Name)
    if err != nil {
        return err
    }
    
    if exists {
        // Update existing entity
        return el.updateExistingEntity(entity, sourceURL)
    }
    
    // Store as new candidate
    entityID := strings.ToLower(strings.ReplaceAll(entity.Name, " ", "_"))
    aliasesJSON, _ := json.Marshal(entity.Aliases)
    factsJSON, _ := json.Marshal(entity.Facts)
    
    _, err = pd.db.Exec(`
        INSERT INTO entities (
            entity_id, entity_type, name, aliases, facts, description,
            base_confidence, confidence_type,
            source, source_url, verified,
            created_at, last_updated, last_verified
        ) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
    `, entityID, entity.Type, entity.Name, aliasesJSON, factsJSON, entity.Description,
       entity.Confidence, string(temporal.SlowChange),
       entity.Source, sourceURL, false,
       time.Now(), time.Now(), time.Now())
    
    if err != nil {
        return err
    }
    
    log.Info().
        Str("entity", entity.Name).
        Str("type", entity.Type).
        Float64("confidence", entity.Confidence).
        Msg("New entity candidate stored")
    
    return nil
}

// entityExists checks if an entity already exists
func (el *EntityLearner) entityExists(name string) (bool, error) {
    var count int
    err := el.db.QueryRow(`
        SELECT COUNT(*) FROM entities
        WHERE name = ? COLLATE NOCASE
    `, name).Scan(&count)
    
    return count > 0, err
}

// updateExistingEntity updates an existing entity with new information
func (el *EntityLearner) updateExistingEntity(entity DiscoveredEntity, sourceURL string) error {
    // Get existing entity
    var existingID string
    var existingConfidence float64
    var lastVerified time.Time
    
    err := el.db.QueryRow(`
        SELECT entity_id, base_confidence, last_verified
        FROM entities
        WHERE name = ? COLLATE NOCASE
    `, entity.Name).Scan(&existingID, &existingConfidence, &lastVerified)
    
    if err != nil {
        return err
    }
    
    // Calculate temporal confidence
    tc := temporal.TemporalConfidence{
        BaseConfidence: existingConfidence,
        Timestamp:      lastVerified,
        Type:           temporal.SlowChange,
    }
    
    effectiveConf := tc.EffectiveConfidence()
    
    // Only update if new information has higher effective confidence
    if entity.Confidence > effectiveConf * 0.9 { // Within 10% is good enough
        _, err = el.db.Exec(`
            UPDATE entities
            SET 
                description = ?,
                base_confidence = ?,
                source_url = ?,
                last_updated = ?,
                last_verified = ?
            WHERE entity_id = ?
        `, entity.Description, entity.Confidence, sourceURL,
           time.Now(), time.Now(), existingID)
        
        if err == nil {
            log.Info().
                Str("entity", entity.Name).
                Float64("old_confidence", effectiveConf).
                Float64("new_confidence", entity.Confidence).
                Msg("Entity updated")
        }
        
        return err
    }
    
    log.Debug().
        Str("entity", entity.Name).
        Float64("existing_confidence", effectiveConf).
        Float64("new_confidence", entity.Confidence).
        Msg("Entity update skipped - existing data better")
    
    return nil
}

// LearnEntityFact learns a new fact about an entity
func (el *EntityLearner) LearnEntityFact(entityID, factKey, factValue string, confidence float64, source, sourceURL string) error {
    // Check if fact already exists
    var existingValue string
    var existingConfidence float64
    var lastVerified time.Time
    
    err := el.db.QueryRow(`
        SELECT fact_value, base_confidence, valid_from
        FROM entity_facts
        WHERE entity_id = ? AND fact_key = ? AND is_current = TRUE
    `, entityID, factKey).Scan(&existingValue, &existingConfidence, &lastVerified)
    
    if err == sql.ErrNoRows {
        // New fact
        return el.insertNewFact(entityID, factKey, factValue, confidence, source, sourceURL)
    }
    
    if err != nil {
        return err
    }
    
    // Fact exists - check if update needed
    tc := temporal.TemporalConfidence{
        BaseConfidence: existingConfidence,
        Timestamp:      lastVerified,
        Type:           temporal.FastChange, // Facts change more often
    }
    
    effectiveConf := tc.EffectiveConfidence()
    
    // Update if new confidence is better or value changed
    if factValue != existingValue && confidence > effectiveConf * 0.9 {
        return el.updateFact(entityID, factKey, existingValue, factValue, confidence, source, sourceURL)
    }
    
    return nil
}

// insertNewFact inserts a new entity fact
func (el *EntityLearner) insertNewFact(entityID, factKey, factValue string, confidence float64, source, sourceURL string) error {
    _, err := el.db.Exec(`
        INSERT INTO entity_facts (
            entity_id, fact_key, fact_value,
            valid_from, base_confidence, confidence_type,
            source, source_url, is_current,
            discovered_at
        ) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
    `, entityID, factKey, factValue,
       time.Now(), confidence, string(temporal.FastChange),
       source, sourceURL, true,
       time.Now())
    
    if err == nil {
        log.Info().
            Str("entity", entityID).
            Str("fact", factKey).
            Str("value", factValue).
            Msg("New entity fact learned")
    }
    
    return err
}

// updateFact updates an existing entity fact
func (el *EntityLearner) updateFact(entityID, factKey, oldValue, newValue string, confidence float64, source, sourceURL string) error {
    tx, err := el.db.Begin()
    if err != nil {
        return err
    }
    defer tx.Rollback()
    
    // Insert new fact
    result, err := tx.Exec(`
        INSERT INTO entity_facts (
            entity_id, fact_key, fact_value,
            valid_from, base_confidence, confidence_type,
            source, source_url, is_current,
            discovered_at
        ) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
    `, entityID, factKey, newValue,
       time.Now(), confidence, string(temporal.FastChange),
       source, sourceURL, true,
       time.Now())
    
    if err != nil {
        return err
    }
    
    newFactID, _ := result.LastInsertId()
    
    // Mark old fact as superseded
    _, err = tx.Exec(`
        UPDATE entity_facts
        SET 
            is_current = FALSE,
            valid_until = ?,
            superseded_by = ?
        WHERE entity_id = ? AND fact_key = ? AND is_current = TRUE
        AND fact_id != ?
    `, time.Now(), newFactID, entityID, factKey, newFactID)
    
    if err != nil {
        return err
    }
    
    // Log to history
    _, err = tx.Exec(`
        INSERT INTO entity_history (
            entity_id, field_name, old_value, new_value,
            source, confidence, changed_at
        ) VALUES (?, ?, ?, ?, ?, ?, ?)
    `, entityID, factKey, oldValue, newValue, source, confidence, time.Now())
    
    if err != nil {
        return err
    }
    
    if err := tx.Commit

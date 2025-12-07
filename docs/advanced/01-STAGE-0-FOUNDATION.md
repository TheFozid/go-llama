# Stage 0: Foundation
## Database Schema & Project Structure Setup

**Timeline:** Week 1 (5-7 days)  
**Prerequisites:** Go 1.21+, SQLite 3, Git  
**Owner:** Backend Team  
**Status:** Not Started

---

## Objectives

1. Create complete database schema with temporal awareness
2. Set up Go project structure and dependencies
3. Initialize repository with proper structure
4. Create seed data for initial operation
5. Write database migration system
6. Set up development environment

---

## Database Schema

### Overview

The database uses SQLite 3 with these extensions:
- **FTS5** for full-text search on entities
- **JSON functions** for flexible fact storage
- **Temporal indexes** for time-based queries

### Schema Design Principles

1. **Temporal First:** Every table has timestamps and confidence tracking
2. **Audit Trail:** All changes logged in history tables
3. **Flexible Facts:** JSON fields for extensible entity attributes
4. **Performance:** Strategic indexes on common query patterns
5. **Learning Ready:** Tables designed for ML/pattern discovery

---

## Core Tables

### 1. Entities Table
```sql
-- Core entity knowledge base
CREATE TABLE entities (
    entity_id TEXT PRIMARY KEY,           -- Unique identifier (slug format)
    entity_type TEXT NOT NULL,            -- person, place, organization, concept, event
    name TEXT NOT NULL,                   -- Canonical name
    aliases TEXT,                         -- JSON array: ["UK", "United Kingdom", "Britain"]
    description TEXT,                     -- Brief description
    
    -- Temporal metadata
    created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    last_updated TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    last_verified TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    next_verification_due TIMESTAMP,
    
    -- Confidence tracking
    base_confidence REAL DEFAULT 0.7,
    confidence_type TEXT DEFAULT 'slow_change', -- static, slow_change, fast_change, real_time
    
    -- Source tracking
    source TEXT,                          -- Where this entity came from
    source_url TEXT,
    verified BOOLEAN DEFAULT FALSE
);

CREATE INDEX idx_entity_type ON entities(entity_type);
CREATE INDEX idx_entity_name ON entities(name);
CREATE INDEX idx_entity_verified ON entities(verified, confidence_type);
CREATE INDEX idx_entity_verification_due ON entities(next_verification_due);

-- Full-text search on entities
CREATE VIRTUAL TABLE entities_fts USING fts5(
    name, 
    aliases, 
    description, 
    content=entities,
    content_rowid=rowid
);

-- Triggers to keep FTS in sync
CREATE TRIGGER entities_ai AFTER INSERT ON entities BEGIN
    INSERT INTO entities_fts(rowid, name, aliases, description)
    VALUES (new.rowid, new.name, new.aliases, new.description);
END;

CREATE TRIGGER entities_ad AFTER DELETE ON entities BEGIN
    DELETE FROM entities_fts WHERE rowid = old.rowid;
END;

CREATE TRIGGER entities_au AFTER UPDATE ON entities BEGIN
    UPDATE entities_fts SET 
        name = new.name, 
        aliases = new.aliases, 
        description = new.description
    WHERE rowid = old.rowid;
END;
```

### 2. Entity Facts Table
```sql
-- Individual facts about entities with full temporal tracking
CREATE TABLE entity_facts (
    fact_id INTEGER PRIMARY KEY AUTOINCREMENT,
    entity_id TEXT NOT NULL,
    fact_key TEXT NOT NULL,              -- e.g., "current_pm", "capital", "population"
    fact_value TEXT NOT NULL,
    
    -- Temporal validity
    valid_from TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    valid_until TIMESTAMP,               -- NULL = still current
    discovered_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    
    -- Confidence tracking
    base_confidence REAL DEFAULT 0.7,
    confidence_type TEXT DEFAULT 'slow_change',
    
    -- Source tracking
    source TEXT,                         -- web_search, user_input, llm_inference, verified_db
    source_url TEXT,
    
    -- Status
    is_current BOOLEAN DEFAULT TRUE,
    superseded_by INTEGER,               -- fact_id that replaced this one
    
    FOREIGN KEY (entity_id) REFERENCES entities(entity_id) ON DELETE CASCADE,
    FOREIGN KEY (superseded_by) REFERENCES entity_facts(fact_id)
);

CREATE INDEX idx_entity_facts_current ON entity_facts(entity_id, fact_key, is_current);
CREATE INDEX idx_entity_facts_temporal ON entity_facts(valid_from, valid_until);
CREATE INDEX idx_entity_facts_entity ON entity_facts(entity_id);
CREATE INDEX idx_entity_facts_confidence ON entity_facts(confidence_type, valid_from);
```

### 3. Entity History Table
```sql
-- Audit trail of all entity changes
CREATE TABLE entity_history (
    history_id INTEGER PRIMARY KEY AUTOINCREMENT,
    entity_id TEXT NOT NULL,
    field_name TEXT NOT NULL,            -- Which field changed
    old_value TEXT,
    new_value TEXT,
    source TEXT,                         -- web_search, user_correction, llm_inference
    confidence REAL,
    changed_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    changed_by TEXT,                     -- system, user_id, job_name
    
    FOREIGN KEY (entity_id) REFERENCES entities(entity_id) ON DELETE CASCADE
);

CREATE INDEX idx_entity_history_entity ON entity_history(entity_id, changed_at DESC);
CREATE INDEX idx_entity_history_date ON entity_history(changed_at);
```

### 4. Query Patterns Table
```sql
-- Regex patterns for fast query matching
CREATE TABLE query_patterns (
    pattern_id INTEGER PRIMARY KEY AUTOINCREMENT,
    pattern TEXT UNIQUE NOT NULL,        -- Regex pattern
    category TEXT NOT NULL,              -- math, factual, conversion, time, definition
    priority INTEGER DEFAULT 0,          -- Higher = checked first
    template TEXT,                       -- Response template (optional)
    handler TEXT,                        -- regex, db_lookup, calculation, system, api
    examples TEXT,                       -- JSON array of example Q&A pairs
    requires_search BOOLEAN DEFAULT FALSE,
    
    -- Confidence tracking
    base_confidence REAL DEFAULT 0.9,
    confidence_type TEXT DEFAULT 'slow_change',
    
    -- Performance metrics
    hit_count INTEGER DEFAULT 0,
    success_count INTEGER DEFAULT 0,
    failure_count INTEGER DEFAULT 0,
    
    -- Recent performance (last 7 days)
    recent_hits INTEGER DEFAULT 0,
    recent_successes INTEGER DEFAULT 0,
    recent_failures INTEGER DEFAULT 0,
    
    -- Temporal metadata
    created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    last_used TIMESTAMP,
    last_success TIMESTAMP,
    last_verified TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    
    -- Computed fields (updated by jobs)
    overall_success_rate REAL DEFAULT 1.0,
    recent_success_rate REAL DEFAULT 1.0,
    effective_confidence REAL DEFAULT 0.9
);

CREATE INDEX idx_pattern_priority ON query_patterns(priority DESC, effective_confidence DESC);
CREATE INDEX idx_pattern_category ON query_patterns(category);
CREATE INDEX idx_pattern_performance ON query_patterns(recent_success_rate DESC, recent_hits DESC);
```

### 5. FAQ Cache Table
```sql
-- Fast lookup cache for common questions
CREATE TABLE faq_cache (
    query_hash TEXT PRIMARY KEY,         -- Hash of normalized query
    canonical_query TEXT NOT NULL,       -- Normalized query text
    answer TEXT NOT NULL,
    sources TEXT,                        -- JSON array of source URLs
    
    -- Temporal fields
    created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    last_verified TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    last_accessed TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    expires_at TIMESTAMP,                -- Hard expiration date
    
    -- Confidence tracking
    base_confidence REAL DEFAULT 1.0,
    current_confidence REAL,             -- Computed from base + age
    confidence_type TEXT DEFAULT 'slow_change',
    
    -- Usage tracking
    hit_count INTEGER DEFAULT 0,
    
    -- Maintenance
    max_age_hours INTEGER DEFAULT 720,   -- 30 days default
    needs_revalidation BOOLEAN DEFAULT FALSE
);

CREATE INDEX idx_faq_canonical ON faq_cache(canonical_query);
CREATE INDEX idx_faq_expires ON faq_cache(expires_at);
CREATE INDEX idx_faq_confidence ON faq_cache(current_confidence DESC, last_accessed DESC);
CREATE INDEX idx_faq_revalidation ON faq_cache(needs_revalidation) WHERE needs_revalidation = TRUE;
```

### 6. Semantic Cache Table
```sql
-- Semantic similarity cache (Stage 7)
CREATE TABLE semantic_cache (
    cache_id INTEGER PRIMARY KEY AUTOINCREMENT,
    query_text TEXT NOT NULL,
    query_embedding BLOB NOT NULL,       -- Serialized float32 array
    answer TEXT NOT NULL,
    
    -- Temporal fields
    created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    last_accessed TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    expires_at TIMESTAMP,
    
    -- Confidence
    base_confidence REAL DEFAULT 0.9,
    confidence_type TEXT DEFAULT 'slow_change',
    
    -- Usage
    hit_count INTEGER DEFAULT 0
);

CREATE INDEX idx_semantic_expires ON semantic_cache(expires_at);
CREATE INDEX idx_semantic_accessed ON semantic_cache(last_accessed DESC);
```

### 7. Learned Abbreviations Table
```sql
-- Abbreviations learned from user interactions
CREATE TABLE learned_abbreviations (
    abbr_id INTEGER PRIMARY KEY AUTOINCREMENT,
    short_form TEXT UNIQUE NOT NULL,     -- "btw", "uk", "pm"
    full_form TEXT NOT NULL,             -- "by the way", "united kingdom", "prime minister"
    
    -- Temporal tracking
    first_seen TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    last_seen TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    
    -- Confidence
    base_confidence REAL DEFAULT 0.7,
    effective_confidence REAL,
    
    -- Usage patterns
    usage_count INTEGER DEFAULT 0,
    recent_usage_count INTEGER DEFAULT 0, -- Last 30 days
    context_tags TEXT,                    -- JSON array of contexts
    
    -- Verification
    last_verified TIMESTAMP,
    verified BOOLEAN DEFAULT FALSE,
    verification_source TEXT
);

CREATE INDEX idx_abbr_short ON learned_abbreviations(short_form);
CREATE INDEX idx_abbr_confidence ON learned_abbreviations(effective_confidence DESC);
CREATE INDEX idx_abbr_usage ON learned_abbreviations(usage_count DESC);
```

### 8. Reasoning Examples Table
```sql
-- Few-shot learning examples for reasoning tasks
CREATE TABLE reasoning_examples (
    example_id INTEGER PRIMARY KEY AUTOINCREMENT,
    problem_type TEXT NOT NULL,          -- syllogism, algebra, probability, logic, word_problem
    difficulty INTEGER DEFAULT 1,        -- 1-5
    question TEXT NOT NULL,
    reasoning_steps TEXT,                -- JSON array of step-by-step reasoning
    answer TEXT NOT NULL,
    explanation TEXT,
    tags TEXT,                           -- JSON array for filtering
    
    -- Temporal
    created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    last_used TIMESTAMP,
    usage_count INTEGER DEFAULT 0
);

CREATE INDEX idx_reasoning_type ON reasoning_examples(problem_type, difficulty);
CREATE INDEX idx_reasoning_usage ON reasoning_examples(usage_count DESC, last_used DESC);
```

### 9. Query Log Table
```sql
-- Complete log of all queries for learning and analytics
CREATE TABLE query_log (
    log_id INTEGER PRIMARY KEY AUTOINCREMENT,
    session_id TEXT,
    
    -- Query details
    original_query TEXT NOT NULL,
    rewritten_query TEXT,
    query_hash TEXT,                     -- For deduplication
    
    -- Routing
    confidence_score REAL,
    execution_path TEXT,                 -- fast, medium, full, cached
    
    -- Execution details
    models_used TEXT,                    -- JSON array
    tools_used TEXT,                     -- JSON array
    latency_ms INTEGER,
    
    -- Results
    success BOOLEAN,
    cached BOOLEAN DEFAULT FALSE,
    cache_hit_type TEXT,                 -- exact, semantic, pattern, null
    
    -- User feedback
    user_feedback INTEGER,               -- -1 (thumbs down), 0 (no feedback), 1 (thumbs up)
    
    -- Temporal
    created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
);

CREATE INDEX idx_log_date ON query_log(created_at DESC);
CREATE INDEX idx_log_path ON query_log(execution_path);
CREATE INDEX idx_log_success ON query_log(success, execution_path);
CREATE INDEX idx_log_hash ON query_log(query_hash);
CREATE INDEX idx_log_feedback ON query_log(user_feedback) WHERE user_feedback IS NOT NULL;
```

### 10. Tool History Table
```sql
-- Track which tool combinations work best
CREATE TABLE tool_history (
    history_id INTEGER PRIMARY KEY AUTOINCREMENT,
    query_pattern TEXT NOT NULL,
    query_category TEXT,
    tools_used TEXT NOT NULL,            -- JSON array: ["web_search", "extract", "summarize"]
    tool_sequence TEXT NOT NULL,         -- JSON array showing order
    
    -- Results
    success BOOLEAN NOT NULL,
    response_time_ms INTEGER,
    user_feedback INTEGER,
    
    -- Temporal
    created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
);

CREATE INDEX idx_tool_pattern ON tool_history(query_pattern, success);
CREATE INDEX idx_tool_category ON tool_history(query_category, success);
```

### 11. Pattern Discoveries Table
```sql
-- Auto-discovered patterns pending approval
CREATE TABLE pattern_discoveries (
    discovery_id INTEGER PRIMARY KEY AUTOINCREMENT,
    query_example TEXT NOT NULL,
    suggested_regex TEXT NOT NULL,
    suggested_category TEXT,
    confidence REAL,
    
    -- Testing
    approval_status TEXT DEFAULT 'pending', -- pending, testing, approved, rejected
    test_success_rate REAL,
    test_count INTEGER DEFAULT 0,
    test_results TEXT,                      -- JSON array of test outcomes
    
    -- Temporal
    discovered_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    approved_at TIMESTAMP,
    approved_by TEXT
);

CREATE INDEX idx_discoveries_status ON pattern_discoveries(approval_status, discovered_at DESC);
CREATE INDEX idx_discoveries_confidence ON pattern_discoveries(confidence DESC, test_success_rate DESC);
```

### 12. Learning Log Table
```sql
-- Track what the system learns
CREATE TABLE learning_log (
    log_id INTEGER PRIMARY KEY AUTOINCREMENT,
    query_id INTEGER,                    -- Links to query_log
    learning_type TEXT NOT NULL,         -- pattern, entity, abbreviation, fact_update, tool_combo
    learned_data TEXT NOT NULL,          -- JSON payload
    confidence REAL,
    
    -- Status
    applied BOOLEAN DEFAULT FALSE,
    applied_at TIMESTAMP,
    rejected BOOLEAN DEFAULT FALSE,
    rejection_reason TEXT,
    
    -- Temporal
    created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    
    FOREIGN KEY (query_id) REFERENCES query_log(log_id)
);

CREATE INDEX idx_learning_type ON learning_log(learning_type, applied);
CREATE INDEX idx_learning_date ON learning_log(created_at DESC);
CREATE INDEX idx_learning_pending ON learning_log(applied) WHERE applied = FALSE AND rejected = FALSE;
```

---

## Seed Data

### Initial Patterns

**File:** `seeds/01_initial_patterns.sql`
```sql
-- Math operations
INSERT INTO query_patterns (pattern, category, priority, handler, base_confidence, confidence_type) VALUES
('(\d+)\s*\+\s*(\d+)', 'math', 100, 'calculation', 1.0, 'static'),
('(\d+)\s*-\s*(\d+)', 'math', 100, 'calculation', 1.0, 'static'),
('(\d+)\s*\*\s*(\d+)', 'math', 100, 'calculation', 1.0, 'static'),
('(\d+)\s*/\s*(\d+)', 'math', 100, 'calculation', 1.0, 'static'),
('what is (\d+) percent of (\d+)', 'math', 100, 'calculation', 1.0, 'static'),
('(\d+)% of (\d+)', 'math', 100, 'calculation', 1.0, 'static');

-- Time and date queries
INSERT INTO query_patterns (pattern, category, priority, handler, base_confidence, confidence_type) VALUES
('what time is it', 'time_query', 100, 'system', 1.0, 'real_time'),
('what''s the time', 'time_query', 100, 'system', 1.0, 'real_time'),
('what date is it', 'time_query', 100, 'system', 1.0, 'real_time'),
('what''s today''s date', 'time_query', 100, 'system', 1.0, 'real_time');

-- Unit conversions
INSERT INTO query_patterns (pattern, category, priority, handler, base_confidence, confidence_type) VALUES
('convert (\d+(?:\.\d+)?)\s*(\w+) to (\w+)', 'conversion', 95, 'calculation', 1.0, 'static'),
('(\d+(?:\.\d+)?)\s*(\w+) in (\w+)', 'conversion', 95, 'calculation', 1.0, 'static'),
('how many (\w+) in (\d+(?:\.\d+)?)\s*(\w+)', 'conversion', 95, 'calculation', 1.0, 'static');

-- Current facts (require web search)
INSERT INTO query_patterns (pattern, category, priority, handler, requires_search, base_confidence, confidence_type) VALUES
('(?:who|what) is (?:the )?(?:current|latest) (?:prime minister|pm) of (?:the )?(\w+)', 'current_leader', 90, 'search', TRUE, 0.9, 'fast_change'),
('who is (?:the )?(?:president|pres) of (?:the )?(\w+)', 'current_leader', 90, 'search', TRUE, 0.9, 'fast_change'),
('(?:current|latest|today''s) (?:price|value|rate) of (\w+)', 'current_price', 90, 'search', TRUE, 0.85, 'real_time'),
('what is (?:the )?(?:bitcoin|btc|ethereum|eth) price', 'crypto_price', 90, 'search', TRUE, 0.85, 'real_time');

-- Definitions
INSERT INTO query_patterns (pattern, category, priority, handler, base_confidence, confidence_type) VALUES
('what (?:is|are|does|do) (?:an? )?(\w+)', 'definition', 70, 'db_lookup', 0.8, 'slow_change'),
('define (\w+)', 'definition', 70, 'db_lookup', 0.8, 'slow_change'),
('meaning of (\w+)', 'definition', 70, 'db_lookup', 0.8, 'slow_change');
```

### Initial Entities

**File:** `seeds/02_initial_entities.sql`
```sql
-- Countries
INSERT INTO entities (entity_id, entity_type, name, aliases, description, base_confidence, confidence_type, verified) VALUES
('uk', 'country', 'United Kingdom', 
 '["UK", "Britain", "Great Britain", "United Kingdom of Great Britain and Northern Ireland", "U.K."]',
 'A country in Europe comprising England, Scotland, Wales, and Northern Ireland',
 0.95, 'static', TRUE),

('usa', 'country', 'United States', 
 '["USA", "US", "United States of America", "America", "U.S.", "U.S.A."]',
 'A country in North America consisting of 50 states',
 0.95, 'static', TRUE),

('france', 'country', 'France', 
 '["French Republic", "République française"]',
 'A country in Western Europe',
 0.95, 'static', TRUE);

-- Initial facts for UK
INSERT INTO entity_facts (entity_id, fact_key, fact_value, base_confidence, confidence_type, source, verified) VALUES
('uk', 'capital', 'London', 0.99, 'static', 'verified_db', TRUE),
('uk', 'continent', 'Europe', 0.99, 'static', 'verified_db', TRUE),
('uk', 'currency', 'GBP', 0.99, 'static', 'verified_db', TRUE),
('uk', 'population', '67000000', 0.85, 'slow_change', 'verified_db', TRUE);

-- Initial facts for USA
INSERT INTO entity_facts (entity_id, fact_key, fact_value, base_confidence, confidence_type, source, verified) VALUES
('usa', 'capital', 'Washington D.C.', 0.99, 'static', 'verified_db', TRUE),
('usa', 'continent', 'North America', 0.99, 'static', 'verified_db', TRUE),
('usa', 'currency', 'USD', 0.99, 'static', 'verified_db', TRUE),
('usa', 'population', '331000000', 0.85, 'slow_change', 'verified_db', TRUE);

-- Mathematical constants
INSERT INTO entities (entity_id, entity_type, name, description, base_confidence, confidence_type, verified) VALUES
('pi', 'constant', 'Pi', 'Mathematical constant, ratio of circle circumference to diameter', 1.0, 'static', TRUE),
('e', 'constant', 'Euler''s Number', 'Mathematical constant, base of natural logarithms', 1.0, 'static', TRUE);

INSERT INTO entity_facts (entity_id, fact_key, fact_value, base_confidence, confidence_type, source, verified) VALUES
('pi', 'value', '3.14159265359', 1.0, 'static', 'verified_db', TRUE),
('pi', 'approximate', '3.14', 1.0, 'static', 'verified_db', TRUE),
('e', 'value', '2.71828182846', 1.0, 'static', 'verified_db', TRUE),
('e', 'approximate', '2.718', 1.0, 'static', 'verified_db', TRUE);
```

### Initial Abbreviations

**File:** `seeds/03_initial_abbreviations.sql`
```sql
INSERT INTO learned_abbreviations (short_form, full_form, base_confidence, verified, verification_source) VALUES
('who''s', 'who is', 1.0, TRUE, 'manual'),
('what''s', 'what is', 1.0, TRUE, 'manual'),
('where''s', 'where is', 1.0, TRUE, 'manual'),
('when''s', 'when is', 1.0, TRUE, 'manual'),
('how''s', 'how is', 1.0, TRUE, 'manual'),
('uk', 'united kingdom', 1.0, TRUE, 'manual'),
('us', 'united states', 1.0, TRUE, 'manual'),
('usa', 'united states of america', 1.0, TRUE, 'manual'),
('pm', 'prime minister', 0.9, TRUE, 'manual'),
('pres', 'president', 0.9, TRUE, 'manual'),
('sec', 'secretary', 0.9, TRUE, 'manual'),
('min', 'minister', 0.9, TRUE, 'manual'),
('govt', 'government', 0.95, TRUE, 'manual'),
('info', 'information', 0.95, TRUE, 'manual'),
('btw', 'by the way', 0.9, TRUE, 'manual'),
('fyi', 'for your information', 0.9, TRUE, 'manual');
```

### Initial Reasoning Examples

**File:** `seeds/04_reasoning_examples.sql`
```sql
-- Logic examples
INSERT INTO reasoning_examples (problem_type, difficulty, question, reasoning_steps, answer, explanation) VALUES
('syllogism', 2, 'If all A are B, and all B are C, then are all A also C?',
 '["Premise 1: All A are B", "Premise 2: All B are C", "Transitive property: If A→B and B→C, then A→C", "Conclusion: All A are C"]',
 'Yes, all A are C',
 'This is a basic syllogism using the transitive property of logical implication');

-- Math examples
INSERT INTO reasoning_examples (problem_type, difficulty, question, reasoning_steps, answer, explanation) VALUES
('percentage', 1, 'What is 20% of 50?',
 '["Convert percentage to decimal: 20% = 0.20", "Multiply: 0.20 × 50 = 10"]',
 '10',
 'To find a percentage of a number, convert the percentage to decimal and multiply'),

('word_problem', 2, 'If 5 apples cost $10, how much do 3 apples cost?',
 '["Find cost per apple: $10 ÷ 5 = $2 per apple", "Multiply by quantity: $2 × 3 = $6"]',
 '$6',
 'Find the unit rate first, then multiply by the desired quantity'),

('algebra', 3, 'Solve for x: 2x + 5 = 15',
 '["Subtract 5 from both sides: 2x = 10", "Divide both sides by 2: x = 5", "Verify: 2(5) + 5 = 15 ✓"]',
 'x = 5',
 'Use inverse operations to isolate the variable');
```

---

## Project Structure
```
intelligent-llm-orchestration/
├── cmd/
│   ├── server/
│   │   └── main.go                 # Main application entry point
│   └── migrate/
│       └── main.go                 # Database migration tool
├── internal/
│   ├── config/
│   │   └── config.go               # Configuration management
│   ├── database/
│   │   ├── db.go                   # Database connection
│   │   ├── migrations/             # SQL migration files
│   │   │   ├── 001_initial_schema.up.sql
│   │   │   ├── 001_initial_schema.down.sql
│   │   │   └── ...
│   │   └── seeds/                  # Seed data files
│   │       ├── 01_initial_patterns.sql
│   │       ├── 02_initial_entities.sql
│   │       ├── 03_initial_abbreviations.sql
│   │       └── 04_reasoning_examples.sql
│   ├── intelligence/               # Stage 1 (empty for now)
│   │   ├── pattern_matcher.go
│   │   ├── entity_extractor.go
│   │   ├── query_rewriter.go
│   │   ├── confidence_scorer.go
│   │   ├── semantic_cache.go
│   │   └── meta_brain.go
│   ├── models/                     # LLM model handling
│   │   └── model.go
│   ├── api/                        # API handlers
│   │   ├── ws_chat_handler.go     # Existing WebSocket handler
│   │   └── routes.go
│   └── jobs/                       # Scheduled jobs (Stage 6)
│       ├── pattern_mining.go
│       ├── cache_maintenance.go
│       └── temporal_maintenance.go
├── pkg/                            # Public packages
│   └── temporal/
│       └── confidence.go           # Temporal confidence logic
├── web/                            # Frontend assets (if any)
├── models/                         # LLM model files (.gguf)
│   ├── router-1.5b.gguf
│   ├── factual-1.5b.gguf
│   ├── reasoning-3b.gguf
│   └── ...
├── docs/                           # Documentation
│   ├── 00-PROJECT-OVERVIEW.md
│   ├── 01-STAGE-0-FOUNDATION.md
│   └── ...
├── scripts/
│   ├── setup_db.sh                 # Database setup script
│   └── download_models.sh          # Model download script
├── .gitignore
├── go.mod
├── go.sum
├── Makefile
└── README.md
```

---

## Implementation Tasks

### Task 1: Initialize Go Project
```bash
# Create project directory
mkdir intelligent-llm-orchestration
cd intelligent-llm-orchestration

# Initialize Go module
go mod init github.com/yourusername/intelligent-llm-orchestration

# Install dependencies
go get -u github.com/mattn/go-sqlite3
go get -u github.com/gorilla/websocket
go get -u github.com/gorilla/mux
go get -u github.com/rs/zerolog
go get -u github.com/joho/godotenv
```

**File:** `go.mod`
```go
module github.com/yourusername/intelligent-llm-orchestration

go 1.21

require (
    github.com/mattn/go-sqlite3 v1.14.18
    github.com/gorilla/websocket v1.5.1
    github.com/gorilla/mux v1.8.1
    github.com/rs/zerolog v1.31.0
    github.com/joho/godotenv v1.5.1
)
```

### Task 2: Database Connection

**File:** `internal/database/db.go`
```go
package database

import (
    "database/sql"
    "fmt"
    "os"
    "path/filepath"
    
    _ "github.com/mattn/go-sqlite3"
)

type DB struct {
    *sql.DB
}

func New(dbPath string) (*DB, error) {
    // Ensure directory exists
    dir := filepath.Dir(dbPath)
    if err := os.MkdirAll(dir, 0755); err != nil {
        return nil, fmt.Errorf("failed to create database directory: %w", err)
    }
    
    // Open database
    db, err := sql.Open("sqlite3", dbPath+"?_foreign_keys=on&_journal_mode=WAL")
    if err != nil {
        return nil, fmt.Errorf("failed to open database: %w", err)
    }
    
    // Test connection
    if err := db.Ping(); err != nil {
        return nil, fmt.Errorf("failed to ping database: %w", err)
    }
    
    // Set connection pool settings
    db.SetMaxOpenConns(25)
    db.SetMaxIdleConns(5)
    
    return &DB{db}, nil
}

func (db *DB) Close() error {
    return db.DB.Close()
}

// Health check
func (db *DB) Ping() error {
    return db.DB.Ping()
}
```

### Task 3: Migration System

**File:** `internal/database/migrate.go`
```go
package database

import (
    "database/sql"
    "fmt"
    "io/fs"
    "os"
    "path/filepath"
    "sort"
    "strings"
)

type Migration struct {
    Version int
    Name    string
    UpSQL   string
    DownSQL string
}

type Migrator struct {
    db             *DB
    migrationsPath string
}

func NewMigrator(db *DB, migrationsPath string) *Migrator {
    return &Migrator{
        db:             db,
        migrationsPath: migrationsPath,
    }
}

func (m *Migrator) createMigrationsTable() error {
    _, err := m.db.Exec(`
        CREATE TABLE IF NOT EXISTS schema_migrations
        (
version INTEGER PRIMARY KEY,
name TEXT NOT NULL,
applied_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
)
`)
return err
}

func (m *Migrator) getCurrentVersion() (int, error) {
var version int
err := m.db.QueryRow("SELECT COALESCE(MAX(version), 0) FROM schema_migrations").Scan(&version)
return version, err
}

func (m *Migrator) loadMigrations() ([]Migration, error) {
var migrations []Migration

err := filepath.Walk(m.migrationsPath, func(path string, info fs.FileInfo, err error) error {
    if err != nil {
        return err
    }
    
    if info.IsDir() {
        return nil
    }
    
    if !strings.HasSuffix(path, ".up.sql") {
        return nil
    }
    
    // Parse version from filename: 001_initial_schema.up.sql
    filename := filepath.Base(path)
    parts := strings.SplitN(filename, "_", 2)
    if len(parts) < 2 {
        return fmt.Errorf("invalid migration filename: %s", filename)
    }
    
    var version int
    _, err = fmt.Sscanf(parts[0], "%d", &version)
    if err != nil {
        return fmt.Errorf("failed to parse version from %s: %w", filename, err)
    }
    
    name := strings.TrimSuffix(parts[1], ".up.sql")
    
    // Read up migration
    upSQL, err := os.ReadFile(path)
    if err != nil {
        return fmt.Errorf("failed to read migration %s: %w", path, err)
    }
    
    // Read down migration (if exists)
    downPath := strings.Replace(path, ".up.sql", ".down.sql", 1)
    var downSQL []byte
    if _, err := os.Stat(downPath); err == nil {
        downSQL, err = os.ReadFile(downPath)
        if err != nil {
            return fmt.Errorf("failed to read down migration %s: %w", downPath, err)
        }
    }
    
    migrations = append(migrations, Migration{
        Version: version,
        Name:    name,
        UpSQL:   string(upSQL),
        DownSQL: string(downSQL),
    })
    
    return nil
})

if err != nil {
    return nil, err
}

// Sort by version
sort.Slice(migrations, func(i, j int) bool {
    return migrations[i].Version < migrations[j].Version
})

return migrations, nil

}

func (m *Migrator) Up() error {
// Create migrations table
if err := m.createMigrationsTable(); err != nil {
return fmt.Errorf("failed to create migrations table: %w", err)
}

// Get current version
currentVersion, err := m.getCurrentVersion()
if err != nil {
    return fmt.Errorf("failed to get current version: %w", err)
}

// Load migrations
migrations, err := m.loadMigrations()
if err != nil {
    return fmt.Errorf("failed to load migrations: %w", err)
}

// Apply pending migrations
for _, migration := range migrations {
    if migration.Version <= currentVersion {
        continue
    }
    
    fmt.Printf("Applying migration %d: %s\n", migration.Version, migration.Name)
    
    tx, err := m.db.Begin()
    if err != nil {
        return fmt.Errorf("failed to begin transaction: %w", err)
    }
    
    // Execute migration
    if _, err := tx.Exec(migration.UpSQL); err != nil {
        tx.Rollback()
        return fmt.Errorf("failed to execute migration %d: %w", migration.Version, err)
    }
    
    // Record migration
    if _, err := tx.Exec("INSERT INTO schema_migrations (version, name) VALUES (?, ?)", 
        migration.Version, migration.Name); err != nil {
        tx.Rollback()
        return fmt.Errorf("failed to record migration %d: %w", migration.Version, err)
    }
    
    if err := tx.Commit(); err != nil {
        return fmt.Errorf("failed to commit migration %d: %w", migration.Version, err)
    }
    
    fmt.Printf("✓ Applied migration %d\n", migration.Version)
}

fmt.Println("All migrations applied successfully")
return nil

}

func (m *Migrator) Down() error {
// Get current version
currentVersion, err := m.getCurrentVersion()
if err != nil {
return fmt.Errorf("failed to get current version: %w", err)
}

if currentVersion == 0 {
    fmt.Println("No migrations to rollback")
    return nil
}

// Load migrations
migrations, err := m.loadMigrations()
if err != nil {
    return fmt.Errorf("failed to load migrations: %w", err)
}

// Find migration to rollback
var migration *Migration
for i := range migrations {
    if migrations[i].Version == currentVersion {
        migration = &migrations[i]
        break
    }
}

if migration == nil {
    return fmt.Errorf("migration %d not found", currentVersion)
}

if migration.DownSQL == "" {
    return fmt.Errorf("migration %d has no down migration", currentVersion)
}

fmt.Printf("Rolling back migration %d: %s\n", migration.Version, migration.Name)

tx, err := m.db.Begin()
if err != nil {
    return fmt.Errorf("failed to begin transaction: %w", err)
}

// Execute down migration
if _, err := tx.Exec(migration.DownSQL); err != nil {
    tx.Rollback()
    return fmt.Errorf("failed to execute down migration %d: %w", migration.Version, err)
}

// Remove migration record
if _, err := tx.Exec("DELETE FROM schema_migrations WHERE version = ?", migration.Version); err != nil {
    tx.Rollback()
    return fmt.Errorf("failed to remove migration record %d: %w", migration.Version, err)
}

if err := tx.Commit(); err != nil {
    return fmt.Errorf("failed to commit rollback %d: %w", migration.Version, err)
}

fmt.Printf("✓ Rolled back migration %d\n", migration.Version)
return nil

}


### Task 4: Migration Command

**File:** `cmd/migrate/main.go`
```go
package main

import (
    "flag"
    "fmt"
    "log"
    "os"
    
    "github.com/yourusername/intelligent-llm-orchestration/internal/database"
)

func main() {
    var (
        dbPath         = flag.String("db", "./data/orchestration.db", "Database path")
        migrationsPath = flag.String("migrations", "./internal/database/migrations", "Migrations directory")
        action         = flag.String("action", "up", "Migration action: up or down")
    )
    flag.Parse()
    
    // Open database
    db, err := database.New(*dbPath)
    if err != nil {
        log.Fatalf("Failed to open database: %v", err)
    }
    defer db.Close()
    
    // Create migrator
    migrator := database.NewMigrator(db, *migrationsPath)
    
    // Run migration
    switch *action {
    case "up":
        if err := migrator.Up(); err != nil {
            log.Fatalf("Migration failed: %v", err)
        }
    case "down":
        if err := migrator.Down(); err != nil {
            log.Fatalf("Rollback failed: %v", err)
        }
    default:
        fmt.Fprintf(os.Stderr, "Unknown action: %s (use 'up' or 'down')\n", *action)
        os.Exit(1)
    }
}
```

### Task 5: Initial Migration File

**File:** `internal/database/migrations/001_initial_schema.up.sql`
```sql
-- This file contains all the CREATE TABLE statements from the schema section above
-- (Copy all table creation SQL from sections 1-12 above)

-- Also include all CREATE INDEX statements

-- Example (full content would be all tables):
CREATE TABLE entities (
    entity_id TEXT PRIMARY KEY,
    entity_type TEXT NOT NULL,
    name TEXT NOT NULL,
    aliases TEXT,
    description TEXT,
    created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    last_updated TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    last_verified TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    next_verification_due TIMESTAMP,
    base_confidence REAL DEFAULT 0.7,
    confidence_type TEXT DEFAULT 'slow_change',
    source TEXT,
    source_url TEXT,
    verified BOOLEAN DEFAULT FALSE
);

-- ... (all other tables)
```

**File:** `internal/database/migrations/001_initial_schema.down.sql`
```sql
-- Reverse migration - drop all tables in reverse order
DROP TABLE IF EXISTS learning_log;
DROP TABLE IF EXISTS pattern_discoveries;
DROP TABLE IF EXISTS tool_history;
DROP TABLE IF EXISTS query_log;
DROP TABLE IF EXISTS reasoning_examples;
DROP TABLE IF EXISTS learned_abbreviations;
DROP TABLE IF EXISTS semantic_cache;
DROP TABLE IF EXISTS faq_cache;
DROP TABLE IF EXISTS query_patterns;
DROP TABLE IF EXISTS entity_history;
DROP TABLE IF EXISTS entity_facts;
DROP TABLE IF EXISTS entities_fts;
DROP TABLE IF EXISTS entities;
DROP TABLE IF EXISTS schema_migrations;
```

### Task 6: Seed Data Script

**File:** `scripts/setup_db.sh`
```bash
#!/bin/bash

set -e

DB_PATH="${1:-./data/orchestration.db}"
MIGRATIONS_PATH="./internal/database/migrations"
SEEDS_PATH="./internal/database/seeds"

echo "Setting up database at: $DB_PATH"

# Run migrations
echo "Running migrations..."
go run cmd/migrate/main.go -db="$DB_PATH" -migrations="$MIGRATIONS_PATH" -action=up

# Load seed data
echo "Loading seed data..."
for seed_file in "$SEEDS_PATH"/*.sql; do
    if [ -f "$seed_file" ]; then
        echo "Loading: $(basename "$seed_file")"
        sqlite3 "$DB_PATH" < "$seed_file"
    fi
done

echo "✓ Database setup complete!"
echo "Database location: $DB_PATH"

# Show stats
echo ""
echo "Database statistics:"
sqlite3 "$DB_PATH" "SELECT 'Entities: ' || COUNT(*) FROM entities;"
sqlite3 "$DB_PATH" "SELECT 'Patterns: ' || COUNT(*) FROM query_patterns;"
sqlite3 "$DB_PATH" "SELECT 'Abbreviations: ' || COUNT(*) FROM learned_abbreviations;"
sqlite3 "$DB_PATH" "SELECT 'Reasoning Examples: ' || COUNT(*) FROM reasoning_examples;"
```

### Task 7: Makefile

**File:** `Makefile`
```makefile
.PHONY: help setup migrate-up migrate-down seed clean test run

DB_PATH=./data/orchestration.db

help:
	@echo "Available commands:"
	@echo "  make setup       - Initialize database with migrations and seeds"
	@echo "  make migrate-up  - Run database migrations"
	@echo "  make migrate-down- Rollback last migration"
	@echo "  make seed        - Load seed data"
	@echo "  make clean       - Remove database file"
	@echo "  make test        - Run tests"
	@echo "  make run         - Run the server"

setup:
	@echo "Setting up database..."
	@./scripts/setup_db.sh $(DB_PATH)

migrate-up:
	@go run cmd/migrate/main.go -db=$(DB_PATH) -action=up

migrate-down:
	@go run cmd/migrate/main.go -db=$(DB_PATH) -action=down

seed:
	@echo "Loading seed data..."
	@for file in internal/database/seeds/*.sql; do \
		echo "Loading: $$file"; \
		sqlite3 $(DB_PATH) < $$file; \
	done

clean:
	@echo "Removing database..."
	@rm -f $(DB_PATH)
	@rm -f $(DB_PATH)-shm
	@rm -f $(DB_PATH)-wal
	@echo "✓ Database removed"

test:
	@go test -v ./...

run:
	@go run cmd/server/main.go
```

### Task 8: Configuration

**File:** `internal/config/config.go`
```go
package config

import (
    "fmt"
    "os"
    "time"
    
    "github.com/joho/godotenv"
)

type Config struct {
    // Database
    DatabasePath string
    
    // Server
    ServerPort int
    
    // Intelligence
    EnableSemanticCache  bool
    EnablePatternMining  bool
    CacheExpiryHours     int
    MaxLatencyMs         int
    
    // Models
    ModelsPath string
    
    // Logging
    LogLevel string
}

func Load() (*Config, error) {
    // Load .env file if exists
    godotenv.Load()
    
    config := &Config{
        DatabasePath:        getEnv("DATABASE_PATH", "./data/orchestration.db"),
        ServerPort:          getEnvInt("SERVER_PORT", 8080),
        EnableSemanticCache: getEnvBool("ENABLE_SEMANTIC_CACHE", false),
        EnablePatternMining: getEnvBool("ENABLE_PATTERN_MINING", true),
        CacheExpiryHours:    getEnvInt("CACHE_EXPIRY_HOURS", 24),
        MaxLatencyMs:        getEnvInt("MAX_LATENCY_MS", 3000),
        ModelsPath:          getEnv("MODELS_PATH", "./models"),
        LogLevel:            getEnv("LOG_LEVEL", "info"),
    }
    
    return config, nil
}

func getEnv(key, defaultValue string) string {
    if value := os.Getenv(key); value != "" {
        return value
    }
    return defaultValue
}

func getEnvInt(key string, defaultValue int) int {
    if value := os.Getenv(key); value != "" {
        var intValue int
        if _, err := fmt.Sscanf(value, "%d", &intValue); err == nil {
            return intValue
        }
    }
    return defaultValue
}

func getEnvBool(key string, defaultValue bool) bool {
    if value := os.Getenv(key); value != "" {
        return value == "true" || value == "1" || value == "yes"
    }
    return defaultValue
}
```

**File:** `.env.example`
```env
# Database
DATABASE_PATH=./data/orchestration.db

# Server
SERVER_PORT=8080

# Intelligence
ENABLE_SEMANTIC_CACHE=false
ENABLE_PATTERN_MINING=true
CACHE_EXPIRY_HOURS=24
MAX_LATENCY_MS=3000

# Models
MODELS_PATH=./models

# Logging
LOG_LEVEL=info
```

---

## Testing Checklist

### Database Tests
```bash
# Create database
make setup

# Verify tables exist
sqlite3 data/orchestration.db ".tables"

# Check entity count
sqlite3 data/orchestration.db "SELECT COUNT(*) FROM entities;"

# Check pattern count
sqlite3 data/orchestration.db "SELECT COUNT(*) FROM query_patterns;"

# Test FTS search
sqlite3 data/orchestration.db "SELECT name FROM entities_fts WHERE entities_fts MATCH 'kingdom';"

# Test foreign keys
sqlite3 data/orchestration.db "PRAGMA foreign_keys;"

# Check indexes
sqlite3 data/orchestration.db ".schema entities" | grep INDEX
```

### Migration Tests
```bash
# Test up migration
make migrate-up

# Test down migration
make migrate-down

# Test up again
make migrate-up
```

### Connection Tests
```go
// internal/database/db_test.go
package database_test

import (
    "testing"
    "os"
    
    "github.com/yourusername/intelligent-llm-orchestration/internal/database"
)

func TestDatabaseConnection(t *testing.T) {
    dbPath := "./test_orchestration.db"
    defer os.Remove(dbPath)
    
    db, err := database.New(dbPath)
    if err != nil {
        t.Fatalf("Failed to create database: %v", err)
    }
    defer db.Close()
    
    if err := db.Ping(); err != nil {
        t.Fatalf("Failed to ping database: %v", err)
    }
}

func TestMigrations(t *testing.T) {
    dbPath := "./test_orchestration.db"
    defer os.Remove(dbPath)
    
    db, err := database.New(dbPath)
    if err != nil {
        t.Fatalf("Failed to create database: %v", err)
    }
    defer db.Close()
    
    migrator := database.NewMigrator(db, "../../internal/database/migrations")
    
    if err := migrator.Up(); err != nil {
        t.Fatalf("Failed to run migrations: %v", err)
    }
    
    // Verify tables exist
    var count int
    err = db.QueryRow("SELECT COUNT(*) FROM sqlite_master WHERE type='table'").Scan(&count)
    if err != nil {
        t.Fatalf("Failed to count tables: %v", err)
    }
    
    if count < 10 {
        t.Errorf("Expected at least 10 tables, got %d", count)
    }
}
```

---

## Success Criteria

- [ ] All database tables created successfully
- [ ] All indexes created
- [ ] FTS5 tables operational
- [ ] Foreign key constraints working
- [ ] Migration system functional (up and down)
- [ ] Seed data loads without errors
- [ ] Project compiles with no errors
- [ ] All Stage 0 tests pass
- [ ] Database file size < 10MB after seeding
- [ ] Can query entities, patterns, and facts
- [ ] Documentation complete

---

## Estimated Time

- Database schema design: ✅ Complete
- Go project setup: 2 hours
- Migration system: 3 hours
- Seed data creation: 2 hours
- Testing: 2 hours
- Documentation: 1 hour

**Total: 10 hours (1-2 days)**

---

## Next Stage

Once Stage 0 is complete, proceed to **Stage 1: Core Intelligence Components** where we'll build:
- Pattern matcher
- Entity extractor
- Query rewriter
- Confidence scorer
- Temporal confidence framework

---

**Last Updated:** [Current Date]
**Status:** Ready for Implementation

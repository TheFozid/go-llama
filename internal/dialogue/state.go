// internal/dialogue/state.go
package dialogue

import (
    "context"
    "encoding/json"
    "fmt"
    "time"

    "gorm.io/datatypes"
    "gorm.io/gorm"
)

// DialogueState represents the persistent internal state (singleton)
type DialogueState struct {
    ID                 int            `gorm:"primaryKey" json:"id"`
    ActiveMission      datatypes.JSON `gorm:"type:jsonb" json:"active_mission"`
    QueuedMissions     datatypes.JSON `gorm:"type:jsonb;not null;default:'[]'" json:"queued_missions"`
    CompletedMissions  datatypes.JSON `gorm:"type:jsonb;not null;default:'[]'" json:"completed_missions"`
    RecentFailures     datatypes.JSON `gorm:"type:jsonb;not null;default:'[]'" json:"recent_failures"`
    Patterns           datatypes.JSON `gorm:"type:jsonb;not null;default:'[]'" json:"patterns"`
    LastCycleTime      time.Time      `gorm:"not null;default:NOW()" json:"last_cycle_time"`
    CycleCount         int            `gorm:"not null;default:0" json:"cycle_count"`
    CreatedAt          time.Time      `json:"created_at"`
    UpdatedAt          time.Time      `json:"updated_at"`
}

// TableName specifies the table name for GORM
func (DialogueState) TableName() string {
    return "growerai_dialogue_state"
}

// DialogueMetrics tracks performance of each cycle
type DialogueMetrics struct {
    CycleID        int       `gorm:"primaryKey;autoIncrement" json:"cycle_id"`
    StartTime      time.Time `gorm:"not null" json:"start_time"`
    EndTime        time.Time `gorm:"not null" json:"end_time"`
    DurationMs     int       `gorm:"not null" json:"duration_ms"`
    ThoughtCount   int       `gorm:"not null;default:0" json:"thought_count"`
    ActionCount    int       `gorm:"not null;default:0" json:"action_count"`
    TokensUsed     int       `gorm:"not null;default:0" json:"tokens_used"`
    GoalsCreated   int       `gorm:"not null;default:0" json:"goals_created"`
    GoalsCompleted int       `gorm:"not null;default:0" json:"goals_completed"`
    MemoriesStored int       `gorm:"not null;default:0" json:"memories_stored"`
    StopReason     string    `gorm:"type:varchar(50);not null" json:"stop_reason"`
    CreatedAt      time.Time `json:"created_at"`
}

// TableName specifies the table name for GORM
func (DialogueMetrics) TableName() string {
    return "growerai_dialogue_metrics"
}

// DialogueThought records individual thoughts during cycles
type DialogueThought struct {
    ID          int       `gorm:"primaryKey;autoIncrement" json:"id"`
    CycleID     int       `gorm:"not null;index:idx_cycle_thought" json:"cycle_id"`
    ThoughtNum  int       `gorm:"not null;index:idx_cycle_thought" json:"thought_num"`
    Content     string    `gorm:"type:text;not null" json:"content"`
    TokensUsed  int       `gorm:"not null;default:0" json:"tokens_used"`
    ActionTaken bool      `gorm:"not null;default:false" json:"action_taken"`
    Timestamp   time.Time `gorm:"not null;default:NOW()" json:"timestamp"`
}

// TableName specifies the table name for GORM
func (DialogueThought) TableName() string {
    return "growerai_dialogue_thoughts"
}

// StateManager handles loading and saving internal state
type StateManager struct {
    db *gorm.DB
}

// NewStateManager creates a new state manager
func NewStateManager(db *gorm.DB) *StateManager {
    return &StateManager{db: db}
}

// LoadState retrieves the current internal state from database
func (sm *StateManager) LoadState(ctx context.Context) (*InternalState, error) {
    var dbState DialogueState
    
    // Get or create the singleton state record
    if err := sm.db.WithContext(ctx).FirstOrCreate(&dbState, DialogueState{ID: 1}).Error; err != nil {
        return nil, fmt.Errorf("failed to load dialogue state: %w", err)
    }

    // Unmarshal JSONB fields into InternalState
    state := &InternalState{
        LastCycleTime: dbState.LastCycleTime,
        CycleCount:    dbState.CycleCount,
    }

    // Unmarshal Active Mission
    if dbState.ActiveMission != nil && len(dbState.ActiveMission) > 0 {
        var m Mission
        if err := json.Unmarshal(dbState.ActiveMission, &m); err == nil {
            state.ActiveMission = &m
        }
    }

    // Unmarshal Queued Missions
    if err := json.Unmarshal(dbState.QueuedMissions, &state.QueuedMissions); err != nil {
        state.QueuedMissions = []Mission{}
    }

    // Unmarshal Completed Missions
    if err := json.Unmarshal(dbState.CompletedMissions, &state.CompletedMissions); err != nil {
        state.CompletedMissions = []Mission{}
    }

    if err := json.Unmarshal(dbState.RecentFailures, &state.RecentFailures); err != nil {
        state.RecentFailures = []string{}
    }
    
    if err := json.Unmarshal(dbState.Patterns, &state.Patterns); err != nil {
        state.Patterns = []string{}
    }

    return state, nil
}

// SaveState persists the internal state to database
func (sm *StateManager) SaveState(ctx context.Context, state *InternalState) error {
    recentFailures, _ := json.Marshal(state.RecentFailures)
    patterns, _ := json.Marshal(state.Patterns)
    activeMission, _ := json.Marshal(state.ActiveMission)
    queuedMissions, _ := json.Marshal(state.QueuedMissions)
    completedMissions, _ := json.Marshal(state.CompletedMissions)
    
    // Update the singleton record
    updates := map[string]interface{}{
        "active_mission":     datatypes.JSON(activeMission),
        "queued_missions":    datatypes.JSON(queuedMissions),
        "completed_missions": datatypes.JSON(completedMissions),
        "recent_failures":    datatypes.JSON(recentFailures),
        "patterns":           datatypes.JSON(patterns),
        "last_cycle_time":    state.LastCycleTime,
        "cycle_count":        state.CycleCount,
        "updated_at":         time.Now(),
    }

    if err := sm.db.WithContext(ctx).Model(&DialogueState{}).Where("id = ?", 1).Updates(updates).Error; err != nil {
        return fmt.Errorf("failed to save dialogue state: %w", err)
    }

    return nil
}

// SaveMetrics stores cycle performance metrics
func (sm *StateManager) SaveMetrics(ctx context.Context, metrics *CycleMetrics) error {
    dbMetrics := DialogueMetrics{
        StartTime:      metrics.StartTime,
        EndTime:        metrics.EndTime,
        DurationMs:     int(metrics.Duration.Milliseconds()),
        ThoughtCount:   metrics.ThoughtCount,
        ActionCount:    metrics.ActionCount,
        TokensUsed:     metrics.TokensUsed,
        GoalsCreated:   metrics.GoalsCreated,
        GoalsCompleted: metrics.GoalsCompleted,
        MemoriesStored: metrics.MemoriesStored,
        StopReason:     metrics.StopReason,
    }

    if err := sm.db.WithContext(ctx).Create(&dbMetrics).Error; err != nil {
        return fmt.Errorf("failed to save metrics: %w", err)
    }

    return nil
}

// SaveThought stores a thought record
func (sm *StateManager) SaveThought(ctx context.Context, thought *ThoughtRecord) error {
    dbThought := DialogueThought{
        CycleID:     thought.CycleID,
        ThoughtNum:  thought.ThoughtNum,
        Content:     thought.Content,
        TokensUsed:  thought.TokensUsed,
        ActionTaken: thought.ActionTaken,
        Timestamp:   thought.Timestamp,
    }

    if err := sm.db.WithContext(ctx).Create(&dbThought).Error; err != nil {
        return fmt.Errorf("failed to save thought: %w", err)
    }

    return nil
}

// InitializeDefaultState ensures the singleton state record exists
func InitializeDefaultState(db *gorm.DB) error {
    // Use FirstOrCreate to ensure singleton exists (will create if missing)
    defaultState := DialogueState{
        ID:              1,
        QueuedMissions:  datatypes.JSON([]byte("[]")),
        CompletedMissions: datatypes.JSON([]byte("[]")),
        RecentFailures:  datatypes.JSON([]byte("[]")),
        Patterns:        datatypes.JSON([]byte("[]")),
        LastCycleTime:   time.Now(),
        CycleCount:      0,
    }

    if err := db.Where(DialogueState{ID: 1}).FirstOrCreate(&defaultState).Error; err != nil {
        return fmt.Errorf("failed to initialize default dialogue state: %w", err)
    }

    return nil
}

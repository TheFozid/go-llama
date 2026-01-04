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
	ID             int            `gorm:"primaryKey;default:1" json:"id"`
	ActiveGoals    datatypes.JSON `gorm:"type:jsonb;not null;default:'[]'" json:"active_goals"`
	CompletedGoals datatypes.JSON `gorm:"type:jsonb;not null;default:'[]'" json:"completed_goals"`
	KnowledgeGaps  datatypes.JSON `gorm:"type:jsonb;not null;default:'[]'" json:"knowledge_gaps"`
	RecentFailures datatypes.JSON `gorm:"type:jsonb;not null;default:'[]'" json:"recent_failures"`
	Patterns       datatypes.JSON `gorm:"type:jsonb;not null;default:'[]'" json:"patterns"`
	LastCycleTime  time.Time      `gorm:"not null;default:NOW()" json:"last_cycle_time"`
	CycleCount     int            `gorm:"not null;default:0" json:"cycle_count"`
	CreatedAt      time.Time      `json:"created_at"`
	UpdatedAt      time.Time      `json:"updated_at"`
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

	if err := json.Unmarshal(dbState.ActiveGoals, &state.ActiveGoals); err != nil {
		state.ActiveGoals = []Goal{}
	}
	if err := json.Unmarshal(dbState.CompletedGoals, &state.CompletedGoals); err != nil {
		state.CompletedGoals = []Goal{}
	}
	if err := json.Unmarshal(dbState.KnowledgeGaps, &state.KnowledgeGaps); err != nil {
		state.KnowledgeGaps = []string{}
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
	// Marshal InternalState fields to JSON
	activeGoals, _ := json.Marshal(state.ActiveGoals)
	completedGoals, _ := json.Marshal(state.CompletedGoals)
	knowledgeGaps, _ := json.Marshal(state.KnowledgeGaps)
	recentFailures, _ := json.Marshal(state.RecentFailures)
	patterns, _ := json.Marshal(state.Patterns)

	// Update the singleton record
	updates := map[string]interface{}{
		"active_goals":    datatypes.JSON(activeGoals),
		"completed_goals": datatypes.JSON(completedGoals),
		"knowledge_gaps":  datatypes.JSON(knowledgeGaps),
		"recent_failures": datatypes.JSON(recentFailures),
		"patterns":        datatypes.JSON(patterns),
		"last_cycle_time": state.LastCycleTime,
		"cycle_count":     state.CycleCount,
		"updated_at":      time.Now(),
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
	var count int64
	if err := db.Model(&DialogueState{}).Count(&count).Error; err != nil {
		return fmt.Errorf("failed to count dialogue state: %w", err)
	}

	if count > 0 {
		return nil // Already initialized
	}

	// Create default empty state
	defaultState := DialogueState{
		ID:             1,
		ActiveGoals:    datatypes.JSON([]byte("[]")),
		CompletedGoals: datatypes.JSON([]byte("[]")),
		KnowledgeGaps:  datatypes.JSON([]byte("[]")),
		RecentFailures: datatypes.JSON([]byte("[]")),
		Patterns:       datatypes.JSON([]byte("[]")),
		LastCycleTime:  time.Now(),
		CycleCount:     0,
	}

	if err := db.Create(&defaultState).Error; err != nil {
		return fmt.Errorf("failed to create default dialogue state: %w", err)
	}

	return nil
}

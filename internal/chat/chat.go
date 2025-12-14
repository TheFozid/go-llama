package chat

import (
	"time"
	"gorm.io/gorm"
)

type Chat struct {
	ID           uint           `json:"id" gorm:"primaryKey"`
	Title        string         `json:"title"`
	UserID       uint           `json:"user_id"`
	ModelName    string         `json:"model_name"`     // LLM model assigned to this chat
	LlmSessionID string         `json:"llm_session_id"` // LLM session token/id for context
    UseGrowerAI  bool			`json:"use_grower_ai" gorm:"default:false"`
	CreatedAt    time.Time      `json:"createdAt"`
	UpdatedAt    time.Time      `json:"updatedAt"`
	DeletedAt    gorm.DeletedAt `json:"-" gorm:"index"`
	Messages     []Message      `json:"-" gorm:"foreignKey:ChatID"`
}

type Message struct {
	ID        uint           `json:"id" gorm:"primaryKey"`
	ChatID    uint           `json:"chat_id"`
	Sender    string         `json:"sender"`   // "user" or "bot"
	Content   string         `json:"content"`
	CreatedAt time.Time      `json:"createdAt"`
	DeletedAt gorm.DeletedAt `json:"-" gorm:"index"`
}

// Add a trivial method so coverage can be measured
func (c *Chat) DisplayTitle() string {
	return c.Title
}
// Sliding window for context limitation
func BuildSlidingWindow(messages []Message, contextSize int) []Message {
	maxChars := int(float64(contextSize) * 0.85) * 4 // Use 85% of context, 4 chars/token
	var window []Message
	totalChars := 0

	// Start from the end (latest message), prepend to window
	for i := len(messages) - 1; i >= 0; i-- {
		m := messages[i]
		msgLen := len(m.Content)
		if totalChars+msgLen > maxChars {
			break
		}
		window = append([]Message{m}, window...)
		totalChars += msgLen
	}
	return window
}

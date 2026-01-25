package dialogue

import (
    "math/rand"
    "strings"
    "time"
)

// extractGoalTopic extracts the topic from a goal content string based on a trigger phrase
func extractGoalTopic(content string, triggerPhrase string) string {
    // Find the trigger phrase
    idx := strings.Index(content, triggerPhrase)
    if idx == -1 {
        return ""
    }
    
    // Get text after the trigger phrase
    afterPhrase := content[idx+len(triggerPhrase):]
    
    // Take up to the next period, newline, or 200 characters
    var topic string
    for i, char := range afterPhrase {
        if char == '.' || char == '\n' || i > 200 {
            topic = afterPhrase[:i]
            break
        }
    }
    
    if topic == "" {
        topic = afterPhrase
    }
    
    // Clean up
    topic = strings.TrimSpace(topic)
    topic = strings.Trim(topic, ".,!?")
    
    // If it starts with "to ", remove it
    topic = strings.TrimPrefix(topic, "to ")
    
    return topic
}

// cosineSimilarity calculates the cosine similarity between two float32 vectors
func cosineSimilarity(a, b []float32) float64 {
    if len(a) != len(b) {
        return 0.0
    }
    
    var dotProduct, normA, normB float64
    
    for i := 0; i < len(a); i++ {
        dotProduct += float64(a[i]) * float64(b[i])
        normA += float64(a[i]) * float64(a[i])
        normB += float64(b[i]) * float64(b[i])
    }
    
    if normA == 0 || normB == 0 {
        return 0.0
    }
    
    return dotProduct / (sqrt(normA) * sqrt(normB))
}

// sqrt is a simple square root helper
func sqrt(x float64) float64 {
    if x < 0 {
        return 0
    }
    // Use Newton's method for square root
    if x == 0 {
        return 0
    }
    z := x
    for i := 0; i < 10; i++ {
        z = (z + x/z) / 2
    }
    return z
}

// min returns the minimum of two integers
func min(a, b int) int {
    if a < b {
        return a
    }
    return b
}

// sortGoalsByPriority sorts a slice of goals by Priority in descending order
func sortGoalsByPriority(goals []Goal) []Goal {
    // Simple bubble sort by priority (descending)
    sorted := make([]Goal, len(goals))
    copy(sorted, goals)
    
    for i := 0; i < len(sorted); i++ {
        for j := i + 1; j < len(sorted); j++ {
            if sorted[j].Priority > sorted[i].Priority {
                sorted[i], sorted[j] = sorted[j], sorted[i]
            }
        }
    }
    
    return sorted
}

// truncate truncates a string to maxLen and appends "..."
func truncate(s string, maxLen int) string {
    if len(s) <= maxLen {
        return s
    }
    return s[:maxLen] + "..."
}

// hasPendingActions checks if a goal has any pending actions
func hasPendingActions(goal *Goal) bool {
    for _, action := range goal.Actions {
        if action.Status == ActionStatusPending {
            return true
        }
    }
    return false
}

// generateJitter creates a random time duration within a +/- window of minutes
func generateJitter(windowMinutes int) time.Duration {
    if windowMinutes <= 0 {
        return 0
    }
    
    // Random value between -windowMinutes and +windowMinutes
    jitterMinutes := rand.Intn(windowMinutes*2+1) - windowMinutes
    return time.Duration(jitterMinutes) * time.Minute
}

// truncateResponse truncates a string for logging
func truncateResponse(s string, maxLen int) string {
    if len(s) <= maxLen {
        return s
    }
    return s[:maxLen] + "... (truncated)"
}

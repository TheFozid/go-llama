package dialogue

import (
	"context"
	"fmt"
	"log"
	"math/rand"
	"strings"
	"time"
	
	"go-llama/internal/memory"
)

// UserProfile represents learned patterns about the user
type UserProfile struct {
	TopTopics          []string          // Most frequent topics user discusses
	PreferredStyle     string            // Communication style preference
	ActiveHours        []int             // Hours user is typically active (0-23)
	InteractionRate    float64           // Average messages per session
	TechnicalLevel     float64           // 0.0-1.0, how technical user is
	TopicPreferences   map[string]float64 // Topic -> interest score
	LastUpdated        time.Time
}

// BuildUserProfile analyzes user memories to create a profile
func (e *Engine) BuildUserProfile(ctx context.Context) (*UserProfile, error) {
	// Search for user's personal memories
	embedding, err := e.embedder.Embed(ctx, "user questions discussions topics interests")
	if err != nil {
		return nil, err
	}
	
	query := memory.RetrievalQuery{
		Limit:             50, // Analyze more memories for better profile
		MinScore:          0.2, // Lower threshold for broad coverage
		IncludePersonal:   true,
		IncludeCollective: false, // Only user interactions
	}
	
	results, err := e.storage.Search(ctx, query, embedding)
	if err != nil {
		return nil, err
	}
	
	if len(results) == 0 {
		log.Printf("[UserProfile] No user memories found, cannot build profile")
		return &UserProfile{
			TopTopics:       []string{},
			PreferredStyle:  "neutral",
			ActiveHours:     []int{},
			InteractionRate: 0,
			TechnicalLevel:  0.5,
			TopicPreferences: make(map[string]float64),
			LastUpdated:     time.Now(),
		}, nil
	}
	
	log.Printf("[UserProfile] Building profile from %d user memories", len(results))
	
	// Analyze topics from concept tags
	topicFrequency := make(map[string]int)
	topicImportance := make(map[string]float64)
	
	for _, result := range results {
		for _, tag := range result.Memory.ConceptTags {
			topicFrequency[tag]++
			topicImportance[tag] += result.Memory.ImportanceScore
		}
	}
	
	// Calculate normalized topic preferences
	topicPreferences := make(map[string]float64)
	for topic, freq := range topicFrequency {
		// Combine frequency and importance
		avgImportance := topicImportance[topic] / float64(freq)
		score := (float64(freq) / float64(len(results))) * avgImportance
		topicPreferences[topic] = score
	}
	
	// Extract top topics
	type topicScore struct {
		topic string
		score float64
	}
	var topics []topicScore
	for topic, score := range topicPreferences {
		topics = append(topics, topicScore{topic, score})
	}
	
	// Sort by score
	for i := 0; i < len(topics); i++ {
		for j := i + 1; j < len(topics); j++ {
			if topics[j].score > topics[i].score {
				topics[i], topics[j] = topics[j], topics[i]
			}
		}
	}
	
	// Get top 10 topics
	topTopics := []string{}
	for i := 0; i < len(topics) && i < 10; i++ {
		topTopics = append(topTopics, topics[i].topic)
	}
	
	// Determine technical level
	technicalLevel := 0.5 // Default neutral
	technicalIndicators := []string{
		"code", "programming", "algorithm", "function", "api",
		"database", "server", "compile", "debug", "syntax",
	}
	
	technicalCount := 0
	for _, result := range results {
		contentLower := strings.ToLower(result.Memory.Content)
		for _, indicator := range technicalIndicators {
			if strings.Contains(contentLower, indicator) {
				technicalCount++
				break
			}
		}
	}
	
	technicalLevel = float64(technicalCount) / float64(len(results))
	if technicalLevel > 1.0 {
		technicalLevel = 1.0
	}
	
	// Analyze active hours from timestamps
	activeHours := make(map[int]int)
	for _, result := range results {
		hour := result.Memory.CreatedAt.Hour()
		activeHours[hour]++
	}
	
	// Get top 3 active hours
	type hourCount struct {
		hour  int
		count int
	}
	var hours []hourCount
	for hour, count := range activeHours {
		hours = append(hours, hourCount{hour, count})
	}
	
	for i := 0; i < len(hours); i++ {
		for j := i + 1; j < len(hours); j++ {
			if hours[j].count > hours[i].count {
				hours[i], hours[j] = hours[j], hours[i]
			}
		}
	}
	
	topActiveHours := []int{}
	for i := 0; i < len(hours) && i < 3; i++ {
		topActiveHours = append(topActiveHours, hours[i].hour)
	}
	
	// Determine preferred style from memory content
	preferredStyle := "neutral"
	formalIndicators := []string{"please", "thank you", "would you", "could you"}
	casualIndicators := []string{"hey", "yeah", "gonna", "wanna", "cool"}
	
	formalCount := 0
	casualCount := 0
	
	for _, result := range results {
		contentLower := strings.ToLower(result.Memory.Content)
		for _, indicator := range formalIndicators {
			if strings.Contains(contentLower, indicator) {
				formalCount++
			}
		}
		for _, indicator := range casualIndicators {
			if strings.Contains(contentLower, indicator) {
				casualCount++
			}
		}
	}
	
	if formalCount > casualCount*2 {
		preferredStyle = "formal"
	} else if casualCount > formalCount*2 {
		preferredStyle = "casual"
	}
	
	profile := &UserProfile{
		TopTopics:        topTopics,
		PreferredStyle:   preferredStyle,
		ActiveHours:      topActiveHours,
		InteractionRate:  float64(len(results)) / 10.0, // Rough estimate
		TechnicalLevel:   technicalLevel,
		TopicPreferences: topicPreferences,
		LastUpdated:      time.Now(),
	}
	
	log.Printf("[UserProfile] Profile built: top_topics=%v, style=%s, technical_level=%.2f",
		topTopics, preferredStyle, technicalLevel)
	
	return profile, nil
}

// GenerateUserAlignedGoal creates a goal based on user profile
func (e *Engine) GenerateUserAlignedGoal(ctx context.Context, profile *UserProfile, avoidRecent []string) (Goal, error) {
	if len(profile.TopTopics) == 0 {
		return Goal{}, fmt.Errorf("no user topics available")
	}
	
	// Filter out recently explored topics
	availableTopics := []string{}
	for _, topic := range profile.TopTopics {
		isRecent := false
		for _, recent := range avoidRecent {
			if strings.Contains(strings.ToLower(recent), topic) {
				isRecent = true
				break
			}
		}
		if !isRecent {
			availableTopics = append(availableTopics, topic)
		}
	}
	
	if len(availableTopics) == 0 {
		// If all topics exhausted, use any topic
		availableTopics = profile.TopTopics
	}
	
	// Pick highest-interest available topic
	selectedTopic := availableTopics[0]
	
	// Generate goal variations based on technical level
	var description string
	if profile.TechnicalLevel > 0.7 {
		// Technical user - use technical framing
		variations := []string{
			fmt.Sprintf("Research advanced techniques in %s", selectedTopic),
			fmt.Sprintf("Investigate implementation patterns for %s", selectedTopic),
			fmt.Sprintf("Analyze performance optimization in %s", selectedTopic),
			fmt.Sprintf("Explore architectural approaches to %s", selectedTopic),
		}
		description = variations[rand.Intn(len(variations))]
	} else if profile.TechnicalLevel < 0.3 {
		// Non-technical user - use accessible framing
		variations := []string{
			fmt.Sprintf("Learn about practical uses of %s", selectedTopic),
			fmt.Sprintf("Understand the basics of %s", selectedTopic),
			fmt.Sprintf("Explore real-world examples of %s", selectedTopic),
			fmt.Sprintf("Discover how %s works in simple terms", selectedTopic),
		}
		description = variations[rand.Intn(len(variations))]
	} else {
		// Balanced user
		variations := []string{
			fmt.Sprintf("Research recent developments in %s", selectedTopic),
			fmt.Sprintf("Explore practical applications of %s", selectedTopic),
			fmt.Sprintf("Investigate current trends in %s", selectedTopic),
			fmt.Sprintf("Analyze key concepts in %s", selectedTopic),
		}
		description = variations[rand.Intn(len(variations))]
	}
	
	goal := Goal{
		ID:          fmt.Sprintf("goal_%d", time.Now().UnixNano()),
		Description: description,
		Source:      "user_interest", // New source type
		Priority:    8, // High priority - user is interested in this
		Created:     time.Now(),
		Progress:    0.0,
		Status:      GoalStatusActive,
		Actions:     []Action{},
	}
	
	log.Printf("[UserProfile] Generated user-aligned goal: %s (technical_level=%.2f)",
		truncate(description, 60), profile.TechnicalLevel)
	
	return goal, nil
}

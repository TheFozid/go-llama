package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"sync"
)

type LLMConfig struct {
	Name        string `json:"name"`
	URL         string `json:"url"`
	ContextSize int    `json:"context_size"`
}

type GrowerAIConfig struct {
	ReasoningModel struct {
		Name        string `json:"name"`
		URL         string `json:"url"`
		ContextSize int    `json:"context_size"`
	} `json:"reasoning_model"`
	EmbeddingModel struct {
		Name string `json:"name"`
		URL  string `json:"url"`
	} `json:"embedding_model"`
	Qdrant struct {
		URL        string `json:"url"`
		Collection string `json:"collection"`
		APIKey     string `json:"api_key"`
	} `json:"qdrant"`
	Compression struct {
		Enabled       bool `json:"enabled"`
		Model         struct {
			Name string `json:"name"`
			URL  string `json:"url"`
		} `json:"model"`
		ScheduleHours int `json:"schedule_hours"`
		TierRules     struct {
			RecentToMediumDays int `json:"recent_to_medium_days"`
			MediumToLongDays   int `json:"medium_to_long_days"`
			LongToAncientDays  int `json:"long_to_ancient_days"`
		} `json:"tier_rules"`
		ImportanceMod float64 `json:"importance_modifier"`
		AccessMod     float64 `json:"access_modifier"`
	} `json:"compression"`
}

type Config struct {
	Server struct {
		Host      string `json:"host"`
		Port      int    `json:"port"`
		Subpath   string `json:"subpath"`
		JWTSecret string `json:"jwtSecret"`
	} `json:"server"`
	Postgres struct {
		DSN string `json:"dsn"`
	} `json:"postgres"`
	Redis struct {
		Addr     string `json:"addr"`
		Password string `json:"password"`
		DB       int    `json:"db"`
	} `json:"redis"`
	LLMs     []LLMConfig    `json:"llms"`
	GrowerAI GrowerAIConfig `json:"growerai"`
	SearxNG  struct {
		URL        string `json:"url"`
		MaxResults int    `json:"max_results"`
	} `json:"searxng"`
}

var (
	once   sync.Once
	cfg    *Config
	cfgErr error
)

// LoadConfig reads config.json from disk (singleton)
func LoadConfig(path string) (*Config, error) {
	once.Do(func() {
		raw, err := os.ReadFile(path)
		if err != nil {
			cfgErr = fmt.Errorf("failed to read config file: %w", err)
			return
		}
		var c Config
		if err := json.Unmarshal(raw, &c); err != nil {
			cfgErr = fmt.Errorf("invalid config format: %w", err)
			return
		}
		// Minimal validation
		if c.Server.JWTSecret == "" {
			cfgErr = errors.New("jwtSecret must be set in config")
			return
		}
		cfg = &c
	})
	return cfg, cfgErr
}

// GetConfig returns the loaded config (must call LoadConfig first)
func GetConfig() *Config {
	return cfg
}

// ResetConfigForTest resets the singleton state (for testing only)
func ResetConfigForTest() {
	once = sync.Once{}
	cfg = nil
	cfgErr = nil
}

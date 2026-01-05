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
	
	// Storage limits and space-based compression
	StorageLimits struct {
		MaxTotalMemories   int     `json:"max_total_memories"`    // Total memory limit across all tiers
		TierAllocation     struct {
			Recent  float64 `json:"recent"`   // Percentage allocation for Recent tier (0.0-1.0)
			Medium  float64 `json:"medium"`   // Percentage allocation for Medium tier
			Long    float64 `json:"long"`     // Percentage allocation for Long tier
			Ancient float64 `json:"ancient"`  // Percentage allocation for Ancient tier
		} `json:"tier_allocation"`
		CompressionTrigger float64 `json:"compression_trigger"`   // Compress when tier hits this % of allocation (0.0-1.0)
		AllowTierOverflow  bool    `json:"allow_tier_overflow"`   // Allow tiers to borrow space from others
		CompressionWeights struct {
			Age        float64 `json:"age"`        // Weight for age in compression scoring (0.0-1.0)
			Importance float64 `json:"importance"` // Weight for importance in compression scoring
			Access     float64 `json:"access"`     // Weight for access frequency in compression scoring
		} `json:"compression_weights"`
	} `json:"storage_limits"`
	
	// Memory retrieval configuration
	Retrieval struct {
		MaxMemories       int     `json:"max_memories"`        // Max memories to retrieve per query
		MinScore          float64 `json:"min_score"`           // Minimum similarity score
		MaxLinkedMemories int     `json:"max_linked_memories"` // Max linked memories to traverse
	} `json:"retrieval"`
	
	// Tagging configuration
	Tagging struct {
		BatchSize int `json:"batch_size"` // Memories to tag per cycle
	} `json:"tagging"`
	
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
		// Phase 4: Merge windows for cluster-based compression
		MergeWindowRecent int `json:"merge_window_recent"` // Days
		MergeWindowMedium int `json:"merge_window_medium"` // Days
		MergeWindowLong   int `json:"merge_window_long"`   // Days
	} `json:"compression"`
	
	// Phase 4: Principles System (10 Commandments)
	Principles struct {
		AdminSlots             []int   `json:"admin_slots"`               // Slots 1-3: admin-controlled
		AIManagedSlots         []int   `json:"ai_managed_slots"`          // Slots 4-10: AI-managed
		EvolutionScheduleHours int     `json:"evolution_schedule_hours"`  // How often to evolve principles
		MinRatingThreshold     float64 `json:"min_rating_threshold"`      // Minimum rating to become a principle
		ExtractionLimit        int     `json:"extraction_limit"`          // Max memories to analyze for patterns
	} `json:"principles"`
	
	// Phase 4: Personality Control
	Personality struct {
		GoodBehaviorBias  float64 `json:"good_behavior_bias"`   // 0.0-1.0: prioritize good-tagged memories
		AllowDisagreement bool    `json:"allow_disagreement"`   // Can AI refuse/challenge requests?
		TrustLearningRate float64 `json:"trust_learning_rate"`  // How fast trust scores adjust (0.0-1.0)
	} `json:"personality"`
	
	// Phase 4: Memory Linking (Neural Network)
	Linking struct {
		SimilarityThreshold    float64 `json:"similarity_threshold"`     // Min similarity to create link (0.0-1.0)
		MaxLinksPerMemory      int     `json:"max_links_per_memory"`     // Limit graph size
		LinkDecayRate          float64 `json:"link_decay_rate"`          // How fast unused links weaken
		CoOccurrenceThrottle   int     `json:"co_occurrence_throttle"`   // Minutes between counting same co-occurrence
	} `json:"linking"`
	// Phase 3.1: Internal Dialogue System
	Dialogue struct {
		Enabled                   bool `json:"enabled"`
		BaseIntervalMinutes       int  `json:"base_interval_minutes"`
		JitterWindowMinutes       int  `json:"jitter_window_minutes"`
		MaxTokensPerCycle         int  `json:"max_tokens_per_cycle"`
		MaxDurationMinutes        int  `json:"max_duration_minutes"`
		MaxThoughtsPerCycle       int  `json:"max_thoughts_per_cycle"`
		ActionRequirementInterval int  `json:"action_requirement_interval"`
		NoveltyWindowHours        int  `json:"novelty_window_hours"`
		// Enhanced reasoning
		ReasoningDepth            string `json:"reasoning_depth"`             // "conservative", "moderate", "deep"
		EnableSelfAssessment      bool   `json:"enable_self_assessment"`      // Analyze strengths/weaknesses
		EnableMetaLearning        bool   `json:"enable_meta_learning"`        // Learn about learning strategies
		EnableStrategyTracking    bool   `json:"enable_strategy_tracking"`    // Track what works/doesn't
		StoreInsights             bool   `json:"store_insights"`              // Store learnings in memory
		DynamicActionPlanning     bool   `json:"dynamic_action_planning"`     // LLM generates action plans
	} `json:"dialogue"`
	
	// Phase 3.2: Tool Infrastructure
	Tools struct {
		SearXNG struct {
			Enabled               bool   `json:"enabled"`
			URL                   string `json:"url"`
			TimeoutInteractive    int    `json:"timeout_interactive"`    // seconds
			TimeoutIdle           int    `json:"timeout_idle"`           // seconds
			MaxResultsInteractive int    `json:"max_results_interactive"`
			MaxResultsIdle        int    `json:"max_results_idle"`
			SafeSearch            bool   `json:"safe_search"`
		} `json:"searxng"`
		WebParse struct {
			Enabled        bool   `json:"enabled"`
			MaxPageSizeMB  int    `json:"max_page_size_mb"`
			Timeout        int    `json:"timeout"` // seconds
			UserAgent      string `json:"user_agent"`
			ChunkSize      int    `json:"chunk_size"`
		} `json:"webparse"`
		Sandbox struct {
			Enabled        bool   `json:"enabled"`
			BaseImage      string `json:"base_image"`
			CPULimit       string `json:"cpu_limit"`
			MemoryLimit    string `json:"memory_limit"`
			StorageQuota   string `json:"storage_quota"`
			NetworkMode    string `json:"network_mode"`
			VPNContainer   string `json:"vpn_container"`
			WorkspacePath  string `json:"workspace_path"`
			LogLevel       string `json:"log_level"`
		} `json:"sandbox"`
	} `json:"tools"`
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
		
		// Apply defaults for Phase 4 settings if not provided
		applyGrowerAIDefaults(&c.GrowerAI)
		
		cfg = &c
	})
	return cfg, cfgErr
}

// applyGrowerAIDefaults sets sensible defaults for Phase 4 configuration
func applyGrowerAIDefaults(gai *GrowerAIConfig) {
	// Compression merge windows (temporal clustering for compression)
	if gai.Compression.MergeWindowRecent == 0 {
		gai.Compression.MergeWindowRecent = 3 // 3 days
	}
	if gai.Compression.MergeWindowMedium == 0 {
		gai.Compression.MergeWindowMedium = 7 // 7 days
	}
	if gai.Compression.MergeWindowLong == 0 {
		gai.Compression.MergeWindowLong = 30 // 30 days
	}
	// Note: TemporalResolution field removed - using full CreatedAt precision for all tiers
	
	// Principles system
	if len(gai.Principles.AdminSlots) == 0 {
		gai.Principles.AdminSlots = []int{1, 2, 3}
	}
	if len(gai.Principles.AIManagedSlots) == 0 {
		gai.Principles.AIManagedSlots = []int{4, 5, 6, 7, 8, 9, 10}
	}
	if gai.Principles.EvolutionScheduleHours == 0 {
		gai.Principles.EvolutionScheduleHours = 168 // 1 week
	}
	if gai.Principles.MinRatingThreshold == 0 {
		gai.Principles.MinRatingThreshold = 0.75
	}
	if gai.Principles.ExtractionLimit == 0 {
		gai.Principles.ExtractionLimit = 1000 // Analyze up to 1000 good memories
	}
	
	// Personality control
	if gai.Personality.GoodBehaviorBias == 0 {
		gai.Personality.GoodBehaviorBias = 0.60 // 60% good bias
	}
	if gai.Personality.TrustLearningRate == 0 {
		gai.Personality.TrustLearningRate = 0.05
	}
	// AllowDisagreement defaults to false (zero value)
	
	// Memory linking
	if gai.Linking.SimilarityThreshold == 0 {
		gai.Linking.SimilarityThreshold = 0.70
	}
	if gai.Linking.MaxLinksPerMemory == 0 {
		gai.Linking.MaxLinksPerMemory = 10
	}
	if gai.Linking.LinkDecayRate == 0 {
		gai.Linking.LinkDecayRate = 0.02
	}
	if gai.Linking.CoOccurrenceThrottle == 0 {
		gai.Linking.CoOccurrenceThrottle = 60 // 60 minutes (1 hour) default
	}

	// Dialogue system defaults (Phase 3.1)
	if gai.Dialogue.BaseIntervalMinutes == 0 {
		gai.Dialogue.BaseIntervalMinutes = 15
	}
	if gai.Dialogue.JitterWindowMinutes == 0 {
		gai.Dialogue.JitterWindowMinutes = 10
	}
	if gai.Dialogue.MaxTokensPerCycle == 0 {
		gai.Dialogue.MaxTokensPerCycle = 1000
	}
	if gai.Dialogue.MaxDurationMinutes == 0 {
		gai.Dialogue.MaxDurationMinutes = 10
	}
	if gai.Dialogue.MaxThoughtsPerCycle == 0 {
		gai.Dialogue.MaxThoughtsPerCycle = 20
	}
	if gai.Dialogue.ActionRequirementInterval == 0 {
		gai.Dialogue.ActionRequirementInterval = 5
	}
	if gai.Dialogue.NoveltyWindowHours == 0 {
		gai.Dialogue.NoveltyWindowHours = 2
	}
	// Enhanced reasoning defaults
	if gai.Dialogue.ReasoningDepth == "" {
		gai.Dialogue.ReasoningDepth = "conservative"
	}
	// Enable all enhanced features by default
	if !gai.Dialogue.EnableSelfAssessment {
		gai.Dialogue.EnableSelfAssessment = true
	}
	if !gai.Dialogue.EnableMetaLearning {
		gai.Dialogue.EnableMetaLearning = true
	}
	if !gai.Dialogue.EnableStrategyTracking {
		gai.Dialogue.EnableStrategyTracking = true
	}
	if !gai.Dialogue.StoreInsights {
		gai.Dialogue.StoreInsights = true
	}
	if !gai.Dialogue.DynamicActionPlanning {
		gai.Dialogue.DynamicActionPlanning = true
	}
	
	// Tools defaults (Phase 3.2)
	if gai.Tools.SearXNG.URL == "" {
		gai.Tools.SearXNG.URL = "http://searxng:8080/search"
	}
	if gai.Tools.SearXNG.TimeoutInteractive == 0 {
		gai.Tools.SearXNG.TimeoutInteractive = 5
	}
	if gai.Tools.SearXNG.TimeoutIdle == 0 {
		gai.Tools.SearXNG.TimeoutIdle = 2400
	}
	if gai.Tools.SearXNG.MaxResultsInteractive == 0 {
		gai.Tools.SearXNG.MaxResultsInteractive = 3
	}
	if gai.Tools.SearXNG.MaxResultsIdle == 0 {
		gai.Tools.SearXNG.MaxResultsIdle = 20
	}
	// SafeSearch defaults to false (zero value)
	
	// WebParse defaults (Phase 3.4)
	if gai.Tools.WebParse.MaxPageSizeMB == 0 {
		gai.Tools.WebParse.MaxPageSizeMB = 10
	}
	if gai.Tools.WebParse.Timeout == 0 {
		gai.Tools.WebParse.Timeout = 30
	}
	if gai.Tools.WebParse.UserAgent == "" {
		gai.Tools.WebParse.UserAgent = "GrowerAI/1.0"
	}
	if gai.Tools.WebParse.ChunkSize == 0 {
		gai.Tools.WebParse.ChunkSize = 4000
	}
	
	// Sandbox defaults (Phase 3.5)
	if gai.Tools.Sandbox.BaseImage == "" {
		gai.Tools.Sandbox.BaseImage = "alpine:latest"
	}
	if gai.Tools.Sandbox.CPULimit == "" {
		gai.Tools.Sandbox.CPULimit = "2.0"
	}
	if gai.Tools.Sandbox.MemoryLimit == "" {
		gai.Tools.Sandbox.MemoryLimit = "4G"
	}
	if gai.Tools.Sandbox.StorageQuota == "" {
		gai.Tools.Sandbox.StorageQuota = "20G"
	}
	if gai.Tools.Sandbox.NetworkMode == "" {
		gai.Tools.Sandbox.NetworkMode = "vpn"
	}
	if gai.Tools.Sandbox.VPNContainer == "" {
		gai.Tools.Sandbox.VPNContainer = "growerai-vpn"
	}
	if gai.Tools.Sandbox.WorkspacePath == "" {
		gai.Tools.Sandbox.WorkspacePath = "/workspace"
	}
	if gai.Tools.Sandbox.LogLevel == "" {
		gai.Tools.Sandbox.LogLevel = "info"
	}
	
	// Retrieval defaults
	if gai.Retrieval.MaxMemories == 0 {
		gai.Retrieval.MaxMemories = 5
	}
	if gai.Retrieval.MinScore == 0 {
		gai.Retrieval.MinScore = 0.3
	}
	if gai.Retrieval.MaxLinkedMemories == 0 {
		gai.Retrieval.MaxLinkedMemories = 5
	}
	
	// Tagging defaults
	if gai.Tagging.BatchSize == 0 {
		gai.Tagging.BatchSize = 100
	}
	
	// Storage limits defaults
	if gai.StorageLimits.MaxTotalMemories == 0 {
		gai.StorageLimits.MaxTotalMemories = 100000 // Default: 100K memories (~270 MB)
	}
	if gai.StorageLimits.TierAllocation.Recent == 0 {
		gai.StorageLimits.TierAllocation.Recent = 0.325
	}
	if gai.StorageLimits.TierAllocation.Medium == 0 {
		gai.StorageLimits.TierAllocation.Medium = 0.275
	}
	if gai.StorageLimits.TierAllocation.Long == 0 {
		gai.StorageLimits.TierAllocation.Long = 0.225
	}
	if gai.StorageLimits.TierAllocation.Ancient == 0 {
		gai.StorageLimits.TierAllocation.Ancient = 0.175
	}
	if gai.StorageLimits.CompressionTrigger == 0 {
		gai.StorageLimits.CompressionTrigger = 0.90
	}
	// AllowTierOverflow defaults to false (zero value)
	if gai.StorageLimits.CompressionWeights.Age == 0 {
		gai.StorageLimits.CompressionWeights.Age = 0.5
	}
	if gai.StorageLimits.CompressionWeights.Importance == 0 {
		gai.StorageLimits.CompressionWeights.Importance = 0.3
	}
	if gai.StorageLimits.CompressionWeights.Access == 0 {
		gai.StorageLimits.CompressionWeights.Access = 0.2
	}
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

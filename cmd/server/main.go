package main

import (
	"fmt"
	"log"
	"os"
	"time"

	"go-llama/internal/api"
	"go-llama/internal/config"
	"go-llama/internal/db"
	"go-llama/internal/dialogue"
	"go-llama/internal/memory"
	"go-llama/internal/tools"
	redisdb "go-llama/internal/redis"
)

func main() {
	cfg, err := config.LoadConfig("config.json")
	if err != nil {
		fmt.Fprintf(os.Stderr, "Config error: %v\n", err)
		os.Exit(1)
	}

	if err := db.Init(cfg); err != nil {
		fmt.Fprintf(os.Stderr, "DB init error: %v\n", err)
		os.Exit(1)
	}

	// Initialize GrowerAI principles (10 Commandments)
	log.Printf("[Main] Initializing GrowerAI principles...")
	if err := memory.InitializeDefaultPrinciples(db.DB); err != nil {
		log.Printf("[Main] WARNING: Failed to initialize principles: %v", err)
	} else {
		log.Printf("[Main] ✓ GrowerAI principles initialized")
	}

	// Initialize GrowerAI dialogue state (Phase 3.1)
	log.Printf("[Main] Initializing GrowerAI dialogue state...")
	if err := dialogue.InitializeDefaultState(db.DB); err != nil {
		log.Printf("[Main] WARNING: Failed to initialize dialogue state: %v", err)
	} else {
		log.Printf("[Main] ✓ GrowerAI dialogue state initialized")
	}

	rdb := redisdb.NewClient(cfg)

	// Initialize GrowerAI dialogue state (Phase 3.1)
	log.Printf("[Main] Initializing GrowerAI dialogue state...")
	if err := dialogue.InitializeDefaultState(db.DB); err != nil {
		log.Printf("[Main] WARNING: Failed to initialize dialogue state: %v", err)
	} else {
		log.Printf("[Main] ✓ GrowerAI dialogue state initialized")
	}

	// Start GrowerAI compression worker if enabled
	if cfg.GrowerAI.Compression.Enabled {
		log.Printf("[Main] Initializing GrowerAI compression worker...")

		storage, err := memory.NewStorage(
			cfg.GrowerAI.Qdrant.URL,
			cfg.GrowerAI.Qdrant.Collection,
			cfg.GrowerAI.Qdrant.APIKey,
		)
		if err != nil {
			log.Printf("[Main] WARNING: Failed to initialize storage for compression: %v", err)
		} else {
			embedder := memory.NewEmbedder(cfg.GrowerAI.EmbeddingModel.URL)

			// Initialize linker (Phase 4D)
			linker := memory.NewLinker(
				storage,
				cfg.GrowerAI.Linking.SimilarityThreshold,
				cfg.GrowerAI.Linking.MaxLinksPerMemory,
			)

			// Initialize compressor with embedder and linker (Phase 4D)
			compressor := memory.NewCompressor(
				cfg.GrowerAI.Compression.Model.URL,
				cfg.GrowerAI.Compression.Model.Name,
				embedder,
				linker,
			)

			// Initialize tagger (uses same LLM as compressor, needs embedder for recovery)
			tagger := memory.NewTagger(
				cfg.GrowerAI.Compression.Model.URL,
				cfg.GrowerAI.Compression.Model.Name,
				cfg.GrowerAI.Tagging.BatchSize,
				embedder, // Pass embedder for embedding regeneration
			)

			tierRules := memory.TierRules{
				RecentToMediumDays: cfg.GrowerAI.Compression.TierRules.RecentToMediumDays,
				MediumToLongDays:   cfg.GrowerAI.Compression.TierRules.MediumToLongDays,
				LongToAncientDays:  cfg.GrowerAI.Compression.TierRules.LongToAncientDays,
			}

			// Phase 4D: Merge windows for cluster-based compression
			mergeWindows := memory.MergeWindows{
				RecentDays: cfg.GrowerAI.Compression.MergeWindowRecent,
				MediumDays: cfg.GrowerAI.Compression.MergeWindowMedium,
				LongDays:   cfg.GrowerAI.Compression.MergeWindowLong,
			}

			// Prepare storage limits configuration
			storageLimits := memory.StorageLimits{
				MaxTotalMemories:   cfg.GrowerAI.StorageLimits.MaxTotalMemories,
				TierAllocation: memory.TierAllocation{
					Recent:  cfg.GrowerAI.StorageLimits.TierAllocation.Recent,
					Medium:  cfg.GrowerAI.StorageLimits.TierAllocation.Medium,
					Long:    cfg.GrowerAI.StorageLimits.TierAllocation.Long,
					Ancient: cfg.GrowerAI.StorageLimits.TierAllocation.Ancient,
				},
				CompressionTrigger: cfg.GrowerAI.StorageLimits.CompressionTrigger,
				AllowTierOverflow:  cfg.GrowerAI.StorageLimits.AllowTierOverflow,
			}
			
			// Prepare compression weights configuration
			compressionWeights := memory.CompressionWeights{
				Age:        cfg.GrowerAI.StorageLimits.CompressionWeights.Age,
				Importance: cfg.GrowerAI.StorageLimits.CompressionWeights.Importance,
				Access:     cfg.GrowerAI.StorageLimits.CompressionWeights.Access,
			}
			
			worker := memory.NewDecayWorker(
				storage,
				compressor,
				embedder,
				tagger,
				linker,                                          // Phase 4D: Add linker
				db.DB,                                           // Pass database connection for principles
				cfg.GrowerAI.Compression.Model.URL,              // LLM URL for principle generation
				cfg.GrowerAI.Compression.Model.Name,             // LLM model for principle generation
				cfg.GrowerAI.Compression.ScheduleHours,
				cfg.GrowerAI.Principles.EvolutionScheduleHours,  // Principle evolution schedule
				cfg.GrowerAI.Principles.MinRatingThreshold,      // Minimum rating for principles
				cfg.GrowerAI.Principles.ExtractionLimit,         // Max memories to analyze
				tierRules,                                       // DEPRECATED: kept for compatibility
				mergeWindows,                                    // Phase 4D: Add merge windows
				cfg.GrowerAI.Compression.ImportanceMod,          // DEPRECATED: kept for compatibility
				cfg.GrowerAI.Compression.AccessMod,              // DEPRECATED: kept for compatibility
				storageLimits,                                   // NEW: space-based compression config
				compressionWeights,                              // NEW: compression scoring weights
			)
			// Start worker in background goroutine
			go worker.Start()

			log.Printf("[Main] ✓ GrowerAI compression worker started (schedule: every %d hours)",
				cfg.GrowerAI.Compression.ScheduleHours)
			log.Printf("[Main] ✓ Principle evolution worker started (schedule: every %d hours)",
				cfg.GrowerAI.Principles.EvolutionScheduleHours)
			log.Printf("[Main] ✓ Memory linking enabled (similarity: %.2f, max links: %d)",
				cfg.GrowerAI.Linking.SimilarityThreshold, cfg.GrowerAI.Linking.MaxLinksPerMemory)
			log.Printf("[Main] ✓ Cluster compression enabled (merge windows: %d/%d/%d days)",
				mergeWindows.RecentDays, mergeWindows.MediumDays, mergeWindows.LongDays)
			log.Printf("[Main] ✓ Space-based compression enabled (limit: %d memories, trigger: %.0f%%)",
				storageLimits.MaxTotalMemories, storageLimits.CompressionTrigger*100)
			log.Printf("[Main] ✓ Tier allocation: Recent=%.1f%%, Medium=%.1f%%, Long=%.1f%%, Ancient=%.1f%%",
				storageLimits.TierAllocation.Recent*100,
				storageLimits.TierAllocation.Medium*100,
				storageLimits.TierAllocation.Long*100,
				storageLimits.TierAllocation.Ancient*100)
		}
	} else {
		log.Printf("[Main] GrowerAI compression disabled in config")
	}

	// Initialize GrowerAI tool registry (Phase 3.2)
	log.Printf("[Main] Initializing GrowerAI tool registry...")
	toolRegistry := tools.NewRegistry()
	toolConfigs := make(map[string]tools.ToolConfig)
	
	// Register SearXNG tool if enabled
	if cfg.GrowerAI.Tools.SearXNG.Enabled {
		searxngConfig := tools.ToolConfig{
			Enabled:               cfg.GrowerAI.Tools.SearXNG.Enabled,
			TimeoutInteractive:    time.Duration(cfg.GrowerAI.Tools.SearXNG.TimeoutInteractive) * time.Second,
			TimeoutIdle:           time.Duration(cfg.GrowerAI.Tools.SearXNG.TimeoutIdle) * time.Second,
			MaxResultsInteractive: cfg.GrowerAI.Tools.SearXNG.MaxResultsInteractive,
			MaxResultsIdle:        cfg.GrowerAI.Tools.SearXNG.MaxResultsIdle,
		}
		
		searxngTool := tools.NewSearXNGTool(cfg.GrowerAI.Tools.SearXNG.URL, searxngConfig)
		if err := toolRegistry.Register(searxngTool); err != nil {
			log.Printf("[Main] WARNING: Failed to register SearXNG tool: %v", err)
		} else {
			toolConfigs[tools.ToolNameSearch] = searxngConfig
			log.Printf("[Main] ✓ SearXNG tool registered (url: %s)", cfg.GrowerAI.Tools.SearXNG.URL)
		}
	}
	
// Register Web Parse tools if enabled (Phase 3.4)
if cfg.GrowerAI.Tools.WebParse.Enabled {
    webParseConfig := tools.ToolConfig{
        Enabled:               cfg.GrowerAI.Tools.WebParse.Enabled,
        TimeoutInteractive:    time.Duration(cfg.GrowerAI.Tools.WebParse.Timeout) * time.Second,
        TimeoutIdle:           time.Duration(cfg.GrowerAI.Tools.WebParse.Timeout) * time.Second,
    }
    
    userAgent := cfg.GrowerAI.Tools.WebParse.UserAgent
    maxPageSizeMB := cfg.GrowerAI.Tools.WebParse.MaxPageSizeMB
    chunkSize := cfg.GrowerAI.Tools.WebParse.ChunkSize
    
    // Use compression model for web parsing (optimized for summarization)
    llmURL := cfg.GrowerAI.Compression.Model.URL
    llmModel := cfg.GrowerAI.Compression.Model.Name
    
    // Register metadata tool
    metadataTool := tools.NewWebParserMetadataTool(userAgent, webParseConfig)
    if err := toolRegistry.Register(metadataTool); err != nil {
        log.Printf("[Main] WARNING: Failed to register web_parse_metadata tool: %v", err)
    } else {
        log.Printf("[Main] ✓ Web parser metadata tool registered")
    }
    
    // Register general summary tool
    generalTool := tools.NewWebParserGeneralTool(userAgent, llmURL, llmModel, maxPageSizeMB, webParseConfig)
    if err := toolRegistry.Register(generalTool); err != nil {
        log.Printf("[Main] WARNING: Failed to register web_parse_general tool: %v", err)
    } else {
        log.Printf("[Main] ✓ Web parser general tool registered")
    }
    
    // Register contextual summary tool
    contextualTool := tools.NewWebParserContextualTool(userAgent, llmURL, llmModel, maxPageSizeMB, webParseConfig)
    if err := toolRegistry.Register(contextualTool); err != nil {
        log.Printf("[Main] WARNING: Failed to register web_parse_contextual tool: %v", err)
    } else {
        log.Printf("[Main] ✓ Web parser contextual tool registered")
    }
    
    // Register chunked access tool
    chunkedTool := tools.NewWebParserChunkedTool(userAgent, maxPageSizeMB, chunkSize, webParseConfig)
    if err := toolRegistry.Register(chunkedTool); err != nil {
        log.Printf("[Main] WARNING: Failed to register web_parse_chunked tool: %v", err)
    } else {
        log.Printf("[Main] ✓ Web parser chunked tool registered")
    }
    
    log.Printf("[Main] ✓ Web parsing enabled (4 tools, max page: %dMB, chunk: %d chars)", 
        maxPageSizeMB, chunkSize)
}

// Sandbox tool (Phase 3.5 - placeholder)
if cfg.GrowerAI.Tools.Sandbox.Enabled {
    log.Printf("[Main] Sandbox tool enabled but not yet implemented (Phase 3.5)")
}
	
	// Create contextual registry
	contextualRegistry := tools.NewContextualRegistry(toolRegistry, toolConfigs)
	log.Printf("[Main] ✓ Tool registry initialized with %d tools", len(toolRegistry.List()))

	// Start GrowerAI dialogue worker if enabled (Phase 3.1)
	if cfg.GrowerAI.Dialogue.Enabled {
		log.Printf("[Main] Initializing GrowerAI dialogue worker...")

		storage, err := memory.NewStorage(
			cfg.GrowerAI.Qdrant.URL,
			cfg.GrowerAI.Qdrant.Collection,
			cfg.GrowerAI.Qdrant.APIKey,
		)
		if err != nil {
			log.Printf("[Main] WARNING: Failed to initialize storage for dialogue: %v", err)
		} else {
			embedder := memory.NewEmbedder(cfg.GrowerAI.EmbeddingModel.URL)
			stateManager := dialogue.NewStateManager(db.DB)

			engine := dialogue.NewEngine(
				storage,
				embedder,
				stateManager,
				contextualRegistry,
				cfg.GrowerAI.ReasoningModel.URL,
				cfg.GrowerAI.ReasoningModel.Name,
				cfg.GrowerAI.ReasoningModel.ContextSize,
				cfg.GrowerAI.Dialogue.MaxTokensPerCycle,
				cfg.GrowerAI.Dialogue.MaxDurationMinutes,
				cfg.GrowerAI.Dialogue.MaxThoughtsPerCycle,
				cfg.GrowerAI.Dialogue.ActionRequirementInterval,
				cfg.GrowerAI.Dialogue.NoveltyWindowHours,
				cfg.GrowerAI.Dialogue.ReasoningDepth,
				cfg.GrowerAI.Dialogue.EnableSelfAssessment,
				cfg.GrowerAI.Dialogue.EnableMetaLearning,
				cfg.GrowerAI.Dialogue.EnableStrategyTracking,
				cfg.GrowerAI.Dialogue.StoreInsights,
				cfg.GrowerAI.Dialogue.DynamicActionPlanning,
			)

			worker := dialogue.NewWorker(
				engine,
				cfg.GrowerAI.Dialogue.BaseIntervalMinutes,
				cfg.GrowerAI.Dialogue.JitterWindowMinutes,
			)

			// Start worker in background goroutine
			go worker.Start()

			log.Printf("[Main] ✓ GrowerAI dialogue worker started (interval: %d±%d minutes)",
				cfg.GrowerAI.Dialogue.BaseIntervalMinutes,
				cfg.GrowerAI.Dialogue.JitterWindowMinutes)
		}
	} else {
		log.Printf("[Main] GrowerAI dialogue disabled in config")
	}
	
	r := api.SetupRouter(cfg, rdb)
	addr := fmt.Sprintf("%s:%d", cfg.Server.Host, cfg.Server.Port)
	fmt.Printf("Starting server on %s%s\n", addr, cfg.Server.Subpath)
	if err := r.Run(addr); err != nil {
		fmt.Fprintf(os.Stderr, "Server error: %v\n", err)
		os.Exit(1)
	}
}

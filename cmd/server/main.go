package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"time"

	"go-llama/internal/api"
	"go-llama/internal/config"
	"go-llama/internal/db"
	"go-llama/internal/dialogue"
	"go-llama/internal/llm"
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

    // Start dynamic model refresher (every 5 minutes)
    // This updates model names and context limits without restart
    cfg.StartModelRefresher(5 * time.Minute)

	if err := db.Init(cfg); err != nil {
		fmt.Fprintf(os.Stderr, "DB init error: %v\n", err)
		os.Exit(1)
	}

	rdb := redisdb.NewClient(cfg)

	// Declare llmManager outside the block so it's accessible later
	var llmManager *llm.Manager

	// Check if GrowerAI is enabled globally
	if cfg.GrowerAI.Enabled {
		log.Printf("[Main] GrowerAI enabled - initializing components...")

		// Initialize LLM Queue Manager (if enabled)
		if cfg.GrowerAI.LLMQueue.Enabled {
			log.Printf("[Main] Initializing LLM queue manager...")
			
			llmConfig := &llm.Config{
				MaxConcurrent:            cfg.GrowerAI.LLMQueue.MaxConcurrent,
				CriticalQueueSize:        cfg.GrowerAI.LLMQueue.CriticalQueueSize,
				BackgroundQueueSize:      cfg.GrowerAI.LLMQueue.BackgroundQueueSize,
				CriticalTimeout:          time.Duration(cfg.GrowerAI.LLMQueue.CriticalTimeoutSeconds) * time.Second,
				BackgroundTimeout:        time.Duration(cfg.GrowerAI.LLMQueue.BackgroundTimeoutSeconds) * time.Second,
			}
			
			// Circuit breaker will be created later, pass nil for now
			llmManager = llm.NewManager(llmConfig, nil)
			defer llmManager.Stop()
			
			log.Printf("[Main] ✓ LLM queue manager initialized (concurrent: %d, critical queue: %d, background queue: %d)",
				llmConfig.MaxConcurrent, llmConfig.CriticalQueueSize, llmConfig.BackgroundQueueSize)
		} else {
			log.Printf("[Main] LLM queue disabled in config")
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

		// Initialize shared storage for GrowerAI (used by compression and dialogue)
		var storage *memory.Storage
		if cfg.GrowerAI.Compression.Enabled || cfg.GrowerAI.Dialogue.Enabled {
			log.Printf("[Main] Initializing GrowerAI storage...")
			
			var err error
			storage, err = memory.NewStorage(
				cfg.GrowerAI.Qdrant.URL,
				cfg.GrowerAI.Qdrant.Collection,
				cfg.GrowerAI.Qdrant.APIKey,
			)
			if err != nil {
				log.Fatalf("[Main] Failed to initialize GrowerAI storage: %v", err)
			}
			
			// Wait for collection to be ready before starting workers
			log.Printf("[Main] Ensuring memory collection is ready...")
			if err := storage.WaitForCollection(context.Background(), 30*time.Second); err != nil {
				log.Fatalf("[Main] Failed to initialize memory collection: %v", err)
			}
			log.Printf("[Main] ✓ Memory collection ready")
		}

		// Start GrowerAI compression worker if enabled
		if cfg.GrowerAI.Compression.Enabled {
			log.Printf("[Main] Initializing GrowerAI compression worker...")

			if storage == nil {
				log.Fatalf("[Main] Storage not initialized for compression worker")
			}
			
			embedder := memory.NewEmbedder(cfg.GrowerAI.EmbeddingModel.URL)

				linker := memory.NewLinker(
					storage,
					cfg.GrowerAI.Linking.SimilarityThreshold,
					cfg.GrowerAI.Linking.MaxLinksPerMemory,
				)

				// Create LLM client for compressor (background priority)
				var compressorLLMClient interface{}
				if llmManager != nil {
					compressorLLMClient = llm.NewClient(
						llmManager,
						llm.PriorityBackground,
						time.Duration(cfg.GrowerAI.LLMQueue.BackgroundTimeoutSeconds)*time.Second,
					)
					log.Printf("[Main] ✓ Compressor using LLM queue (priority: background)")
				}

				compressor := memory.NewCompressor(
					cfg.GrowerAI.Compression.Model.URL,
					cfg.GrowerAI.Compression.Model.Name,
					embedder,
					linker,
					compressorLLMClient,
				)

				// Create LLM client for tagger (background priority)
				var taggerLLMClient interface{}
				if llmManager != nil {
					taggerLLMClient = llm.NewClient(
						llmManager,
						llm.PriorityBackground,
						time.Duration(cfg.GrowerAI.LLMQueue.BackgroundTimeoutSeconds)*time.Second,
					)
					log.Printf("[Main] ✓ Tagger using LLM queue (priority: background)")
				}

				tagger := memory.NewTagger(
					cfg.GrowerAI.Compression.Model.URL,
					cfg.GrowerAI.Compression.Model.Name,
					cfg.GrowerAI.Tagging.BatchSize,
					embedder,
					taggerLLMClient,
				)

				// Initialize async tagger queue with parallel workers
				taggerQueue := memory.NewTaggerQueue(
					tagger,
					storage,
					3,    // 3 parallel workers
					1000, // Queue buffer size
				)
				defer taggerQueue.Stop()
				log.Printf("[Main] ✓ Async tagger queue initialized (workers: 3, queue: 1000)")

				tierRules := memory.TierRules{
					RecentToMediumDays: cfg.GrowerAI.Compression.TierRules.RecentToMediumDays,
					MediumToLongDays:   cfg.GrowerAI.Compression.TierRules.MediumToLongDays,
					LongToAncientDays:  cfg.GrowerAI.Compression.TierRules.LongToAncientDays,
				}

				mergeWindows := memory.MergeWindows{
					RecentDays: cfg.GrowerAI.Compression.MergeWindowRecent,
					MediumDays: cfg.GrowerAI.Compression.MergeWindowMedium,
					LongDays:   cfg.GrowerAI.Compression.MergeWindowLong,
				}

				storageLimits := memory.StorageLimits{
					MaxTotalMemories: cfg.GrowerAI.StorageLimits.MaxTotalMemories,
					TierAllocation: memory.TierAllocation{
						Recent:  cfg.GrowerAI.StorageLimits.TierAllocation.Recent,
						Medium:  cfg.GrowerAI.StorageLimits.TierAllocation.Medium,
						Long:    cfg.GrowerAI.StorageLimits.TierAllocation.Long,
						Ancient: cfg.GrowerAI.StorageLimits.TierAllocation.Ancient,
					},
					CompressionTrigger: cfg.GrowerAI.StorageLimits.CompressionTrigger,
					AllowTierOverflow:  cfg.GrowerAI.StorageLimits.AllowTierOverflow,
				}

				compressionWeights := memory.CompressionWeights{
					Age:        cfg.GrowerAI.StorageLimits.CompressionWeights.Age,
					Importance: cfg.GrowerAI.StorageLimits.CompressionWeights.Importance,
					Access:     cfg.GrowerAI.StorageLimits.CompressionWeights.Access,
				}

				// Create LLM client for decay worker (background priority, for principles)
				var decayWorkerLLMClient interface{}
				if llmManager != nil {
					decayWorkerLLMClient = llm.NewClient(
						llmManager,
						llm.PriorityBackground,
						time.Duration(cfg.GrowerAI.LLMQueue.BackgroundTimeoutSeconds)*time.Second,
					)
					log.Printf("[Main] ✓ DecayWorker using LLM queue for principles (priority: background)")
				}

				worker := memory.NewDecayWorker(
					storage,
					compressor,
					embedder,
					taggerQueue, // Use tagger queue instead of tagger
					linker,
					db.DB,
					cfg.GrowerAI.Compression.Model.URL,
					cfg.GrowerAI.Compression.Model.Name,
					decayWorkerLLMClient, // NEW: Pass LLM client for principle generation
					cfg.GrowerAI.Compression.ScheduleHours,
					cfg.GrowerAI.Principles.EvolutionScheduleHours,
					cfg.GrowerAI.Principles.MinRatingThreshold,
					cfg.GrowerAI.Principles.ExtractionLimit,
					tierRules,
					mergeWindows,
					cfg.GrowerAI.Compression.ImportanceMod,
					cfg.GrowerAI.Compression.AccessMod,
					storageLimits,
					compressionWeights,
				)
				// Start linking worker
				linkWorker := memory.NewLinkWorker(
					storage,
					linker,
					cfg.GrowerAI.Linking.WorkerScheduleHours,
				)
				go linkWorker.Start()

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

		// Initialize GrowerAI tool registry
		log.Printf("[Main] Initializing GrowerAI tool registry...")
		toolRegistry := tools.NewRegistry()
		toolConfigs := make(map[string]tools.ToolConfig)

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

		if cfg.GrowerAI.Tools.WebParse.Enabled {
			webParseConfig := tools.ToolConfig{
				Enabled:            cfg.GrowerAI.Tools.WebParse.Enabled,
				TimeoutInteractive: time.Duration(cfg.GrowerAI.Tools.WebParse.Timeout) * time.Second,
				TimeoutIdle:        time.Duration(cfg.GrowerAI.Tools.WebParse.Timeout) * time.Second,
			}

			userAgent := cfg.GrowerAI.Tools.WebParse.UserAgent
			maxPageSizeMB := cfg.GrowerAI.Tools.WebParse.MaxPageSizeMB
			chunkSize := cfg.GrowerAI.Tools.WebParse.ChunkSize
			llmURL := cfg.GrowerAI.Compression.Model.URL
			llmModel := cfg.GrowerAI.Compression.Model.Name

			metadataTool := tools.NewWebParserMetadataTool(userAgent, webParseConfig)
			if err := toolRegistry.Register(metadataTool); err != nil {
				log.Printf("[Main] WARNING: Failed to register web_parse_metadata tool: %v", err)
			} else {
				log.Printf("[Main] ✓ Web parser metadata tool registered")
			}

			generalTool := tools.NewWebParserGeneralTool(userAgent, llmURL, llmModel, maxPageSizeMB, webParseConfig)
			if err := toolRegistry.Register(generalTool); err != nil {
				log.Printf("[Main] WARNING: Failed to register web_parse_general tool: %v", err)
			} else {
				log.Printf("[Main] ✓ Web parser general tool registered")
			}

			contextualTool := tools.NewWebParserContextualTool(userAgent, llmURL, llmModel, maxPageSizeMB, webParseConfig)
			if err := toolRegistry.Register(contextualTool); err != nil {
				log.Printf("[Main] WARNING: Failed to register web_parse_contextual tool: %v", err)
			} else {
				log.Printf("[Main] ✓ Web parser contextual tool registered")
			}

			chunkedTool := tools.NewWebParserChunkedTool(userAgent, maxPageSizeMB, chunkSize, webParseConfig)
			if err := toolRegistry.Register(chunkedTool); err != nil {
				log.Printf("[Main] WARNING: Failed to register web_parse_chunked tool: %v", err)
			} else {
				log.Printf("[Main] ✓ Web parser chunked tool registered")
			}

			log.Printf("[Main] ✓ Web parsing enabled (4 tools, max page: %dMB, chunk: %d chars)",
				maxPageSizeMB, chunkSize)
		}

		if cfg.GrowerAI.Tools.Sandbox.Enabled {
			log.Printf("[Main] Sandbox tool enabled but not yet implemented (Phase 3.5)")
		}

		contextualRegistry := tools.NewContextualRegistry(toolRegistry, toolConfigs)
		log.Printf("[Main] ✓ Tool registry initialized with %d tools", len(toolRegistry.List()))

		// Start GrowerAI dialogue worker if enabled
		if cfg.GrowerAI.Dialogue.Enabled {
			log.Printf("[Main] Initializing GrowerAI dialogue worker...")

			if storage == nil {
				log.Printf("[Main] WARNING: Storage not initialized, skipping dialogue worker")
			} else {
				embedder := memory.NewEmbedder(cfg.GrowerAI.EmbeddingModel.URL)
				stateManager := dialogue.NewStateManager(db.DB)

				// Initialize circuit breaker for LLM resilience
				llmCircuitBreaker := tools.NewCircuitBreaker(
					3,              // Open after 3 failures
					5*time.Minute,  // Stay open for 5 minutes
				)
				log.Printf("[Main] ✓ LLM circuit breaker initialized (threshold: 3 failures, timeout: 5m)")

				// Create LLM client for dialogue (background priority)
				var llmClient interface{}
				if llmManager != nil {
					llmClient = llm.NewClient(
						llmManager,
						llm.PriorityBackground,
						time.Duration(cfg.GrowerAI.LLMQueue.BackgroundTimeoutSeconds)*time.Second,
					)
					log.Printf("[Main] ✓ Dialogue using LLM queue (priority: background, timeout: %ds)",
						cfg.GrowerAI.LLMQueue.BackgroundTimeoutSeconds)
				} else {
					log.Printf("[Main] Dialogue using legacy direct HTTP calls")
				}

				engine := dialogue.NewEngine(
					storage,
					embedder,
					stateManager,
					contextualRegistry,
					db.DB, // Add DB parameter for principles
					cfg.GrowerAI.ReasoningModel.URL,
					cfg.GrowerAI.ReasoningModel.Name,
					cfg.GrowerAI.ReasoningModel.ContextSize,
					llmClient, // NEW PARAMETER - insert here
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
					llmCircuitBreaker, // Add circuit breaker parameter
				)

				worker := dialogue.NewWorker(
					engine,
					cfg.GrowerAI.Dialogue.BaseIntervalMinutes,
					cfg.GrowerAI.Dialogue.JitterWindowMinutes,
				)

				go worker.Start()

				log.Printf("[Main] ✓ GrowerAI dialogue worker started (interval: %d±%d minutes)",
					cfg.GrowerAI.Dialogue.BaseIntervalMinutes,
					cfg.GrowerAI.Dialogue.JitterWindowMinutes)
			}
		} else {
			log.Printf("[Main] GrowerAI dialogue disabled in config")
		}

		log.Printf("[Main] ✓ GrowerAI initialization complete")
	} else {
		log.Printf("[Main] GrowerAI disabled in config - skipping initialization")
	}

	r := api.SetupRouter(cfg, rdb, llmManager)
	addr := fmt.Sprintf("%s:%d", cfg.Server.Host, cfg.Server.Port)
	fmt.Printf("Starting server on %s%s\n", addr, cfg.Server.Subpath)
	if err := r.Run(addr); err != nil {
		fmt.Fprintf(os.Stderr, "Server error: %v\n", err)
		os.Exit(1)
	}
}

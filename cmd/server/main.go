package main

import (
	"fmt"
	"log"
	"os"
	"go-llama/internal/config"
	"go-llama/internal/db"
	redisdb "go-llama/internal/redis"
	"go-llama/internal/api"
	"go-llama/internal/memory"
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
	rdb := redisdb.NewClient(cfg)
	
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
			compressor := memory.NewCompressor(
				cfg.GrowerAI.Compression.Model.URL,
				cfg.GrowerAI.Compression.Model.Name,
			)
			
			embedder := memory.NewEmbedder(cfg.GrowerAI.EmbeddingModel.URL)
			
			tierRules := memory.TierRules{
				RecentToMediumDays: cfg.GrowerAI.Compression.TierRules.RecentToMediumDays,
				MediumToLongDays:   cfg.GrowerAI.Compression.TierRules.MediumToLongDays,
				LongToAncientDays:  cfg.GrowerAI.Compression.TierRules.LongToAncientDays,
			}
			
			worker := memory.NewDecayWorker(
				storage,
				compressor,
				embedder,
				cfg.GrowerAI.Compression.ScheduleHours,
				tierRules,
				cfg.GrowerAI.Compression.ImportanceMod,
				cfg.GrowerAI.Compression.AccessMod,
			)
			
			// Start worker in background goroutine
			go worker.Start()
			
			log.Printf("[Main] ✓ GrowerAI compression worker started (schedule: every %d hours)", 
				cfg.GrowerAI.Compression.ScheduleHours)
		}
	} else {
		log.Printf("[Main] GrowerAI compression disabled in config")
	}
	
	r := api.SetupRouter(cfg, rdb)
	addr := fmt.Sprintf("%s:%d", cfg.Server.Host, cfg.Server.Port)
	fmt.Printf("Starting server on %s%s\n", addr, cfg.Server.Subpath)
	if err := r.Run(addr); err != nil {
		fmt.Fprintf(os.Stderr, "Server error: %v\n", err)
		os.Exit(1)
	}
}

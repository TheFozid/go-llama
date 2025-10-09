package main

import (
	"fmt"
	"os"
	"go-llama/internal/config"
	"go-llama/internal/db"
	redisdb "go-llama/internal/redis"
	"go-llama/internal/api"
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
	r := api.SetupRouter(cfg, rdb)
	addr := fmt.Sprintf("%s:%d", cfg.Server.Host, cfg.Server.Port)
	fmt.Printf("Starting server on %s%s\n", addr, cfg.Server.Subpath)
	if err := r.Run(addr); err != nil {
		fmt.Fprintf(os.Stderr, "Server error: %v\n", err)
		os.Exit(1)
	}
}

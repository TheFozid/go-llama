package redisdb

import (
	"testing"
	"go-llama/internal/config"
)

func TestNewClient_BasicConfig(t *testing.T) {
	cfg := &config.Config{}
	cfg.Redis.Addr = "localhost:6379"
	cfg.Redis.Password = ""
	cfg.Redis.DB = 15

	client := NewClient(cfg)
	if client == nil {
		t.Fatalf("NewClient returned nil")
	}
	// Check that options are set as expected
	opts := client.Options()
	if opts.Addr != cfg.Redis.Addr {
		t.Errorf("expected Addr %s, got %s", cfg.Redis.Addr, opts.Addr)
	}
	if opts.Password != cfg.Redis.Password {
		t.Errorf("expected Password %s, got %s", cfg.Redis.Password, opts.Password)
	}
	if opts.DB != cfg.Redis.DB {
		t.Errorf("expected DB %d, got %d", cfg.Redis.DB, opts.DB)
	}
}

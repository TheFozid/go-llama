package db

import (
	"testing"
	"go-llama/internal/config"
	"go-llama/internal/user"
	"go-llama/internal/chat"
	"os"
)

// Dummy DSN for test (won't actually connect, just checks error path)
func TestInit_InvalidDSN(t *testing.T) {
	cfg := &config.Config{}
	cfg.Postgres.DSN = "invalid-dsn-for-testing"
	err := Init(cfg)
	if err == nil {
		t.Errorf("expected error for invalid DSN, got nil")
	}
}

// You can only run actual DB tests if you have a valid Postgres test instance
// This test is optional and skipped unless TEST_DB_DSN is set
func TestInit_ValidDSN_AndMigrates(t *testing.T) {
	dsn := os.Getenv("TEST_DB_DSN")
	if dsn == "" {
		t.Skip("set TEST_DB_DSN to run real DB test")
	}
	cfg := &config.Config{}
	cfg.Postgres.DSN = dsn
	err := Init(cfg)
	if err != nil {
		t.Fatalf("Init failed: %v", err)
	}
	// Check that DB is non-nil and can be used
	if DB == nil {
		t.Fatalf("DB not set")
	}
	// Check migration created tables
	if err := DB.AutoMigrate(&user.User{}, &chat.Chat{}, &chat.Message{}); err != nil {
		t.Errorf("AutoMigrate failed: %v", err)
	}
}

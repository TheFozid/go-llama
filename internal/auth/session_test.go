package auth

import (
	"fmt"
	"os"
	"testing"
	"time"

	redisdb "go-llama/internal/redis"
	"go-llama/internal/config"
)

func TestSessionSetGetDelete(t *testing.T) {
	config.ResetConfigForTest() // <-- THIS IS THE FIX

	filename := "/home/danny/go-llama/config.json"

	// Debug: print working directory and first 200 bytes of config.json
	dir, _ := os.Getwd()
	raw, _ := os.ReadFile(filename)
	fmt.Printf("DEBUG: Current working directory: %s\n", dir)
	fmt.Printf("DEBUG: First 200 bytes of %s:\n%s\n", filename, raw)

	cfg, err := config.LoadConfig(filename)
	if err != nil {
		t.Fatalf("FAILED: could not load config.json for redis: %v", err)
	}
	rdb := redisdb.NewClient(cfg)

	userId := uint(12345)
	token := "session_test_token"
	duration := 2 * time.Second

	// Set session
	if err := SetSession(rdb, userId, token, duration); err != nil {
		t.Fatalf("SetSession failed: %v", err)
	}

	// Get session
	gotToken, err := GetSession(rdb, userId)
	if err != nil {
		t.Fatalf("GetSession failed: %v", err)
	}
	if gotToken != token {
		t.Errorf("expected token %q, got %q", token, gotToken)
	}

	// Delete session
	if err := DeleteSession(rdb, userId); err != nil {
		t.Fatalf("DeleteSession failed: %v", err)
	}

	// Get session after deletion
	_, err = GetSession(rdb, userId)
	if err == nil {
		t.Errorf("expected error for deleted session, got nil")
	}
}

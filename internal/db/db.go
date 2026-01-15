package db

import (
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
	"go-llama/internal/config"
	"go-llama/internal/user"
	"go-llama/internal/chat"
	"go-llama/internal/memory"
	"go-llama/internal/dialogue"  // NEW
	"log"
)

var DB *gorm.DB

func Init(cfg *config.Config) error {
	db, err := gorm.Open(postgres.Open(cfg.Postgres.DSN), &gorm.Config{
		Logger: logger.Default.LogMode(logger.Error), // Only log errors, not full queries
	})
	if err != nil {
		return err
	}
	
	// Auto-migrate user model
	if err := db.AutoMigrate(&user.User{}); err != nil {
		return err
	}
	
	// Auto-migrate chat and message models
	if err := db.AutoMigrate(&chat.Chat{}, &chat.Message{}); err != nil {
		return err
	}
	
	// Auto-migrate GrowerAI principles
	if err := db.AutoMigrate(&memory.Principle{}); err != nil {
		return err
	}
	
	// Auto-migrate dialogue state tables (Phase 3.1)
	if err := db.AutoMigrate(
		&dialogue.DialogueState{},
		&dialogue.DialogueMetrics{},
		&dialogue.DialogueThought{},
	); err != nil {
		return err
	}
	
	DB = db
	log.Printf("Database connected and migrated")
	return nil
}

package db

import (
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"go-llama/internal/config"
	"go-llama/internal/user"
	"go-llama/internal/chat"
	"go-llama/internal/memory"
	"log"
)

var DB *gorm.DB

func Init(cfg *config.Config) error {
	db, err := gorm.Open(postgres.Open(cfg.Postgres.DSN), &gorm.Config{})
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
	
	DB = db
	log.Printf("Database connected and migrated")
	return nil
}

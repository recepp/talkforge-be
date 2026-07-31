package model

import (
	"fmt"
	"log"
	"strings"

	"talkforge-be/config"
	"golang.org/x/crypto/bcrypt"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

// DB is the global database connection handle.
var DB *gorm.DB

// InitDB initializes the PostgreSQL connection using GORM and runs auto-migrations.
func InitDB(cfg *config.Config) (*gorm.DB, error) {
	db, err := gorm.Open(postgres.Open(cfg.DatabaseURL), &gorm.Config{})
	if err != nil {
		return nil, fmt.Errorf("failed to connect to database using connection string: %v", err)
	}

	log.Println("Database connection established successfully using DATABASE_URL.")

	// Run auto-migrations for talkforge_users and talk_requests
	err = db.AutoMigrate(&User{}, &TalkRequest{})
	if err != nil {
		return nil, fmt.Errorf("failed to run database auto-migrations: %v", err)
	}

	log.Println("Database auto-migrations executed successfully.")

	DB = db

	// Bootstrap Admin User if config credentials are set
	err = bootstrapAdmin(db, cfg)
	if err != nil {
		log.Printf("Warning: Failed to bootstrap admin user: %v", err)
	}

	return db, nil
}

func bootstrapAdmin(db *gorm.DB, cfg *config.Config) error {
	adminEmail := cfg.AdminBootstrapName
	if !strings.Contains(adminEmail, "@") {
		adminEmail = adminEmail + "@talkforge.local"
	}

	var count int64
	err := db.Model(&User{}).Where("email = ? OR role = ?", adminEmail, "admin").Count(&count).Error
	if err != nil {
		return err
	}

	if count == 0 {
		hashed, err := bcrypt.GenerateFromPassword([]byte(cfg.AdminBootstrapPassword), bcrypt.DefaultCost)
		if err != nil {
			return err
		}

		adminUser := User{
			Email:        adminEmail,
			Nickname:     "admin",
			PasswordHash: string(hashed),
			Role:         "admin",
			Avatar:       "👑",
		}

		err = db.Create(&adminUser).Error
		if err != nil {
			return err
		}
		log.Printf("Admin user bootstrapped successfully: %s", adminEmail)
	}

	return nil
}

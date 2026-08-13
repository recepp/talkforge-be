package main

import (
	"log"
	"talkforge-be/config"
	"talkforge-be/model"
)

func main() {
	cfg := config.LoadConfig()
	db, err := model.InitDB(cfg)
	if err != nil {
		log.Fatalf("Failed to init DB: %v", err)
	}
	model.HealCorruptedUsers(db)
	log.Println("HEAL SCRIPT FINISHED SUCCESSFULLY")
}

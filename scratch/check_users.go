package main

import (
	"fmt"
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

	var users []model.User
	db.Find(&users)
	for _, u := range users {
		fmt.Printf("ID: %d | Nickname: %s | Email: %s | Role: %s | Avatar: %s\n", u.ID, u.Nickname, u.Email, u.Role, u.Avatar)
	}
}

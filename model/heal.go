package model

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"log"
	"strings"

	"gorm.io/gorm"
)

// HealCorruptedUsers decodes any JWT tokens stored in user.Email or google_id
// and updates the database record with the actual name, email, and avatar extracted from Google JWT claims.
func HealCorruptedUsers(db *gorm.DB) {
	if db == nil {
		return
	}
	var users []User
	if err := db.Where("email LIKE ? OR google_id LIKE ?", "eyJh%", "google-id-eyJh%").Find(&users).Error; err != nil {
		return
	}

	for _, u := range users {
		tokenStr := u.Email
		if strings.HasPrefix(tokenStr, "google-id-") {
			tokenStr = strings.TrimPrefix(tokenStr, "google-id-")
		}
		if !strings.HasPrefix(tokenStr, "eyJh") {
			continue
		}

		parts := strings.Split(tokenStr, ".")
		if len(parts) != 3 {
			continue
		}
		seg := parts[1]
		if l := len(seg)%4; l > 0 {
			seg += strings.Repeat("=", 4-l)
		}
		decoded, err := base64.URLEncoding.DecodeString(seg)
		if err != nil {
			decoded, err = base64.StdEncoding.DecodeString(seg)
			if err != nil {
				continue
			}
		}
		var claims map[string]interface{}
		if err := json.Unmarshal(decoded, &claims); err != nil {
			continue
		}

		updated := false
		if em, ok := claims["email"].(string); ok && em != "" {
			u.Email = em
			updated = true
		}
		if sub, ok := claims["sub"].(string); ok && sub != "" {
			u.GoogleID = &sub
			updated = true
		}

		var realName string
		if nm, ok := claims["name"].(string); ok && nm != "" {
			realName = nm
		} else {
			given, _ := claims["given_name"].(string)
			family, _ := claims["family_name"].(string)
			combined := strings.TrimSpace(given + " " + family)
			if combined != "" {
				realName = combined
			}
		}

		if realName != "" {
			finalNick := realName
			var count int64
			if db.Model(&User{}).Where("nickname = ? AND id != ?", finalNick, u.ID).Count(&count); count > 0 {
				finalNick = fmt.Sprintf("%s-%d", realName, u.ID)
			}
			u.Nickname = finalNick
			updated = true
		}

		if pic, ok := claims["picture"].(string); ok && pic != "" {
			u.Avatar = pic
			updated = true
		}

		if updated {
			if err := db.Save(&u).Error; err != nil {
				log.Printf("Failed to heal user ID %d: %v", u.ID, err)
			} else {
				log.Printf("Successfully healed user ID %d: email=%s, nickname=%s", u.ID, u.Email, u.Nickname)
			}
		}
	}
}

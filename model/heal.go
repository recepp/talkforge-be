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
// and updates the database record with clean email, nickname, and avatar.
func HealCorruptedUsers(db *gorm.DB) {
	if db == nil {
		return
	}
	var users []User
	if err := db.Where("email LIKE ? OR google_id LIKE ? OR nickname LIKE ?", "eyJh%", "google-id-eyJh%", "Google User-%").Find(&users).Error; err != nil {
		return
	}

	for _, u := range users {
		tokenStr := u.Email
		if strings.HasPrefix(tokenStr, "google-id-") {
			tokenStr = strings.TrimPrefix(tokenStr, "google-id-")
		}

		var claims map[string]interface{}
		if strings.HasPrefix(tokenStr, "eyJh") {
			parts := strings.Split(tokenStr, ".")
			if len(parts) == 3 {
				seg := parts[1]
				if l := len(seg)%4; l > 0 {
					seg += strings.Repeat("=", 4-l)
				}
				if decoded, err := base64.URLEncoding.DecodeString(seg); err == nil {
					_ = json.Unmarshal(decoded, &claims)
				} else if decoded, err := base64.StdEncoding.DecodeString(seg); err == nil {
					_ = json.Unmarshal(decoded, &claims)
				}
			}
		}

		// 1. Determine clean email
		targetEmail := ""
		if claims != nil {
			if em, ok := claims["email"].(string); ok && em != "" && !strings.HasPrefix(em, "eyJh") {
				targetEmail = em
			}
		}

		if targetEmail == "" || targetEmail == "googleuser@example.com" {
			targetEmail = fmt.Sprintf("google.user.%d@talkforge.local", u.ID)
		} else {
			var count int64
			db.Model(&User{}).Where("email = ? AND id != ?", targetEmail, u.ID).Count(&count)
			if count > 0 {
				targetEmail = fmt.Sprintf("user.%d.%s", u.ID, targetEmail)
			}
		}

		// 2. Determine clean nickname
		targetNick := ""
		if claims != nil {
			if nm, ok := claims["name"].(string); ok && nm != "" {
				targetNick = nm
			} else {
				given, _ := claims["given_name"].(string)
				family, _ := claims["family_name"].(string)
				combined := strings.TrimSpace(given + " " + family)
				if combined != "" {
					targetNick = combined
				}
			}
		}

		if targetNick == "" || targetNick == "Google User" || strings.HasPrefix(targetNick, "Google User-") {
			targetNick = fmt.Sprintf("Google Kullanıcısı #%d", u.ID)
		}

		var nickCount int64
		if db.Model(&User{}).Where("nickname = ? AND id != ?", targetNick, u.ID).Count(&nickCount); nickCount > 0 {
			targetNick = fmt.Sprintf("%s-%d", targetNick, u.ID)
		}

		// 3. Avatar
		targetAvatar := u.Avatar
		if targetAvatar == "" || targetAvatar == "👤" {
			if claims != nil {
				if pic, ok := claims["picture"].(string); ok && pic != "" {
					targetAvatar = pic
				}
			}
		}
		if targetAvatar == "" {
			targetAvatar = "👤"
		}

		// Update fields
		u.Email = targetEmail
		u.Nickname = targetNick
		u.Avatar = targetAvatar
		dummyGID := fmt.Sprintf("google-id-healed-%d", u.ID)
		if u.GoogleID == nil || strings.HasPrefix(*u.GoogleID, "google-id-eyJh") {
			if claims != nil {
				if sub, ok := claims["sub"].(string); ok && sub != "" {
					u.GoogleID = &sub
				} else {
					u.GoogleID = &dummyGID
				}
			} else {
				u.GoogleID = &dummyGID
			}
		}

		if err := db.Save(&u).Error; err != nil {
			log.Printf("[Heal] Error saving user ID %d: %v", u.ID, err)
		} else {
			log.Printf("[Heal] Successfully healed user ID %d -> Email: %s, Nickname: %s", u.ID, u.Email, u.Nickname)
		}
	}
}

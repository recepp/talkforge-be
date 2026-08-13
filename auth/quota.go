package auth

import (
	"fmt"
	"net/http"
	"time"

	"talkforge-be/config"
	"talkforge-be/model"

	"github.com/gin-gonic/gin"
)

// GetUserUsageStats returns current consumption statistics and remaining limits for a user.
func GetUserUsageStats(cfg *config.Config, userID uint) (*model.UserUsageStats, error) {
	now := time.Now()
	dailyStart := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())

	var dailyCreates int64
	var dailyEdits int64
	var dailyTokens int64
	var totalTokens int64
	var totalReqs int64

	// Apply quota multiplier based on subscription tier or admin role (free=1x, pro=3x, enterprise=10x, admin=10x)
	multiplier := 1
	var user model.User
	if err := model.DB.Select("role, subscription_tier").First(&user, userID).Error; err == nil {
		if user.Role == "admin" || user.SubscriptionTier == "enterprise" {
			multiplier = 10
		} else if user.SubscriptionTier == "pro" {
			multiplier = 3
		}
	}

	dailyCreateLimit := cfg.GeminiDailyCreateLimit * multiplier
	dailyEditLimit := cfg.GeminiDailyEditLimit * multiplier
	dailyTokLimit := cfg.GeminiDailyTokenLimit * multiplier

	// Count talk_requests created today as creates (mode 'new' or parent_id IS NULL) including soft-deleted ones
	if err := model.DB.Unscoped().Model(&model.TalkRequest{}).
		Where("user_id = ? AND (mode = 'new' OR parent_id IS NULL) AND created_at >= ?", userID, dailyStart).
		Count(&dailyCreates).Error; err != nil {
		return nil, err
	}

	// Count talk_requests created today as edits (mode IN ('update', 'partial_update')) including soft-deleted ones
	if err := model.DB.Unscoped().Model(&model.TalkRequest{}).
		Where("user_id = ? AND mode IN ('update', 'partial_update') AND created_at >= ?", userID, dailyStart).
		Count(&dailyEdits).Error; err != nil {
		return nil, err
	}

	// Query token sums from gemini_call_logs
	model.DB.Model(&model.GeminiCallLog{}).
		Where("user_id = ? AND status = 'success' AND created_at >= ?", userID, dailyStart).
		Select("COALESCE(SUM(total_tokens), 0)").
		Scan(&dailyTokens)

	model.DB.Model(&model.GeminiCallLog{}).
		Where("user_id = ? AND status = 'success'", userID).
		Select("COALESCE(SUM(total_tokens), 0)").
		Scan(&totalTokens)

	model.DB.Model(&model.GeminiCallLog{}).
		Where("user_id = ? AND status = 'success'", userID).
		Count(&totalReqs)

	dailyCreatesRemaining := int64(dailyCreateLimit) - dailyCreates
	if dailyCreatesRemaining < 0 {
		dailyCreatesRemaining = 0
	}

	dailyEditsRemaining := int64(dailyEditLimit) - dailyEdits
	if dailyEditsRemaining < 0 {
		dailyEditsRemaining = 0
	}

	dailyTokRemaining := int64(dailyTokLimit) - dailyTokens
	if dailyTokRemaining < 0 {
		dailyTokRemaining = 0
	}

	var roomsUsed int64
	if err := model.DB.Model(&model.RoomMember{}).
		Where("user_id = ? AND status = 'accepted'", userID).
		Count(&roomsUsed).Error; err != nil {
		roomsUsed = 0
	}

	roomsLimit := cfg.MaxRoomMembershipLimit
	if roomsLimit <= 0 {
		roomsLimit = 1
	}
	roomsLimit = roomsLimit * multiplier
	roomsRemaining := int64(roomsLimit) - roomsUsed
	if roomsRemaining < 0 {
		roomsRemaining = 0
	}

	return &model.UserUsageStats{
		DailyCreatesUsed:      dailyCreates,
		DailyCreatesLimit:     dailyCreateLimit,
		DailyCreatesRemaining: dailyCreatesRemaining,

		DailyEditsUsed:      dailyEdits,
		DailyEditsLimit:     dailyEditLimit,
		DailyEditsRemaining: dailyEditsRemaining,

		DailyTokensUsed:      dailyTokens,
		DailyTokensLimit:     dailyTokLimit,
		DailyTokensRemaining: dailyTokRemaining,

		TotalTokensUsed:    totalTokens,
		TotalRequestsCount: totalReqs,

		RoomsUsed:      roomsUsed,
		RoomsLimit:     roomsLimit,
		RoomsRemaining: roomsRemaining,
	}, nil
}

// CheckQuota verifies whether the user is within allowed daily limits.
// Optional mode parameter specifies "new", "update", "partial_update", etc.
// Returns ok=true if within limits, or ok=false with an error message.
func CheckQuota(cfg *config.Config, userID uint, modes ...string) (bool, string, error) {
	stats, err := GetUserUsageStats(cfg, userID)
	if err != nil {
		return false, "Kota bilgisi sorgulanırken bir hata oluştu", err
	}

	if stats.DailyTokensUsed >= int64(stats.DailyTokensLimit) {
		return false, fmt.Sprintf("Günlük Gemini token limitinize (%d token) ulaştınız. Lütfen yarın tekrar deneyin.", stats.DailyTokensLimit), nil
	}

	mode := ""
	if len(modes) > 0 {
		mode = modes[0]
	}

	if mode == "new" || mode == "" {
		if stats.DailyCreatesUsed >= int64(stats.DailyCreatesLimit) {
			return false, fmt.Sprintf("Günlük konuşma oluşturma limitinize (%d adet) ulaştınız. Lütfen yarın tekrar deneyin.", stats.DailyCreatesLimit), nil
		}
	}

	if mode == "update" || mode == "partial_update" || mode == "" {
		if stats.DailyEditsUsed >= int64(stats.DailyEditsLimit) {
			return false, fmt.Sprintf("Günlük konuşma düzenleme limitinize (%d adet) ulaştınız. Lütfen yarın tekrar deneyin.", stats.DailyEditsLimit), nil
		}
	}

	return true, "", nil
}

// QuotaMiddleware checks if the user has remaining quota before executing Gemini operations.
func QuotaMiddleware(cfg *config.Config) gin.HandlerFunc {
	return func(c *gin.Context) {
		userID, exists := c.Get("userID")
		if !exists {
			c.JSON(http.StatusUnauthorized, gin.H{"error": "Yetkilendirme bilgisi eksik"})
			c.Abort()
			return
		}

		ok, errMsg, err := CheckQuota(cfg, userID.(uint))
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			c.Abort()
			return
		}

		if !ok {
			c.JSON(http.StatusTooManyRequests, gin.H{
				"error": errMsg,
				"code":  "QUOTA_EXCEEDED",
			})
			c.Abort()
			return
		}

		c.Next()
	}
}

package handler

import (
	"net/http"
	"strconv"
	"strings"
	"time"

	"talkforge-be/auth"
	"talkforge-be/config"
	"talkforge-be/model"

	"github.com/gin-gonic/gin"
)

// AdminHandler handles administration and monitoring endpoints.
type AdminHandler struct {
	cfg *config.Config
}

// NewAdminHandler instantiates a new AdminHandler.
func NewAdminHandler(cfg *config.Config) *AdminHandler {
	return &AdminHandler{cfg: cfg}
}

// AdminUserItem represents user summary for admin dashboard.
type AdminUserItem struct {
	ID               uint      `json:"id"`
	Email            string    `json:"email"`
	Nickname         string    `json:"nickname"`
	Avatar           string    `json:"avatar"`
	Role             string    `json:"role"`
	SubscriptionTier string    `json:"subscription_tier"`
	Language         string    `json:"language"`
	IsSuspended      bool      `json:"is_suspended"`
	CreatedAt        time.Time `json:"created_at"`
	TalkCount        int64     `json:"talk_count"`
	GeminiCallCount  int64     `json:"gemini_call_count"`
	GeminiTokenCount int64     `json:"gemini_token_count"`
}

// AdminStatsResponse represents platform-wide usage metrics.
type AdminStatsResponse struct {
	TotalUsers         int64            `json:"total_users"`
	TotalTalks         int64            `json:"total_talks"`
	TotalRooms         int64            `json:"total_rooms"`
	StatusCounts       map[string]int64 `json:"status_counts"`
	GeminiCallsToday   int64            `json:"gemini_calls_today"`
	GeminiCallsMonth   int64            `json:"gemini_calls_month"`
	GeminiCallsTotal   int64            `json:"gemini_calls_total"`
	GeminiTokensToday  int64            `json:"gemini_tokens_today"`
	GeminiTokensMonth  int64            `json:"gemini_tokens_month"`
	GeminiTokensTotal  int64            `json:"gemini_tokens_total"`
	QuotaExceededUsers int64            `json:"quota_exceeded_users"`
}

// UpdateUserRequest represents payload for updating user role or suspension state.
type UpdateUserRequest struct {
	Role             *string `json:"role,omitempty" example:"admin"`
	SubscriptionTier *string `json:"subscription_tier,omitempty" example:"pro"`
	IsSuspended      *bool   `json:"is_suspended,omitempty" example:"true"`
}

// AdminRoomItem represents room summary for admin view.
type AdminRoomItem struct {
	ID            uint      `json:"id"`
	Name          string    `json:"name"`
	OwnerID       uint      `json:"owner_id"`
	OwnerEmail    string    `json:"owner_email"`
	OwnerNickname string    `json:"owner_nickname"`
	CreatedAt     time.Time `json:"created_at"`
	MemberCount   int64     `json:"member_count"`
	TalkCount     int64     `json:"talk_count"`
}

// GetUsers returns list of all registered users with usage stats.
// @Summary List Users (Admin)
// @Description Returns list of all users with their role, suspension status, total talk requests and Gemini calls.
// @Tags Admin
// @Produce json
// @Security BearerAuth
// @Success 200 {array} AdminUserItem
// @Failure 401 {object} ErrorResponse
// @Failure 403 {object} ErrorResponse
// @Router /api/v1/admin/users [get]
func (h *AdminHandler) GetUsers(c *gin.Context) {
	var users []model.User
	if err := model.DB.Order("id ASC").Find(&users).Error; err != nil {
		c.JSON(http.StatusInternalServerError, ErrorResponse{Error: "Failed to fetch users"})
		return
	}

	result := make([]AdminUserItem, 0, len(users))
	for _, u := range users {
		var talkCount int64
		model.DB.Model(&model.TalkRequest{}).Where("user_id = ?", u.ID).Count(&talkCount)

		var geminiCount int64
		model.DB.Model(&model.GeminiCallLog{}).Where("user_id = ?", u.ID).Count(&geminiCount)

		var geminiTokens int64
		model.DB.Model(&model.GeminiCallLog{}).Where("user_id = ?", u.ID).Select("COALESCE(SUM(total_tokens), 0)").Scan(&geminiTokens)

		tier := u.SubscriptionTier
		if tier == "" {
			tier = "free"
		}
		result = append(result, AdminUserItem{
			ID:               u.ID,
			Email:            u.Email,
			Nickname:         u.Nickname,
			Avatar:           u.Avatar,
			Role:             u.Role,
			SubscriptionTier: tier,
			Language:         u.Language,
			IsSuspended:      u.IsSuspended,
			CreatedAt:        u.CreatedAt,
			TalkCount:        talkCount,
			GeminiCallCount:  geminiCount,
			GeminiTokenCount: geminiTokens,
		})
	}

	c.JSON(http.StatusOK, result)
}

// GetStats returns platform-wide usage metrics.
// @Summary Platform Statistics (Admin)
// @Description Returns metrics such as user counts, talk status breakdown, and daily/monthly Gemini API calls.
// @Tags Admin
// @Produce json
// @Security BearerAuth
// @Success 200 {object} AdminStatsResponse
// @Failure 401 {object} ErrorResponse
// @Failure 403 {object} ErrorResponse
// @Router /api/v1/admin/stats [get]
func (h *AdminHandler) GetStats(c *gin.Context) {
	var totalUsers int64
	model.DB.Model(&model.User{}).Count(&totalUsers)

	var totalTalks int64
	model.DB.Model(&model.TalkRequest{}).Count(&totalTalks)

	var totalRooms int64
	model.DB.Model(&model.Room{}).Count(&totalRooms)

	statusCounts := map[string]int64{
		"pending":    0,
		"processing": 0,
		"completed":  0,
		"failed":     0,
	}

	type StatusGroup struct {
		Status string
		Count  int64
	}
	var groups []StatusGroup
	model.DB.Model(&model.TalkRequest{}).
		Select("status, count(*) as count").
		Group("status").
		Scan(&groups)

	for _, g := range groups {
		statusCounts[g.Status] = g.Count
	}

	now := time.Now()
	startOfDay := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())
	startOfMonth := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, now.Location())

	var geminiToday int64
	model.DB.Model(&model.GeminiCallLog{}).Where("created_at >= ?", startOfDay).Count(&geminiToday)

	var geminiMonth int64
	model.DB.Model(&model.GeminiCallLog{}).Where("created_at >= ?", startOfMonth).Count(&geminiMonth)

	var geminiTotal int64
	model.DB.Model(&model.GeminiCallLog{}).Count(&geminiTotal)

	var geminiTokensToday int64
	model.DB.Model(&model.GeminiCallLog{}).Where("created_at >= ?", startOfDay).Select("COALESCE(SUM(total_tokens), 0)").Scan(&geminiTokensToday)

	var geminiTokensMonth int64
	model.DB.Model(&model.GeminiCallLog{}).Where("created_at >= ?", startOfMonth).Select("COALESCE(SUM(total_tokens), 0)").Scan(&geminiTokensMonth)

	var geminiTokensTotal int64
	model.DB.Model(&model.GeminiCallLog{}).Select("COALESCE(SUM(total_tokens), 0)").Scan(&geminiTokensTotal)

	var quotaExceededUsers int64
	var allUsers []model.User
	if err := model.DB.Find(&allUsers).Error; err == nil {
		for _, u := range allUsers {
			if u.IsSuspended {
				quotaExceededUsers++
				continue
			}
			st, err := auth.GetUserUsageStats(h.cfg, u.ID)
			if err == nil && (st.DailyTokensRemaining <= 0 || st.DailyCreatesRemaining <= 0 || st.DailyEditsRemaining <= 0) {
				quotaExceededUsers++
			}
		}
	}

	c.JSON(http.StatusOK, AdminStatsResponse{
		TotalUsers:         totalUsers,
		TotalTalks:         totalTalks,
		TotalRooms:         totalRooms,
		StatusCounts:       statusCounts,
		GeminiCallsToday:   geminiToday,
		GeminiCallsMonth:   geminiMonth,
		GeminiCallsTotal:   geminiTotal,
		GeminiTokensToday:  geminiTokensToday,
		GeminiTokensMonth:  geminiTokensMonth,
		GeminiTokensTotal:  geminiTokensTotal,
		QuotaExceededUsers: quotaExceededUsers,
	})
}

// UpdateUser modifies user role or suspension state.
// @Summary Update User (Admin)
// @Description Allows admins to update a user's role ("user" or "admin") or suspend/unsuspend their account.
// @Tags Admin
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param id path int true "User ID"
// @Param request body UpdateUserRequest true "Update User Payload"
// @Success 200 {object} AdminUserItem
// @Failure 400 {object} ErrorResponse
// @Failure 403 {object} ErrorResponse
// @Failure 404 {object} ErrorResponse
// @Router /api/v1/admin/users/{id} [patch]
func (h *AdminHandler) UpdateUser(c *gin.Context) {
	currentUserIDVal, _ := c.Get("userID")
	currentUserID, _ := currentUserIDVal.(uint)

	targetIDParam := c.Param("id")
	targetID, err := strconv.ParseUint(targetIDParam, 10, 64)
	if err != nil {
		c.JSON(http.StatusBadRequest, ErrorResponse{Error: "Invalid user ID"})
		return
	}

	var user model.User
	if err := model.DB.First(&user, targetID).Error; err != nil {
		c.JSON(http.StatusNotFound, ErrorResponse{Error: "User not found"})
		return
	}

	var req UpdateUserRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, ErrorResponse{Error: err.Error()})
		return
	}

	// Prevent self-suspension or self-demotion to preserve admin access
	if uint(targetID) == currentUserID {
		if req.IsSuspended != nil && *req.IsSuspended {
			c.JSON(http.StatusBadRequest, ErrorResponse{Error: "You cannot suspend your own admin account"})
			return
		}
		if req.Role != nil && *req.Role != "admin" {
			c.JSON(http.StatusBadRequest, ErrorResponse{Error: "You cannot revoke your own administrator role"})
			return
		}
	}

	// Prevent modification of root admin account (admin@talkforge.local)
	if strings.ToLower(user.Email) == "admin@talkforge.local" {
		if (req.IsSuspended != nil && *req.IsSuspended != user.IsSuspended) || (req.Role != nil && *req.Role != user.Role) {
			c.JSON(http.StatusForbidden, ErrorResponse{Error: "The root admin account (admin@talkforge.local) role and status cannot be modified"})
			return
		}
	}

	updates := map[string]interface{}{}

	if req.Role != nil {
		if *req.Role != "user" && *req.Role != "admin" {
			c.JSON(http.StatusBadRequest, ErrorResponse{Error: "Role must be 'user' or 'admin'"})
			return
		}
		updates["role"] = *req.Role
		user.Role = *req.Role
	}

	if req.IsSuspended != nil {
		updates["is_suspended"] = *req.IsSuspended
		user.IsSuspended = *req.IsSuspended
	}

	if req.SubscriptionTier != nil {
		t := strings.ToLower(*req.SubscriptionTier)
		if t != "free" && t != "pro" && t != "enterprise" {
			c.JSON(http.StatusBadRequest, ErrorResponse{Error: "SubscriptionTier must be 'free', 'pro' or 'enterprise'"})
			return
		}
		updates["subscription_tier"] = t
		user.SubscriptionTier = t
	}

	if len(updates) > 0 {
		if err := model.DB.Model(&user).Updates(updates).Error; err != nil {
			c.JSON(http.StatusInternalServerError, ErrorResponse{Error: "Failed to update user"})
			return
		}
	}

	var talkCount int64
	model.DB.Model(&model.TalkRequest{}).Where("user_id = ?", user.ID).Count(&talkCount)

	var geminiCount int64
	model.DB.Model(&model.GeminiCallLog{}).Where("user_id = ?", user.ID).Count(&geminiCount)

	c.JSON(http.StatusOK, AdminUserItem{
		ID:              user.ID,
		Email:           user.Email,
		Nickname:        user.Nickname,
		Avatar:          user.Avatar,
		Role:            user.Role,
		Language:        user.Language,
		IsSuspended:     user.IsSuspended,
		CreatedAt:       user.CreatedAt,
		TalkCount:       talkCount,
		GeminiCallCount: geminiCount,
	})
}

// GetRooms returns all rooms with member & talk counts.
// @Summary List Rooms (Admin)
// @Description Returns list of all rooms with owner info, active member counts and total room talks.
// @Tags Admin
// @Produce json
// @Security BearerAuth
// @Success 200 {array} AdminRoomItem
// @Failure 401 {object} ErrorResponse
// @Failure 403 {object} ErrorResponse
// @Router /api/v1/admin/rooms [get]
func (h *AdminHandler) GetRooms(c *gin.Context) {
	var rooms []model.Room
	if err := model.DB.Preload("Owner").Order("id ASC").Find(&rooms).Error; err != nil {
		c.JSON(http.StatusInternalServerError, ErrorResponse{Error: "Failed to fetch rooms"})
		return
	}

	result := make([]AdminRoomItem, 0, len(rooms))
	for _, r := range rooms {
		var memberCount int64
		model.DB.Model(&model.RoomMember{}).Where("room_id = ? AND status = 'accepted'", r.ID).Count(&memberCount)

		var talkCount int64
		model.DB.Model(&model.TalkRequest{}).Where("room_id = ?", r.ID).Count(&talkCount)

		result = append(result, AdminRoomItem{
			ID:            r.ID,
			Name:          r.Name,
			OwnerID:       r.OwnerID,
			OwnerEmail:    r.Owner.Email,
			OwnerNickname: r.Owner.Nickname,
			CreatedAt:     r.CreatedAt,
			MemberCount:   memberCount,
			TalkCount:     talkCount,
		})
	}

	c.JSON(http.StatusOK, result)
}

package handler

import (
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"talkforge-be/auth"
	"talkforge-be/config"
	"talkforge-be/model"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

// RoomHandler handles shared-room (multi-user workspace) endpoints.
type RoomHandler struct {
	cfg *config.Config
}

// NewRoomHandler instantiates a new RoomHandler.
func NewRoomHandler(cfg *config.Config) *RoomHandler {
	return &RoomHandler{cfg: cfg}
}

// CreateRoomBody represents the payload to create a new room.
type CreateRoomBody struct {
	Name string `json:"name" binding:"required" example:"Pazarlama Ekibi"`
}

// InviteMemberBody represents the payload to invite a user into a room.
type InviteMemberBody struct {
	Email string `json:"email" binding:"required" example:"teammate@example.com"`
	Role  string `json:"role" binding:"required" example:"writer"` // "writer" or "reader"
}

// RoomResponse represents a room as returned to clients, including the requesting user's role in it.
type RoomResponse struct {
	ID          uint                 `json:"id"`
	Name        string               `json:"name"`
	OwnerID     uint                 `json:"owner_id"`
	Role        string               `json:"role"`
	MemberCount int64                `json:"member_count"`
	TalkCount   int64                `json:"talk_count"`
	HasUnread        bool                 `json:"has_unread"`
	UnreadCount      int64                `json:"unread_count"`
	TalkUnreadCounts map[uint]int64       `json:"talk_unread_counts,omitempty"`
	Members          []RoomMemberResponse `json:"members,omitempty"`
	CreatedAt   time.Time            `json:"created_at"`
}

// RoomMemberResponse represents a room member as returned to clients.
type RoomMemberResponse struct {
	UserID   uint   `json:"user_id"`
	Email    string `json:"email"`
	Nickname string `json:"nickname"`
	Role     string `json:"role"`
	Status   string `json:"status"`
}

// InviteResponse represents a pending room invitation as seen by the invitee.
type InviteResponse struct {
	ID        uint      `json:"id"`
	RoomID    uint      `json:"room_id"`
	RoomName  string    `json:"room_name"`
	InvitedBy string    `json:"invited_by"`
	Role      string    `json:"role"`
	Status    string    `json:"status"`
	CreatedAt time.Time `json:"created_at"`
}

// isRoomMember looks up a user's accepted role within a room.
// Returns isMember=false if the user is not an accepted member.
func isRoomMember(userID uint, roomID uint) (role string, isMember bool) {
	var m model.RoomMember
	if err := model.DB.Where("room_id = ? AND user_id = ? AND status = 'accepted'", roomID, userID).First(&m).Error; err != nil {
		return "", false
	}
	return m.Role, true
}

// canAccessTalk reports whether userID may access talk: always true for the owner,
// otherwise true only if talk belongs to a room the user is an accepted member of
// (and, when requireWrite is set, only if their role in that room is "writer").
func canAccessTalk(userID uint, talk model.TalkRequest, requireWrite bool) bool {
	if talk.UserID == userID {
		return true
	}
	if talk.RoomID == nil {
		return false
	}
	role, member := isRoomMember(userID, *talk.RoomID)
	if !member {
		return false
	}
	if requireWrite {
		return role == "writer"
	}
	return true
}

// CreateRoom creates a new shared room. The creator is automatically added as an accepted writer member.
// @Summary Create a shared room
// @Description Creates a room that talks can be shared inside; the creator becomes its first writer member.
// @Tags Rooms
// @Security BearerAuth
// @Accept json
// @Produce json
// @Param request body CreateRoomBody true "Create Room Payload"
// @Success 201 {object} model.Room
// @Failure 400 {object} ErrorResponse
// @Failure 401 {object} ErrorResponse
// @Router /api/v1/rooms [post]
func (h *RoomHandler) CreateRoom(c *gin.Context) {
	userID, exists := c.Get("userID")
	if !exists {
		c.JSON(http.StatusUnauthorized, ErrorResponse{Error: "Unauthorized context missing"})
		return
	}

	var req CreateRoomBody
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, ErrorResponse{Error: err.Error()})
		return
	}

	var roomsUsed int64
	model.DB.Model(&model.RoomMember{}).Where("user_id = ? AND status = 'accepted'", userID.(uint)).Count(&roomsUsed)

	stats, err := auth.GetUserUsageStats(h.cfg, userID.(uint))
	limit := int64(1)
	if err == nil {
		limit = int64(stats.RoomsLimit)
	}

	if roomsUsed >= limit {
		c.JSON(http.StatusBadRequest, ErrorResponse{Error: fmt.Sprintf("En fazla %d odada yer alabilirsiniz. Oda limitinize ulaştınız.", limit)})
		return
	}

	room := model.Room{Name: req.Name, OwnerID: userID.(uint)}
	if err := model.DB.Create(&room).Error; err != nil {
		c.JSON(http.StatusInternalServerError, ErrorResponse{Error: "Failed to create room: " + err.Error()})
		return
	}

	// Room owner is always accepted immediately.
	uid := userID.(uint)
	member := model.RoomMember{RoomID: room.ID, UserID: &uid, Role: "writer", Status: "accepted"}
	if err := model.DB.Create(&member).Error; err != nil {
		c.JSON(http.StatusInternalServerError, ErrorResponse{Error: "Failed to add owner as member: " + err.Error()})
		return
	}

	c.JSON(http.StatusCreated, room)
}

// InviteMember sends an invitation (status=pending) to a user by email.
// If the email belongs to an existing user, a pending RoomMember is created for them.
// If the email is unknown, a placeholder invite is stored that will be linked when they sign up.
// Only existing accepted writer members (including the owner) may invite others.
// @Summary Invite a member into a room
// @Description Creates a pending invitation for a user by email. Requires the requester to be an accepted writer member.
// @Tags Rooms
// @Security BearerAuth
// @Accept json
// @Produce json
// @Param id path int true "Room ID"
// @Param request body InviteMemberBody true "Invite Payload"
// @Success 201 {object} model.RoomMember
// @Failure 400 {object} ErrorResponse
// @Failure 403 {object} ErrorResponse
// @Failure 404 {object} ErrorResponse
// @Router /api/v1/rooms/{id}/members [post]
func (h *RoomHandler) InviteMember(c *gin.Context) {
	userID, exists := c.Get("userID")
	if !exists {
		c.JSON(http.StatusUnauthorized, ErrorResponse{Error: "Unauthorized context missing"})
		return
	}

	roomID, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil {
		c.JSON(http.StatusBadRequest, ErrorResponse{Error: "Invalid room ID"})
		return
	}

	var req InviteMemberBody
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, ErrorResponse{Error: err.Error()})
		return
	}
	if req.Role != "writer" && req.Role != "reader" {
		c.JSON(http.StatusBadRequest, ErrorResponse{Error: "role must be 'writer' or 'reader'"})
		return
	}

	// Only accepted writer members can send invites.
	if role, member := isRoomMember(userID.(uint), uint(roomID)); !member || role != "writer" {
		c.JSON(http.StatusForbidden, ErrorResponse{Error: "only accepted writer members can invite people to this room"})
		return
	}

	// Look up the invitee.
	var invitee model.User
	userFound := model.DB.Where("email = ?", req.Email).First(&invitee).Error == nil

	if userFound {
		// Check for an existing membership (any status).
		var existing model.RoomMember
		err = model.DB.Where("room_id = ? AND user_id = ?", roomID, invitee.ID).First(&existing).Error
		if err == nil {
			// Already has a record; if accepted leave it alone, otherwise re-invite.
			if existing.Status == "accepted" {
				c.JSON(http.StatusConflict, ErrorResponse{Error: "user is already an accepted member of this room"})
				return
			}
			existing.Role = req.Role
			existing.Status = "pending"
			if err := model.DB.Save(&existing).Error; err != nil {
				c.JSON(http.StatusInternalServerError, ErrorResponse{Error: "Failed to update invite: " + err.Error()})
				return
			}
			c.JSON(http.StatusOK, existing)
			return
		}

		// New invite for an existing user.
		member := model.RoomMember{RoomID: uint(roomID), UserID: &invitee.ID, Role: req.Role, Status: "pending"}
		if err := model.DB.Create(&member).Error; err != nil {
			c.JSON(http.StatusInternalServerError, ErrorResponse{Error: "Failed to create invite: " + err.Error()})
			return
		}
		c.JSON(http.StatusCreated, member)
		return
	}

	// Invitee not found — disallow sending invite to unregistered user.
	c.JSON(http.StatusNotFound, ErrorResponse{Error: "Bu e-posta adresine sahip kayıtlı bir kullanıcı bulunamadı."})
}

// ListRooms returns rooms the authenticated user is an accepted member of, with their role in each.
// @Summary List my rooms
// @Description Returns every room the authenticated user is an accepted member of.
// @Tags Rooms
// @Security BearerAuth
// @Produce json
// @Success 200 {array} RoomResponse
// @Failure 401 {object} ErrorResponse
// @Router /api/v1/rooms [get]
func (h *RoomHandler) ListRooms(c *gin.Context) {
	userID, exists := c.Get("userID")
	if !exists {
		c.JSON(http.StatusUnauthorized, ErrorResponse{Error: "Unauthorized context missing"})
		return
	}

	var memberships []model.RoomMember
	if err := model.DB.Where("user_id = ? AND status = 'accepted'", userID.(uint)).Find(&memberships).Error; err != nil {
		c.JSON(http.StatusInternalServerError, ErrorResponse{Error: "Failed to query memberships: " + err.Error()})
		return
	}

	roomIDs := make([]uint, 0, len(memberships))
	roleByRoom := make(map[uint]string)
	for _, m := range memberships {
		roomIDs = append(roomIDs, m.RoomID)
		roleByRoom[m.RoomID] = m.Role
	}

	resp := make([]RoomResponse, 0, len(roomIDs))
	if len(roomIDs) > 0 {
		var rooms []model.Room
		if err := model.DB.Where("id IN ?", roomIDs).Find(&rooms).Error; err != nil {
			c.JSON(http.StatusInternalServerError, ErrorResponse{Error: "Failed to query rooms: " + err.Error()})
			return
		}

		memberCounts, talkCounts := roomCounts(roomIDs)
		unreadCounts := roomUnreadCounts(userID.(uint), memberships)

		for _, r := range rooms {
			uCount := unreadCounts[r.ID]
			resp = append(resp, RoomResponse{
				ID: r.ID, Name: r.Name, OwnerID: r.OwnerID, Role: roleByRoom[r.ID],
				MemberCount: memberCounts[r.ID], TalkCount: talkCounts[r.ID],
				HasUnread: uCount > 0, UnreadCount: uCount,
				CreatedAt: r.CreatedAt,
			})
		}
	}

	c.JSON(http.StatusOK, resp)
}

// roomUnreadCounts calculates the number of unread talks/messages per room for a given user.
func roomUnreadCounts(userID uint, memberships []model.RoomMember) map[uint]int64 {
	unreadCounts := make(map[uint]int64)
	for _, m := range memberships {
		lastRead := m.LastReadAt
		var talkCount int64
		model.DB.Model(&model.TalkRequest{}).
			Where("room_id = ? AND user_id != ? AND (created_at > ? OR updated_at > ?)", m.RoomID, userID, lastRead, lastRead).
			Count(&talkCount)

		var msgCount int64
		model.DB.Model(&model.RoomMessage{}).
			Where("room_id = ? AND user_id != ? AND created_at > ?", m.RoomID, userID, lastRead).
			Count(&msgCount)

		unreadCounts[m.RoomID] = talkCount + msgCount
	}
	return unreadCounts
}

// getRoomTalkUnreadCounts calculates the number of unread updates (versions + messages) for each talk in a room for a given user.
func getRoomTalkUnreadCounts(roomID uint, userID uint, lastRead time.Time) map[uint]int64 {
	talkUnreadCounts := make(map[uint]int64)

	var allReqs []model.TalkRequest
	model.DB.Unscoped().Find(&allReqs)
	allReqsMap := make(map[uint]model.TalkRequest)
	for _, r := range allReqs {
		allReqsMap[r.ID] = r
	}

	// 1. Unread talk request versions in this room created/updated by other users since lastRead
	var talkReqs []model.TalkRequest
	model.DB.Where("room_id = ? AND user_id != ? AND (created_at > ? OR updated_at > ?)", roomID, userID, lastRead, lastRead).Find(&talkReqs)

	for _, tr := range talkReqs {
		rootID := getUltimateRootID(tr.ID, allReqsMap)
		talkUnreadCounts[rootID]++
	}

	// 2. Unread discussion messages in this room created by other users since lastRead
	var roomMsgs []model.RoomMessage
	model.DB.Where("room_id = ? AND user_id != ? AND created_at > ?", roomID, userID, lastRead).Find(&roomMsgs)

	for _, msg := range roomMsgs {
		rootID := getUltimateRootID(msg.TalkRequestID, allReqsMap)
		talkUnreadCounts[rootID]++
	}

	return talkUnreadCounts
}


// roomCounts returns accepted-member and talk counts for each of the given rooms in two batched queries.
func roomCounts(roomIDs []uint) (memberCounts map[uint]int64, talkCounts map[uint]int64) {
	memberCounts = make(map[uint]int64)
	talkCounts = make(map[uint]int64)

	var memberRows []struct {
		RoomID uint
		Count  int64
	}
	// Count only accepted members.
	model.DB.Model(&model.RoomMember{}).Select("room_id, count(*) as count").
		Where("room_id IN ? AND status = 'accepted'", roomIDs).Group("room_id").Scan(&memberRows)
	for _, row := range memberRows {
		memberCounts[row.RoomID] = row.Count
	}

	var talkRows []struct {
		RoomID uint
		Count  int64
	}
	model.DB.Model(&model.TalkRequest{}).Select("room_id, count(*) as count").Where("room_id IN ?", roomIDs).Group("room_id").Scan(&talkRows)
	for _, row := range talkRows {
		talkCounts[row.RoomID] = row.Count
	}

	return memberCounts, talkCounts
}

// GetRoom returns room details including its accepted member list. Requires the requester to be an accepted member.
// @Summary Get room details
// @Description Returns a room's details and accepted member list. Requires accepted membership.
// @Tags Rooms
// @Security BearerAuth
// @Produce json
// @Param id path int true "Room ID"
// @Success 200 {object} RoomResponse
// @Failure 403 {object} ErrorResponse
// @Failure 404 {object} ErrorResponse
// @Router /api/v1/rooms/{id} [get]
func (h *RoomHandler) GetRoom(c *gin.Context) {
	userID, exists := c.Get("userID")
	if !exists {
		c.JSON(http.StatusUnauthorized, ErrorResponse{Error: "Unauthorized context missing"})
		return
	}

	roomID, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil {
		c.JSON(http.StatusBadRequest, ErrorResponse{Error: "Invalid room ID"})
		return
	}

	role, member := isRoomMember(userID.(uint), uint(roomID))
	if !member {
		c.JSON(http.StatusForbidden, ErrorResponse{Error: "you are not an accepted member of this room"})
		return
	}

	// Retrieve member record to read previous LastReadAt before updating it
	var rm model.RoomMember
	model.DB.Where("room_id = ? AND user_id = ?", roomID, userID.(uint)).First(&rm)
	lastRead := rm.LastReadAt

	// Calculate per-talk unread counts for this room relative to previous lastRead
	talkUnreadCounts := getRoomTalkUnreadCounts(uint(roomID), userID.(uint), lastRead)

	// Mark room as read for this member
	model.DB.Model(&model.RoomMember{}).
		Where("room_id = ? AND user_id = ?", roomID, userID.(uint)).
		Update("last_read_at", time.Now())

	var room model.Room
	if err := model.DB.First(&room, roomID).Error; err != nil {
		c.JSON(http.StatusNotFound, ErrorResponse{Error: "room not found"})
		return
	}

	// List accepted and pending members in the room detail view.
	var members []model.RoomMember
	model.DB.Where("room_id = ? AND status != 'declined'", roomID).Find(&members)

	memberResp := make([]RoomMemberResponse, 0, len(members))
	for _, m := range members {
		if m.UserID == nil {
			if m.InvitedEmail != "" {
				memberResp = append(memberResp, RoomMemberResponse{
					UserID: 0, Email: m.InvitedEmail, Nickname: m.InvitedEmail, Role: m.Role, Status: m.Status,
				})
			}
			continue
		}
		var u model.User
		if err := model.DB.First(&u, *m.UserID).Error; err == nil {
			memberResp = append(memberResp, RoomMemberResponse{
				UserID: u.ID, Email: u.Email, Nickname: u.Nickname, Role: m.Role, Status: m.Status,
			})
		}
	}

	var talkCount int64
	model.DB.Model(&model.TalkRequest{}).Where("room_id = ?", roomID).Count(&talkCount)

	c.JSON(http.StatusOK, RoomResponse{
		ID: room.ID, Name: room.Name, OwnerID: room.OwnerID, Role: role,
		MemberCount: int64(len(memberResp)), TalkCount: talkCount,
		TalkUnreadCounts: talkUnreadCounts,
		Members: memberResp, CreatedAt: room.CreatedAt,
	})
}

// ListInvites returns pending room invitations for the authenticated user.
// @Summary List my pending invitations
// @Description Returns all pending room invitations addressed to the authenticated user.
// @Tags Invites
// @Security BearerAuth
// @Produce json
// @Success 200 {array} InviteResponse
// @Failure 401 {object} ErrorResponse
// @Router /api/v1/invites [get]
func (h *RoomHandler) ListInvites(c *gin.Context) {
	userID, exists := c.Get("userID")
	if !exists {
		c.JSON(http.StatusUnauthorized, ErrorResponse{Error: "Unauthorized context missing"})
		return
	}

	var memberships []model.RoomMember
	if err := model.DB.Where("user_id = ? AND status = 'pending'", userID.(uint)).Find(&memberships).Error; err != nil {
		c.JSON(http.StatusInternalServerError, ErrorResponse{Error: "Failed to query invites: " + err.Error()})
		return
	}

	resp := make([]InviteResponse, 0, len(memberships))
	for _, m := range memberships {
		var room model.Room
		if err := model.DB.First(&room, m.RoomID).Error; err != nil {
			continue
		}
		var inviter model.User
		invitedBy := ""
		if err := model.DB.First(&inviter, room.OwnerID).Error; err == nil {
			if inviter.Nickname != "" {
				invitedBy = inviter.Nickname
			} else {
				invitedBy = inviter.Email
			}
		}

		resp = append(resp, InviteResponse{
			ID:        m.ID,
			RoomID:    m.RoomID,
			RoomName:  room.Name,
			InvitedBy: invitedBy,
			Role:      m.Role,
			Status:    m.Status,
			CreatedAt: m.CreatedAt,
		})
	}

	c.JSON(http.StatusOK, resp)
}

// AcceptInvite accepts a pending room invitation.
// @Summary Accept a room invitation
// @Description Sets the invitation status to "accepted", granting the user access to the room.
// @Tags Invites
// @Security BearerAuth
// @Produce json
// @Param id path int true "Invite (RoomMember) ID"
// @Success 200 {object} InviteResponse
// @Failure 403 {object} ErrorResponse
// @Failure 404 {object} ErrorResponse
// @Router /api/v1/invites/{id}/accept [post]
func (h *RoomHandler) AcceptInvite(c *gin.Context) {
	h.respondInvite(c, "accepted")
}

// DeclineInvite declines a pending room invitation.
// @Summary Decline a room invitation
// @Description Sets the invitation status to "declined". The user will not be added to the room.
// @Tags Invites
// @Security BearerAuth
// @Produce json
// @Param id path int true "Invite (RoomMember) ID"
// @Success 200 {object} InviteResponse
// @Failure 403 {object} ErrorResponse
// @Failure 404 {object} ErrorResponse
// @Router /api/v1/invites/{id}/decline [post]
func (h *RoomHandler) DeclineInvite(c *gin.Context) {
	h.respondInvite(c, "declined")
}

// respondInvite is a shared helper for AcceptInvite and DeclineInvite.
func (h *RoomHandler) respondInvite(c *gin.Context, newStatus string) {
	userID, exists := c.Get("userID")
	if !exists {
		c.JSON(http.StatusUnauthorized, ErrorResponse{Error: "Unauthorized context missing"})
		return
	}

	inviteID, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil {
		c.JSON(http.StatusBadRequest, ErrorResponse{Error: "Invalid invite ID"})
		return
	}

	var m model.RoomMember
	if err := model.DB.First(&m, inviteID).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			c.JSON(http.StatusNotFound, ErrorResponse{Error: "invite not found"})
			return
		}
		c.JSON(http.StatusInternalServerError, ErrorResponse{Error: err.Error()})
		return
	}

	// Ensure the invite belongs to the authenticated user.
	if m.UserID == nil || *m.UserID != userID.(uint) {
		c.JSON(http.StatusForbidden, ErrorResponse{Error: "this invite does not belong to you"})
		return
	}

	if m.Status != "pending" {
		c.JSON(http.StatusBadRequest, ErrorResponse{Error: "invite is no longer pending"})
		return
	}

	if newStatus == "accepted" {
		var roomsUsed int64
		model.DB.Model(&model.RoomMember{}).Where("user_id = ? AND status = 'accepted'", userID.(uint)).Count(&roomsUsed)

		stats, err := auth.GetUserUsageStats(h.cfg, userID.(uint))
		limit := int64(1)
		if err == nil {
			limit = int64(stats.RoomsLimit)
		}

		if roomsUsed >= limit {
			c.JSON(http.StatusBadRequest, ErrorResponse{Error: fmt.Sprintf("En fazla %d odada yer alabilirsiniz. Oda limitinize ulaştınız.", limit)})
			return
		}
	}

	m.Status = newStatus
	if err := model.DB.Save(&m).Error; err != nil {
		c.JSON(http.StatusInternalServerError, ErrorResponse{Error: "Failed to update invite: " + err.Error()})
		return
	}

	var room model.Room
	model.DB.First(&room, m.RoomID)

	var inviter model.User
	invitedBy := ""
	if err := model.DB.First(&inviter, room.OwnerID).Error; err == nil {
		if inviter.Nickname != "" {
			invitedBy = inviter.Nickname
		} else {
			invitedBy = inviter.Email
		}
	}

	c.JSON(http.StatusOK, InviteResponse{
		ID:        m.ID,
		RoomID:    m.RoomID,
		RoomName:  room.Name,
		InvitedBy: invitedBy,
		Role:      m.Role,
		Status:    m.Status,
		CreatedAt: m.CreatedAt,
	})
}

// LeaveRoom removes the authenticated user from the specified room.
// If the user is the owner, ownership is transferred to the oldest remaining accepted member,
// or the room is deleted if no accepted members remain.
// @Summary Leave a room
// @Description Removes the requester from a room. Transfers ownership or deletes room if owner leaves.
// @Tags Rooms
// @Security BearerAuth
// @Produce json
// @Param id path int true "Room ID"
// @Success 200 {object} map[string]string
// @Failure 400 {object} ErrorResponse
// @Failure 404 {object} ErrorResponse
// @Router /api/v1/rooms/{id}/leave [post]
func (h *RoomHandler) LeaveRoom(c *gin.Context) {
	userID, exists := c.Get("userID")
	if !exists {
		c.JSON(http.StatusUnauthorized, ErrorResponse{Error: "Unauthorized context missing"})
		return
	}

	roomID, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil {
		c.JSON(http.StatusBadRequest, ErrorResponse{Error: "Invalid room ID"})
		return
	}

	uid := userID.(uint)

	var room model.Room
	if err := model.DB.First(&room, roomID).Error; err != nil {
		c.JSON(http.StatusNotFound, ErrorResponse{Error: "room not found"})
		return
	}

	var member model.RoomMember
	if err := model.DB.Where("room_id = ? AND user_id = ?", roomID, uid).First(&member).Error; err != nil {
		c.JSON(http.StatusBadRequest, ErrorResponse{Error: "you are not a member of this room"})
		return
	}

	if err := model.DB.Delete(&member).Error; err != nil {
		c.JSON(http.StatusInternalServerError, ErrorResponse{Error: "Failed to leave room: " + err.Error()})
		return
	}

	if room.OwnerID == uid {
		var nextMember model.RoomMember
		err := model.DB.Where("room_id = ? AND status = 'accepted'", roomID).Order("created_at asc").First(&nextMember).Error
		if err == nil && nextMember.UserID != nil {
			room.OwnerID = *nextMember.UserID
			model.DB.Save(&room)
			nextMember.Role = "writer"
			model.DB.Save(&nextMember)
		} else {
			model.DB.Where("room_id = ?", roomID).Delete(&model.RoomMember{})
			model.DB.Where("room_id = ?", roomID).Delete(&model.RoomMessage{})
			model.DB.Model(&model.TalkRequest{}).Where("room_id = ?", roomID).Update("room_id", nil)
			model.DB.Delete(&room)
		}
	}

	c.JSON(http.StatusOK, gin.H{"message": "Successfully left the room"})
}


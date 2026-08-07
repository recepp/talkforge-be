package handler

import (
	"net/http"
	"strconv"
	"time"

	"talkforge-be/model"
	"github.com/gin-gonic/gin"
)

// RoomHandler handles shared-room (multi-user workspace) endpoints.
type RoomHandler struct{}

// NewRoomHandler instantiates a new RoomHandler.
func NewRoomHandler() *RoomHandler {
	return &RoomHandler{}
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
	Members     []RoomMemberResponse `json:"members,omitempty"`
	CreatedAt   time.Time            `json:"created_at"`
}

// RoomMemberResponse represents a room member as returned to clients.
type RoomMemberResponse struct {
	UserID   uint   `json:"user_id"`
	Email    string `json:"email"`
	Nickname string `json:"nickname"`
	Role     string `json:"role"`
}

// isRoomMember looks up a user's role within a room. Returns isMember=false if not a member.
func isRoomMember(userID uint, roomID uint) (role string, isMember bool) {
	var m model.RoomMember
	if err := model.DB.Where("room_id = ? AND user_id = ?", roomID, userID).First(&m).Error; err != nil {
		return "", false
	}
	return m.Role, true
}

// canAccessTalk reports whether userID may access talk: always true for the owner,
// otherwise true only if talk belongs to a room the user is a member of (and, when
// requireWrite is set, only if their role in that room is "writer").
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

// CreateRoom creates a new shared room. The creator is automatically added as a writer member.
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

	room := model.Room{Name: req.Name, OwnerID: userID.(uint)}
	if err := model.DB.Create(&room).Error; err != nil {
		c.JSON(http.StatusInternalServerError, ErrorResponse{Error: "Failed to create room: " + err.Error()})
		return
	}

	member := model.RoomMember{RoomID: room.ID, UserID: userID.(uint), Role: "writer"}
	if err := model.DB.Create(&member).Error; err != nil {
		c.JSON(http.StatusInternalServerError, ErrorResponse{Error: "Failed to add owner as member: " + err.Error()})
		return
	}

	c.JSON(http.StatusCreated, room)
}

// InviteMember adds a user (looked up by email) to a room with the given role.
// Only existing writer members (including the owner) may invite others.
// @Summary Invite a member into a room
// @Description Adds (or updates the role of) a user in a room by email. Requires the requester to be a writer member.
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

	if role, member := isRoomMember(userID.(uint), uint(roomID)); !member || role != "writer" {
		c.JSON(http.StatusForbidden, ErrorResponse{Error: "only writer members can invite people to this room"})
		return
	}

	var invitee model.User
	if err := model.DB.Where("email = ?", req.Email).First(&invitee).Error; err != nil {
		c.JSON(http.StatusNotFound, ErrorResponse{Error: "user with that email not found"})
		return
	}

	var existing model.RoomMember
	err = model.DB.Where("room_id = ? AND user_id = ?", roomID, invitee.ID).First(&existing).Error
	if err == nil {
		existing.Role = req.Role
		if err := model.DB.Save(&existing).Error; err != nil {
			c.JSON(http.StatusInternalServerError, ErrorResponse{Error: "Failed to update member role: " + err.Error()})
			return
		}
		c.JSON(http.StatusOK, existing)
		return
	}

	member := model.RoomMember{RoomID: uint(roomID), UserID: invitee.ID, Role: req.Role}
	if err := model.DB.Create(&member).Error; err != nil {
		c.JSON(http.StatusInternalServerError, ErrorResponse{Error: "Failed to add member: " + err.Error()})
		return
	}

	c.JSON(http.StatusCreated, member)
}

// ListRooms returns rooms the authenticated user is a member of, with their role in each.
// @Summary List my rooms
// @Description Returns every room the authenticated user is a member of.
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
	if err := model.DB.Where("user_id = ?", userID.(uint)).Find(&memberships).Error; err != nil {
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

		for _, r := range rooms {
			resp = append(resp, RoomResponse{
				ID: r.ID, Name: r.Name, OwnerID: r.OwnerID, Role: roleByRoom[r.ID],
				MemberCount: memberCounts[r.ID], TalkCount: talkCounts[r.ID],
				CreatedAt: r.CreatedAt,
			})
		}
	}

	c.JSON(http.StatusOK, resp)
}

// roomCounts returns member and talk counts for each of the given rooms in two batched queries.
func roomCounts(roomIDs []uint) (memberCounts map[uint]int64, talkCounts map[uint]int64) {
	memberCounts = make(map[uint]int64)
	talkCounts = make(map[uint]int64)

	var memberRows []struct {
		RoomID uint
		Count  int64
	}
	model.DB.Model(&model.RoomMember{}).Select("room_id, count(*) as count").Where("room_id IN ?", roomIDs).Group("room_id").Scan(&memberRows)
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

// GetRoom returns room details including its member list. Requires the requester to be a member.
// @Summary Get room details
// @Description Returns a room's details and member list. Requires membership.
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
		c.JSON(http.StatusForbidden, ErrorResponse{Error: "you are not a member of this room"})
		return
	}

	var room model.Room
	if err := model.DB.First(&room, roomID).Error; err != nil {
		c.JSON(http.StatusNotFound, ErrorResponse{Error: "room not found"})
		return
	}

	var members []model.RoomMember
	model.DB.Where("room_id = ?", roomID).Find(&members)

	memberResp := make([]RoomMemberResponse, 0, len(members))
	for _, m := range members {
		var u model.User
		if err := model.DB.First(&u, m.UserID).Error; err == nil {
			memberResp = append(memberResp, RoomMemberResponse{
				UserID: u.ID, Email: u.Email, Nickname: u.Nickname, Role: m.Role,
			})
		}
	}

	var talkCount int64
	model.DB.Model(&model.TalkRequest{}).Where("room_id = ?", roomID).Count(&talkCount)

	c.JSON(http.StatusOK, RoomResponse{
		ID: room.ID, Name: room.Name, OwnerID: room.OwnerID, Role: role,
		MemberCount: int64(len(memberResp)), TalkCount: talkCount,
		Members: memberResp, CreatedAt: room.CreatedAt,
	})
}

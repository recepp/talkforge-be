package handler

import (
	"context"
	"fmt"
	"net/http"
	"strconv"

	"talkforge-be/config"
	"talkforge-be/model"

	"github.com/gin-gonic/gin"
	"github.com/google/generative-ai-go/genai"
	"google.golang.org/api/option"
)

// DiscussionHandler handles a room's per-talk discussion thread and the
// "summarize the discussion into a new version" action.
type DiscussionHandler struct {
	cfg *config.Config
}

// NewDiscussionHandler instantiates a new DiscussionHandler.
func NewDiscussionHandler(cfg *config.Config) *DiscussionHandler {
	return &DiscussionHandler{cfg: cfg}
}

// PostMessageBody represents the payload to post a discussion message.
type PostMessageBody struct {
	Text string `json:"text" binding:"required" example:"Girişi biraz daha resmi yapalım mı?"`
}

// RoomMessageResponse represents a discussion message as returned to clients.
type RoomMessageResponse struct {
	model.RoomMessage
	Nickname string `json:"nickname"`
	Avatar   string `json:"avatar"`
}

// talkRoomOrForbidden loads the talk by id and confirms it belongs to a room
// the requesting user is a member of; writes the appropriate error response
// and returns ok=false if not.
func (h *DiscussionHandler) talkRoomOrForbidden(c *gin.Context, userID uint) (model.TalkRequest, bool) {
	id, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil {
		c.JSON(http.StatusBadRequest, ErrorResponse{Error: "Invalid talk request ID"})
		return model.TalkRequest{}, false
	}

	var talk model.TalkRequest
	if err := model.DB.First(&talk, uint(id)).Error; err != nil {
		c.JSON(http.StatusNotFound, ErrorResponse{Error: "talk request not found"})
		return model.TalkRequest{}, false
	}

	if talk.RoomID == nil {
		c.JSON(http.StatusBadRequest, ErrorResponse{Error: "this talk is not shared inside a room, there is no discussion thread for it"})
		return model.TalkRequest{}, false
	}

	if _, member := isRoomMember(userID, *talk.RoomID); !member {
		c.JSON(http.StatusForbidden, ErrorResponse{Error: "you are not a member of this talk's room"})
		return model.TalkRequest{}, false
	}

	return talk, true
}

// ListMessages returns a talk's discussion thread, oldest first.
// @Summary List a talk's discussion messages
// @Description Returns the discussion thread for a room-shared talk. Requires room membership.
// @Tags Discussion
// @Security BearerAuth
// @Produce json
// @Param id path int true "Talk Request ID"
// @Success 200 {array} RoomMessageResponse
// @Failure 400 {object} ErrorResponse
// @Failure 403 {object} ErrorResponse
// @Failure 404 {object} ErrorResponse
// @Router /api/v1/talks/{id}/messages [get]
func (h *DiscussionHandler) ListMessages(c *gin.Context) {
	userID, exists := c.Get("userID")
	if !exists {
		c.JSON(http.StatusUnauthorized, ErrorResponse{Error: "Unauthorized context missing"})
		return
	}

	talk, ok := h.talkRoomOrForbidden(c, userID.(uint))
	if !ok {
		return
	}

	var messages []model.RoomMessage
	if err := model.DB.Where("talk_request_id = ?", talk.ID).Order("created_at asc").Find(&messages).Error; err != nil {
		c.JSON(http.StatusInternalServerError, ErrorResponse{Error: "Failed to query messages: " + err.Error()})
		return
	}

	resp := make([]RoomMessageResponse, 0, len(messages))
	for _, m := range messages {
		var u model.User
		nickname, avatar := "", ""
		if err := model.DB.First(&u, m.UserID).Error; err == nil {
			nickname, avatar = u.Nickname, u.Avatar
		}
		resp = append(resp, RoomMessageResponse{RoomMessage: m, Nickname: nickname, Avatar: avatar})
	}

	c.JSON(http.StatusOK, resp)
}

// PostMessage adds a message to a talk's discussion thread. Any room member (writer or reader) may post.
// @Summary Post a discussion message
// @Description Adds a message to a room-shared talk's discussion thread. Requires room membership.
// @Tags Discussion
// @Security BearerAuth
// @Accept json
// @Produce json
// @Param id path int true "Talk Request ID"
// @Param request body PostMessageBody true "Message Payload"
// @Success 201 {object} model.RoomMessage
// @Failure 400 {object} ErrorResponse
// @Failure 403 {object} ErrorResponse
// @Failure 404 {object} ErrorResponse
// @Router /api/v1/talks/{id}/messages [post]
func (h *DiscussionHandler) PostMessage(c *gin.Context) {
	userID, exists := c.Get("userID")
	if !exists {
		c.JSON(http.StatusUnauthorized, ErrorResponse{Error: "Unauthorized context missing"})
		return
	}

	talk, ok := h.talkRoomOrForbidden(c, userID.(uint))
	if !ok {
		return
	}

	var req PostMessageBody
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, ErrorResponse{Error: err.Error()})
		return
	}

	msg := model.RoomMessage{
		RoomID:        *talk.RoomID,
		TalkRequestID: talk.ID,
		UserID:        userID.(uint),
		Text:          req.Text,
	}
	if err := model.DB.Create(&msg).Error; err != nil {
		c.JSON(http.StatusInternalServerError, ErrorResponse{Error: "Failed to post message: " + err.Error()})
		return
	}

	c.JSON(http.StatusCreated, msg)
}

// SummarizeAndUpdate summarizes a talk's discussion thread with Gemini and queues
// a new "update" version using that summary as the rewrite instruction. Only
// writer room members may trigger this (it consumes generation quota).
// @Summary Summarize discussion into a new version
// @Description Summarizes the talk's discussion thread and queues a new version using the summary as the update instruction. Requires writer room membership.
// @Tags Discussion
// @Security BearerAuth
// @Produce json
// @Param id path int true "Talk Request ID"
// @Success 201 {object} model.TalkRequest
// @Failure 400 {object} ErrorResponse
// @Failure 403 {object} ErrorResponse
// @Failure 404 {object} ErrorResponse
// @Router /api/v1/talks/{id}/messages/summarize [post]
func (h *DiscussionHandler) SummarizeAndUpdate(c *gin.Context) {
	userID, exists := c.Get("userID")
	if !exists {
		c.JSON(http.StatusUnauthorized, ErrorResponse{Error: "Unauthorized context missing"})
		return
	}

	talk, ok := h.talkRoomOrForbidden(c, userID.(uint))
	if !ok {
		return
	}

	if role, _ := isRoomMember(userID.(uint), *talk.RoomID); role != "writer" {
		c.JSON(http.StatusForbidden, ErrorResponse{Error: "only writer members can generate a new version from the discussion"})
		return
	}

	if talk.Status != "completed" {
		c.JSON(http.StatusBadRequest, ErrorResponse{Error: "cannot update a talk that has not completed successfully"})
		return
	}

	var messages []model.RoomMessage
	if err := model.DB.Where("talk_request_id = ?", talk.ID).Order("created_at asc").Find(&messages).Error; err != nil {
		c.JSON(http.StatusInternalServerError, ErrorResponse{Error: "Failed to query messages: " + err.Error()})
		return
	}
	if len(messages) == 0 {
		c.JSON(http.StatusBadRequest, ErrorResponse{Error: "there is no discussion to summarize yet"})
		return
	}

	if h.cfg.GeminiAPIKey == "" {
		c.JSON(http.StatusInternalServerError, ErrorResponse{Error: "GEMINI_API_KEY is not configured"})
		return
	}

	var transcript string
	for _, m := range messages {
		var u model.User
		name := fmt.Sprintf("User#%d", m.UserID)
		if err := model.DB.First(&u, m.UserID).Error; err == nil && u.Nickname != "" {
			name = u.Nickname
		}
		transcript += fmt.Sprintf("%s: %s\n", name, m.Text)
	}

	summary, err := h.summarizeDiscussion(c.Request.Context(), userID.(uint), transcript)
	if err != nil {
		c.JSON(http.StatusInternalServerError, ErrorResponse{Error: "Failed to summarize discussion: " + err.Error()})
		return
	}


	// From here it's the same shape as a regular "update" request — same
	// version numbering, same worker pipeline. Only the instruction's source differs.
	rootID := talk.ID
	current := talk
	for current.ParentID != nil {
		var ancestor model.TalkRequest
		if err := model.DB.Unscoped().First(&ancestor, *current.ParentID).Error; err != nil {
			break
		}
		current = ancestor
	}
	rootID = current.ID

	var maxVersion int
	model.DB.Unscoped().Raw(`
		WITH RECURSIVE tree AS (
			SELECT id, version_number FROM talk_requests WHERE id = ?
			UNION ALL
			SELECT tr.id, tr.version_number FROM talk_requests tr
			INNER JOIN tree ON tr.parent_id = tree.id
		)
		SELECT COALESCE(MAX(version_number), 0) FROM tree`, rootID).Scan(&maxVersion)

	newTalk := model.TalkRequest{
		UserID:           userID.(uint),
		Mode:             "update",
		Status:           "pending",
		Language:         talk.Language,
		Place:            talk.Place,
		Topic:            talk.Topic,
		Duration:         talk.Duration,
		SpeechType:       talk.SpeechType,
		CustomSpeechType: talk.CustomSpeechType,
		VersionNumber:    maxVersion + 1,
		ParentID:         &talk.ID,
		RoomID:           talk.RoomID,
		Instruction:      summary,
	}

	if err := model.DB.Create(&newTalk).Error; err != nil {
		c.JSON(http.StatusInternalServerError, ErrorResponse{Error: "Failed to create talk request: " + err.Error()})
		return
	}

	c.JSON(http.StatusCreated, newTalk)
}

// summarizeDiscussion asks Gemini for a short, actionable rewrite instruction
// distilled from the discussion transcript.
func (h *DiscussionHandler) summarizeDiscussion(ctx context.Context, userID uint, transcript string) (string, error) {
	client, err := genai.NewClient(ctx, option.WithAPIKey(h.cfg.GeminiAPIKey))
	if err != nil {
		return "", fmt.Errorf("failed to init Gemini client: %v", err)
	}
	defer client.Close()

	geminiModel := client.GenerativeModel("gemini-3.1-flash-lite")
	geminiModel.SystemInstruction = &genai.Content{
		Parts: []genai.Part{genai.Text(
			"You distill a team chat discussion about how to revise a speech into a single, concise, " +
				"actionable rewrite instruction — as if one person had written it directly. Focus on the " +
				"final decisions reached, not the back-and-forth. Output ONLY the instruction itself, no " +
				"preamble, quotes, or markdown.",
		)},
	}

	prompt := fmt.Sprintf("TEAM DISCUSSION TRANSCRIPT:\n%s\n\nDistill this into one rewrite instruction.", transcript)

	resp, err := geminiModel.GenerateContent(ctx, genai.Text(prompt))
	if err != nil {
		model.LogGeminiCall(userID, "discussion_summary", "failed")
		return "", fmt.Errorf("gemini API error: %v", err)
	}
	model.LogGeminiCall(userID, "discussion_summary", "success")

	var out string
	if len(resp.Candidates) > 0 && resp.Candidates[0].Content != nil {
		for _, part := range resp.Candidates[0].Content.Parts {
			if txt, ok := part.(genai.Text); ok {
				out += string(txt)
			}
		}
	}
	if out == "" {
		return "", fmt.Errorf("empty response received from Gemini model")
	}
	return out, nil
}


package handler

import (
	"fmt"
	"net/http"
	"time"

	"talkforge-be/model"
	"github.com/gin-gonic/gin"
)

// TalkHandler handles dialogue generation request endpoints.
type TalkHandler struct{}

// NewTalkHandler instantiates a new TalkHandler.
func NewTalkHandler() *TalkHandler {
	return &TalkHandler{}
}

// CreateTalkRequestBody represents the payload to create a new dialogue request.
type CreateTalkRequestBody struct {
	Mode        string `json:"mode" binding:"required" example:"new"` // "new" or "update"
	Language    string `json:"language" example:"German"`
	Place       string `json:"place" example:"Airport Check-in"`
	Topic       string `json:"topic" example:"Checking in baggage and asking for a window seat"`
	Duration    int    `json:"duration" example:"5"`
	SpeechType  string `json:"speech_type" example:"Siyasetçi Konuşması"`
	Instruction string `json:"instruction" example:"make the staff member more helpful"` // Required only for update mode
	ParentID    *uint  `json:"parent_id" example:"1"`                                  // Required only for update mode
}

// TalkRequestResponse represents a dialogue node in the tree response.
type TalkRequestResponse struct {
	ID            uint                   `json:"id"`
	UserID        uint                   `json:"user_id"`
	Mode          string                 `json:"mode"`
	Status        string                 `json:"status"`
	Language      string                 `json:"language"`
	Place                 string                 `json:"place"`
	Topic                 string                 `json:"topic"`
	Duration      int                    `json:"duration"`
	SpeechType    string                 `json:"speech_type"`
	Instruction   string                 `json:"instruction,omitempty"`
	ParentID      *uint                  `json:"parent_id,omitempty"`
	GeneratedText string                 `json:"generated_text,omitempty"`
	ErrorMessage  string                 `json:"error_message,omitempty"`
	CreatedAt     time.Time              `json:"created_at"`
	UpdatedAt     time.Time              `json:"updated_at"`
	Children      []*TalkRequestResponse `json:"children"`
}

// CreateTalkRequest handles creation of a new dialogue text generation request.
// @Summary Create dialogue generation request
// @Description Creates a pending request to generate dialogue text via Gemini. Mode can be "new" or "update".
// @Tags Talks
// @Security BearerAuth
// @Accept json
// @Produce json
// @Param request body CreateTalkRequestBody true "Create Request Payload"
// @Success 201 {object} model.TalkRequest
// @Failure 400 {object} ErrorResponse
// @Failure 401 {object} ErrorResponse
// @Failure 404 {object} ErrorResponse
// @Router /api/v1/talks [post]
func (h *TalkHandler) CreateTalkRequest(c *gin.Context) {
	userID, exists := c.Get("userID")
	if !exists {
		c.JSON(http.StatusUnauthorized, ErrorResponse{Error: "Unauthorized context missing"})
		return
	}

	var req CreateTalkRequestBody
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, ErrorResponse{Error: err.Error()})
		return
	}

	if req.Mode != "new" && req.Mode != "update" {
		c.JSON(http.StatusBadRequest, ErrorResponse{Error: "mode must be 'new' or 'update'"})
		return
	}

	var talkReq model.TalkRequest
	talkReq.UserID = userID.(uint)
	talkReq.Mode = req.Mode
	talkReq.Status = "pending"

	if req.Mode == "new" {
		if req.Language == "" || req.Place == "" || req.Topic == "" || req.SpeechType == "" || req.Duration <= 0 {
			c.JSON(http.StatusBadRequest, ErrorResponse{Error: "new mode requires 'language', 'place', 'topic', 'speech_type', and a positive 'duration'"})
			return
		}
		talkReq.Language = req.Language
		talkReq.Place = req.Place
		talkReq.Topic = req.Topic
		talkReq.SpeechType = req.SpeechType
		talkReq.Duration = req.Duration
	} else { // update mode
		if req.ParentID == nil || req.Instruction == "" {
			c.JSON(http.StatusBadRequest, ErrorResponse{Error: "update mode requires 'parent_id' and 'instruction'"})
			return
		}

		// Verify parent request exists and belongs to this user
		var parent model.TalkRequest
		if err := model.DB.First(&parent, *req.ParentID).Error; err != nil {
			c.JSON(http.StatusNotFound, ErrorResponse{Error: fmt.Sprintf("parent request %d not found", *req.ParentID)})
			return
		}

		if parent.UserID != userID.(uint) {
			c.JSON(http.StatusForbidden, ErrorResponse{Error: "you cannot update a dialogue belonging to another user"})
			return
		}

		if parent.Status != "completed" {
			c.JSON(http.StatusBadRequest, ErrorResponse{Error: "cannot update a dialogue request that has not completed successfully"})
			return
		}

		// Inherit details from parent for update request
		talkReq.Language = parent.Language
		talkReq.Place = parent.Place
		talkReq.Topic = parent.Topic
		talkReq.SpeechType = parent.SpeechType
		talkReq.Duration = parent.Duration
		talkReq.Instruction = req.Instruction
		talkReq.ParentID = req.ParentID
	}

	if err := model.DB.Create(&talkReq).Error; err != nil {
		c.JSON(http.StatusInternalServerError, ErrorResponse{Error: "Failed to create talk request: " + err.Error()})
		return
	}

	c.JSON(http.StatusCreated, talkReq)
}

// ListTalkRequests lists user's dialogue requests structured as a parent-child relationship tree.
// @Summary List dialogue requests as a tree
// @Description Returns the dialogue generation requests for the authenticated user, nested in a parent-child tree structure representing branches created by updates.
// @Tags Talks
// @Security BearerAuth
// @Produce json
// @Success 200 {array} TalkRequestResponse
// @Failure 401 {object} ErrorResponse
// @Router /api/v1/talks [get]
func (h *TalkHandler) ListTalkRequests(c *gin.Context) {
	userID, exists := c.Get("userID")
	if !exists {
		c.JSON(http.StatusUnauthorized, ErrorResponse{Error: "Unauthorized context missing"})
		return
	}

	var reqs []model.TalkRequest
	// Query in chronological order so parent nodes are processed before children
	if err := model.DB.Where("user_id = ?", userID.(uint)).Order("created_at asc").Find(&reqs).Error; err != nil {
		c.JSON(http.StatusInternalServerError, ErrorResponse{Error: "Failed to query talk requests: " + err.Error()})
		return
	}

	// Build the parent-child tree structure
	nodeMap := make(map[uint]*TalkRequestResponse)
	var roots []*TalkRequestResponse

	for _, r := range reqs {
		node := &TalkRequestResponse{
			ID:            r.ID,
			UserID:        r.UserID,
			Mode:          r.Mode,
			Status:        r.Status,
			Language:      r.Language,
			Place:         r.Place,
			Topic:         r.Topic,
			Duration:      r.Duration,
			SpeechType:    r.SpeechType,
			Instruction:   r.Instruction,
			ParentID:      r.ParentID,
			GeneratedText: r.GeneratedText,
			ErrorMessage:  r.ErrorMessage,
			CreatedAt:     r.CreatedAt,
			UpdatedAt:     r.UpdatedAt,
			Children:      []*TalkRequestResponse{},
		}
		nodeMap[r.ID] = node
	}

	for _, node := range nodeMap {
		if node.ParentID != nil {
			if parentNode, exists := nodeMap[*node.ParentID]; exists {
				parentNode.Children = append(parentNode.Children, node)
			} else {
				// Parent request is not in the list (e.g. deleted or anomalous), treat as root
				roots = append(roots, node)
			}
		} else {
			roots = append(roots, node)
		}
	}

	// If no records, return empty array instead of null
	if roots == nil {
		roots = []*TalkRequestResponse{}
	}

	c.JSON(http.StatusOK, roots)
}

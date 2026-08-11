package handler

import (
	"context"
	"fmt"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"talkforge-be/config"
	"talkforge-be/model"

	"github.com/gin-gonic/gin"
	"github.com/google/generative-ai-go/genai"
	"google.golang.org/api/option"
)

// TalkHandler handles dialogue generation request endpoints.
type TalkHandler struct {
	cfg *config.Config
}

// NewTalkHandler instantiates a new TalkHandler.
func NewTalkHandler(cfg *config.Config) *TalkHandler {
	return &TalkHandler{cfg: cfg}
}

// CreateTalkRequestBody represents the payload to create a new dialogue request.
type CreateTalkRequestBody struct {
	Mode             string `json:"mode" binding:"required" example:"new"` // "new", "update", "partial_update", or "manual_update"
	Language         string `json:"language" example:"German"`
	Place            string `json:"place" example:"Airport Check-in"`
	Topic            string `json:"topic" example:"Checking in baggage and asking for a window seat"`
	Duration         int    `json:"duration" example:"5"`
	SpeechType       string `json:"speech_type" example:"politician"`
	CustomSpeechType string `json:"custom_speech_type,omitempty" example:"Özel Şirket İçi Sunum"`
	Instruction      string `json:"instruction" example:"make the staff member more helpful"` // Required for update/partial_update
	SelectedText     string `json:"selected_text,omitempty" example:"Ladies and gentlemen..."` // Required only for partial_update mode
	GeneratedText    string `json:"generated_text,omitempty"`
	ParentID         *uint  `json:"parent_id" example:"1"`                                  // Required for update/partial_update
	RoomID           *uint  `json:"room_id,omitempty" example:"1"`                          // Optional: attach a "new" talk to a shared room (requires writer membership)
}

// TalkRequestResponse represents a dialogue node in the tree response.
type TalkRequestResponse struct {
	ID               uint                   `json:"id"`
	UserID           uint                   `json:"user_id"`
	Mode             string                 `json:"mode"`
	Status           string                 `json:"status"`
	Language         string                 `json:"language"`
	Place            string                 `json:"place"`
	Topic            string                 `json:"topic"`
	Duration         int                    `json:"duration"`
	SpeechType       string                 `json:"speech_type"`
	CustomSpeechType string                 `json:"custom_speech_type,omitempty"`
	Instruction      string                 `json:"instruction,omitempty"`
	SelectedText     string                 `json:"selected_text,omitempty"`
	VersionNumber    int                    `json:"version_number"`
	VersionLabel     string                 `json:"version_label"`
	ParentID         *uint                  `json:"parent_id,omitempty"`
	RoomID           *uint                  `json:"room_id,omitempty"`
	IsFavorite       bool                   `json:"is_favorite"`
	IsArchived       bool                   `json:"is_archived"`
	Tags             []string               `json:"tags"`
	GeneratedText    string                 `json:"generated_text,omitempty"`
	ErrorMessage     string                 `json:"error_message,omitempty"`
	CreatedAt        time.Time              `json:"created_at"`
	UpdatedAt        time.Time              `json:"updated_at"`
	UnreadCount      int64                  `json:"unread_count"`
	HasUnread        bool                   `json:"has_unread"`
	Children         []*TalkRequestResponse `json:"children"`
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

	if req.Mode != "new" && req.Mode != "update" && req.Mode != "partial_update" && req.Mode != "manual_update" {
		c.JSON(http.StatusBadRequest, ErrorResponse{Error: "mode must be 'new', 'update', 'partial_update', or 'manual_update'"})
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
		if req.RoomID != nil {
			if role, member := isRoomMember(userID.(uint), *req.RoomID); !member || role != "writer" {
				c.JSON(http.StatusForbidden, ErrorResponse{Error: "you must be a writer member of this room to create talks in it"})
				return
			}
			talkReq.RoomID = req.RoomID
		}
		talkReq.Language = req.Language
		talkReq.Place = req.Place
		talkReq.Topic = req.Topic
		talkReq.SpeechType = req.SpeechType
		talkReq.CustomSpeechType = req.CustomSpeechType
		talkReq.Duration = req.Duration
		talkReq.VersionNumber = 1 // Root is always version 1
		talkReq.VersionLabel = "1"
	} else if req.Mode == "update" {
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

		if !canAccessTalk(userID.(uint), parent, true) {
			c.JSON(http.StatusForbidden, ErrorResponse{Error: "you do not have write access to this dialogue"})
			return
		}

		if parent.Status != "completed" {
			c.JSON(http.StatusBadRequest, ErrorResponse{Error: "cannot update a dialogue request that has not completed successfully"})
			return
		}

		// Find root of this conversation tree (including soft-deleted ancestors)
		rootID := *req.ParentID
		current := parent
		for current.ParentID != nil {
			var ancestor model.TalkRequest
			if err := model.DB.Unscoped().First(&ancestor, *current.ParentID).Error; err != nil {
				break
			}
			current = ancestor
		}
		rootID = current.ID

		// Find maximum version_number in this tree (including soft-deleted nodes) so version numbers strictly increment even after deletions
		var maxVersion int
		model.DB.Unscoped().Raw(`
			WITH RECURSIVE tree AS (
				SELECT id, version_number FROM talk_requests WHERE id = ?
				UNION ALL
				SELECT tr.id, tr.version_number FROM talk_requests tr
				INNER JOIN tree ON tr.parent_id = tree.id
			)
			SELECT COALESCE(MAX(version_number), 0) FROM tree`, rootID).Scan(&maxVersion)

		talkReq.VersionNumber = maxVersion + 1
		talkReq.VersionLabel = generateVersionLabel(parent)

		// Inherit details from parent for update request
		talkReq.Language = parent.Language
		talkReq.Place = parent.Place
		talkReq.Topic = parent.Topic
		talkReq.SpeechType = parent.SpeechType
		talkReq.CustomSpeechType = parent.CustomSpeechType
		talkReq.Duration = parent.Duration
		talkReq.RoomID = parent.RoomID
		talkReq.Instruction = req.Instruction
		talkReq.ParentID = req.ParentID
	} else if req.Mode == "partial_update" { // partial_update mode — edits only a selected section of the parent text
		if req.ParentID == nil || req.Instruction == "" || req.SelectedText == "" {
			c.JSON(http.StatusBadRequest, ErrorResponse{Error: "partial_update mode requires 'parent_id', 'instruction', and 'selected_text'"})
			return
		}

		// Verify parent request exists and belongs to this user
		var parent model.TalkRequest
		if err := model.DB.First(&parent, *req.ParentID).Error; err != nil {
			c.JSON(http.StatusNotFound, ErrorResponse{Error: fmt.Sprintf("parent request %d not found", *req.ParentID)})
			return
		}

		if !canAccessTalk(userID.(uint), parent, true) {
			c.JSON(http.StatusForbidden, ErrorResponse{Error: "you do not have write access to this dialogue"})
			return
		}

		if parent.Status != "completed" {
			c.JSON(http.StatusBadRequest, ErrorResponse{Error: "cannot partially update a dialogue request that has not completed successfully"})
			return
		}

		// Find root of this conversation tree (including soft-deleted ancestors)
		rootID := *req.ParentID
		current := parent
		for current.ParentID != nil {
			var ancestor model.TalkRequest
			if err := model.DB.Unscoped().First(&ancestor, *current.ParentID).Error; err != nil {
				break
			}
			current = ancestor
		}
		rootID = current.ID

		// Find maximum version_number in this tree (including soft-deleted nodes)
		var maxVersion int
		model.DB.Unscoped().Raw(`
			WITH RECURSIVE tree AS (
				SELECT id, version_number FROM talk_requests WHERE id = ?
				UNION ALL
				SELECT tr.id, tr.version_number FROM talk_requests tr
				INNER JOIN tree ON tr.parent_id = tree.id
			)
			SELECT COALESCE(MAX(version_number), 0) FROM tree`, rootID).Scan(&maxVersion)

		talkReq.VersionNumber = maxVersion + 1
		talkReq.VersionLabel = generateVersionLabel(parent)

		// Inherit details from parent
		talkReq.Language = parent.Language
		talkReq.Place = parent.Place
		talkReq.Topic = parent.Topic
		talkReq.SpeechType = parent.SpeechType
		talkReq.CustomSpeechType = parent.CustomSpeechType
		talkReq.Duration = parent.Duration
		talkReq.RoomID = parent.RoomID
		talkReq.Instruction = req.Instruction
		talkReq.SelectedText = req.SelectedText
		talkReq.ParentID = req.ParentID
	} else if req.Mode == "manual_update" {
		if req.ParentID == nil || req.GeneratedText == "" {
			c.JSON(http.StatusBadRequest, ErrorResponse{Error: "manual_update mode requires 'parent_id' and 'generated_text'"})
			return
		}

		// Verify parent request exists and belongs to this user
		var parent model.TalkRequest
		if err := model.DB.First(&parent, *req.ParentID).Error; err != nil {
			c.JSON(http.StatusNotFound, ErrorResponse{Error: fmt.Sprintf("parent request %d not found", *req.ParentID)})
			return
		}

		if !canAccessTalk(userID.(uint), parent, true) {
			c.JSON(http.StatusForbidden, ErrorResponse{Error: "you do not have write access to this dialogue"})
			return
		}

		if parent.Status != "completed" {
			c.JSON(http.StatusBadRequest, ErrorResponse{Error: "cannot update a dialogue request that has not completed successfully"})
			return
		}

		rootID := *req.ParentID
		current := parent
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

		talkReq.VersionNumber = maxVersion + 1
		talkReq.VersionLabel = generateVersionLabel(parent)
		talkReq.Language = parent.Language
		talkReq.Place = parent.Place
		talkReq.Topic = parent.Topic
		talkReq.SpeechType = parent.SpeechType
		talkReq.CustomSpeechType = parent.CustomSpeechType
		talkReq.Duration = parent.Duration
		talkReq.RoomID = parent.RoomID
		talkReq.Instruction = req.Instruction
		if talkReq.Instruction == "" {
			talkReq.Instruction = "Manuel Düzeltme"
		}
		talkReq.SelectedText = req.SelectedText
		talkReq.GeneratedText = req.GeneratedText
		talkReq.Status = "completed"
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
// @Description Returns the dialogue generation requests for the authenticated user, nested in a parent-child tree structure representing branches created by updates. Supports filtering by favorite, archived, and tag. Supports limit/offset pagination on root conversations.
// @Tags Talks
// @Security BearerAuth
// @Produce json
// @Param favorite query bool false "Filter by favorite status (true = only favorites)"
// @Param archived query bool false "Filter archived talks (true = only archived; omit or false = hide archived)"
// @Param tag query string false "Filter talks containing this tag (exact match)"
// @Param limit query int false "Max root conversations to return (default 50, max 200)"
// @Param offset query int false "Number of root conversations to skip (default 0)"
// @Success 200 {array} TalkRequestResponse
// @Failure 401 {object} ErrorResponse
// @Router /api/v1/talks [get]
func (h *TalkHandler) ListTalkRequests(c *gin.Context) {
	userID, exists := c.Get("userID")
	if !exists {
		c.JSON(http.StatusUnauthorized, ErrorResponse{Error: "Unauthorized context missing"})
		return
	}

	// Parse filter parameters
	favoriteStr := c.Query("favorite")
	archivedStr := c.Query("archived")
	tagFilter := strings.TrimSpace(c.Query("tag"))

	limit := 50
	offset := 0
	if v, err := strconv.Atoi(c.Query("limit")); err == nil && v > 0 {
		if v > 200 {
			v = 200
		}
		limit = v
	}
	if v, err := strconv.Atoi(c.Query("offset")); err == nil && v >= 0 {
		offset = v
	}

	// Include talks the user owns directly, plus every talk inside a room
	// they're a member of (regardless of which member authored each version)
	// so a shared room tree looks the same to every member.
	var memberRoomIDs []uint
	model.DB.Model(&model.RoomMember{}).Where("user_id = ? AND status = 'accepted'", userID.(uint)).Pluck("room_id", &memberRoomIDs)

	// -----------------------------------------------------------------------
	// Step 1: Resolve the set of qualifying ROOT talk IDs applying filters
	// (pagination is over root conversations, not individual versions).
	// -----------------------------------------------------------------------
	rootQuery := model.DB.Model(&model.TalkRequest{}).Where("parent_id IS NULL")
	if len(memberRoomIDs) > 0 {
		rootQuery = rootQuery.Where("user_id = ? OR room_id IN ?", userID.(uint), memberRoomIDs)
	} else {
		rootQuery = rootQuery.Where("user_id = ?", userID.(uint))
	}

	// Favorite filter
	if favoriteStr == "true" {
		rootQuery = rootQuery.Where("is_favorite = true")
	}

	// Archived filter — hide archived by default unless caller explicitly asks
	if archivedStr == "true" {
		rootQuery = rootQuery.Where("is_archived = true")
	} else {
		// archived=false (explicit) or omitted: exclude archived talks
		rootQuery = rootQuery.Where("is_archived = false")
	}

	// Tag filter — EXISTS subquery on talk_tags
	if tagFilter != "" {
		rootQuery = rootQuery.Where(
			"EXISTS (SELECT 1 FROM talk_tags tt WHERE tt.root_talk_id = talk_requests.id AND tt.user_id = ? AND tt.name = ?)",
			userID.(uint), tagFilter,
		)
	}

	var rootIDs []uint
	if err := rootQuery.Order("id DESC").Limit(limit).Offset(offset).Pluck("id", &rootIDs).Error; err != nil {
		c.JSON(http.StatusInternalServerError, ErrorResponse{Error: "Failed to query root talk IDs: " + err.Error()})
		return
	}

	// -----------------------------------------------------------------------
	// Step 2: Load ALL versions (full tree) for the qualifying roots.
	// We use the recursive CTE approach to get every descendant.
	// -----------------------------------------------------------------------
	var reqs []model.TalkRequest
	if len(rootIDs) > 0 {
		if err := model.DB.
			Where("id IN (?) OR parent_id IN (SELECT id FROM talk_requests WHERE id IN (?) OR parent_id IN (?))", rootIDs, rootIDs, rootIDs).
			Order("created_at asc").
			Find(&reqs).Error; err != nil {
			c.JSON(http.StatusInternalServerError, ErrorResponse{Error: "Failed to query talk requests: " + err.Error()})
			return
		}
		// For deep trees: load all descendants via recursive query
		var allDescendants []model.TalkRequest
		if err := model.DB.Raw(`
			WITH RECURSIVE tree AS (
				SELECT * FROM talk_requests WHERE id = ANY(?) AND deleted_at IS NULL
				UNION ALL
				SELECT tr.* FROM talk_requests tr
				INNER JOIN tree ON tr.parent_id = tree.id
				WHERE tr.deleted_at IS NULL
			)
			SELECT DISTINCT * FROM tree ORDER BY created_at ASC`, rootIDs).Scan(&allDescendants).Error; err == nil && len(allDescendants) > 0 {
			reqs = allDescendants
		}
	}

	// -----------------------------------------------------------------------
	// Step 3: Load all talk_tags for these roots in a single query
	// -----------------------------------------------------------------------
	tagsByRootID := make(map[uint][]string)
	if len(rootIDs) > 0 {
		var tags []model.TalkTag
		model.DB.Where("root_talk_id IN ? AND user_id = ?", rootIDs, userID.(uint)).Find(&tags)
		for _, t := range tags {
			tagsByRootID[t.RootTalkID] = append(tagsByRootID[t.RootTalkID], t.Name)
		}
	}

	// -----------------------------------------------------------------------
	// Step 4: Build the parent-child tree structure (same logic as before)
	// -----------------------------------------------------------------------
	nodeMap := make(map[uint]*TalkRequestResponse)
	var roots []*TalkRequestResponse

	backfillVersionLabels()

	var allReqs []model.TalkRequest
	model.DB.Unscoped().Find(&allReqs)
	allReqsMap := make(map[uint]model.TalkRequest)
	for _, r := range allReqs {
		allReqsMap[r.ID] = r
	}

	for _, r := range reqs {
		node := &TalkRequestResponse{
			ID:               r.ID,
			UserID:           r.UserID,
			Mode:             r.Mode,
			Status:           r.Status,
			Language:         r.Language,
			Place:            r.Place,
			Topic:            r.Topic,
			Duration:         r.Duration,
			SpeechType:       r.SpeechType,
			CustomSpeechType: r.CustomSpeechType,
			Instruction:      r.Instruction,
			SelectedText:     r.SelectedText,
			VersionNumber:    r.VersionNumber,
			VersionLabel:     r.VersionLabel,
			ParentID:         r.ParentID,
			RoomID:           r.RoomID,
			IsFavorite:       r.IsFavorite,
			IsArchived:       r.IsArchived,
			Tags:             []string{},
			GeneratedText:    r.GeneratedText,
			ErrorMessage:     r.ErrorMessage,
			CreatedAt:        r.CreatedAt,
			UpdatedAt:        r.UpdatedAt,
			Children:         []*TalkRequestResponse{},
		}
		// Tags only meaningful on root nodes; fill from bulk-loaded map
		if r.ParentID == nil {
			if ts, ok := tagsByRootID[r.ID]; ok {
				node.Tags = ts
			}
		}
		nodeMap[r.ID] = node
	}

	// Group active nodes by ultimate root ID
	familyMap := make(map[uint][]*TalkRequestResponse)
	for _, node := range nodeMap {
		rootID := getUltimateRootID(node.ID, allReqsMap)
		familyMap[rootID] = append(familyMap[rootID], node)
	}

	familyPrimaryRoot := make(map[uint]*TalkRequestResponse)
	for rootID, members := range familyMap {
		var primary *TalkRequestResponse
		for _, m := range members {
			if m.ID == rootID {
				primary = m
				break
			}
			if primary == nil || m.ID < primary.ID {
				primary = m
			}
		}
		familyPrimaryRoot[rootID] = primary
	}

	for _, node := range nodeMap {
		if node.ParentID != nil {
			if parentNode, exists := nodeMap[*node.ParentID]; exists {
				parentNode.Children = append(parentNode.Children, node)
			} else {
				// Parent request is soft-deleted, attach to nearest active ancestor in tree
				ancestor := findNearestActiveAncestor(node, nodeMap, allReqsMap)
				if ancestor != nil {
					ancestor.Children = append(ancestor.Children, node)
				} else {
					rootID := getUltimateRootID(node.ID, allReqsMap)
					primary := familyPrimaryRoot[rootID]
					if primary != nil && node.ID == primary.ID {
						roots = append(roots, node)
					} else if primary != nil {
						primary.Children = append(primary.Children, node)
					} else {
						roots = append(roots, node)
					}
				}
			}
		} else {
			rootID := getUltimateRootID(node.ID, allReqsMap)
			primary := familyPrimaryRoot[rootID]
			if primary != nil && node.ID == primary.ID {
				roots = append(roots, node)
			} else if primary != nil {
				primary.Children = append(primary.Children, node)
			} else {
				roots = append(roots, node)
			}
		}
	}

	// Sort root conversations by ID descending (newest first) for consistent, stable ordering
	sort.Slice(roots, func(i, j int) bool {
		return roots[i].ID > roots[j].ID
	})

	// Sort child revisions by ID ascending (chronological order)
	for _, node := range nodeMap {
		if len(node.Children) > 1 {
			sort.Slice(node.Children, func(i, j int) bool {
				return node.Children[i].ID < node.Children[j].ID
			})
		}
	}

	fillTalkUnreadCounts(roots, userID.(uint))

	// If no records, return empty array instead of null
	if roots == nil {
		roots = []*TalkRequestResponse{}
	}

	c.JSON(http.StatusOK, roots)
}

func fillTalkUnreadCounts(roots []*TalkRequestResponse, userID uint) {
	roomIDsMap := make(map[uint]bool)
	for _, r := range roots {
		if r.RoomID != nil {
			roomIDsMap[*r.RoomID] = true
		}
	}
	if len(roomIDsMap) == 0 {
		return
	}

	var roomIDs []uint
	for id := range roomIDsMap {
		roomIDs = append(roomIDs, id)
	}

	var members []model.RoomMember
	model.DB.Where("room_id IN ? AND user_id = ? AND status = 'accepted'", roomIDs, userID).Find(&members)
	memberLastRead := make(map[uint]time.Time)
	for _, m := range members {
		memberLastRead[m.RoomID] = m.LastReadAt
	}

	for _, root := range roots {
		if root.RoomID == nil {
			continue
		}
		lastRead, exists := memberLastRead[*root.RoomID]
		if !exists {
			continue
		}
		uMap := getRoomTalkUnreadCounts(*root.RoomID, userID, lastRead)
		c := uMap[root.ID]
		root.UnreadCount = c
		root.HasUnread = c > 0
	}
}

func assignVersionLabels(node *TalkRequestResponse, label string) {
	node.VersionLabel = label

	if len(node.Children) == 0 {
		return
	}

	sort.Slice(node.Children, func(i, j int) bool {
		return node.Children[i].ID < node.Children[j].ID
	})

	for i, child := range node.Children {
		var childLabel string
		if i == 0 {
			parts := strings.Split(label, ".")
			lastVal, _ := strconv.Atoi(parts[len(parts)-1])
			parts[len(parts)-1] = strconv.Itoa(lastVal + 1)
			childLabel = strings.Join(parts, ".")
		} else {
			childLabel = fmt.Sprintf("%s.%d", label, i)
		}
		assignVersionLabels(child, childLabel)
	}
}

// DeleteTalkRequest deletes a talk request by ID along with all its child branches and descendants.
// @Summary Delete dialogue request
// @Description Deletes a dialogue generation request and any nested descendant updates.
// @Tags Talks
// @Security BearerAuth
// @Produce json
// @Param id path int true "Talk Request ID"
// @Success 200 {object} map[string]string
// @Failure 401 {object} ErrorResponse
// @Failure 403 {object} ErrorResponse
// @Failure 404 {object} ErrorResponse
// @Router /api/v1/talks/{id} [delete]
func (h *TalkHandler) DeleteTalkRequest(c *gin.Context) {
	userID, exists := c.Get("userID")
	if !exists {
		c.JSON(http.StatusUnauthorized, ErrorResponse{Error: "Unauthorized context missing"})
		return
	}

	idStr := c.Param("id")
	id, err := strconv.ParseUint(idStr, 10, 64)
	if err != nil {
		c.JSON(http.StatusBadRequest, ErrorResponse{Error: "Invalid talk request ID"})
		return
	}

	var talk model.TalkRequest
	if err := model.DB.First(&talk, uint(id)).Error; err != nil {
		c.JSON(http.StatusNotFound, ErrorResponse{Error: "Talk request not found"})
		return
	}

	if !canAccessTalk(userID.(uint), talk, true) {
		c.JSON(http.StatusForbidden, ErrorResponse{Error: "You do not have permission to delete this talk request"})
		return
	}

	cascade := c.Query("cascade") == "true"

	if cascade {
		// Full talk tree delete: Recursively collect all descendant IDs of this talk request (including itself)
		var idsToDelete []uint
		if err := model.DB.Raw(`
			WITH RECURSIVE tree AS (
				SELECT id FROM talk_requests WHERE id = ?
				UNION ALL
				SELECT tr.id FROM talk_requests tr
				INNER JOIN tree ON tr.parent_id = tree.id
			)
			SELECT id FROM tree`, talk.ID).Scan(&idsToDelete).Error; err != nil {
			c.JSON(http.StatusInternalServerError, ErrorResponse{Error: "Failed to find descendant talk requests: " + err.Error()})
			return
		}

		// Delete target talk request and all its child versions
		if len(idsToDelete) > 0 {
			if err := model.DB.Where("id IN ?", idsToDelete).Delete(&model.TalkRequest{}).Error; err != nil {
				c.JSON(http.StatusInternalServerError, ErrorResponse{Error: "Failed to delete talk request and its versions: " + err.Error()})
				return
			}
		}
	} else {
		// Single node delete: Soft-delete ONLY this talk request. Keep parent_id intact in DB
		// so tree lineage and version_label calculations remain 100% immutable!
		if err := model.DB.Delete(&talk).Error; err != nil {
			c.JSON(http.StatusInternalServerError, ErrorResponse{Error: "Failed to delete talk request: " + err.Error()})
			return
		}
	}

	c.JSON(http.StatusOK, gin.H{"message": "Talk request deleted successfully"})
}

// PatchTalkMetaBody represents the optional fields to update for a root conversation's metadata.
type PatchTalkMetaBody struct {
	IsFavorite *bool    `json:"is_favorite"`  // nil = no change
	IsArchived *bool    `json:"is_archived"`  // nil = no change
	Tags       *[]string `json:"tags"`          // nil = no change; empty slice = clear all tags
}

// PatchTalkMeta updates the organisational metadata (favorite, archived, tags) of a root conversation.
// @Summary Update talk metadata (favorite / archived / tags)
// @Description Sets is_favorite, is_archived, and/or tags on a root TalkRequest. Only root conversations (parent_id IS NULL) are accepted. Tags replace the full tag list for the requesting user.
// @Tags Talks
// @Security BearerAuth
// @Accept json
// @Produce json
// @Param id path int true "Root Talk Request ID"
// @Param request body PatchTalkMetaBody true "Metadata patch payload"
// @Success 200 {object} TalkRequestResponse
// @Failure 400 {object} ErrorResponse
// @Failure 401 {object} ErrorResponse
// @Failure 403 {object} ErrorResponse
// @Failure 404 {object} ErrorResponse
// @Router /api/v1/talks/{id}/meta [patch]
func (h *TalkHandler) PatchTalkMeta(c *gin.Context) {
	userID, exists := c.Get("userID")
	if !exists {
		c.JSON(http.StatusUnauthorized, ErrorResponse{Error: "Unauthorized context missing"})
		return
	}

	idStr := c.Param("id")
	id, err := strconv.ParseUint(idStr, 10, 64)
	if err != nil {
		c.JSON(http.StatusBadRequest, ErrorResponse{Error: "Invalid talk request ID"})
		return
	}

	var talk model.TalkRequest
	if err := model.DB.First(&talk, uint(id)).Error; err != nil {
		c.JSON(http.StatusNotFound, ErrorResponse{Error: "Talk request not found"})
		return
	}

	// Only root conversations may carry organisational metadata
	if talk.ParentID != nil {
		c.JSON(http.StatusBadRequest, ErrorResponse{Error: "Metadata (favorite, archived, tags) can only be set on root conversations (parent_id must be null)"})
		return
	}

	if !canAccessTalk(userID.(uint), talk, true) {
		c.JSON(http.StatusForbidden, ErrorResponse{Error: "You do not have permission to modify this talk"})
		return
	}

	var body PatchTalkMetaBody
	if err := c.ShouldBindJSON(&body); err != nil {
		c.JSON(http.StatusBadRequest, ErrorResponse{Error: err.Error()})
		return
	}

	// Update is_favorite / is_archived on TalkRequest if provided
	updates := map[string]interface{}{}
	if body.IsFavorite != nil {
		updates["is_favorite"] = *body.IsFavorite
	}
	if body.IsArchived != nil {
		updates["is_archived"] = *body.IsArchived
	}
	if len(updates) > 0 {
		if err := model.DB.Model(&talk).Updates(updates).Error; err != nil {
			c.JSON(http.StatusInternalServerError, ErrorResponse{Error: "Failed to update talk metadata: " + err.Error()})
			return
		}
	}

	// Replace tags if provided (replace semantics: delete old, insert new)
	if body.Tags != nil {
		// Validate tag names
		for _, name := range *body.Tags {
			if strings.TrimSpace(name) == "" {
				c.JSON(http.StatusBadRequest, ErrorResponse{Error: "Tag names must not be empty or whitespace-only"})
				return
			}
			if len(name) > 100 {
				c.JSON(http.StatusBadRequest, ErrorResponse{Error: "Tag names must not exceed 100 characters"})
				return
			}
		}

		// Delete existing tags for this user on this talk
		if err := model.DB.Where("root_talk_id = ? AND user_id = ?", talk.ID, userID.(uint)).Delete(&model.TalkTag{}).Error; err != nil {
			c.JSON(http.StatusInternalServerError, ErrorResponse{Error: "Failed to clear existing tags: " + err.Error()})
			return
		}

		// Deduplicate and insert new tags
		seenTags := make(map[string]bool)
		var newTags []model.TalkTag
		for _, name := range *body.Tags {
			trimmed := strings.TrimSpace(name)
			if !seenTags[trimmed] {
				seenTags[trimmed] = true
				newTags = append(newTags, model.TalkTag{
					RootTalkID: talk.ID,
					UserID:     userID.(uint),
					Name:       trimmed,
				})
			}
		}
		if len(newTags) > 0 {
			if err := model.DB.Create(&newTags).Error; err != nil {
				c.JSON(http.StatusInternalServerError, ErrorResponse{Error: "Failed to save new tags: " + err.Error()})
				return
			}
		}
	}

	// Reload updated talk record
	model.DB.First(&talk, talk.ID)

	// Load tags to include in response
	var tagRows []model.TalkTag
	model.DB.Where("root_talk_id = ? AND user_id = ?", talk.ID, userID.(uint)).Find(&tagRows)
	tagNames := make([]string, 0, len(tagRows))
	for _, t := range tagRows {
		tagNames = append(tagNames, t.Name)
	}

	// Build response
	resp := TalkRequestResponse{
		ID:               talk.ID,
		UserID:           talk.UserID,
		Mode:             talk.Mode,
		Status:           talk.Status,
		Language:         talk.Language,
		Place:            talk.Place,
		Topic:            talk.Topic,
		Duration:         talk.Duration,
		SpeechType:       talk.SpeechType,
		CustomSpeechType: talk.CustomSpeechType,
		VersionNumber:    talk.VersionNumber,
		VersionLabel:     talk.VersionLabel,
		ParentID:         talk.ParentID,
		RoomID:           talk.RoomID,
		IsFavorite:       talk.IsFavorite,
		IsArchived:       talk.IsArchived,
		Tags:             tagNames,
		GeneratedText:    talk.GeneratedText,
		CreatedAt:        talk.CreatedAt,
		UpdatedAt:        talk.UpdatedAt,
		Children:         []*TalkRequestResponse{},
	}
	c.JSON(http.StatusOK, resp)
}

func generateVersionLabel(parent model.TalkRequest) string {
	parentLabel := parent.VersionLabel
	if parentLabel == "" {
		parentLabel = "1"
	}

	var childCount int64
	model.DB.Unscoped().Model(&model.TalkRequest{}).Where("parent_id = ?", parent.ID).Count(&childCount)

	if childCount == 0 {
		parts := strings.Split(parentLabel, ".")
		lastVal, _ := strconv.Atoi(parts[len(parts)-1])
		parts[len(parts)-1] = strconv.Itoa(lastVal + 1)
		return strings.Join(parts, ".")
	}
	return fmt.Sprintf("%s.%d", parentLabel, childCount)
}

func backfillVersionLabels() {
	var allReqs []model.TalkRequest
	if err := model.DB.Unscoped().Order("id asc").Find(&allReqs).Error; err != nil || len(allReqs) == 0 {
		return
	}

	labelMap := make(map[uint]string)
	parentChildrenMap := make(map[uint][]uint)

	for _, r := range allReqs {
		if r.ParentID != nil {
			parentChildrenMap[*r.ParentID] = append(parentChildrenMap[*r.ParentID], r.ID)
		}
	}

	for _, r := range allReqs {
		var label string
		if r.ParentID == nil {
			label = "1"
		} else {
			parentLabel := labelMap[*r.ParentID]
			if parentLabel == "" {
				parentLabel = "1"
			}

			siblings := parentChildrenMap[*r.ParentID]
			idx := 0
			for i, sibID := range siblings {
				if sibID == r.ID {
					idx = i
					break
				}
			}

			if idx == 0 {
				parts := strings.Split(parentLabel, ".")
				lastVal, _ := strconv.Atoi(parts[len(parts)-1])
				parts[len(parts)-1] = strconv.Itoa(lastVal + 1)
				label = strings.Join(parts, ".")
			} else {
				label = fmt.Sprintf("%s.%d", parentLabel, idx)
			}
		}

		labelMap[r.ID] = label
		if r.VersionLabel != label {
			model.DB.Unscoped().Model(&model.TalkRequest{}).Where("id = ?", r.ID).Update("version_label", label)
		}
	}
}

func getUltimateRootID(id uint, allReqsMap map[uint]model.TalkRequest) uint {
	currID := id
	visited := make(map[uint]bool)
	for {
		if visited[currID] {
			break
		}
		visited[currID] = true
		req, exists := allReqsMap[currID]
		if !exists || req.ParentID == nil {
			return currID
		}
		currID = *req.ParentID
	}
	return currID
}

func findNearestActiveAncestor(node *TalkRequestResponse, nodeMap map[uint]*TalkRequestResponse, allReqsMap map[uint]model.TalkRequest) *TalkRequestResponse {
	currParentID := node.ParentID
	for currParentID != nil {
		if activeParent, exists := nodeMap[*currParentID]; exists {
			return activeParent
		}
		if parentReq, exists := allReqsMap[*currParentID]; exists {
			currParentID = parentReq.ParentID
		} else {
			break
		}
	}
	return nil
}

// TranslateBody represents the payload to preview a talk's text in another language.
type TranslateBody struct {
	Language string `json:"language" binding:"required" example:"İngilizce"`
}

// TranslateResponse represents an ephemeral translation preview.
type TranslateResponse struct {
	Language string `json:"language"`
	Text     string `json:"text"`
}

// TranslateTalk translates a talk's generated text into another language for
// display purposes only — nothing is persisted, no new version is created.
// The version tree, and any subsequent edit/update, is always based on the
// talk's own original-language text.
// @Summary Preview a talk translated into another language
// @Description Returns the talk's text translated into the given language. Not persisted — no new version is created. Requires read access to the talk.
// @Tags Talks
// @Security BearerAuth
// @Accept json
// @Produce json
// @Param id path int true "Talk Request ID"
// @Param request body TranslateBody true "Target Language"
// @Success 200 {object} TranslateResponse
// @Failure 400 {object} ErrorResponse
// @Failure 403 {object} ErrorResponse
// @Failure 404 {object} ErrorResponse
// @Router /api/v1/talks/{id}/translate [post]
func (h *TalkHandler) TranslateTalk(c *gin.Context) {
	userID, exists := c.Get("userID")
	if !exists {
		c.JSON(http.StatusUnauthorized, ErrorResponse{Error: "Unauthorized context missing"})
		return
	}

	id, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil {
		c.JSON(http.StatusBadRequest, ErrorResponse{Error: "Invalid talk request ID"})
		return
	}

	var req TranslateBody
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, ErrorResponse{Error: err.Error()})
		return
	}

	var talk model.TalkRequest
	if err := model.DB.First(&talk, uint(id)).Error; err != nil {
		c.JSON(http.StatusNotFound, ErrorResponse{Error: "Talk request not found"})
		return
	}

	if !canAccessTalk(userID.(uint), talk, false) {
		c.JSON(http.StatusForbidden, ErrorResponse{Error: "You do not have permission to view this talk request"})
		return
	}

	if talk.Status != "completed" || talk.GeneratedText == "" {
		c.JSON(http.StatusBadRequest, ErrorResponse{Error: "cannot translate a talk that has not completed successfully"})
		return
	}

	if h.cfg.GeminiAPIKey == "" {
		c.JSON(http.StatusInternalServerError, ErrorResponse{Error: "GEMINI_API_KEY is not configured"})
		return
	}

	translated, err := h.translateText(c.Request.Context(), talk.GeneratedText, req.Language, talk.Duration)
	if err != nil {
		c.JSON(http.StatusInternalServerError, ErrorResponse{Error: "Failed to translate: " + err.Error()})
		return
	}

	c.JSON(http.StatusOK, TranslateResponse{Language: req.Language, Text: translated})
}

// translateText asks Gemini to translate a speech into targetLanguage,
// preserving meaning, tone, and approximate spoken length.
func (h *TalkHandler) translateText(ctx context.Context, text string, targetLanguage string, durationMinutes int) (string, error) {
	client, err := genai.NewClient(ctx, option.WithAPIKey(h.cfg.GeminiAPIKey))
	if err != nil {
		return "", fmt.Errorf("failed to init Gemini client: %v", err)
	}
	defer client.Close()

	geminiModel := client.GenerativeModel("gemini-3.1-flash-lite")
	geminiModel.SystemInstruction = &genai.Content{
		Parts: []genai.Part{genai.Text(
			"You are an expert speech translator. Translate the provided speech into the target language, " +
				"preserving its meaning, tone, structure, and speaking pace — this is a translation, not a " +
				"rewrite. Output ONLY the raw translated speech text itself, without any introductory or " +
				"concluding meta-commentary, notes, or markdown formatting (no backticks or ```).",
		)},
	}

	wordCount := durationMinutes * 130
	prompt := fmt.Sprintf(
		"ORIGINAL SPEECH:\n%s\n\nTranslate the speech above into %s. Keep it natural for a native speaker of "+
			"that language while preserving the original meaning and approximate length (approx. %d words).",
		text, targetLanguage, wordCount,
	)

	resp, err := geminiModel.GenerateContent(ctx, genai.Text(prompt))
	if err != nil {
		return "", fmt.Errorf("gemini API error: %v", err)
	}

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


package handler

import (
	"net/http"
	"strings"

	"talkforge-be/model"
	"github.com/gin-gonic/gin"
)

// TalkTypeHandler handles lookup operations for speech/talk types.
type TalkTypeHandler struct{}

// NewTalkTypeHandler instantiates a new TalkTypeHandler.
func NewTalkTypeHandler() *TalkTypeHandler {
	return &TalkTypeHandler{}
}

// TalkTypeResponse represents a localized talk type item returned by lookup API.
type TalkTypeResponse struct {
	ID       uint   `json:"id"`
	Key      string `json:"key"`
	Title    string `json:"title"`
	Symbol   string `json:"symbol"`
	IsCustom bool   `json:"is_custom"`
}

// GetTalkTypes returns a list of dynamic, localized speech types.
// @Summary List dynamic talk types / purposes
// @Description Returns multi-language talk types localized based on Accept-Language header or lang query parameter.
// @Tags Lookup
// @Produce json
// @Param Accept-Language header string false "Language header e.g. tr, en, de"
// @Param lang query string false "Language override query param e.g. tr, en, de"
// @Success 200 {array} TalkTypeResponse
// @Failure 500 {object} ErrorResponse
// @Router /api/v1/talk-types [get]
func (h *TalkTypeHandler) GetTalkTypes(c *gin.Context) {
	lang := c.Query("lang")
	if lang == "" {
		acceptLang := c.GetHeader("Accept-Language")
		lang = parsePreferredLanguage(acceptLang)
	}

	var talkTypes []model.TalkType
	if err := model.DB.Order("sort_order asc").Find(&talkTypes).Error; err != nil {
		c.JSON(http.StatusInternalServerError, ErrorResponse{Error: "Failed to fetch talk types: " + err.Error()})
		return
	}

	response := make([]TalkTypeResponse, 0, len(talkTypes))
	for _, tt := range talkTypes {
		response = append(response, TalkTypeResponse{
			ID:       tt.ID,
			Key:      tt.Key,
			Title:    getLocalizedTitle(&tt, lang),
			Symbol:   tt.Symbol,
			IsCustom: tt.IsCustom,
		})
	}

	c.JSON(http.StatusOK, response)
}

func parsePreferredLanguage(acceptLang string) string {
	if acceptLang == "" {
		return "tr"
	}
	parts := strings.Split(acceptLang, ",")
	if len(parts) == 0 {
		return "tr"
	}

	primary := strings.TrimSpace(parts[0])
	subparts := strings.Split(primary, ";")
	code := strings.ToLower(strings.TrimSpace(subparts[0]))

	if strings.HasPrefix(code, "tr") {
		return "tr"
	} else if strings.HasPrefix(code, "en") {
		return "en"
	} else if strings.HasPrefix(code, "de") {
		return "de"
	} else if strings.HasPrefix(code, "fr") {
		return "fr"
	} else if strings.HasPrefix(code, "es") {
		return "es"
	} else if strings.HasPrefix(code, "ar") {
		return "ar"
	} else if strings.HasPrefix(code, "ru") {
		return "ru"
	}

	return "tr"
}

func getLocalizedTitle(tt *model.TalkType, lang string) string {
	lang = strings.ToLower(lang)
	switch lang {
	case "en":
		if tt.LabelEN != "" { return tt.LabelEN }
	case "de":
		if tt.LabelDE != "" { return tt.LabelDE }
	case "fr":
		if tt.LabelFR != "" { return tt.LabelFR }
	case "es":
		if tt.LabelES != "" { return tt.LabelES }
	case "ar":
		if tt.LabelAR != "" { return tt.LabelAR }
	case "ru":
		if tt.LabelRU != "" { return tt.LabelRU }
	case "tr":
		if tt.LabelTR != "" { return tt.LabelTR }
	}

	if tt.LabelTR != "" {
		return tt.LabelTR
	}
	if tt.LabelEN != "" {
		return tt.LabelEN
	}
	return tt.Key
}

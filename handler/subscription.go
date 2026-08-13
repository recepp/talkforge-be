package handler

import (
	"net/http"
	"strings"
	"time"

	"talkforge-be/config"
	"talkforge-be/model"

	"github.com/gin-gonic/gin"
)

// SubscriptionHandler handles subscription plans and user tier operations.
type SubscriptionHandler struct {
	cfg *config.Config
}

// NewSubscriptionHandler instantiates a new SubscriptionHandler.
func NewSubscriptionHandler(cfg *config.Config) *SubscriptionHandler {
	return &SubscriptionHandler{cfg: cfg}
}

// SubscribeRequest represents payload for subscribing or changing plan.
type SubscribeRequest struct {
	Tier string `json:"tier" binding:"required" example:"pro"`
}

// GetPlans returns the list of available subscription packages.
// @Summary List Subscription Plans
// @Description Returns available tier plans (Free, Pro, Enterprise) with features, prices, and badges.
// @Tags Subscription
// @Produce json
// @Success 200 {array} model.SubscriptionPlan
// @Router /api/v1/plans [get]
func (h *SubscriptionHandler) GetPlans(c *gin.Context) {
	lang := c.GetHeader("Accept-Language")
	if lang == "" {
		lang = "tr"
	}
	lang = strings.ToLower(lang)

	isTR := strings.HasPrefix(lang, "tr")

	var plans []model.SubscriptionPlan

	if isTR {
		plans = []model.SubscriptionPlan{
			{
				ID:          "free",
				Tier:        "free",
				Name:        "Ücretsiz / Standart",
				Price:       "₺0",
				Period:      "/ Ay",
				Badge:       "",
				Description: "TalkForge AI konuşma simülasyonlarını keşfetmek isteyen bireysel kullanıcılar için başlangıç paketi.",
				Features: []string{
					"Günlük 20 Konuşma Düzenleme Limiti",
					"Günlük 5 Konuşma Oluşturma Limiti",
					"Günlük 30.000 Token Limiti",
					"En Fazla 1 Ekip Odası Katılımı",
					"Temel Hitabet Şablonları & Rol Yapma",
					"Standart AI Yanıt Süresi",
				},
				IsPopular:  false,
				ButtonText: "Mevcut Planınız",
			},
			{
				ID:          "pro",
				Tier:        "pro",
				Name:        "Pro Aylık",
				Price:       "$5",
				Period:      "/ Ay",
				Badge:       "En Popüler 🔥",
				Description: "Hitabetini ve ikna kabiliyetini üst seviyeye taşımak isteyen profesyoneller için 3 kat limit.",
				Features: []string{
					"Günlük 60 Konuşma Düzenleme Limiti (3x)",
					"Günlük 15 Konuşma Oluşturma Limiti (3x)",
					"Günlük 90.000 Token Limiti (3x)",
					"En Fazla 3 Ekip Odası Katılımı (3x)",
					"İleri Düzey İkna & İtiraz Yanıtlama",
					"Yüksek Hızlı Gemini 1.5 Pro AI Yanıt Süresi",
					"7/24 Öncelikli Konuşma Çevirileri",
				},
				IsPopular:  true,
				ButtonText: "Pro'ya Yükselt",
			},
			{
				ID:          "enterprise",
				Tier:        "enterprise",
				Name:        "Kurumsal",
				Price:       "$10",
				Period:      "/ Ay",
				Badge:       "Kurumsal ⭐",
				Description: "Geniş ekipler, kurumlar ve organizasyonlar için 10 kat devasa limit paketi.",
				Features: []string{
					"Günlük 200 Konuşma Düzenleme Limiti (10x)",
					"Günlük 50 Konuşma Oluşturma Limiti (10x)",
					"Günlük 300.000 Token Limiti (10x)",
					"En Fazla 10 Ekip Odası Katılımı (10x)",
					"Özel Şirket İçi Sistem İstemleri (Prompts)",
					"7/24 Özel Müşteri Temsilcisi & Destek",
					"Detaylı Kullanım İstatistikleri & Raporlama",
				},
				IsPopular:  false,
				ButtonText: "Kurumsal'a Geç",
			},
		}
	} else {
		plans = []model.SubscriptionPlan{
			{
				ID:          "free",
				Tier:        "free",
				Name:        "Free / Standard",
				Price:       "$0",
				Period:      "/ month",
				Badge:       "",
				Description: "Ideal for individual users discovering TalkForge AI speech simulations.",
				Features: []string{
					"20 Daily Conversation Edits",
					"5 Daily Conversation Creations",
					"30,000 Daily Token Limit",
					"Up to 1 Team Room Membership",
					"Basic Speech Templates & Roleplay",
					"Standard AI Response Time",
				},
				IsPopular:  false,
				ButtonText: "Current Plan",
			},
			{
				ID:          "pro",
				Tier:        "pro",
				Name:        "Pro Monthly",
				Price:       "$5",
				Period:      "/ month",
				Badge:       "Most Popular 🔥",
				Description: "3x limits for professionals elevating their public speaking and persuasion.",
				Features: []string{
					"60 Daily Conversation Edits (3x)",
					"15 Daily Conversation Creations (3x)",
					"90,000 Daily Token Limit (3x)",
					"Up to 3 Team Room Memberships (3x)",
					"Advanced Persuasion & Objection Handling",
					"High-Speed Gemini 1.5 Pro AI Processing",
					"24/7 Priority Speech Translations",
				},
				IsPopular:  true,
				ButtonText: "Upgrade to Pro",
			},
			{
				ID:          "enterprise",
				Tier:        "enterprise",
				Name:        "Enterprise",
				Price:       "$10",
				Period:      "/ month",
				Badge:       "Enterprise ⭐",
				Description: "10x massive limits for large teams, institutions, and enterprise projects.",
				Features: []string{
					"200 Daily Conversation Edits (10x)",
					"50 Daily Conversation Creations (10x)",
					"300,000 Daily Token Limit (10x)",
					"Up to 10 Team Room Memberships (10x)",
					"Custom In-House System Prompts",
					"24/7 Dedicated Support & Manager",
					"Detailed Team Usage Analytics",
				},
				IsPopular:  false,
				ButtonText: "Contact Enterprise",
			},
		}
	}

	c.JSON(http.StatusOK, plans)
}

// GetUserSubscription returns the current user's subscription details.
// @Summary Get Current Subscription
// @Description Returns the active tier, expiry date, and plan details for the authenticated user.
// @Tags Subscription
// @Produce json
// @Security BearerAuth
// @Success 200 {object} model.UserSubscriptionInfo
// @Failure 401 {object} ErrorResponse
// @Router /api/v1/user/subscription [get]
func (h *SubscriptionHandler) GetUserSubscription(c *gin.Context) {
	userIDVal, exists := c.Get("userID")
	if !exists {
		c.JSON(http.StatusUnauthorized, ErrorResponse{Error: "Unauthorized"})
		return
	}

	userID, ok := userIDVal.(uint)
	if !ok {
		c.JSON(http.StatusUnauthorized, ErrorResponse{Error: "Invalid user session"})
		return
	}

	var u model.User
	if err := model.DB.First(&u, userID).Error; err != nil {
		c.JSON(http.StatusNotFound, ErrorResponse{Error: "User not found"})
		return
	}

	tier := u.SubscriptionTier
	if tier == "" {
		tier = "free"
	}

	planName := "Ücretsiz (Free)"
	if tier == "pro" {
		planName = "Pro Aylık"
	} else if tier == "enterprise" {
		planName = "Kurumsal"
	}

	isActive := true
	if u.SubscriptionEndsAt != nil && time.Now().After(*u.SubscriptionEndsAt) {
		isActive = false
	}

	c.JSON(http.StatusOK, model.UserSubscriptionInfo{
		UserID:             u.ID,
		SubscriptionTier:   tier,
		SubscriptionEndsAt: u.SubscriptionEndsAt,
		IsActive:           isActive,
		PlanName:           planName,
	})
}

// Subscribe simulates or prepares user subscription upgrade.
// @Summary Subscribe or Upgrade Plan
// @Description Endpoint prepared for payment gateway integration (Stripe/Iyzico).
// @Tags Subscription
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param request body SubscribeRequest true "Subscribe Payload"
// @Success 200 {object} map[string]interface{}
// @Failure 400 {object} ErrorResponse
// @Failure 401 {object} ErrorResponse
// @Router /api/v1/user/subscription [post]
func (h *SubscriptionHandler) Subscribe(c *gin.Context) {
	userIDVal, exists := c.Get("userID")
	if !exists {
		c.JSON(http.StatusUnauthorized, ErrorResponse{Error: "Unauthorized"})
		return
	}

	userID, ok := userIDVal.(uint)
	if !ok {
		c.JSON(http.StatusUnauthorized, ErrorResponse{Error: "Invalid user session"})
		return
	}

	var req SubscribeRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, ErrorResponse{Error: err.Error()})
		return
	}

	tier := strings.ToLower(req.Tier)
	if tier != "free" && tier != "pro" && tier != "enterprise" {
		c.JSON(http.StatusBadRequest, ErrorResponse{Error: "Invalid subscription tier. Allowed: free, pro, enterprise"})
		return
	}

	var u model.User
	if err := model.DB.First(&u, userID).Error; err != nil {
		c.JSON(http.StatusNotFound, ErrorResponse{Error: "User not found"})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"message": "Ödeme altyapısı yakında aktif edilecektir, gösterilen ilgi için teşekkürler!",
		"status":  "payment_gateway_pending",
		"tier":    tier,
		"user_id": userID,
	})
}

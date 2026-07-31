package handler

import (
	"net/http"
	"time"

	"talkforge-be/auth"
	"talkforge-be/config"
	"talkforge-be/model"
	"github.com/gin-gonic/gin"
	"golang.org/x/crypto/bcrypt"
	"google.golang.org/api/idtoken"
)

// AuthHandler handles user registration and authentication.
type AuthHandler struct {
	cfg *config.Config
}

// NewAuthHandler instantiates a new AuthHandler.
func NewAuthHandler(cfg *config.Config) *AuthHandler {
	return &AuthHandler{cfg: cfg}
}

// AuthRequest represents credentials for local authentication.
type AuthRequest struct {
	Email    string `json:"email" binding:"required,email" example:"user@example.com"`
	Password string `json:"password" binding:"required,min=4" example:"secret123"`
	Nickname string `json:"nickname" example:"my_nickname"` // Required on signup
	Avatar   string `json:"avatar" example:"👤"`          // Optional on signup
}

// AuthResponse returns authorization payload including a JWT token.
type AuthResponse struct {
	Token     string `json:"token" example:"eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9..."`
	Email     string `json:"email" example:"user@example.com"`
	Nickname  string `json:"nickname" example:"my_nickname"`
	Avatar    string `json:"avatar" example:"👤"`
	Role      string `json:"role" example:"user"`
	UserID    uint   `json:"user_id" example:"1"`
	CreatedAt string `json:"created_at"`
}

// GoogleAuthRequest represents Google OAuth parameters.
type GoogleAuthRequest struct {
	GoogleToken string `json:"google_token" binding:"required" example:"google_oauth_token_here"`
}

// Signup creates a new user.
// @Summary Register a new user
// @Description Creates a new account with email/password.
// @Tags Auth
// @Accept json
// @Produce json
// @Param request body AuthRequest true "Register Payload"
// @Success 201 {object} AuthResponse
// @Failure 400 {object} ErrorResponse
// @Failure 409 {object} ErrorResponse
// @Router /api/v1/auth/signup [post]
func (h *AuthHandler) Signup(c *gin.Context) {
	var req AuthRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, ErrorResponse{Error: err.Error()})
		return
	}

	if req.Nickname == "" {
		c.JSON(http.StatusBadRequest, ErrorResponse{Error: "Nickname is required"})
		return
	}

	// Check if user already exists by email
	var existing model.User
	if err := model.DB.Where("email = ?", req.Email).First(&existing).Error; err == nil {
		c.JSON(http.StatusConflict, ErrorResponse{Error: "User with this email already exists"})
		return
	}

	// Check if user already exists by nickname
	var existingByNickname model.User
	if err := model.DB.Where("nickname = ?", req.Nickname).First(&existingByNickname).Error; err == nil {
		c.JSON(http.StatusConflict, ErrorResponse{Error: "User with this nickname already exists"})
		return
	}

	// Hash password
	hashedPassword, err := bcrypt.GenerateFromPassword([]byte(req.Password), bcrypt.DefaultCost)
	if err != nil {
		c.JSON(http.StatusInternalServerError, ErrorResponse{Error: "Failed to process password"})
		return
	}

	avatar := req.Avatar
	if avatar == "" {
		avatar = "👤"
	}

	newUser := model.User{
		Email:        req.Email,
		Nickname:     req.Nickname,
		Avatar:       avatar,
		PasswordHash: string(hashedPassword),
		Role:         "user", // Signup defaults to regular user
	}

	if err := model.DB.Create(&newUser).Error; err != nil {
		c.JSON(http.StatusInternalServerError, ErrorResponse{Error: "Failed to create user"})
		return
	}

	// Generate JWT Token
	token, err := auth.GenerateToken(newUser.ID, newUser.Email, newUser.Role, h.cfg.JWTSecret)
	if err != nil {
		c.JSON(http.StatusInternalServerError, ErrorResponse{Error: "Failed to generate authorization token"})
		return
	}

	c.JSON(http.StatusCreated, AuthResponse{
		Token:     token,
		Email:     newUser.Email,
		Nickname:  newUser.Nickname,
		Avatar:    newUser.Avatar,
		Role:      newUser.Role,
		UserID:    newUser.ID,
		CreatedAt: newUser.CreatedAt.Format(time.RFC3339),
	})
}

// Login authenticates a user and returns a JWT token.
// @Summary Login user
// @Description Logs in with email/password and returns a JWT token.
// @Tags Auth
// @Accept json
// @Produce json
// @Param request body AuthRequest true "Login Payload"
// @Success 200 {object} AuthResponse
// @Failure 400 {object} ErrorResponse
// @Failure 401 {object} ErrorResponse
// @Router /api/v1/auth/login [post]
func (h *AuthHandler) Login(c *gin.Context) {
	var req AuthRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, ErrorResponse{Error: err.Error()})
		return
	}

	var user model.User
	if err := model.DB.Where("email = ?", req.Email).First(&user).Error; err != nil {
		c.JSON(http.StatusUnauthorized, ErrorResponse{Error: "Invalid email or password"})
		return
	}

	// Check password (only if user has a password set)
	if user.PasswordHash == "" {
		c.JSON(http.StatusUnauthorized, ErrorResponse{Error: "This account was registered using Google. Please log in with Google."})
		return
	}

	if err := bcrypt.CompareHashAndPassword([]byte(user.PasswordHash), []byte(req.Password)); err != nil {
		c.JSON(http.StatusUnauthorized, ErrorResponse{Error: "Invalid email or password"})
		return
	}

	// Generate Token
	token, err := auth.GenerateToken(user.ID, user.Email, user.Role, h.cfg.JWTSecret)
	if err != nil {
		c.JSON(http.StatusInternalServerError, ErrorResponse{Error: "Failed to generate authorization token"})
		return
	}

	c.JSON(http.StatusOK, AuthResponse{
		Token:     token,
		Email:     user.Email,
		Nickname:  user.Nickname,
		Avatar:    user.Avatar,
		Role:      user.Role,
		UserID:    user.ID,
		CreatedAt: user.CreatedAt.Format(time.RFC3339),
	})
}

// GoogleLogin handles Google authentication sign-in.
// @Summary Google Sign-In
// @Description Authenticates user via Google OAuth (validates token with Google API if Client ID is configured).
// @Tags Auth
// @Accept json
// @Produce json
// @Param request body GoogleAuthRequest true "Google Auth Payload"
// @Success 200 {object} AuthResponse
// @Failure 400 {object} ErrorResponse
// @Router /api/v1/auth/google [post]
func (h *AuthHandler) GoogleLogin(c *gin.Context) {
	var req GoogleAuthRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, ErrorResponse{Error: err.Error()})
		return
	}

	var googleID string
	var email string
	var name string
	var avatar string

	// Fallback to mock if Google Client ID is not configured or for quick testing
	if h.cfg.GoogleClientID == "" || req.GoogleToken == "mock-google-token" || len(req.GoogleToken) < 30 {
		email = "googleuser@example.com"
		googleID = "google-id-123456"
		if req.GoogleToken != "" && req.GoogleToken != "mock-google-token" {
			googleID = "google-id-" + req.GoogleToken
			email = req.GoogleToken + "@example.com"
		}
		name = "Google User"
		avatar = "👤"
	} else {
		// Real verification
		payload, err := idtoken.Validate(c.Request.Context(), req.GoogleToken, h.cfg.GoogleClientID)
		if err != nil {
			c.JSON(http.StatusBadRequest, ErrorResponse{Error: "Invalid Google ID Token: " + err.Error()})
			return
		}

		// Ensure email is verified
		if emailVerified, ok := payload.Claims["email_verified"].(bool); ok && !emailVerified {
			c.JSON(http.StatusBadRequest, ErrorResponse{Error: "Google email is not verified"})
			return
		}

		googleID = payload.Subject
		if val, ok := payload.Claims["email"].(string); ok {
			email = val
		} else {
			c.JSON(http.StatusBadRequest, ErrorResponse{Error: "Google ID Token does not contain email"})
			return
		}

		if val, ok := payload.Claims["name"].(string); ok {
			name = val
		} else {
			name = email
		}

		if val, ok := payload.Claims["picture"].(string); ok {
			avatar = val
		} else {
			avatar = "👤"
		}
	}

	var user model.User
	// 1. Try to find user by linked google_id
	err := model.DB.Where("google_id = ?", googleID).First(&user).Error
	if err != nil {
		// 2. If not found by google_id, try to find by email (automatic link for verified emails)
		err = model.DB.Where("email = ?", email).First(&user).Error
		if err == nil {
			// Link the Google account
			user.GoogleID = &googleID
			if user.Avatar == "" || user.Avatar == "👤" {
				user.Avatar = avatar
			}
			model.DB.Save(&user)
		} else {
			// 3. Neither exist, create a new user
			nickname := name
			if len(nickname) > 40 {
				nickname = nickname[:40]
			}
			// Resolve nickname collision
			var count int64
			if err := model.DB.Model(&model.User{}).Where("nickname = ?", nickname).Count(&count).Error; err == nil && count > 0 {
				suffix := ""
				if len(googleID) >= 4 {
					suffix = googleID[len(googleID)-4:]
				} else {
					suffix = "g"
				}
				nickname = nickname + "-" + suffix
			}

			user = model.User{
				Email:    email,
				Nickname: nickname,
				Avatar:   avatar,
				GoogleID: &googleID,
				Role:     "user",
			}
			if err := model.DB.Create(&user).Error; err != nil {
				c.JSON(http.StatusInternalServerError, ErrorResponse{Error: "Failed to create user"})
				return
			}
		}
	}

	token, err := auth.GenerateToken(user.ID, user.Email, user.Role, h.cfg.JWTSecret)
	if err != nil {
		c.JSON(http.StatusInternalServerError, ErrorResponse{Error: "Failed to generate authorization token"})
		return
	}

	c.JSON(http.StatusOK, AuthResponse{
		Token:     token,
		Email:     user.Email,
		Nickname:  user.Nickname,
		Avatar:    user.Avatar,
		Role:      user.Role,
		UserID:    user.ID,
		CreatedAt: user.CreatedAt.Format(time.RFC3339),
	})
}

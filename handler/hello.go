package handler

import (
	"net/http"

	"github.com/gin-gonic/gin"
)

// HelloHandler handles greeting endpoints for role and permission verification.
type HelloHandler struct{}

// NewHelloHandler instantiates a new HelloHandler.
func NewHelloHandler() *HelloHandler {
	return &HelloHandler{}
}

// HelloPublic greets anyone without authentication.
// @Summary Public Hello
// @Description Returns a public greeting without requiring authentication.
// @Tags Hello
// @Produce json
// @Success 200 {object} map[string]string
// @Router /api/v1/hello/public [get]
func (h *HelloHandler) HelloPublic(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{
		"message": "Hello from Public endpoint! No authentication required.",
	})
}

// HelloUser greets authenticated users (requires user role or admin role).
// @Summary User Hello
// @Description Returns a greeting for authenticated users.
// @Tags Hello
// @Security BearerAuth
// @Produce json
// @Success 200 {object} map[string]string
// @Failure 401 {object} ErrorResponse
// @Router /api/v1/hello/user [get]
func (h *HelloHandler) HelloUser(c *gin.Context) {
	email, _ := c.Get("userEmail")
	role, _ := c.Get("userRole")
	c.JSON(http.StatusOK, gin.H{
		"message": "Hello from User endpoint!",
		"email":   email,
		"role":    role,
	})
}

// HelloAdmin greets authenticated admins (requires admin role).
// @Summary Admin Hello
// @Description Returns a greeting for authenticated administrators only.
// @Tags Hello
// @Security BearerAuth
// @Produce json
// @Success 200 {object} map[string]string
// @Failure 401 {object} ErrorResponse
// @Failure 403 {object} ErrorResponse
// @Router /api/v1/hello/admin [get]
func (h *HelloHandler) HelloAdmin(c *gin.Context) {
	email, _ := c.Get("userEmail")
	c.JSON(http.StatusOK, gin.H{
		"message": "Hello from Admin endpoint! Only accessible by administrators.",
		"email":   email,
		"role":    "admin",
	})
}

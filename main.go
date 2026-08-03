package main

import (
	"context"
	"log"
	"net/http"

	"talkforge-be/auth"
	"talkforge-be/config"
	_ "talkforge-be/docs"
	"talkforge-be/handler"
	"talkforge-be/model"
	"talkforge-be/worker"

	"github.com/gin-gonic/gin"
	swaggerFiles "github.com/swaggo/files"
	ginSwagger "github.com/swaggo/gin-swagger"
)

// @title TalkForge API
// @version 1.0
// @description Production-ready authentication and role verification API for TalkForge.
// @termsOfService http://swagger.io/terms/

// @contact.name TalkForge Support
// @contact.email support@talkforge.local

// @license.name Apache 2.0
// @license.url http://www.apache.org/licenses/LICENSE-2.0.html

// @BasePath /

// @securityDefinitions.apikey BearerAuth
// @in header
// @name Authorization
func main() {
	// Load Configuration
	cfg := config.LoadConfig()

	// Initialize Database (AutoMigrate is run inside InitDB)
	_, err := model.InitDB(cfg)
	if err != nil {
		log.Fatalf("Database initialization failed: %v", err)
	}

	// Start Background Queue Worker
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	qWorker := worker.NewWorker(cfg)
	go qWorker.Start(ctx)

	// Set Gin mode
	gin.SetMode(cfg.GinMode)

	// Initialize Gin router
	r := gin.Default()

	// Enable CORS (simple middleware for ease of use)
	r.Use(func(c *gin.Context) {
		c.Writer.Header().Set("Access-Control-Allow-Origin", "*")
		c.Writer.Header().Set("Access-Control-Allow-Credentials", "true")
		c.Writer.Header().Set("Access-Control-Allow-Headers", "Content-Type, Content-Length, Accept-Encoding, X-CSRF-Token, Authorization, accept, origin, Cache-Control, X-Requested-With")
		c.Writer.Header().Set("Access-Control-Allow-Methods", "POST, OPTIONS, GET, PUT, DELETE")

		if c.Request.Method == "OPTIONS" {
			c.AbortWithStatus(204)
			return
		}
		c.Next()
	})

	// Health check route
	// @Summary API Health Check
	// @Description Returns the status of the server to verify it is running.
	// @Tags Utility
	// @Produce json
	// @Success 200 {object} map[string]string
	// @Router /health [get]
	r.GET("/health", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{
			"status":  "healthy",
			"message": "TalkForge server is running smoothly",
		})
	})

	// Swagger UI route
	r.GET("/swagger/*any", ginSwagger.WrapHandler(swaggerFiles.Handler))

	// Instantiating Handlers
	authHandler := handler.NewAuthHandler(cfg)
	helloHandler := handler.NewHelloHandler()
	talkHandler := handler.NewTalkHandler()

	// API Routing Group
	api := r.Group("/api/v1")
	{
		// Public Auth Endpoints
		authGroup := api.Group("/auth")
		{
			authGroup.POST("/signup", authHandler.Signup)
			authGroup.POST("/login", authHandler.Login)
			authGroup.POST("/google", authHandler.GoogleLogin)
		}

		// Hello Endpoints
		helloGroup := api.Group("/hello")
		{
			// Public Hello (no auth)
			helloGroup.GET("/public", helloHandler.HelloPublic)

			// Authenticated User Hello (requires JWT auth)
			authorizedUser := helloGroup.Group("/")
			authorizedUser.Use(auth.AuthMiddleware(cfg.JWTSecret))
			{
				authorizedUser.GET("/user", helloHandler.HelloUser)
			}

			// Authenticated Admin Hello (requires JWT auth & Admin middleware)
			authorizedAdmin := helloGroup.Group("/")
			authorizedAdmin.Use(auth.AuthMiddleware(cfg.JWTSecret), auth.AdminMiddleware())
			{
				authorizedAdmin.GET("/admin", helloHandler.HelloAdmin)
			}
		}

		// Talk Generation Endpoints (Protected by AuthMiddleware)
		talksGroup := api.Group("/talks")
		talksGroup.Use(auth.AuthMiddleware(cfg.JWTSecret))
		{
			talksGroup.POST("", talkHandler.CreateTalkRequest)
			talksGroup.GET("", talkHandler.ListTalkRequests)
			talksGroup.DELETE("/:id", talkHandler.DeleteTalkRequest)
		}
	}

	// Start Server
	log.Printf("Starting TalkForge server on port %s", cfg.Port)
	log.Printf("Swagger documentation is available at http://localhost:%s/swagger/index.html", cfg.Port)
	if err := r.Run(":" + cfg.Port); err != nil {
		log.Fatalf("Failed to run server: %v", err)
	}
}

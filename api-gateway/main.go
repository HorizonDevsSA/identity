package main

import (
	"log"
	"os"

	"api-gateway/db"
	"api-gateway/handlers"
	"api-gateway/models"
	"api-gateway/services"

	"github.com/gofiber/fiber/v2"
	"github.com/gofiber/fiber/v2/middleware/cors"
	"github.com/gofiber/fiber/v2/middleware/logger"
	"github.com/joho/godotenv"
)

func main() {
	// Load .env file if it exists (primarily for local run outside Docker)
	if err := godotenv.Load("../.env"); err != nil {
		log.Println("Note: No .env file found in parent directory, using system env variables")
	} else if err := godotenv.Load(); err != nil {
		log.Println("Note: No local .env file found, using system env variables")
	}

	// Initialize services
	db.Connect()
	services.InitStorage()
	services.InitRedis()
	handlers.InitJWTSecret()
	handlers.InitTwilio()
	if err := services.InitFirebase(); err != nil {
		log.Printf("[FIREBASE-INIT-WARN] Firebase initialization warning: %v", err)
	}

	app := fiber.New(fiber.Config{
		BodyLimit: 10 * 1024 * 1024, // 10MB limit
	})

	// Middleware
	app.Use(cors.New())
	app.Use(logger.New())

	// Public routes
	app.Get("/health", func(c *fiber.Ctx) error {
		return c.JSON(fiber.Map{"status": "healthy", "service": "api-gateway"})
	})
	// Twilio phone (SMS/call/whatsapp OTP) authentication & webhooks
	app.Post("/auth/otp/send", handlers.SendOTP)
	app.Post("/auth/otp/resend", handlers.ResendOTP)
	app.Post("/auth/otp/verify", handlers.VerifyOTP)
	app.Post("/auth/twilio/webhook", handlers.TwilioWebhookCallback)

	// Open Public KYC Document Upload (bypasses tenant, automatic fallback to default system tenant)
	app.Post("/api/documents/upload", handlers.OptionalJWTMiddleware, handlers.UploadDocument)

	// Open Verification Status Lookup (for checking KYC verification status)
	app.Post("/api/documents/verify-status", handlers.OptionalJWTMiddleware, handlers.CheckVerificationStatus)

	// All identity API routes are open (no auth required).
	// OptionalJWTMiddleware still resolves the caller's tenant/role when a token is provided.
	adminApi := app.Group("/api", handlers.OptionalJWTMiddleware)

	// Documents endpoints (Admin-restricted view & inspect)
	adminApi.Get("/documents", handlers.ListDocuments)
	adminApi.Get("/documents/:id", handlers.GetDocument)
	adminApi.Get("/documents/:id/prediction", handlers.GetDocumentPrediction)
	adminApi.Get("/documents/:id/extracted-fields", handlers.GetExtractedFields)

	// Reviews & manual verification endpoints (Admin/Reviewer only)
	adminApi.Get("/reviews", handlers.ListReviewQueue)
	adminApi.Post("/documents/:id/review", handlers.ReviewDocument)

	// Datasets endpoints
	adminApi.Post("/datasets", handlers.CreateDataset)
	adminApi.Get("/datasets", handlers.ListDatasets)
	adminApi.Get("/datasets/:id", handlers.GetDataset)
	adminApi.Delete("/datasets/:id", handlers.DeleteDataset)

	// Dataset images endpoints
	adminApi.Post("/datasets/:id/images", handlers.UploadDatasetImage)
	adminApi.Get("/datasets/images/:image_id", handlers.GetDatasetImage)
	adminApi.Delete("/datasets/images/:image_id", handlers.DeleteDatasetImage)

	// Image annotations endpoints
	adminApi.Post("/datasets/images/:image_id/annotations", handlers.SaveAnnotations)
	adminApi.Get("/datasets/images/:image_id/annotations", handlers.GetAnnotations)

	// Training endpoints
	adminApi.Post("/training/start", handlers.StartTrainingJob)
	adminApi.Get("/training/jobs", handlers.ListTrainingJobs)
	adminApi.Get("/training/jobs/:id", handlers.GetTrainingJob)

	// Models endpoints
	adminApi.Get("/models", handlers.ListModels)
	adminApi.Post("/models/:id/deploy", handlers.DeployModelVersion)

	// Extraction schemas
	adminApi.Post("/schemas", handlers.CreateSchema)
	adminApi.Get("/schemas", handlers.ListSchemas)
	adminApi.Get("/schemas/:id", handlers.GetSchema)
	adminApi.Delete("/schemas/:id", handlers.DeleteSchema)

	// Notifications & Preferences endpoints
	adminApi.Get("/notifications", handlers.ListNotifications)
	adminApi.Post("/notifications/:id/read", handlers.MarkNotificationRead)
	adminApi.Get("/notifications/preferences", handlers.GetNotificationPreferences)
	adminApi.Put("/notifications/preferences", handlers.UpdateNotificationPreferences)
	adminApi.Post("/devices/tokens", handlers.RegisterDeviceToken)
	adminApi.Delete("/devices/tokens", handlers.DeleteDeviceToken)
	adminApi.Post("/transactions/notify", handlers.NotifyTransaction)

	// Device Trust, Biometric Session Refresh & Security Audit endpoints
	adminApi.Get("/devices", handlers.ListDevices)
	adminApi.Delete("/devices/:id", handlers.RevokeDevice)
	adminApi.Post("/devices/biometric-refresh", handlers.BiometricSessionRefresh)
	adminApi.Get("/security/audit-logs", handlers.GetSecurityAuditLogs)

	// Merchant Profile endpoints
	adminApi.Post("/merchants/register", handlers.RegisterMerchant)
	adminApi.Get("/merchants/profile", handlers.RequireRoles(models.RoleMerchant, models.RoleAdmin), handlers.GetMerchantProfile)

	// Admin Governance & Role Assignment
	adminApi.Post("/admin/roles/assign", handlers.RequireAdmin, handlers.AssignUserRole)

	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}

	log.Printf("Starting API Gateway on port %s...", port)
	if err := app.Listen(":" + port); err != nil {
		log.Fatalf("Failed to start server: %v", err)
	}
}

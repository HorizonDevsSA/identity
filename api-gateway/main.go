package main

import (
	"log"
	"os"

	"api-gateway/db"
	"api-gateway/handlers"
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

	app := fiber.New(fiber.Config{
		BodyLimit: 50 * 1024 * 1024, // 50MB limit
	})

	// Middleware
	app.Use(cors.New())
	app.Use(logger.New())

	// Public routes
	app.Get("/health", func(c *fiber.Ctx) error {
		return c.JSON(fiber.Map{"status": "healthy", "service": "api-gateway"})
	})
	app.Post("/auth/register", handlers.Register)
	app.Post("/auth/login", handlers.Login)

	// Authenticated routes
	api := app.Group("/api", handlers.JWTMiddleware)

	// Documents endpoints
	api.Post("/documents/upload", handlers.UploadDocument)
	api.Get("/documents", handlers.ListDocuments)
	api.Get("/documents/:id", handlers.GetDocument)
	api.Get("/documents/:id/prediction", handlers.GetDocumentPrediction)

	// Datasets endpoints
	api.Post("/datasets", handlers.CreateDataset)
	api.Get("/datasets", handlers.ListDatasets)
	api.Get("/datasets/:id", handlers.GetDataset)
	api.Delete("/datasets/:id", handlers.DeleteDataset)

	// Dataset images endpoints
	api.Post("/datasets/:id/images", handlers.UploadDatasetImage)
	api.Get("/datasets/images/:image_id", handlers.GetDatasetImage)
	api.Delete("/datasets/images/:image_id", handlers.DeleteDatasetImage)

	// Image annotations endpoints
	api.Post("/datasets/images/:image_id/annotations", handlers.SaveAnnotations)
	api.Get("/datasets/images/:image_id/annotations", handlers.GetAnnotations)

	// Training endpoints
	api.Post("/training/start", handlers.StartTrainingJob)
	api.Get("/training/jobs", handlers.ListTrainingJobs)
	api.Get("/training/jobs/:id", handlers.GetTrainingJob)

	// Models endpoints
	api.Get("/models", handlers.ListModels)
	api.Post("/models/:id/deploy", handlers.DeployModelVersion)

	// Extraction schemas
	api.Post("/schemas", handlers.CreateSchema)
	api.Get("/schemas", handlers.ListSchemas)
	api.Get("/schemas/:id", handlers.GetSchema)
	api.Delete("/schemas/:id", handlers.DeleteSchema)

	// Extracted fields
	api.Get("/documents/:id/extracted-fields", handlers.GetExtractedFields)

	// Reviews & manual verification endpoints
	api.Get("/reviews", handlers.ListReviewQueue)
	api.Post("/documents/:id/review", handlers.ReviewDocument)



	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}

	log.Printf("Starting API Gateway on port %s...", port)
	if err := app.Listen(":" + port); err != nil {
		log.Fatalf("Failed to start server: %v", err)
	}
}

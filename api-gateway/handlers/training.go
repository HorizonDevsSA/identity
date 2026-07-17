package handlers

import (
	"context"
	"fmt"
	"log"

	"api-gateway/db"
	"api-gateway/models"
	"api-gateway/services"

	"github.com/gofiber/fiber/v2"
	"github.com/google/uuid"
	"gorm.io/gorm"
)

type StartTrainingRequest struct {
	DatasetID    string  `json:"dataset_id"`
	ModelName    string  `json:"model_name"`
	BaseModel    string  `json:"base_model"` // e.g. "db_resnet50", "crnn_vgg16_bn"
	Epochs       int     `json:"epochs"`
	LearningRate float64 `json:"learning_rate"`
}

type DeployModelRequest struct {
	VersionID string `json:"version_id"`
}

// StartTrainingJob initiates a new training process
func StartTrainingJob(c *fiber.Ctx) error {
	tenantIDStr, ok := c.Locals("tenant_id").(string)
	if !ok || tenantIDStr == "" {
		return c.Status(fiber.StatusUnauthorized).JSON(fiber.Map{"error": "Unauthorized tenant"})
	}
	tenantID, err := uuid.Parse(tenantIDStr)
	if err != nil {
		return c.Status(fiber.StatusUnauthorized).JSON(fiber.Map{"error": "Invalid tenant ID"})
	}

	var req StartTrainingRequest
	if err := c.BodyParser(&req); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "Invalid request body"})
	}

	if req.DatasetID == "" || req.ModelName == "" {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "Dataset ID and model name are required"})
	}

	datasetID, err := uuid.Parse(req.DatasetID)
	if err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "Invalid dataset ID"})
	}

	// Verify dataset exists and belongs to the tenant
	var dataset models.Dataset
	if err := db.DB.Where("id = ? AND tenant_id = ?", datasetID, tenantID).First(&dataset).Error; err != nil {
		return c.Status(fiber.StatusNotFound).JSON(fiber.Map{"error": "Dataset not found"})
	}

	// Set defaults
	if req.BaseModel == "" {
		req.BaseModel = "db_resnet50"
	}
	if req.Epochs <= 0 {
		req.Epochs = 10
	}
	if req.LearningRate <= 0 {
		req.LearningRate = 0.001
	}

	var model models.Model
	err = db.DB.Where("tenant_id = ? AND name = ?", tenantID, req.ModelName).First(&model).Error
	if err != nil {
		if err == gorm.ErrRecordNotFound {
			// Create a new Model record
			model = models.Model{
				ID:          uuid.New(),
				Name:        req.ModelName,
				Description: fmt.Sprintf("Custom trained model based on %s", req.BaseModel),
				BaseModel:   req.BaseModel,
				TenantID:    tenantID,
				ProjectID:   dataset.ProjectID,
			}
			if err := db.DB.Create(&model).Error; err != nil {
				return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "Failed to create model registry entry"})
			}
		} else {
			return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "Database error checking model registry"})
		}
	}

	// Create TrainingJob entry
	jobID := uuid.New()
	job := models.TrainingJob{
		ID:           jobID,
		TenantID:     tenantID,
		ProjectID:    dataset.ProjectID,
		DatasetID:    datasetID,
		ModelID:      model.ID,
		BaseModel:    req.BaseModel,
		Status:       "pending",
		Epochs:       req.Epochs,
		LearningRate: req.LearningRate,
		Logs:         "Job registered. Waiting for worker...\n",
	}

	if err := db.DB.Create(&job).Error; err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "Failed to create training job in database"})
	}

	// Publish to Redis
	payload := services.TrainingJobPayload{
		JobID:        jobID.String(),
		TenantID:     tenantID.String(),
		ProjectID:    dataset.ProjectID.String(),
		DatasetID:    datasetID.String(),
		ModelID:      model.ID.String(),
		BaseModel:    req.BaseModel,
		Epochs:       req.Epochs,
		LearningRate: req.LearningRate,
	}

	ctx := context.Background()
	if err := services.PublishTrainingJob(ctx, payload); err != nil {
		log.Printf("Failed to publish training job: %v", err)
		db.DB.Model(&job).Update("status", "failed")
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "Failed to queue training job"})
	}

	return c.Status(fiber.StatusAccepted).JSON(job)
}

// ListTrainingJobs lists all training jobs for the tenant
func ListTrainingJobs(c *fiber.Ctx) error {
	tenantIDStr, ok := c.Locals("tenant_id").(string)
	if !ok || tenantIDStr == "" {
		return c.Status(fiber.StatusUnauthorized).JSON(fiber.Map{"error": "Unauthorized tenant"})
	}

	var jobs []models.TrainingJob
	err := db.DB.Where("tenant_id = ?", tenantIDStr).Order("created_at desc").Find(&jobs).Error
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "Failed to fetch training jobs"})
	}

	return c.JSON(jobs)
}

// GetTrainingJob gets details for a single training job
func GetTrainingJob(c *fiber.Ctx) error {
	tenantIDStr, ok := c.Locals("tenant_id").(string)
	if !ok || tenantIDStr == "" {
		return c.Status(fiber.StatusUnauthorized).JSON(fiber.Map{"error": "Unauthorized tenant"})
	}

	jobIDStr := c.Params("id")
	jobID, err := uuid.Parse(jobIDStr)
	if err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "Invalid training job ID"})
	}

	var job models.TrainingJob
	if err := db.DB.Where("id = ? AND tenant_id = ?", jobID, tenantIDStr).First(&job).Error; err != nil {
		return c.Status(fiber.StatusNotFound).JSON(fiber.Map{"error": "Training job not found"})
	}

	return c.JSON(job)
}

// ListModels lists all models and their version history
func ListModels(c *fiber.Ctx) error {
	tenantIDStr, ok := c.Locals("tenant_id").(string)
	if !ok || tenantIDStr == "" {
		return c.Status(fiber.StatusUnauthorized).JSON(fiber.Map{"error": "Unauthorized tenant"})
	}

	var registry []models.Model
	err := db.DB.Preload("Versions").Where("tenant_id = ?", tenantIDStr).Order("created_at desc").Find(&registry).Error
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "Failed to fetch models"})
	}

	return c.JSON(registry)
}

// DeployModelVersion sets a specific model version to active
func DeployModelVersion(c *fiber.Ctx) error {
	tenantIDStr, ok := c.Locals("tenant_id").(string)
	if !ok || tenantIDStr == "" {
		return c.Status(fiber.StatusUnauthorized).JSON(fiber.Map{"error": "Unauthorized tenant"})
	}

	modelIDStr := c.Params("id")
	modelID, err := uuid.Parse(modelIDStr)
	if err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "Invalid model ID"})
	}

	var req DeployModelRequest
	if err := c.BodyParser(&req); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "Invalid request body"})
	}

	if req.VersionID == "" {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "Version ID is required"})
	}

	versionID, err := uuid.Parse(req.VersionID)
	if err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "Invalid version ID"})
	}

	// Verify model belongs to the tenant
	var model models.Model
	if err := db.DB.Where("id = ? AND tenant_id = ?", modelID, tenantIDStr).First(&model).Error; err != nil {
		return c.Status(fiber.StatusNotFound).JSON(fiber.Map{"error": "Model not found"})
	}

	// Verify version belongs to this model
	var targetVersion models.ModelVersion
	if err := db.DB.Where("id = ? AND model_id = ?", versionID, modelID).First(&targetVersion).Error; err != nil {
		return c.Status(fiber.StatusNotFound).JSON(fiber.Map{"error": "Model version not found for this model"})
	}

	// Run update inside a transaction to ensure mutual exclusivity of active status
	err = db.DB.Transaction(func(tx *gorm.DB) error {
		// Set all versions of this model to inactive
		err := tx.Model(&models.ModelVersion{}).Where("model_id = ?", modelID).Update("status", "inactive").Error
		if err != nil {
			return err
		}

		// Set targeted version to active
		err = tx.Model(&targetVersion).Update("status", "active").Error
		if err != nil {
			return err
		}

		return nil
	})

	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": fmt.Sprintf("Failed to deploy model version: %v", err)})
	}

	return c.JSON(fiber.Map{"message": fmt.Sprintf("Successfully deployed version %s of model %s", targetVersion.Version, model.Name)})
}

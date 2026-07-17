package handlers

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"

	"api-gateway/db"
	"api-gateway/models"
	"api-gateway/services"
	"github.com/gofiber/fiber/v2"
	"github.com/google/uuid"
	"github.com/minio/minio-go/v7"
	"gorm.io/gorm"
)

type CreateDatasetRequest struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	ProjectID   string `json:"project_id"`
}

type AnnotationInput struct {
	BoundingBox string `json:"bounding_box"`
	Label       string `json:"label"`
}

// CreateDataset creates a new dataset
func CreateDataset(c *fiber.Ctx) error {
	tenantIDStr, ok := c.Locals("tenant_id").(string)
	if !ok || tenantIDStr == "" {
		return c.Status(fiber.StatusUnauthorized).JSON(fiber.Map{"error": "Unauthorized tenant"})
	}
	tenantID, err := uuid.Parse(tenantIDStr)
	if err != nil {
		return c.Status(fiber.StatusUnauthorized).JSON(fiber.Map{"error": "Invalid tenant ID"})
	}

	var req CreateDatasetRequest
	if err := c.BodyParser(&req); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "Invalid request body"})
	}

	if req.Name == "" {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "Dataset name is required"})
	}

	var projectID uuid.UUID
	if req.ProjectID != "" {
		projectID, err = uuid.Parse(req.ProjectID)
		if err != nil {
			return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "Invalid project ID"})
		}
	} else {
		// Find default project
		var defaultProj models.Project
		err = db.DB.Where("tenant_id = ? AND name = ?", tenantID, "Default Project").First(&defaultProj).Error
		if err != nil {
			return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "Default project not found"})
		}
		projectID = defaultProj.ID
	}

	dataset := models.Dataset{
		ID:          uuid.New(),
		Name:        req.Name,
		Description: req.Description,
		TenantID:    tenantID,
		ProjectID:   projectID,
	}

	if err := db.DB.Create(&dataset).Error; err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "Failed to create dataset"})
	}

	return c.Status(fiber.StatusCreated).JSON(dataset)
}

// ListDatasets lists datasets for the tenant
func ListDatasets(c *fiber.Ctx) error {
	tenantIDStr, ok := c.Locals("tenant_id").(string)
	if !ok || tenantIDStr == "" {
		return c.Status(fiber.StatusUnauthorized).JSON(fiber.Map{"error": "Unauthorized tenant"})
	}

	var datasets []models.Dataset
	if err := db.DB.Where("tenant_id = ?", tenantIDStr).Order("created_at desc").Find(&datasets).Error; err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "Failed to fetch datasets"})
	}

	return c.JSON(datasets)
}

// GetDataset gets dataset details and its images
func GetDataset(c *fiber.Ctx) error {
	tenantIDStr, ok := c.Locals("tenant_id").(string)
	if !ok || tenantIDStr == "" {
		return c.Status(fiber.StatusUnauthorized).JSON(fiber.Map{"error": "Unauthorized tenant"})
	}
	datasetIDStr := c.Params("id")

	datasetID, err := uuid.Parse(datasetIDStr)
	if err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "Invalid dataset ID"})
	}

	var dataset models.Dataset
	if err := db.DB.Preload("Images").Where("id = ? AND tenant_id = ?", datasetID, tenantIDStr).First(&dataset).Error; err != nil {
		return c.Status(fiber.StatusNotFound).JSON(fiber.Map{"error": "Dataset not found"})
	}

	return c.JSON(dataset)
}

// DeleteDataset deletes a dataset
func DeleteDataset(c *fiber.Ctx) error {
	tenantIDStr, ok := c.Locals("tenant_id").(string)
	if !ok || tenantIDStr == "" {
		return c.Status(fiber.StatusUnauthorized).JSON(fiber.Map{"error": "Unauthorized tenant"})
	}
	datasetIDStr := c.Params("id")

	datasetID, err := uuid.Parse(datasetIDStr)
	if err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "Invalid dataset ID"})
	}

	var dataset models.Dataset
	if err := db.DB.Where("id = ? AND tenant_id = ?", datasetID, tenantIDStr).First(&dataset).Error; err != nil {
		return c.Status(fiber.StatusNotFound).JSON(fiber.Map{"error": "Dataset not found"})
	}

	if err := db.DB.Delete(&dataset).Error; err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "Failed to delete dataset"})
	}

	return c.SendStatus(fiber.StatusNoContent)
}

// UploadDatasetImage uploads an image to a dataset
func UploadDatasetImage(c *fiber.Ctx) error {
	tenantIDStr, ok := c.Locals("tenant_id").(string)
	if !ok || tenantIDStr == "" {
		return c.Status(fiber.StatusUnauthorized).JSON(fiber.Map{"error": "Unauthorized tenant"})
	}
	datasetIDStr := c.Params("id")

	datasetID, err := uuid.Parse(datasetIDStr)
	if err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "Invalid dataset ID"})
	}

	// Verify dataset belongs to tenant
	var dataset models.Dataset
	if err := db.DB.Where("id = ? AND tenant_id = ?", datasetID, tenantIDStr).First(&dataset).Error; err != nil {
		return c.Status(fiber.StatusNotFound).JSON(fiber.Map{"error": "Dataset not found"})
	}

	fileHeader, err := c.FormFile("file")
	if err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "Failed to parse upload file"})
	}

	ext := filepath.Ext(fileHeader.Filename)
	if ext != ".png" && ext != ".jpg" && ext != ".jpeg" {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "Unsupported format. Only PNG, JPG/JPEG are allowed"})
	}

	file, err := fileHeader.Open()
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "Failed to open file"})
	}
	defer file.Close()

	imageID := uuid.New()
	objectName := fmt.Sprintf("datasets/%s/%s/%s/%s%s", tenantIDStr, dataset.ProjectID.String(), dataset.ID.String(), imageID.String(), ext)

	ctx := context.Background()
	_, err = services.UploadFile(ctx, objectName, file, fileHeader.Size, fileHeader.Header.Get("Content-Type"))
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": fmt.Sprintf("Failed to store file: %v", err)})
	}

	datasetImage := models.DatasetImage{
		ID:        imageID,
		DatasetID: dataset.ID,
		FilePath:  objectName,
		Filename:  fileHeader.Filename,
		Status:    "unlabeled",
	}

	if err := db.DB.Create(&datasetImage).Error; err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "Failed to register dataset image in database"})
	}

	return c.Status(fiber.StatusCreated).JSON(datasetImage)
}

// GetDatasetImage retrieves details and presigned URL for a dataset image
func GetDatasetImage(c *fiber.Ctx) error {
	tenantIDStr, ok := c.Locals("tenant_id").(string)
	if !ok || tenantIDStr == "" {
		return c.Status(fiber.StatusUnauthorized).JSON(fiber.Map{"error": "Unauthorized tenant"})
	}
	imageIDStr := c.Params("image_id")

	imageID, err := uuid.Parse(imageIDStr)
	if err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "Invalid image ID"})
	}

	var image models.DatasetImage
	// Join with dataset to verify tenant ownership
	err = db.DB.Joins("JOIN datasets ON datasets.id = dataset_images.dataset_id").
		Preload("Annotations").
		Where("dataset_images.id = ? AND datasets.tenant_id = ?", imageID, tenantIDStr).
		First(&image).Error
	if err != nil {
		return c.Status(fiber.StatusNotFound).JSON(fiber.Map{"error": "Dataset image not found"})
	}

	downloadURL := ""
	urlStr, err := services.GetPresignedURL(context.Background(), image.FilePath)
	if err == nil {
		downloadURL = urlStr
	}

	return c.JSON(fiber.Map{
		"image":        image,
		"download_url": downloadURL,
	})
}

// DeleteDatasetImage deletes an image
func DeleteDatasetImage(c *fiber.Ctx) error {
	tenantIDStr, ok := c.Locals("tenant_id").(string)
	if !ok || tenantIDStr == "" {
		return c.Status(fiber.StatusUnauthorized).JSON(fiber.Map{"error": "Unauthorized tenant"})
	}
	imageIDStr := c.Params("image_id")

	imageID, err := uuid.Parse(imageIDStr)
	if err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "Invalid image ID"})
	}

	var image models.DatasetImage
	err = db.DB.Joins("JOIN datasets ON datasets.id = dataset_images.dataset_id").
		Where("dataset_images.id = ? AND datasets.tenant_id = ?", imageID, tenantIDStr).
		First(&image).Error
	if err != nil {
		return c.Status(fiber.StatusNotFound).JSON(fiber.Map{"error": "Dataset image not found"})
	}

	// Delete from MinIO
	ctx := context.Background()
	_ = services.MinioClient.RemoveObject(ctx, services.BucketName, image.FilePath, minio.RemoveObjectOptions{})

	if err := db.DB.Delete(&image).Error; err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "Failed to delete image"})
	}

	return c.SendStatus(fiber.StatusNoContent)
}

// SaveAnnotations overwrites annotations for a dataset image
func SaveAnnotations(c *fiber.Ctx) error {
	tenantIDStr, ok := c.Locals("tenant_id").(string)
	if !ok || tenantIDStr == "" {
		return c.Status(fiber.StatusUnauthorized).JSON(fiber.Map{"error": "Unauthorized tenant"})
	}
	userIDStr, ok := c.Locals("user_id").(string)
	if !ok || userIDStr == "" {
		return c.Status(fiber.StatusUnauthorized).JSON(fiber.Map{"error": "Unauthorized user"})
	}
	imageIDStr := c.Params("image_id")

	imageID, err := uuid.Parse(imageIDStr)
	if err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "Invalid image ID"})
	}

	userID, err := uuid.Parse(userIDStr)
	if err != nil {
		return c.Status(fiber.StatusUnauthorized).JSON(fiber.Map{"error": "Invalid user ID"})
	}

	// Verify image ownership
	var image models.DatasetImage
	err = db.DB.Joins("JOIN datasets ON datasets.id = dataset_images.dataset_id").
		Where("dataset_images.id = ? AND datasets.tenant_id = ?", imageID, tenantIDStr).
		First(&image).Error
	if err != nil {
		return c.Status(fiber.StatusNotFound).JSON(fiber.Map{"error": "Dataset image not found"})
	}

	var inputs []AnnotationInput
	if err := c.BodyParser(&inputs); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "Invalid request body"})
	}

	// Use a database transaction to overwrite annotations atomically
	err = db.DB.Transaction(func(tx *gorm.DB) error {
		// Delete existing annotations
		if err := tx.Where("dataset_image_id = ?", image.ID).Delete(&models.Annotation{}).Error; err != nil {
			return err
		}

		// Insert new annotations
		for _, input := range inputs {
			// Validate bounding box is valid JSON format
			var boxRaw interface{}
			if err := json.Unmarshal([]byte(input.BoundingBox), &boxRaw); err != nil {
				return fmt.Errorf("invalid bounding box JSON format: %s", input.BoundingBox)
			}

			annotation := models.Annotation{
				ID:             uuid.New(),
				DatasetImageID: image.ID,
				BoundingBox:    input.BoundingBox,
				Label:          input.Label,
				CreatedBy:      userID,
			}
			if err := tx.Create(&annotation).Error; err != nil {
				return err
			}
		}

		// Update image status to labeled
		status := "labeled"
		if len(inputs) == 0 {
			status = "unlabeled"
		}
		if err := tx.Model(&image).Update("status", status).Error; err != nil {
			return err
		}

		return nil
	})

	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": fmt.Sprintf("Failed to save annotations: %v", err)})
	}

	return c.JSON(fiber.Map{"message": "Annotations saved successfully"})
}

// GetAnnotations retrieves annotations for an image
func GetAnnotations(c *fiber.Ctx) error {
	tenantIDStr, ok := c.Locals("tenant_id").(string)
	if !ok || tenantIDStr == "" {
		return c.Status(fiber.StatusUnauthorized).JSON(fiber.Map{"error": "Unauthorized tenant"})
	}
	imageIDStr := c.Params("image_id")

	imageID, err := uuid.Parse(imageIDStr)
	if err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "Invalid image ID"})
	}

	var image models.DatasetImage
	err = db.DB.Joins("JOIN datasets ON datasets.id = dataset_images.dataset_id").
		Where("dataset_images.id = ? AND datasets.tenant_id = ?", imageID, tenantIDStr).
		First(&image).Error
	if err != nil {
		return c.Status(fiber.StatusNotFound).JSON(fiber.Map{"error": "Dataset image not found"})
	}

	var annotations []models.Annotation
	if err := db.DB.Where("dataset_image_id = ?", image.ID).Find(&annotations).Error; err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "Failed to fetch annotations"})
	}

	return c.JSON(annotations)
}

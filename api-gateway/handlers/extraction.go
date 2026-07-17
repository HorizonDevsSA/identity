package handlers

import (
	"encoding/json"
	"fmt"

	"api-gateway/db"
	"api-gateway/models"

	"github.com/gofiber/fiber/v2"
	"github.com/google/uuid"
)

type CreateSchemaRequest struct {
	Name         string      `json:"name"`
	DocumentType string      `json:"document_type"`
	Description  string      `json:"description"`
	Fields       interface{} `json:"fields"` // Can be parsed JSON array
	ProjectID    string      `json:"project_id"`
}

// CreateSchema creates a new extraction schema template
func CreateSchema(c *fiber.Ctx) error {
	tenantIDStr, ok := c.Locals("tenant_id").(string)
	if !ok || tenantIDStr == "" {
		return c.Status(fiber.StatusUnauthorized).JSON(fiber.Map{"error": "Unauthorized tenant"})
	}
	tenantID, err := uuid.Parse(tenantIDStr)
	if err != nil {
		return c.Status(fiber.StatusUnauthorized).JSON(fiber.Map{"error": "Invalid tenant ID"})
	}

	var req CreateSchemaRequest
	if err := c.BodyParser(&req); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "Invalid request body"})
	}

	if req.Name == "" || req.DocumentType == "" || req.Fields == nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "Name, document_type, and fields are required"})
	}

	// Marshall fields into a JSON string
	fieldsJSON, err := json.Marshal(req.Fields)
	if err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "Invalid fields JSON format"})
	}

	// Validate it's a JSON array
	var rawArray []interface{}
	if err := json.Unmarshal(fieldsJSON, &rawArray); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "Fields must be a JSON array"})
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

	schema := models.ExtractionSchema{
		ID:           uuid.New(),
		Name:         req.Name,
		DocumentType: req.DocumentType,
		Description:  req.Description,
		Fields:       string(fieldsJSON),
		TenantID:     tenantID,
		ProjectID:    projectID,
	}

	if err := db.DB.Create(&schema).Error; err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": fmt.Sprintf("Failed to save schema: %v", err)})
	}

	// Return parsed version
	var fieldsParsed interface{}
	_ = json.Unmarshal([]byte(schema.Fields), &fieldsParsed)

	return c.Status(fiber.StatusCreated).JSON(fiber.Map{
		"id":            schema.ID,
		"name":          schema.Name,
		"document_type": schema.DocumentType,
		"description":   schema.Description,
		"fields":        fieldsParsed,
		"tenant_id":     schema.TenantID,
		"project_id":    schema.ProjectID,
		"created_at":    schema.CreatedAt,
		"updated_at":    schema.UpdatedAt,
	})
}

// ListSchemas lists extraction schemas for the tenant
func ListSchemas(c *fiber.Ctx) error {
	tenantIDStr, ok := c.Locals("tenant_id").(string)
	if !ok || tenantIDStr == "" {
		return c.Status(fiber.StatusUnauthorized).JSON(fiber.Map{"error": "Unauthorized tenant"})
	}

	var schemas []models.ExtractionSchema
	if err := db.DB.Where("tenant_id = ?", tenantIDStr).Order("created_at desc").Find(&schemas).Error; err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "Failed to fetch schemas"})
	}

	// Unmarshal fields for each schema before returning
	type schemaResponse struct {
		ID           uuid.UUID   `json:"id"`
		Name         string      `json:"name"`
		DocumentType string      `json:"document_type"`
		Description  string      `json:"description"`
		Fields       interface{} `json:"fields"`
		TenantID     uuid.UUID   `json:"tenant_id"`
		ProjectID    uuid.UUID   `json:"project_id"`
	}

	var res []schemaResponse
	for _, s := range schemas {
		var fieldsParsed interface{}
		_ = json.Unmarshal([]byte(s.Fields), &fieldsParsed)
		res = append(res, schemaResponse{
			ID:           s.ID,
			Name:         s.Name,
			DocumentType: s.DocumentType,
			Description:  s.Description,
			Fields:       fieldsParsed,
			TenantID:     s.TenantID,
			ProjectID:    s.ProjectID,
		})
	}

	return c.JSON(res)
}

// GetSchema returns details for a single schema
func GetSchema(c *fiber.Ctx) error {
	tenantIDStr, ok := c.Locals("tenant_id").(string)
	if !ok || tenantIDStr == "" {
		return c.Status(fiber.StatusUnauthorized).JSON(fiber.Map{"error": "Unauthorized tenant"})
	}

	schemaIDStr := c.Params("id")
	schemaID, err := uuid.Parse(schemaIDStr)
	if err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "Invalid schema ID"})
	}

	var schema models.ExtractionSchema
	if err := db.DB.Where("id = ? AND tenant_id = ?", schemaID, tenantIDStr).First(&schema).Error; err != nil {
		return c.Status(fiber.StatusNotFound).JSON(fiber.Map{"error": "Schema not found"})
	}

	var fieldsParsed interface{}
	_ = json.Unmarshal([]byte(schema.Fields), &fieldsParsed)

	return c.JSON(fiber.Map{
		"id":            schema.ID,
		"name":          schema.Name,
		"document_type": schema.DocumentType,
		"description":   schema.Description,
		"fields":        fieldsParsed,
		"tenant_id":     schema.TenantID,
		"project_id":    schema.ProjectID,
		"created_at":    schema.CreatedAt,
		"updated_at":    schema.UpdatedAt,
	})
}

// DeleteSchema deletes a schema template
func DeleteSchema(c *fiber.Ctx) error {
	tenantIDStr, ok := c.Locals("tenant_id").(string)
	if !ok || tenantIDStr == "" {
		return c.Status(fiber.StatusUnauthorized).JSON(fiber.Map{"error": "Unauthorized tenant"})
	}

	schemaIDStr := c.Params("id")
	schemaID, err := uuid.Parse(schemaIDStr)
	if err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "Invalid schema ID"})
	}

	var schema models.ExtractionSchema
	if err := db.DB.Where("id = ? AND tenant_id = ?", schemaID, tenantIDStr).First(&schema).Error; err != nil {
		return c.Status(fiber.StatusNotFound).JSON(fiber.Map{"error": "Schema not found"})
	}

	if err := db.DB.Delete(&schema).Error; err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "Failed to delete schema"})
	}

	return c.SendStatus(fiber.StatusNoContent)
}

// GetExtractedFields retrieves the custom extracted key-value pairs for a document
func GetExtractedFields(c *fiber.Ctx) error {
	tenantIDStr, ok := c.Locals("tenant_id").(string)
	if !ok || tenantIDStr == "" {
		return c.Status(fiber.StatusUnauthorized).JSON(fiber.Map{"error": "Unauthorized tenant"})
	}

	docIDStr := c.Params("id")
	docID, err := uuid.Parse(docIDStr)
	if err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "Invalid document ID"})
	}

	// Verify document ownership
	var document models.Document
	if err := db.DB.Where("id = ? AND tenant_id = ?", docID, tenantIDStr).First(&document).Error; err != nil {
		return c.Status(fiber.StatusNotFound).JSON(fiber.Map{"error": "Document not found"})
	}

	var fields []models.ExtractedField
	if err := db.DB.Where("document_id = ?", docID).Order("key asc").Find(&fields).Error; err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "Failed to fetch extracted fields"})
	}

	return c.JSON(fields)
}

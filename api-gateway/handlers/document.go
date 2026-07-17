package handlers

import (
	"context"
	"fmt"
	"path/filepath"

	"api-gateway/db"
	"api-gateway/models"
	"api-gateway/services"
	"github.com/gofiber/fiber/v2"
	"github.com/google/uuid"
)

// UploadDocument handles document upload, saves it to MinIO, creates a database record,
// and publishes a job to the Redis queue for async OCR processing.
func UploadDocument(c *fiber.Ctx) error {
	tenantIDStr, ok := c.Locals("tenant_id").(string)
	if !ok || tenantIDStr == "" {
		return c.Status(fiber.StatusUnauthorized).JSON(fiber.Map{"error": "Unauthorized tenant"})
	}
	tenantID, err := uuid.Parse(tenantIDStr)
	if err != nil {
		return c.Status(fiber.StatusUnauthorized).JSON(fiber.Map{"error": "Invalid tenant ID"})
	}

	// Parse project ID (optional, fall back to default project if not provided)
	projectIDStr := c.FormValue("project_id")
	var projectID uuid.UUID
	if projectIDStr != "" {
		projectID, err = uuid.Parse(projectIDStr)
		if err != nil {
			return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "Invalid project ID"})
		}
	} else {
		// Find default project for the tenant
		var defaultProj models.Project
		err = db.DB.Where("tenant_id = ? AND name = ?", tenantID, "Default Project").First(&defaultProj).Error
		if err != nil {
			return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "Default project not found"})
		}
		projectID = defaultProj.ID
	}

	// Parse file from form
	fileHeader, err := c.FormFile("file")
	if err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "Failed to parse upload file"})
	}

	// Validate file type (allow images and PDFs)
	ext := filepath.Ext(fileHeader.Filename)
	if ext != ".pdf" && ext != ".png" && ext != ".jpg" && ext != ".jpeg" {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "Unsupported file format. Only PDF, PNG, JPG/JPEG are allowed"})
	}

	file, err := fileHeader.Open()
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "Failed to open file"})
	}
	defer file.Close()

	// Generate document details
	documentID := uuid.New()
	objectName := fmt.Sprintf("%s/%s/%s%s", tenantID.String(), projectID.String(), documentID.String(), ext)

	// Upload to MinIO
	ctx := context.Background()
	_, err = services.UploadFile(ctx, objectName, file, fileHeader.Size, fileHeader.Header.Get("Content-Type"))
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": fmt.Sprintf("Failed to store file in object storage: %v", err)})
	}

	// Parse entered expected metadata from form fields
	enteredDocTypeStr := c.FormValue("entered_doc_type")
	var enteredDocType *models.DocumentType
	if enteredDocTypeStr != "" {
		dt := models.DocumentType(enteredDocTypeStr)
		enteredDocType = &dt
	}

	enteredIDNumber := c.FormValue("entered_id_number")
	var pEnteredIDNumber *string
	if enteredIDNumber != "" {
		pEnteredIDNumber = &enteredIDNumber
	}

	enteredFirstName := c.FormValue("entered_first_name")
	var pEnteredFirstName *string
	if enteredFirstName != "" {
		pEnteredFirstName = &enteredFirstName
	}

	enteredSurname := c.FormValue("entered_surname")
	var pEnteredSurname *string
	if enteredSurname != "" {
		pEnteredSurname = &enteredSurname
	}

	enteredDateOfIssue := c.FormValue("entered_date_of_issue")
	var pEnteredDateOfIssue *string
	if enteredDateOfIssue != "" {
		pEnteredDateOfIssue = &enteredDateOfIssue
	}

	enteredDOB := c.FormValue("entered_dob")
	var pEnteredDOB *string
	if enteredDOB != "" {
		pEnteredDOB = &enteredDOB
	}

	enteredExpiryDate := c.FormValue("entered_expiry_date")
	var pEnteredExpiryDate *string
	if enteredExpiryDate != "" {
		pEnteredExpiryDate = &enteredExpiryDate
	}

	enteredSex := c.FormValue("entered_sex")
	var pEnteredSex *string
	if enteredSex != "" {
		pEnteredSex = &enteredSex
	}

	// Create document record in Database
	document := models.Document{
		ID:                 documentID,
		Filename:           fileHeader.Filename,
		FilePath:           objectName,
		Status:             "pending",
		TenantID:           tenantID,
		ProjectID:          projectID,
		EnteredDocType:     enteredDocType,
		EnteredIDNumber:    pEnteredIDNumber,
		EnteredFirstName:   pEnteredFirstName,
		EnteredSurname:     pEnteredSurname,
		EnteredDateOfIssue: pEnteredDateOfIssue,
		EnteredDOB:         pEnteredDOB,
		EnteredExpiryDate:  pEnteredExpiryDate,
		EnteredSex:         pEnteredSex,
		VerificationStatus: "unverified",
	}

	if err := db.DB.Create(&document).Error; err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "Failed to register document in database"})
	}

	// Publish job to Redis Queue
	jobPayload := services.OCRJobPayload{
		DocumentID: documentID.String(),
		TenantID:   tenantID.String(),
		FilePath:   objectName,
	}

	err = services.PublishOCRJob(ctx, jobPayload)
	if err != nil {
		// Log error but don't fail user request since record & file are safe; maybe retry worker can pick it up
		fmt.Printf("Error publishing to queue: %v\n", err)
		db.DB.Model(&document).Update("status", "failed")
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "Failed to queue document for processing"})
	}

	return c.Status(fiber.StatusAccepted).JSON(document)
}

// GetDocument returns a single document details
func GetDocument(c *fiber.Ctx) error {
	tenantIDStr := c.Locals("tenant_id").(string)
	docIDStr := c.Params("id")

	docID, err := uuid.Parse(docIDStr)
	if err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "Invalid document ID"})
	}

	var document models.Document
	if err := db.DB.Where("id = ? AND tenant_id = ?", docID, tenantIDStr).First(&document).Error; err != nil {
		return c.Status(fiber.StatusNotFound).JSON(fiber.Map{"error": "Document not found"})
	}

	// Generate pre-signed URL for viewing the file if requested
	viewURL := ""
	if c.Query("download") == "true" {
		urlStr, err := services.GetPresignedURL(context.Background(), document.FilePath)
		if err == nil {
			viewURL = urlStr
		}
	}

	return c.JSON(fiber.Map{
		"document":     document,
		"download_url": viewURL,
	})
}

// GetDocumentPrediction returns the predictions/OCR results for the document
func GetDocumentPrediction(c *fiber.Ctx) error {
	tenantIDStr := c.Locals("tenant_id").(string)
	docIDStr := c.Params("id")

	docID, err := uuid.Parse(docIDStr)
	if err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "Invalid document ID"})
	}

	// Verify document belongs to tenant
	var document models.Document
	if err := db.DB.Where("id = ? AND tenant_id = ?", docID, tenantIDStr).First(&document).Error; err != nil {
		return c.Status(fiber.StatusNotFound).JSON(fiber.Map{"error": "Document not found"})
	}

	var prediction models.Prediction
	if err := db.DB.Where("document_id = ?", docID).First(&prediction).Error; err != nil {
		return c.Status(fiber.StatusNotFound).JSON(fiber.Map{"error": "OCR prediction not available or document still processing", "status": document.Status})
	}

	return c.JSON(prediction)
}

// ListDocuments lists documents for the tenant
func ListDocuments(c *fiber.Ctx) error {
	tenantIDStr := c.Locals("tenant_id").(string)

	var documents []models.Document
	if err := db.DB.Where("tenant_id = ?", tenantIDStr).Order("created_at desc").Find(&documents).Error; err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "Failed to fetch documents"})
	}

	return c.JSON(documents)
}

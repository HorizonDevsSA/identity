package handlers

import (
	"fmt"

	"api-gateway/db"
	"api-gateway/models"

	"github.com/gofiber/fiber/v2"
	"github.com/google/uuid"
	"gorm.io/gorm"
)

type ReviewRequest struct {
	CorrectedFields map[string]string `json:"corrected_fields"`
	AddToDatasetID  string            `json:"add_to_dataset_id"`
}

// ListReviewQueue lists all documents waiting for human review
func ListReviewQueue(c *fiber.Ctx) error {
	tenantIDStr, ok := c.Locals("tenant_id").(string)
	if !ok || tenantIDStr == "" {
		return c.Status(fiber.StatusUnauthorized).JSON(fiber.Map{"error": "Unauthorized tenant"})
	}

	var documents []models.Document
	// Queue includes failed_verification or unverified
	err := db.DB.Where("tenant_id = ? AND (verification_status = ? OR verification_status = ?)", 
		tenantIDStr, "failed_verification", "unverified").
		Order("created_at desc").
		Find(&documents).Error

	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "Failed to fetch review queue"})
	}

	return c.JSON(documents)
}

// ReviewDocument submits manual verification corrections
func ReviewDocument(c *fiber.Ctx) error {
	tenantIDStr, ok := c.Locals("tenant_id").(string)
	if !ok || tenantIDStr == "" {
		return c.Status(fiber.StatusUnauthorized).JSON(fiber.Map{"error": "Unauthorized tenant"})
	}
	tenantID, err := uuid.Parse(tenantIDStr)
	if err != nil {
		return c.Status(fiber.StatusUnauthorized).JSON(fiber.Map{"error": "Invalid tenant ID"})
	}

	userIDStr, ok := c.Locals("user_id").(string)
	if !ok || userIDStr == "" {
		return c.Status(fiber.StatusUnauthorized).JSON(fiber.Map{"error": "Unauthorized user"})
	}
	userID, err := uuid.Parse(userIDStr)
	if err != nil {
		return c.Status(fiber.StatusUnauthorized).JSON(fiber.Map{"error": "Invalid user ID"})
	}

	role, _ := c.Locals("role").(string)
	if role != "admin" && role != "reviewer" {
		return c.Status(fiber.StatusForbidden).JSON(fiber.Map{"error": "Only reviewers or admins can verify documents"})
	}

	docIDStr := c.Params("id")
	docID, err := uuid.Parse(docIDStr)
	if err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "Invalid document ID"})
	}

	var req ReviewRequest
	if err := c.BodyParser(&req); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "Invalid request body"})
	}

	// Fetch document and verify tenant
	var document models.Document
	if err := db.DB.Where("id = ? AND tenant_id = ?", docID, tenantID).First(&document).Error; err != nil {
		return c.Status(fiber.StatusNotFound).JSON(fiber.Map{"error": "Document not found"})
	}

	err = db.DB.Transaction(func(tx *gorm.DB) error {
		// 1. Process Corrections and log Feedback
		for fieldName, correctedVal := range req.CorrectedFields {
			var origVal string
			var changed bool

			switch fieldName {
			case "first_name":
				if document.ExtractedFirstName != nil {
					origVal = *document.ExtractedFirstName
				}
				if origVal != correctedVal {
					document.ExtractedFirstName = &correctedVal
					changed = true
				}
			case "surname":
				if document.ExtractedSurname != nil {
					origVal = *document.ExtractedSurname
				}
				if origVal != correctedVal {
					document.ExtractedSurname = &correctedVal
					changed = true
				}
			case "id_number":
				if document.ExtractedIDNumber != nil {
					origVal = *document.ExtractedIDNumber
				}
				if origVal != correctedVal {
					document.ExtractedIDNumber = &correctedVal
					changed = true
				}
			case "dob":
				if document.ExtractedDOB != nil {
					origVal = *document.ExtractedDOB
				}
				if origVal != correctedVal {
					document.ExtractedDOB = &correctedVal
					changed = true
				}
			case "date_of_issue":
				if document.ExtractedDateOfIssue != nil {
					origVal = *document.ExtractedDateOfIssue
				}
				if origVal != correctedVal {
					document.ExtractedDateOfIssue = &correctedVal
					changed = true
				}
			case "expiry_date":
				if document.ExtractedExpiryDate != nil {
					origVal = *document.ExtractedExpiryDate
				}
				if origVal != correctedVal {
					document.ExtractedExpiryDate = &correctedVal
					changed = true
				}
			case "sex":
				if document.ExtractedSex != nil {
					origVal = *document.ExtractedSex
				}
				if origVal != correctedVal {
					document.ExtractedSex = &correctedVal
					changed = true
				}
			case "doc_type":
				if document.ExtractedDocType != nil {
					origVal = string(*document.ExtractedDocType)
				}
				if origVal != correctedVal {
					dt := models.DocumentType(correctedVal)
					document.ExtractedDocType = &dt
					changed = true
				}
			}

			if changed {
				feedback := models.Feedback{
					ID:             uuid.New(),
					DocumentID:     docID,
					FieldName:      fieldName,
					OriginalValue:  origVal,
					CorrectedValue: correctedVal,
					ReviewedBy:     userID,
				}
				if err := tx.Create(&feedback).Error; err != nil {
					return err
				}
			}
		}

		// Update document verification status
		document.VerificationStatus = "verified"
		document.Status = "completed"
		if err := tx.Save(&document).Error; err != nil {
			return err
		}

		// 2. Continuous Learning Loop: Register corrected file in dataset
		if req.AddToDatasetID != "" {
			datasetID, err := uuid.Parse(req.AddToDatasetID)
			if err != nil {
				return fmt.Errorf("invalid add_to_dataset_id: %w", err)
			}

			var dataset models.Dataset
			if err := tx.Where("id = ? AND tenant_id = ?", datasetID, tenantID).First(&dataset).Error; err != nil {
				return fmt.Errorf("dataset not found: %w", err)
			}

			// Create DatasetImage
			datasetImageID := uuid.New()
			datasetImage := models.DatasetImage{
				ID:        datasetImageID,
				DatasetID: datasetID,
				FilePath:  document.FilePath,
				Filename:  document.Filename,
				Status:    "reviewed", // Directly set to reviewed/labeled
			}
			if err := tx.Create(&datasetImage).Error; err != nil {
				return err
			}

			// Add Annotations for corrected/final document values
			var labelFields = map[string]*string{
				"id_number":     document.ExtractedIDNumber,
				"first_name":    document.ExtractedFirstName,
				"surname":       document.ExtractedSurname,
				"dob":           document.ExtractedDOB,
				"date_of_issue": document.ExtractedDateOfIssue,
				"expiry_date":   document.ExtractedExpiryDate,
				"sex":           document.ExtractedSex,
			}

			for key, valPtr := range labelFields {
				if valPtr != nil && *valPtr != "" {
					annotation := models.Annotation{
						ID:             uuid.New(),
						DatasetImageID: datasetImageID,
						BoundingBox:    "[[0.0, 0.0], [1.0, 1.0]]", // Fallback full-image box
						Label:          fmt.Sprintf("%s: %s", key, *valPtr),
						CreatedBy:      userID,
					}
					if err := tx.Create(&annotation).Error; err != nil {
						return err
					}
				}
			}
		}

		// 3. Write Audit Log
		audit := models.AuditLog{
			ID:        uuid.New(),
			TenantID:  tenantID,
			UserID:    userID,
			Action:    "review_document",
			Details:   fmt.Sprintf("Reviewed and corrected document %s (Dataset Addition: %t)", docID, req.AddToDatasetID != ""),
			IPAddress: c.IP(),
		}
		if err := tx.Create(&audit).Error; err != nil {
			return err
		}

		return nil
	})

	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": fmt.Sprintf("Failed to submit review: %v", err)})
	}

	return c.JSON(fiber.Map{"message": "Document reviewed and verified successfully"})
}

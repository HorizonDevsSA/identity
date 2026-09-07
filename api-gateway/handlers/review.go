package handlers

import (
	"fmt"
	"strings"

	"api-gateway/db"
	"api-gateway/models"
	"api-gateway/services"

	"github.com/gofiber/fiber/v2"
	"github.com/google/uuid"
	"gorm.io/gorm"
)

type ReviewRequest struct {
	CorrectedFields map[string]string `json:"corrected_fields"`
	AddToDatasetID  string            `json:"add_to_dataset_id"`
}

// ListReviewQueue lists all documents waiting for human review (Admin/Reviewer)
func ListReviewQueue(c *fiber.Ctx) error {
	role, _ := c.Locals("role").(string)
	tenantIDStr, _ := c.Locals("tenant_id").(string)

	var documents []models.Document
	query := db.DB.Where("verification_status = ? OR verification_status = ?", "failed_verification", "unverified")
	if role != "admin" && role != "reviewer" && tenantIDStr != "" {
		query = query.Where("tenant_id = ?", tenantIDStr)
	}

	err := query.Order("created_at desc").Find(&documents).Error
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "Failed to fetch review queue"})
	}

	return c.JSON(documents)
}

// ReviewDocument submits manual verification corrections and triggers on-chain KYC/alias registration
func ReviewDocument(c *fiber.Ctx) error {
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

	// Fetch document (Admins can review any document across the system)
	var document models.Document
	query := db.DB.Where("id = ?", docID)
	tenantIDStr, _ := c.Locals("tenant_id").(string)
	if role != "admin" && tenantIDStr != "" {
		query = query.Where("tenant_id = ?", tenantIDStr)
	}

	if err := query.First(&document).Error; err != nil {
		return c.Status(fiber.StatusNotFound).JSON(fiber.Map{"error": "Document not found"})
	}

	userIDStr, _ := c.Locals("user_id").(string)
	userID, _ := uuid.Parse(userIDStr)
	tenantID, _ := uuid.Parse(tenantIDStr)
	if tenantID == uuid.Nil {
		tenantID = document.TenantID
	}

	err = db.DB.Transaction(func(tx *gorm.DB) error {
		// 1. Process Corrections and log Feedback
		for fieldName, correctedVal := range req.CorrectedFields {
			var origVal string
			var changed bool

			switch fieldName {
			case "first_name":
				if document.ExtractedFirstName != nil {
					origVal = string(*document.ExtractedFirstName)
				}
				if origVal != correctedVal {
					es := models.EncryptedString(correctedVal)
					document.ExtractedFirstName = &es
					changed = true
				}
			case "surname":
				if document.ExtractedSurname != nil {
					origVal = string(*document.ExtractedSurname)
				}
				if origVal != correctedVal {
					es := models.EncryptedString(correctedVal)
					document.ExtractedSurname = &es
					changed = true
				}
			case "id_number":
				if document.ExtractedIDNumber != nil {
					origVal = string(*document.ExtractedIDNumber)
				}
				if origVal != correctedVal {
					es := models.EncryptedString(correctedVal)
					document.ExtractedIDNumber = &es
					changed = true
				}
			case "dob":
				if document.ExtractedDOB != nil {
					origVal = string(*document.ExtractedDOB)
				}
				if origVal != correctedVal {
					es := models.EncryptedString(correctedVal)
					document.ExtractedDOB = &es
					changed = true
				}
			case "date_of_issue":
				if document.ExtractedDateOfIssue != nil {
					origVal = string(*document.ExtractedDateOfIssue)
				}
				if origVal != correctedVal {
					es := models.EncryptedString(correctedVal)
					document.ExtractedDateOfIssue = &es
					changed = true
				}
			case "expiry_date":
				if document.ExtractedExpiryDate != nil {
					origVal = string(*document.ExtractedExpiryDate)
				}
				if origVal != correctedVal {
					es := models.EncryptedString(correctedVal)
					document.ExtractedExpiryDate = &es
					changed = true
				}
			case "sex":
				if document.ExtractedSex != nil {
					origVal = string(*document.ExtractedSex)
				}
				if origVal != correctedVal {
					es := models.EncryptedString(correctedVal)
					document.ExtractedSex = &es
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
					OriginalValue:  models.EncryptedString(origVal),
					CorrectedValue: models.EncryptedString(correctedVal),
					ReviewedBy:     userID,
				}
				if err := tx.Create(&feedback).Error; err != nil {
					return err
				}
			}
		}

		// Check if verifying this document will cause duplicate active verified identities
		var finalFirst, finalSurname, finalDOB string
		if document.ExtractedFirstName != nil {
			finalFirst = string(*document.ExtractedFirstName)
		} else if document.EnteredFirstName != nil {
			finalFirst = string(*document.EnteredFirstName)
		}

		if document.ExtractedSurname != nil {
			finalSurname = string(*document.ExtractedSurname)
		} else if document.EnteredSurname != nil {
			finalSurname = string(*document.EnteredSurname)
		}

		if document.ExtractedDOB != nil {
			finalDOB = string(*document.ExtractedDOB)
		} else if document.EnteredDOB != nil {
			finalDOB = string(*document.EnteredDOB)
		}

		if finalFirst != "" && finalSurname != "" && finalDOB != "" {
			isDup, err := isDuplicateVerifiedIdentity(tenantID.String(), finalFirst, finalSurname, finalDOB, document.ID.String())
			if err != nil {
				return err
			}
			if isDup {
				return fmt.Errorf("CONFLICT: Identity already verified under another active document")
			}
		}

		// Update document verification status
		document.VerificationStatus = "verified"
		document.Status = "completed"
		if err := tx.Save(&document).Error; err != nil {
			return err
		}

		// ZWC Blockchain integration: Whitelist address and register alias
		if document.WalletAddress != nil && *document.WalletAddress != "" {
			aliasVal := ""
			if document.AliasValue != nil {
				aliasVal = *document.AliasValue
			}
			aliasType := ""
			if document.AliasType != nil {
				aliasType = *document.AliasType
			}
			go func(addr, aType, aVal string) {
				if err := services.RegisterUserOnChain(addr, aType, aVal); err != nil {
					fmt.Printf("[ZWC-ERROR] Failed to register user on chain: %v\n", err)
				}
			}(*document.WalletAddress, aliasType, aliasVal)
		}

		// Twilio SMS Notification: Alert user of KYC approval and wallet whitelisting
		if document.AliasValue != nil && *document.AliasValue != "" {
			recipientPhone := *document.AliasValue
			aliasType := ""
			if document.AliasType != nil {
				aliasType = *document.AliasType
			}
			if aliasType == "phone" || strings.HasPrefix(recipientPhone, "+") {
				walletStr := ""
				if document.WalletAddress != nil {
					walletStr = *document.WalletAddress
				}
				go func(to, wallet string) {
					msg := fmt.Sprintf("Zimbabwe Coin (ZWC): Your identity verification has been approved! Your wallet address %s is now whitelisted.", wallet)
					if _, err := services.SendSMS(to, msg); err != nil {
						fmt.Printf("[TWILIO-SMS-ERROR] Failed to send KYC approval SMS to %s: %v\n", to, err)
					}
				}(recipientPhone, walletStr)
			}
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
			var labelFields = map[string]*models.EncryptedString{
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
						Label:          fmt.Sprintf("%s: %s", key, string(*valPtr)),
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
		if strings.HasPrefix(err.Error(), "CONFLICT:") {
			return c.Status(fiber.StatusConflict).JSON(fiber.Map{"error": strings.TrimPrefix(err.Error(), "CONFLICT: ")})
		}
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": fmt.Sprintf("Failed to submit review: %v", err)})
	}

	return c.JSON(fiber.Map{"message": "Document reviewed and verified successfully"})
}

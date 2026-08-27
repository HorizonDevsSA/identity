package handlers

import (
	"context"
	"encoding/binary"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

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

	// 1. Malware Scanning via ClamAV (TCP INSTREAM)
	clean, virusName, err := scanFile(file)
	if err != nil {
		fmt.Printf("WARNING: ClamAV virus scan skipped or failed: %v\n", err)
	} else if !clean {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
			"error": fmt.Sprintf("Malware detected: %s", virusName),
		})
	}

	_, err = file.Seek(0, io.SeekStart)
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "Failed to reset file read offset post-scan"})
	}

	// 2. Content Validation via Magic Bytes
	headBuf := make([]byte, 512)
	n, err := file.Read(headBuf)
	if err != nil && err != io.EOF {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "Failed to read file header"})
	}
	detectedType := http.DetectContentType(headBuf[:n])

	_, err = file.Seek(0, io.SeekStart)
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "Failed to reset file read offset"})
	}

	allowedTypes := map[string]bool{
		"image/png":       true,
		"image/jpeg":      true,
		"image/gif":       true,
		"image/webp":      true,
		"application/pdf": true,
	}
	if !allowedTypes[detectedType] {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
			"error": fmt.Sprintf("Invalid file content type detected: %s. Only PDF, PNG, JPG/JPEG are allowed", detectedType),
		})
	}

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

	// Check for active duplicate verified identity if name/DOB parameters are entered
	if enteredFirstName != "" && enteredSurname != "" && enteredDOB != "" {
		isDup, err := isDuplicateVerifiedIdentity(tenantIDStr, enteredFirstName, enteredSurname, enteredDOB, "")
		if err != nil {
			return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "Failed to verify duplicate identity status"})
		}
		if isDup {
			return c.Status(fiber.StatusConflict).JSON(fiber.Map{"error": "Identity already verified under another active document"})
		}
	}

	// Derive deterministic wallet address from entered ID number and master salt
	masterSalt := os.Getenv("ZWC_MASTER_SALT")
	if masterSalt == "" {
		masterSalt = "zwc_default_secure_salt_2026"
	}
	derivedAddress, err := services.DeriveWalletAddress(*pEnteredIDNumber, masterSalt)
	if err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": fmt.Sprintf("Failed to derive wallet address: %v", err)})
	}
	pWalletAddress := &derivedAddress

	aliasType := c.FormValue("alias_type")
	var pAliasType *string
	if aliasType != "" {
		pAliasType = &aliasType
	}

	aliasValue := c.FormValue("alias_value")
	var pAliasValue *string
	if aliasValue != "" {
		pAliasValue = &aliasValue
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
		EnteredIDNumber:    toEncryptedStringPtr(pEnteredIDNumber),
		EnteredFirstName:   toEncryptedStringPtr(pEnteredFirstName),
		EnteredSurname:     toEncryptedStringPtr(pEnteredSurname),
		EnteredDateOfIssue: toEncryptedStringPtr(pEnteredDateOfIssue),
		EnteredDOB:         toEncryptedStringPtr(pEnteredDOB),
		EnteredExpiryDate:  toEncryptedStringPtr(pEnteredExpiryDate),
		EnteredSex:         toEncryptedStringPtr(pEnteredSex),
		VerificationStatus: "unverified",
		WalletAddress:      pWalletAddress,
		AliasType:          pAliasType,
		AliasValue:         pAliasValue,
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

type VerificationLookupRequest struct {
	DocumentType string `json:"document_type"`
	IDNumber     string `json:"id_number"`
	FirstName    string `json:"first_name"`
	Surname      string `json:"surname"`
}

// CheckVerificationStatus checks if a user is verified using document_type/id_number or first_name/surname
func CheckVerificationStatus(c *fiber.Ctx) error {
	tenantIDStr := c.Locals("tenant_id").(string)

	var req VerificationLookupRequest
	if err := c.BodyParser(&req); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "Failed to parse request body"})
	}

	hasDocParams := req.DocumentType != "" && req.IDNumber != ""
	hasNameParams := req.FirstName != "" && req.Surname != ""

	if !hasDocParams && !hasNameParams {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
			"error": "Must provide either both (document_type and id_number) or both (first_name and surname)",
		})
	}

	var documents []models.Document
	if err := db.DB.Where("tenant_id = ? AND verification_status = 'verified'", tenantIDStr).Find(&documents).Error; err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "Failed to query documents"})
	}

	for _, doc := range documents {
		matched := false
		matchedBy := ""

		if hasDocParams {
			if doc.ExtractedDocType != nil && string(*doc.ExtractedDocType) == req.DocumentType &&
				doc.ExtractedIDNumber != nil && string(*doc.ExtractedIDNumber) == req.IDNumber {
				matched = true
				matchedBy = "document_info"
			}
		}

		if !matched && hasNameParams {
			if doc.ExtractedFirstName != nil && string(*doc.ExtractedFirstName) == req.FirstName &&
				doc.ExtractedSurname != nil && string(*doc.ExtractedSurname) == req.Surname {
				matched = true
				matchedBy = "name_info"
			}
		}

		if matched {
			return c.JSON(fiber.Map{
				"verified":    true,
				"document_id": doc.ID,
				"matched_by":  matchedBy,
			})
		}
	}

	return c.JSON(fiber.Map{
		"verified": false,
	})
}

func toEncryptedStringPtr(s *string) *models.EncryptedString {
	if s == nil {
		return nil
	}
	es := models.EncryptedString(*s)
	return &es
}

func normalizeString(s string) string {
	return strings.ToLower(strings.Join(strings.Fields(s), ""))
}

func isExpired(expiryStr string) bool {
	if expiryStr == "" {
		return false
	}
	formats := []string{
		"02-01-2006",
		"02/01/2006",
		"2006-01-02",
	}
	expiryStr = strings.TrimSpace(expiryStr)
	for _, f := range formats {
		t, err := time.Parse(f, expiryStr)
		if err == nil {
			now := time.Now().Truncate(24 * time.Hour)
			return t.Before(now)
		}
	}
	return false
}

func isDuplicateVerifiedIdentity(tenantIDStr, firstName, surname, dob, excludeDocID string) (bool, error) {
	if firstName == "" || surname == "" || dob == "" {
		return false, nil
	}

	var docs []models.Document
	err := db.DB.Where("tenant_id = ? AND verification_status = 'verified'", tenantIDStr).Find(&docs).Error
	if err != nil {
		return false, err
	}

	normFirst := normalizeString(firstName)
	normSurname := normalizeString(surname)
	normDob := normalizeString(dob)

	for _, doc := range docs {
		if excludeDocID != "" && doc.ID.String() == excludeDocID {
			continue
		}

		var docFirst, docSurname, docDob, docDocType, docExpiry string
		if doc.ExtractedFirstName != nil {
			docFirst = string(*doc.ExtractedFirstName)
		}
		if doc.ExtractedSurname != nil {
			docSurname = string(*doc.ExtractedSurname)
		}
		if doc.ExtractedDOB != nil {
			docDob = string(*doc.ExtractedDOB)
		}
		if doc.ExtractedDocType != nil {
			docDocType = string(*doc.ExtractedDocType)
		}
		if doc.ExtractedExpiryDate != nil {
			docExpiry = string(*doc.ExtractedExpiryDate)
		}

		if normalizeString(docFirst) == normFirst &&
			normalizeString(docSurname) == normSurname &&
			normalizeString(docDob) == normDob {

			// If non-national ID and expired, let it pass
			if docDocType != "national_id" && isExpired(docExpiry) {
				continue
			}

			return true, nil
		}
	}

	return false, nil
}

func scanFile(reader io.Reader) (bool, string, error) {
	clamavAddr := os.Getenv("CLAMAV_URL")
	if clamavAddr == "" {
		clamavAddr = "clamav:3310"
	}

	conn, err := net.DialTimeout("tcp", clamavAddr, 2*time.Second)
	if err != nil {
		return false, "", fmt.Errorf("clamav connection failed: %w", err)
	}
	defer conn.Close()

	// Send INSTREAM command
	_, err = conn.Write([]byte("zINSTREAM\x00"))
	if err != nil {
		return false, "", err
	}

	totalBytes := 0
	buf := make([]byte, 8192)
	for {
		n, err := reader.Read(buf)
		if n > 0 {
			totalBytes += n
			// Chunk prefix: 4-byte big-endian chunk length
			lenBuf := make([]byte, 4)
			binary.BigEndian.PutUint32(lenBuf, uint32(n))
			if _, err := conn.Write(lenBuf); err != nil {
				return false, "", err
			}
			if _, err := conn.Write(buf[:n]); err != nil {
				return false, "", err
			}
		}
		if err == io.EOF {
			break
		}
		if err != nil {
			return false, "", err
		}
	}

	// Send zero-length chunk to signal EOF to ClamAV
	if _, err := conn.Write([]byte{0, 0, 0, 0}); err != nil {
		return false, "", err
	}

	// Read response
	resp, err := io.ReadAll(conn)
	if err != nil {
		return false, "", err
	}

	respStr := string(resp)
	fmt.Printf("[ClamAV Scan] Sent %d bytes. Response: %q\n", totalBytes, respStr)

	if strings.Contains(respStr, "OK") {
		return true, "", nil
	}

	if strings.Contains(respStr, "FOUND") {
		return false, strings.TrimSpace(respStr), nil
	}

	return false, respStr, fmt.Errorf("unexpected response from ClamAV: %s", respStr)
}

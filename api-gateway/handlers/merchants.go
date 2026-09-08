package handlers

import (
	"fmt"
	"strings"
	"time"

	"api-gateway/db"
	"api-gateway/models"

	"github.com/gofiber/fiber/v2"
	"github.com/google/uuid"
)

type RegisterMerchantRequest struct {
	BusinessName     string `json:"business_name"`
	TaxNumber        string `json:"tax_number"`
	Category         string `json:"category"`
	WebhookURL       string `json:"webhook_url,omitempty"`
	SettlementBank   string `json:"settlement_bank,omitempty"`
	SettlementAccNum string `json:"settlement_acc_num,omitempty"`
}

type AssignRoleRequest struct {
	UserID uuid.UUID `json:"user_id"`
	Role   string    `json:"role"` // 'user', 'merchant', 'moderator', 'admin'
}

// RegisterMerchant upgrades a verified user to a registered Merchant profile
func RegisterMerchant(c *fiber.Ctx) error {
	userIDStr, ok := c.Locals("user_id").(string)
	if !ok || userIDStr == "" {
		return c.Status(fiber.StatusUnauthorized).JSON(fiber.Map{"error": "Unauthorized"})
	}
	userID, err := uuid.Parse(userIDStr)
	if err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "Invalid user ID"})
	}

	tenantIDStr, _ := c.Locals("tenant_id").(string)
	tenantID, _ := uuid.Parse(tenantIDStr)

	var req RegisterMerchantRequest
	if err := c.BodyParser(&req); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "Invalid request body"})
	}

	if strings.TrimSpace(req.BusinessName) == "" {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "business_name is required"})
	}

	// 1. Check if user exists
	var user models.User
	if err := db.DB.Where("id = ?", userID.String()).First(&user).Error; err != nil {
		return c.Status(fiber.StatusNotFound).JSON(fiber.Map{"error": "User not found"})
	}

	// 2. Check if merchant profile already exists
	var existing models.MerchantProfile
	err = db.DB.Where("user_id = ?", userID.String()).First(&existing).Error
	if err == nil {
		return c.Status(fiber.StatusConflict).JSON(fiber.Map{
			"error":   "Merchant profile already exists for this account",
			"profile": existing,
		})
	}

	// 3. Generate static POS QR URI (Clean, without fixed amount, e.g. "zwc:<tax_number_or_phone>")
	staticIdentifier := req.TaxNumber
	if staticIdentifier == "" {
		staticIdentifier = user.Phone
	}
	staticQR := fmt.Sprintf("zwc:%s?name=%s", staticIdentifier, req.BusinessName)

	profile := models.MerchantProfile{
		ID:               uuid.New(),
		UserID:           userID,
		TenantID:         tenantID,
		BusinessName:     req.BusinessName,
		TaxNumber:        req.TaxNumber,
		Category:         req.Category,
		StaticQRURI:      staticQR,
		WebhookURL:       req.WebhookURL,
		DailyLimit:       100000.0,
		PreAuthEnabled:   true,
		SettlementBank:   req.SettlementBank,
		SettlementAccNum: req.SettlementAccNum,
		CreatedAt:        time.Now(),
		UpdatedAt:        time.Now(),
	}

	if err := db.DB.Create(&profile).Error; err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "Failed to create merchant profile"})
	}

	// 4. Update user role to 'merchant'
	db.DB.Model(&user).Update("role", models.RoleMerchant)

	return c.Status(fiber.StatusCreated).JSON(fiber.Map{
		"message": "Merchant profile registered successfully",
		"profile": profile,
	})
}

// GetMerchantProfile retrieves the authenticated merchant's business details
func GetMerchantProfile(c *fiber.Ctx) error {
	userIDStr, ok := c.Locals("user_id").(string)
	if !ok || userIDStr == "" {
		return c.Status(fiber.StatusUnauthorized).JSON(fiber.Map{"error": "Unauthorized"})
	}
	userID, err := uuid.Parse(userIDStr)
	if err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "Invalid user ID"})
	}

	var profile models.MerchantProfile
	if err := db.DB.Where("user_id = ?", userID.String()).First(&profile).Error; err != nil {
		return c.Status(fiber.StatusNotFound).JSON(fiber.Map{"error": "Merchant profile not found"})
	}

	return c.JSON(profile)
}

// AssignUserRole allows an administrator to assign roles (user, merchant, moderator, admin)
func AssignUserRole(c *fiber.Ctx) error {
	var req AssignRoleRequest
	if err := c.BodyParser(&req); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "Invalid request body"})
	}

	if req.UserID == uuid.Nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "user_id is required"})
	}

	validRole := false
	switch req.Role {
	case models.RoleUser, models.RoleMerchant, models.RoleModerator, models.RoleAdmin, "reviewer":
		validRole = true
	}

	if !validRole {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
			"error": "Invalid role. Supported roles: user, merchant, moderator, admin",
		})
	}

	var user models.User
	if err := db.DB.Where("id = ?", req.UserID.String()).First(&user).Error; err != nil {
		return c.Status(fiber.StatusNotFound).JSON(fiber.Map{"error": "User not found"})
	}

	if err := db.DB.Model(&user).Update("role", req.Role).Error; err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "Failed to update user role"})
	}

	return c.JSON(fiber.Map{
		"message": fmt.Sprintf("User %s role updated to %s", user.Phone, req.Role),
		"user_id": user.ID,
		"role":    req.Role,
	})
}

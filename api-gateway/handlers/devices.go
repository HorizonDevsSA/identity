package handlers

import (
	"fmt"
	"strconv"
	"strings"

	"api-gateway/services"

	"github.com/gofiber/fiber/v2"
	"github.com/google/uuid"
)

// ListDevices returns all active and trusted devices for the user
func ListDevices(c *fiber.Ctx) error {
	userIDStr, ok := c.Locals("user_id").(string)
	if !ok || userIDStr == "" {
		return c.Status(fiber.StatusUnauthorized).JSON(fiber.Map{"error": "Unauthorized"})
	}
	userID, err := uuid.Parse(userIDStr)
	if err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "Invalid user ID"})
	}

	devices, err := services.ListUserDevices(userID)
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "Failed to fetch devices"})
	}

	return c.JSON(fiber.Map{"devices": devices})
}

// RevokeDevice revokes trust for a specific device
func RevokeDevice(c *fiber.Ctx) error {
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

	deviceIDStr := c.Params("id")
	deviceID, err := uuid.Parse(deviceIDStr)
	if err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "Invalid device ID"})
	}

	if err := services.RevokeDevice(tenantID, userID, deviceID, c.IP(), c.Get("User-Agent")); err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": err.Error()})
	}

	return c.JSON(fiber.Map{"message": "Device trust revoked successfully"})
}

// GetSecurityAuditLogs retrieves recent security activity logs for the current user
func GetSecurityAuditLogs(c *fiber.Ctx) error {
	userIDStr, ok := c.Locals("user_id").(string)
	if !ok || userIDStr == "" {
		return c.Status(fiber.StatusUnauthorized).JSON(fiber.Map{"error": "Unauthorized"})
	}
	userID, err := uuid.Parse(userIDStr)
	if err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "Invalid user ID"})
	}

	limit, _ := strconv.Atoi(c.Query("limit", "20"))
	offset, _ := strconv.Atoi(c.Query("offset", "0"))

	logs, total, err := services.ListSecurityAuditLogs(userID, limit, offset)
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "Failed to fetch audit logs"})
	}

	return c.JSON(fiber.Map{
		"audit_logs": logs,
		"total":      total,
		"limit":      limit,
		"offset":     offset,
	})
}

type BiometricRefreshRequest struct {
	DeviceHash string `json:"device_hash"`
}

// BiometricSessionRefresh validates a hardware device unlock and generates a refreshed token
func BiometricSessionRefresh(c *fiber.Ctx) error {
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
	role, _ := c.Locals("role").(string)

	var req BiometricRefreshRequest
	if err := c.BodyParser(&req); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "Invalid request body"})
	}

	if strings.TrimSpace(req.DeviceHash) == "" {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "device_hash is required for biometric session refresh"})
	}

	// Verify device is recognized and trusted
	devices, err := services.ListUserDevices(userID)
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "Failed to verify device"})
	}

	isTrusted := false
	for _, d := range devices {
		if d.DeviceHash == req.DeviceHash && d.Trusted {
			isTrusted = true
			break
		}
	}

	if !isTrusted {
		_ = services.LogSecurityAudit(tenantID, userID, "biometric_refresh_failed", "Untrusted or unknown device attempted biometric refresh", c.IP(), c.Get("User-Agent"), map[string]interface{}{
			"device_hash": req.DeviceHash,
		})
		return c.Status(fiber.StatusForbidden).JSON(fiber.Map{"error": "Device is not trusted or recognized. Please re-authenticate via OTP."})
	}

	// Generate fresh token
	newToken, err := GenerateJWT(userID, tenantID, role)
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "Failed to generate refreshed session token"})
	}

	_ = services.LogSecurityAudit(tenantID, userID, "biometric_refresh_success", "Biometric session refresh successful", c.IP(), c.Get("User-Agent"), map[string]interface{}{
		"device_hash": req.DeviceHash,
	})

	return c.JSON(fiber.Map{
		"token":   newToken,
		"message": fmt.Sprintf("Session refreshed for trusted device"),
	})
}

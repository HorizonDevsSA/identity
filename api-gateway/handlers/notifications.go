package handlers

import (
	"strconv"

	"api-gateway/models"
	"api-gateway/services"

	"github.com/gofiber/fiber/v2"
	"github.com/google/uuid"
)

// ListNotifications returns paginated notifications for the authenticated user
func ListNotifications(c *fiber.Ctx) error {
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

	notifs, total, err := services.ListUserNotifications(userID, limit, offset)
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "Failed to fetch notifications"})
	}

	return c.JSON(fiber.Map{
		"notifications": notifs,
		"total":         total,
		"limit":         limit,
		"offset":        offset,
	})
}

// MarkNotificationRead marks a specific notification as read
func MarkNotificationRead(c *fiber.Ctx) error {
	userIDStr, ok := c.Locals("user_id").(string)
	if !ok || userIDStr == "" {
		return c.Status(fiber.StatusUnauthorized).JSON(fiber.Map{"error": "Unauthorized"})
	}
	userID, err := uuid.Parse(userIDStr)
	if err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "Invalid user ID"})
	}

	notifIDStr := c.Params("id")
	notifID, err := uuid.Parse(notifIDStr)
	if err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "Invalid notification ID"})
	}

	if err := services.MarkNotificationAsRead(userID, notifID); err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "Failed to mark notification as read"})
	}

	return c.JSON(fiber.Map{"message": "Notification marked as read"})
}

// GetNotificationPreferences retrieves user notification settings
func GetNotificationPreferences(c *fiber.Ctx) error {
	userIDStr, ok := c.Locals("user_id").(string)
	if !ok || userIDStr == "" {
		return c.Status(fiber.StatusUnauthorized).JSON(fiber.Map{"error": "Unauthorized"})
	}
	userID, err := uuid.Parse(userIDStr)
	if err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "Invalid user ID"})
	}

	prefs, err := services.GetOrCreatePreferences(userID)
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "Failed to fetch preferences"})
	}

	return c.JSON(prefs)
}

// UpdateNotificationPreferences updates user notification settings
func UpdateNotificationPreferences(c *fiber.Ctx) error {
	userIDStr, ok := c.Locals("user_id").(string)
	if !ok || userIDStr == "" {
		return c.Status(fiber.StatusUnauthorized).JSON(fiber.Map{"error": "Unauthorized"})
	}
	userID, err := uuid.Parse(userIDStr)
	if err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "Invalid user ID"})
	}

	var prefs models.NotificationPreference
	if err := c.BodyParser(&prefs); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "Invalid request body"})
	}

	if err := services.UpdatePreferences(userID, prefs); err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "Failed to update preferences"})
	}

	return c.JSON(fiber.Map{"message": "Notification preferences updated successfully"})
}

type DeviceTokenRequest struct {
	FCMToken   string `json:"fcm_token"`
	Platform   string `json:"platform"` // 'ios', 'android', 'web'
	DeviceName string `json:"device_name"`
}

// RegisterDeviceToken registers or updates an FCM push token
func RegisterDeviceToken(c *fiber.Ctx) error {
	userIDStr, ok := c.Locals("user_id").(string)
	if !ok || userIDStr == "" {
		return c.Status(fiber.StatusUnauthorized).JSON(fiber.Map{"error": "Unauthorized"})
	}
	userID, err := uuid.Parse(userIDStr)
	if err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "Invalid user ID"})
	}

	var req DeviceTokenRequest
	if err := c.BodyParser(&req); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "Invalid request body"})
	}

	if req.FCMToken == "" {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "fcm_token is required"})
	}

	if err := services.RegisterDeviceToken(userID, req.FCMToken, req.Platform, req.DeviceName); err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "Failed to register device token"})
	}

	return c.JSON(fiber.Map{"message": "Device push token registered successfully"})
}

// DeleteDeviceToken removes an FCM push token on logout
func DeleteDeviceToken(c *fiber.Ctx) error {
	userIDStr, ok := c.Locals("user_id").(string)
	if !ok || userIDStr == "" {
		return c.Status(fiber.StatusUnauthorized).JSON(fiber.Map{"error": "Unauthorized"})
	}
	userID, err := uuid.Parse(userIDStr)
	if err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "Invalid user ID"})
	}

	var req DeviceTokenRequest
	if err := c.BodyParser(&req); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "Invalid request body"})
	}

	if err := services.RemoveDeviceToken(userID, req.FCMToken); err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "Failed to remove device token"})
	}

	return c.JSON(fiber.Map{"message": "Device token removed"})
}

type TransactionNotifyRequest struct {
	UserID       string `json:"user_id"`
	Type         string `json:"type"` // payment_received, payment_sent, reserve_minted, etc.
	TxHash       string `json:"tx_hash"`
	Amount       string `json:"amount"`
	Currency     string `json:"currency"`
	Counterparty string `json:"counterparty"`
	Status       string `json:"status"`
}

// NotifyTransaction dispatches a push notification and in-app feed item for a transaction event
func NotifyTransaction(c *fiber.Ctx) error {
	var req TransactionNotifyRequest
	if err := c.BodyParser(&req); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "Invalid request body"})
	}

	targetUserIDStr := req.UserID
	if targetUserIDStr == "" {
		if authID, ok := c.Locals("user_id").(string); ok {
			targetUserIDStr = authID
		}
	}

	if targetUserIDStr == "" {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "user_id is required"})
	}

	targetUserID, err := uuid.Parse(targetUserIDStr)
	if err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "Invalid user_id format"})
	}

	if req.Type == "" {
		req.Type = "payment_received"
	}

	notif, err := services.NotifyTransactionEvent(
		c.Context(),
		targetUserID,
		req.Type,
		req.TxHash,
		req.Amount,
		req.Currency,
		req.Counterparty,
		req.Status,
	)
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": err.Error()})
	}

	return c.Status(fiber.StatusOK).JSON(fiber.Map{
		"message":      "Transaction notification dispatched",
		"notification": notif,
	})
}


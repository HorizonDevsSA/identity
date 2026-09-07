package services

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"strings"
	"time"

	"api-gateway/db"
	"api-gateway/models"

	"github.com/google/uuid"
)

type NotifyRequest struct {
	UserID        uuid.UUID         `json:"user_id"`
	Type          string            `json:"type"` // e.g. 'payment_received', 'payment_sent', 'kyc_verified', 'security_alert'
	Title         string            `json:"title"`
	Body          string            `json:"body"`
	Data          map[string]string `json:"data,omitempty"`
	FallbackPhone string            `json:"fallback_phone,omitempty"`
}

// IsMandatoryNotification returns true if the notification type cannot be opted out of
func IsMandatoryNotification(notifType string) bool {
	switch notifType {
	case "kyc_verified", "alias_bound", "reserve_minted", "reserve_redeemed", "new_device_trusted", "security_alert", "system":
		return true
	default:
		return false
	}
}

// CheckPreferenceAllowed verifies if user preference allows the given notification type
func CheckPreferenceAllowed(prefs *models.NotificationPreference, notifType string) bool {
	if IsMandatoryNotification(notifType) {
		return true
	}
	if prefs == nil {
		return true
	}

	switch notifType {
	case "payment_received":
		return prefs.IncomingTransfers
	case "payment_sent":
		return prefs.OutgoingTransfers
	case "preauth_hold_created", "preauth_hold_captured", "preauth_hold_released":
		return prefs.MerchantActivity
	case "marketing":
		return prefs.Marketing
	default:
		return true
	}
}

// SendNotification routes a notification across in-app DB store, FCM Push, and Twilio SMS fallback
func SendNotification(ctx context.Context, req NotifyRequest) (*models.Notification, error) {
	if req.UserID == uuid.Nil {
		return nil, fmt.Errorf("user_id cannot be nil")
	}
	if strings.TrimSpace(req.Type) == "" {
		return nil, fmt.Errorf("notification type is required")
	}

	// 1. Fetch user notification preferences
	prefs, err := GetOrCreatePreferences(req.UserID)
	if err != nil {
		log.Printf("[NOTIF] Warning: failed to fetch preferences for %s: %v", req.UserID, err)
	}

	if !CheckPreferenceAllowed(prefs, req.Type) {
		log.Printf("[NOTIF] User %s opted out of notification type %s — skipping push", req.UserID, req.Type)
		return nil, nil
	}

	dataBytes, _ := json.Marshal(req.Data)
	dataStr := string(dataBytes)
	if dataStr == "" || dataStr == "null" {
		dataStr = "{}"
	}

	// 2. Persist in-app notification row (Inbox feed)
	notif := models.Notification{
		ID:        uuid.New(),
		UserID:    req.UserID,
		Type:      req.Type,
		Title:     req.Title,
		Body:      req.Body,
		Data:      dataStr,
		Status:    "pending",
		CreatedAt: time.Now(),
	}

	if err := db.DB.Create(&notif).Error; err != nil {
		return nil, fmt.Errorf("failed to persist notification: %w", err)
	}

	// 3. Dispatch to registered FCM device tokens
	var tokens []models.DeviceToken
	db.DB.Where("user_id = ?", req.UserID).Find(&tokens)

	pushedToDevice := false
	for _, dt := range tokens {
		// Mock / Real FCM dispatch: In production, delegates to Firebase FCM SDK
		log.Printf("[FCM-PUSH] Dispatching to Token=%s (Platform=%s, User=%s) | Title=%q | Body=%q",
			dt.FCMToken, dt.Platform, req.UserID, req.Title, req.Body)
		pushedToDevice = true
	}

	// 4. SMS Fallback if no device tokens exist or SMS fallback is enabled for critical alerts
	if (!pushedToDevice || IsMandatoryNotification(req.Type)) && prefs != nil && prefs.SMSFallback {
		phone := req.FallbackPhone
		if phone == "" {
			var user models.User
			if err := db.DB.Select("phone").Where("id = ?", req.UserID).First(&user).Error; err == nil {
				phone = user.Phone
			}
		}

		if phone != "" {
			smsText := fmt.Sprintf("%s: %s", req.Title, req.Body)
			go func(p, msg string) {
				if _, err := SendSMS(p, msg); err != nil {
					log.Printf("[NOTIF-SMS-FALLBACK] Failed to dispatch SMS to %s: %v", p, err)
				}
			}(phone, smsText)
		}
	}

	// 5. Mark sent
	now := time.Now()
	db.DB.Model(&notif).Updates(map[string]interface{}{
		"status":  "sent",
		"sent_at": now,
	})
	notif.Status = "sent"
	notif.SentAt = &now

	return &notif, nil
}

// GetOrCreatePreferences retrieves or initializes a user's notification preferences
func GetOrCreatePreferences(userID uuid.UUID) (*models.NotificationPreference, error) {
	var prefs models.NotificationPreference
	err := db.DB.Where("user_id = ?", userID).First(&prefs).Error
	if err == nil {
		return &prefs, nil
	}

	// Initialize default preferences
	prefs = models.NotificationPreference{
		UserID:            userID,
		IncomingTransfers: true,
		OutgoingTransfers: true,
		MerchantActivity:  true,
		SMSFallback:       true,
		Marketing:         false,
		UpdatedAt:         time.Now(),
	}
	if err := db.DB.Create(&prefs).Error; err != nil {
		return nil, err
	}
	return &prefs, nil
}

// UpdatePreferences updates the user's notification preferences
func UpdatePreferences(userID uuid.UUID, prefs models.NotificationPreference) error {
	prefs.UserID = userID
	prefs.UpdatedAt = time.Now()
	return db.DB.Save(&prefs).Error
}

// RegisterDeviceToken saves or updates an FCM token for a user's device
func RegisterDeviceToken(userID uuid.UUID, fcmToken, platform, deviceName string) error {
	fcmToken = strings.TrimSpace(fcmToken)
	if fcmToken == "" {
		return fmt.Errorf("fcm_token cannot be empty")
	}
	platform = strings.ToLower(strings.TrimSpace(platform))
	if platform != "ios" && platform != "android" && platform != "web" {
		platform = "android"
	}

	var existing models.DeviceToken
	err := db.DB.Where("user_id = ? AND fcm_token = ?", userID, fcmToken).First(&existing).Error
	if err == nil {
		return db.DB.Model(&existing).Updates(map[string]interface{}{
			"platform":     platform,
			"device_name":  deviceName,
			"last_seen_at": time.Now(),
		}).Error
	}

	tokenRecord := models.DeviceToken{
		ID:         uuid.New(),
		UserID:     userID,
		FCMToken:   fcmToken,
		Platform:   platform,
		DeviceName: deviceName,
		CreatedAt:  time.Now(),
		LastSeenAt: time.Now(),
	}
	return db.DB.Create(&tokenRecord).Error
}

// RemoveDeviceToken prunes a device token on logout or invalidation
func RemoveDeviceToken(userID uuid.UUID, fcmToken string) error {
	return db.DB.Where("user_id = ? AND fcm_token = ?", userID, strings.TrimSpace(fcmToken)).
		Delete(&models.DeviceToken{}).Error
}

// ListUserNotifications returns a paginated list of in-app notifications
func ListUserNotifications(userID uuid.UUID, limit, offset int) ([]models.Notification, int64, error) {
	if limit <= 0 || limit > 100 {
		limit = 20
	}
	if offset < 0 {
		offset = 0
	}

	var count int64
	var notifs []models.Notification

	db.DB.Model(&models.Notification{}).Where("user_id = ?", userID).Count(&count)
	err := db.DB.Where("user_id = ?", userID).
		Order("created_at desc").
		Limit(limit).
		Offset(offset).
		Find(&notifs).Error

	return notifs, count, err
}

// MarkNotificationAsRead marks a notification as read
func MarkNotificationAsRead(userID, notifID uuid.UUID) error {
	now := time.Now()
	return db.DB.Model(&models.Notification{}).
		Where("id = ? AND user_id = ?", notifID, userID).
		Updates(map[string]interface{}{
			"status":  "read",
			"read_at": now,
		}).Error
}

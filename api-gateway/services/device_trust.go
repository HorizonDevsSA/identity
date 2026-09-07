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

// LogSecurityAudit writes a security or lifecycle audit record
func LogSecurityAudit(tenantID, actorID uuid.UUID, action, details, ip, userAgent string, metadata map[string]interface{}) error {
	metaBytes, _ := json.Marshal(metadata)
	metaStr := string(metaBytes)
	if metaStr == "" || metaStr == "null" {
		metaStr = "{}"
	}

	audit := models.AuditLog{
		ID:        uuid.New(),
		TenantID:  tenantID,
		UserID:    actorID,
		Action:    action,
		Details:   details,
		IPAddress: ip,
		UserAgent: userAgent,
		Metadata:  metaStr,
		CreatedAt: time.Now(),
	}

	return db.DB.Create(&audit).Error
}

// RegisterOrVerifyDevice registers a new trusted hardware device or updates an existing device
func RegisterOrVerifyDevice(userID, tenantID uuid.UUID, deviceHash, deviceName, publicKey, ip, userAgent string) (*models.Device, bool, error) {
	deviceHash = strings.TrimSpace(deviceHash)
	if deviceHash == "" {
		return nil, false, fmt.Errorf("device_hash is required")
	}

	var existing models.Device
	err := db.DB.Where("user_id = ? AND device_hash = ?", userID, deviceHash).First(&existing).Error
	if err == nil {
		// Existing recognized device
		now := time.Now()
		updates := map[string]interface{}{
			"last_seen_at": now,
		}
		if deviceName != "" {
			updates["device_name"] = deviceName
		}
		if publicKey != "" {
			updates["public_key"] = publicKey
		}
		db.DB.Model(&existing).Updates(updates)

		_ = LogSecurityAudit(tenantID, userID, "login_known_device", fmt.Sprintf("Login from known device %s", existing.DeviceName), ip, userAgent, map[string]interface{}{
			"device_hash": deviceHash,
			"device_id":   existing.ID.String(),
		})

		return &existing, false, nil
	}

	// New device registration
	newDevice := models.Device{
		ID:          uuid.New(),
		UserID:      userID,
		DeviceHash:  deviceHash,
		DeviceName:  deviceName,
		PublicKey:   publicKey,
		Trusted:     true,
		FirstSeenAt: time.Now(),
		LastSeenAt:  time.Now(),
	}

	if err := db.DB.Create(&newDevice).Error; err != nil {
		return nil, false, fmt.Errorf("failed to register device: %w", err)
	}

	// Record security audit log
	_ = LogSecurityAudit(tenantID, userID, "new_device_trusted", fmt.Sprintf("New device trusted: %s", deviceName), ip, userAgent, map[string]interface{}{
		"device_hash": deviceHash,
		"device_id":   newDevice.ID.String(),
		"device_name": deviceName,
	})

	// Dispatch security notification to user
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()

		_, _ = SendNotification(ctx, NotifyRequest{
			UserID: userID,
			Type:   "new_device_trusted",
			Title:  "Security Alert: New Device Linked",
			Body:   fmt.Sprintf("Your ZWC account was just accessed from a new device (%s). If this wasn't you, please review your active devices.", deviceName),
			Data: map[string]string{
				"screen":      "security_devices",
				"device_id":   newDevice.ID.String(),
				"device_name": deviceName,
			},
		})
	}()

	log.Printf("[DEVICE-TRUST] New device registered for user %s: Name=%q, Hash=%s", userID, deviceName, deviceHash)
	return &newDevice, true, nil
}

// ListUserDevices returns all active trusted devices for a user
func ListUserDevices(userID uuid.UUID) ([]models.Device, error) {
	var devices []models.Device
	err := db.DB.Where("user_id = ?", userID).Order("last_seen_at desc").Find(&devices).Error
	return devices, err
}

// RevokeDevice revokes trust for a device and logs the action
func RevokeDevice(tenantID, userID, deviceID uuid.UUID, ip, userAgent string) error {
	var device models.Device
	if err := db.DB.Where("id = ? AND user_id = ?", deviceID, userID).First(&device).Error; err != nil {
		return fmt.Errorf("device not found")
	}

	if err := db.DB.Delete(&device).Error; err != nil {
		return fmt.Errorf("failed to revoke device: %w", err)
	}

	// Also remove any device tokens matching this user/device
	_ = LogSecurityAudit(tenantID, userID, "device_revoked", fmt.Sprintf("Revoked device %s (%s)", device.DeviceName, device.ID), ip, userAgent, map[string]interface{}{
		"device_id":   deviceID.String(),
		"device_hash": device.DeviceHash,
	})

	return nil
}

// ListSecurityAuditLogs retrieves recent security activity logs for a user
func ListSecurityAuditLogs(userID uuid.UUID, limit, offset int) ([]models.AuditLog, int64, error) {
	if limit <= 0 || limit > 100 {
		limit = 20
	}
	if offset < 0 {
		offset = 0
	}

	var count int64
	var logs []models.AuditLog

	db.DB.Model(&models.AuditLog{}).Where("user_id = ?", userID).Count(&count)
	err := db.DB.Where("user_id = ?", userID).
		Order("created_at desc").
		Limit(limit).
		Offset(offset).
		Find(&logs).Error

	return logs, count, err
}

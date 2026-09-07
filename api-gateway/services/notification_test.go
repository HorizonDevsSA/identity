package services

import (
	"context"
	"testing"
	"time"

	"api-gateway/db"
	"api-gateway/models"

	"github.com/glebarez/sqlite"
	"github.com/google/uuid"
	"gorm.io/gorm"
)

func setupTestNotificationDB(t *testing.T) {
	database, err := gorm.Open(sqlite.Open("file::memory:?cache=shared"), &gorm.Config{})
	if err != nil {
		t.Fatalf("failed to open sqlite in-memory db: %v", err)
	}

	schema := `
		CREATE TABLE IF NOT EXISTS users (
			id TEXT PRIMARY KEY,
			phone TEXT UNIQUE NOT NULL,
			email TEXT UNIQUE,
			password_hash TEXT NOT NULL DEFAULT '-',
			role TEXT DEFAULT 'user',
			tenant_id TEXT,
			created_at DATETIME,
			updated_at DATETIME,
			deleted_at DATETIME
		);
		CREATE TABLE IF NOT EXISTS notification_preferences (
			user_id TEXT PRIMARY KEY,
			incoming_transfers BOOLEAN NOT NULL DEFAULT 1,
			outgoing_transfers BOOLEAN NOT NULL DEFAULT 1,
			merchant_activity BOOLEAN NOT NULL DEFAULT 1,
			sms_fallback BOOLEAN NOT NULL DEFAULT 1,
			marketing BOOLEAN NOT NULL DEFAULT 0,
			updated_at DATETIME
		);
		CREATE TABLE IF NOT EXISTS notifications (
			id TEXT PRIMARY KEY,
			user_id TEXT NOT NULL,
			type TEXT NOT NULL,
			title TEXT NOT NULL,
			body TEXT NOT NULL,
			data TEXT DEFAULT '{}',
			status TEXT DEFAULT 'pending',
			sent_at DATETIME,
			read_at DATETIME,
			created_at DATETIME
		);
		CREATE TABLE IF NOT EXISTS device_tokens (
			id TEXT PRIMARY KEY,
			user_id TEXT NOT NULL,
			fcm_token TEXT NOT NULL,
			platform TEXT DEFAULT 'android',
			device_name TEXT,
			created_at DATETIME,
			last_seen_at DATETIME
		);
		CREATE TABLE IF NOT EXISTS devices (
			id TEXT PRIMARY KEY,
			user_id TEXT NOT NULL,
			device_hash TEXT NOT NULL,
			device_name TEXT,
			public_key TEXT,
			trusted BOOLEAN DEFAULT 1,
			first_seen_at DATETIME,
			last_seen_at DATETIME
		);
		CREATE TABLE IF NOT EXISTS audit_logs (
			id TEXT PRIMARY KEY,
			tenant_id TEXT,
			user_id TEXT,
			action TEXT NOT NULL,
			details TEXT,
			ip_address TEXT,
			user_agent TEXT,
			metadata TEXT DEFAULT '{}',
			created_at DATETIME
		);
	`
	if err := database.Exec(schema).Error; err != nil {
		t.Fatalf("failed to initialize sqlite schema: %v", err)
	}

	db.DB = database
}

func TestNotificationPreferences_Logic(t *testing.T) {
	prefs := &models.NotificationPreference{
		IncomingTransfers: false,
		OutgoingTransfers: true,
		MerchantActivity:  false,
		Marketing:         false,
	}

	// Mandatory notifications must always pass
	if !CheckPreferenceAllowed(prefs, "kyc_verified") {
		t.Error("expected kyc_verified to be mandatory and allowed")
	}
	if !CheckPreferenceAllowed(prefs, "new_device_trusted") {
		t.Error("expected new_device_trusted to be mandatory and allowed")
	}

	// Opted out types must return false
	if CheckPreferenceAllowed(prefs, "payment_received") {
		t.Error("expected payment_received to be blocked when incoming_transfers=false")
	}
	if CheckPreferenceAllowed(prefs, "preauth_hold_created") {
		t.Error("expected preauth_hold_created to be blocked when merchant_activity=false")
	}

	// Allowed types must return true
	if !CheckPreferenceAllowed(prefs, "payment_sent") {
		t.Error("expected payment_sent to be allowed when outgoing_transfers=true")
	}
}

func TestSendNotification_Lifecycle(t *testing.T) {
	setupTestNotificationDB(t)

	userID := uuid.New()
	testUser := models.User{
		ID:    userID,
		Phone: "+263770001122",
		Role:  "user",
	}
	db.DB.Create(&testUser)

	// Register device token
	err := RegisterDeviceToken(userID, "fcm_test_token_123", "ios", "iPhone 15 Pro")
	if err != nil {
		t.Fatalf("failed to register device token: %v", err)
	}

	// 1. Send mandatory notification
	req := NotifyRequest{
		UserID: userID,
		Type:   "kyc_verified",
		Title:  "KYC Approved",
		Body:   "Your account has been verified on-chain.",
		Data:   map[string]string{"wallet_address": "zwc1testaddress"},
	}

	notif, err := SendNotification(context.Background(), req)
	if err != nil {
		t.Fatalf("failed to send notification: %v", err)
	}
	if notif == nil || notif.Status != "sent" {
		t.Errorf("expected notification status sent, got %v", notif)
	}

	// 2. Query in-app notifications
	notifs, count, err := ListUserNotifications(userID, 10, 0)
	if err != nil {
		t.Fatalf("failed to list user notifications: %v", err)
	}
	if count != 1 || len(notifs) != 1 {
		t.Errorf("expected 1 notification in inbox, got %d", count)
	}
	if notifs[0].Type != "kyc_verified" {
		t.Errorf("expected type kyc_verified, got %s", notifs[0].Type)
	}

	// 3. Mark notification as read
	err = MarkNotificationAsRead(userID, notifs[0].ID)
	if err != nil {
		t.Fatalf("failed to mark notification as read: %v", err)
	}

	var updated models.Notification
	db.DB.Where("id = ?", notifs[0].ID).First(&updated)
	if updated.Status != "read" || updated.ReadAt == nil {
		t.Errorf("expected status read with read_at timestamp, got status=%s", updated.Status)
	}

	// 4. Remove device token
	err = RemoveDeviceToken(userID, "fcm_test_token_123")
	if err != nil {
		t.Fatalf("failed to remove device token: %v", err)
	}
}

func TestDeviceTrust_Lifecycle(t *testing.T) {
	setupTestNotificationDB(t)

	userID := uuid.New()
	tenantID := uuid.New()
	deviceHash := "sha256_mock_device_hash_98765"

	// 1. Register new device
	device, isNew, err := RegisterOrVerifyDevice(userID, tenantID, deviceHash, "Pixel 8", "pubkey_test", "127.0.0.1", "Go-Test-Agent")
	if err != nil {
		t.Fatalf("failed to register device: %v", err)
	}
	if !isNew {
		t.Error("expected isNew=true for first device registration")
	}
	if !device.Trusted {
		t.Error("expected device to be trusted")
	}

	// 2. Verify existing device
	time.Sleep(10 * time.Millisecond)
	device2, isNew2, err := RegisterOrVerifyDevice(userID, tenantID, deviceHash, "Pixel 8 (Updated)", "pubkey_test", "127.0.0.1", "Go-Test-Agent")
	if err != nil {
		t.Fatalf("failed to verify existing device: %v", err)
	}
	if isNew2 {
		t.Error("expected isNew=false for existing device")
	}
	if device2.ID != device.ID {
		t.Errorf("expected same device ID %s, got %s", device.ID, device2.ID)
	}

	// 3. List devices
	devices, err := ListUserDevices(userID)
	if err != nil {
		t.Fatalf("failed to list devices: %v", err)
	}
	if len(devices) != 1 {
		t.Errorf("expected 1 device, got %d", len(devices))
	}

	// 4. Revoke device
	err = RevokeDevice(tenantID, userID, device.ID, "127.0.0.1", "Go-Test-Agent")
	if err != nil {
		t.Fatalf("failed to revoke device: %v", err)
	}

	devicesAfter, _ := ListUserDevices(userID)
	if len(devicesAfter) != 0 {
		t.Errorf("expected 0 devices after revocation, got %d", len(devicesAfter))
	}

	// 5. Check Audit Logs
	auditLogs, total, err := ListSecurityAuditLogs(userID, 10, 0)
	if err != nil {
		t.Fatalf("failed to list audit logs: %v", err)
	}
	if total < 2 {
		t.Errorf("expected at least 2 audit logs (new_device_trusted, device_revoked), got %d", total)
	}
	_ = auditLogs
}

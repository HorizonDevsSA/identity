package handlers

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"api-gateway/db"
	"api-gateway/models"

	"github.com/gofiber/fiber/v2"
	"github.com/google/uuid"
)

func setupDevicesTestApp(t *testing.T) (*fiber.App, string, uuid.UUID, uuid.UUID) {
	app := setupTestApp(t)

	userID := uuid.New()
	tenantID := uuid.New()

	testUser := models.User{
		ID:       userID,
		TenantID: tenantID,
		Phone:    "+263778889900",
		Role:     "user",
	}
	db.DB.Create(&testUser)

	token, _ := GenerateJWT(userID, tenantID, "user")

	// Register Device & Security routes
	devGroup := app.Group("/api", JWTMiddleware)
	devGroup.Get("/devices", ListDevices)
	devGroup.Delete("/devices/:id", RevokeDevice)
	devGroup.Post("/devices/biometric-refresh", BiometricSessionRefresh)
	devGroup.Get("/security/audit-logs", GetSecurityAuditLogs)

	return app, token, userID, tenantID
}

func TestDeviceTrustAndBiometricRefresh(t *testing.T) {
	app, token, userID, tenantID := setupDevicesTestApp(t)

	deviceHash := "sha256_trusted_hardware_hash_111"
	deviceID := uuid.New()

	// 1. Create a trusted device record in DB
	dev := models.Device{
		ID:          deviceID,
		UserID:      userID,
		DeviceHash:  deviceHash,
		DeviceName:  "iPhone 15 Pro",
		Trusted:     true,
		FirstSeenAt: time.Now(),
		LastSeenAt:  time.Now(),
	}
	db.DB.Create(&dev)

	// Also create an audit log
	audit := models.AuditLog{
		ID:        uuid.New(),
		TenantID:  tenantID,
		UserID:    userID,
		Action:    "new_device_trusted",
		Details:   "Device iPhone 15 Pro registered",
		CreatedAt: time.Now(),
	}
	db.DB.Create(&audit)

	// 2. List devices
	req := httptest.NewRequest("GET", "/api/devices", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	resp, _ := app.Test(req)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 listing devices, got %d", resp.StatusCode)
	}

	var devListResp struct {
		Devices []models.Device `json:"devices"`
	}
	json.NewDecoder(resp.Body).Decode(&devListResp)
	if len(devListResp.Devices) != 1 {
		t.Errorf("expected 1 device, got %d", len(devListResp.Devices))
	}

	// 3. Biometric Session Refresh with valid device hash
	refreshBody, _ := json.Marshal(BiometricRefreshRequest{DeviceHash: deviceHash})
	req = httptest.NewRequest("POST", "/api/devices/biometric-refresh", bytes.NewBuffer(refreshBody))
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	resp, _ = app.Test(req)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 on biometric refresh, got %d", resp.StatusCode)
	}

	var refreshResp map[string]string
	json.NewDecoder(resp.Body).Decode(&refreshResp)
	if refreshResp["token"] == "" {
		t.Error("expected non-empty refreshed token")
	}

	// 4. Biometric Session Refresh with unknown device hash -> 403 Forbidden
	badBody, _ := json.Marshal(BiometricRefreshRequest{DeviceHash: "unknown_attacker_hash"})
	req = httptest.NewRequest("POST", "/api/devices/biometric-refresh", bytes.NewBuffer(badBody))
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	resp, _ = app.Test(req)
	if resp.StatusCode != http.StatusForbidden {
		t.Errorf("expected 403 for untrusted device biometric refresh, got %d", resp.StatusCode)
	}

	// 5. Query Security Audit Logs
	req = httptest.NewRequest("GET", "/api/security/audit-logs?limit=10", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	resp, _ = app.Test(req)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 querying audit logs, got %d", resp.StatusCode)
	}

	var auditResp struct {
		AuditLogs []models.AuditLog `json:"audit_logs"`
		Total     int               `json:"total"`
	}
	json.NewDecoder(resp.Body).Decode(&auditResp)
	if auditResp.Total < 1 {
		t.Errorf("expected audit logs to be returned, got %d", auditResp.Total)
	}

	// 6. Revoke device
	req = httptest.NewRequest("DELETE", "/api/devices/"+deviceID.String(), nil)
	req.Header.Set("Authorization", "Bearer "+token)
	resp, _ = app.Test(req)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 revoking device, got %d", resp.StatusCode)
	}

	var checkDevice models.Device
	err := db.DB.Where("id = ?", deviceID).First(&checkDevice).Error
	if err == nil {
		t.Error("expected device to be deleted/revoked")
	}
}

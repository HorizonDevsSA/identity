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

func setupNotificationsTestApp(t *testing.T) (*fiber.App, string, uuid.UUID) {
	app := setupTestApp(t)

	userID := uuid.New()
	tenantID := uuid.New()

	testUser := models.User{
		ID:       userID,
		TenantID: tenantID,
		Phone:    "+263773334455",
		Role:     "user",
	}
	db.DB.Create(&testUser)

	token, _ := GenerateJWT(userID, tenantID, "user")

	// Register Notification routes
	notifGroup := app.Group("/api", JWTMiddleware)
	notifGroup.Get("/notifications", ListNotifications)
	notifGroup.Post("/notifications/:id/read", MarkNotificationRead)
	notifGroup.Get("/notifications/preferences", GetNotificationPreferences)
	notifGroup.Put("/notifications/preferences", UpdateNotificationPreferences)
	notifGroup.Post("/devices/tokens", RegisterDeviceToken)
	notifGroup.Delete("/devices/tokens", DeleteDeviceToken)

	return app, token, userID
}

func TestNotifications_InboxAndRead(t *testing.T) {
	app, token, userID := setupNotificationsTestApp(t)

	// Create test notifications in DB
	notifID := uuid.New()
	n := models.Notification{
		ID:        notifID,
		UserID:    userID,
		Type:      "payment_received",
		Title:     "Payment Received",
		Body:      "You received 50.00 ZWC from +263771112233",
		Status:    "sent",
		CreatedAt: time.Now(),
	}
	db.DB.Create(&n)

	// 1. List notifications
	req := httptest.NewRequest("GET", "/api/notifications?limit=10", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	resp, _ := app.Test(req)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 listing notifications, got %d", resp.StatusCode)
	}

	var listResp struct {
		Notifications []models.Notification `json:"notifications"`
		Total         int                   `json:"total"`
	}
	json.NewDecoder(resp.Body).Decode(&listResp)
	if listResp.Total != 1 || len(listResp.Notifications) != 1 {
		t.Errorf("expected 1 notification in inbox, got %d", listResp.Total)
	}

	// 2. Mark notification as read
	req = httptest.NewRequest("POST", "/api/notifications/"+notifID.String()+"/read", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	resp, _ = app.Test(req)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 marking notification as read, got %d", resp.StatusCode)
	}

	var check models.Notification
	db.DB.Where("id = ?", notifID).First(&check)
	if check.Status != "read" || check.ReadAt == nil {
		t.Errorf("expected status read, got %s", check.Status)
	}
}

func TestNotificationPreferences_Endpoints(t *testing.T) {
	app, token, _ := setupNotificationsTestApp(t)

	// 1. Get default preferences
	req := httptest.NewRequest("GET", "/api/notifications/preferences", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	resp, _ := app.Test(req)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 getting preferences, got %d", resp.StatusCode)
	}

	var prefs models.NotificationPreference
	json.NewDecoder(resp.Body).Decode(&prefs)
	if !prefs.IncomingTransfers || !prefs.OutgoingTransfers {
		t.Errorf("expected defaults to be true, got incoming=%t, outgoing=%t", prefs.IncomingTransfers, prefs.OutgoingTransfers)
	}

	// 2. Update preferences
	prefs.IncomingTransfers = false
	prefs.Marketing = true
	bodyBytes, _ := json.Marshal(prefs)

	req = httptest.NewRequest("PUT", "/api/notifications/preferences", bytes.NewBuffer(bodyBytes))
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	resp, _ = app.Test(req)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 updating preferences, got %d", resp.StatusCode)
	}
}

func TestDeviceTokens_Endpoints(t *testing.T) {
	app, token, _ := setupNotificationsTestApp(t)

	// 1. Register push token
	body, _ := json.Marshal(DeviceTokenRequest{
		FCMToken:   "fcm_mock_token_abc123",
		Platform:   "android",
		DeviceName: "Samsung Galaxy S24",
	})
	req := httptest.NewRequest("POST", "/api/devices/tokens", bytes.NewBuffer(body))
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	resp, _ := app.Test(req)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 registering push token, got %d", resp.StatusCode)
	}

	// 2. Delete push token on logout
	req = httptest.NewRequest("DELETE", "/api/devices/tokens", bytes.NewBuffer(body))
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	resp, _ = app.Test(req)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 deleting push token, got %d", resp.StatusCode)
	}
}

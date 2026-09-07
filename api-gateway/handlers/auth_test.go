package handlers

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"api-gateway/db"
	"api-gateway/services"

	"github.com/glebarez/sqlite"
	"github.com/gofiber/fiber/v2"
	"gorm.io/gorm"
)

func setupTestApp(t *testing.T) *fiber.App {
	// Initialize in-memory SQLite DB
	database, err := gorm.Open(sqlite.Open("file::memory:?cache=shared"), &gorm.Config{})
	if err != nil {
		t.Fatalf("failed to open sqlite in-memory db: %v", err)
	}

	// Create tables with SQLite-compatible schema
	schema := `
		CREATE TABLE IF NOT EXISTS tenants (
			id TEXT PRIMARY KEY,
			name TEXT NOT NULL,
			created_at DATETIME,
			updated_at DATETIME,
			deleted_at DATETIME
		);
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
		CREATE TABLE IF NOT EXISTS projects (
			id TEXT PRIMARY KEY,
			name TEXT NOT NULL,
			tenant_id TEXT,
			created_at DATETIME,
			updated_at DATETIME,
			deleted_at DATETIME
		);
		CREATE TABLE IF NOT EXISTS otp_verifications (
			id TEXT PRIMARY KEY,
			phone TEXT NOT NULL,
			channel TEXT DEFAULT 'sms',
			status TEXT DEFAULT 'pending',
			attempts INTEGER DEFAULT 0,
			created_at DATETIME,
			updated_at DATETIME,
			expires_at DATETIME
		);
		CREATE TABLE IF NOT EXISTS audit_logs (
			id TEXT PRIMARY KEY,
			tenant_id TEXT,
			user_id TEXT,
			action TEXT NOT NULL,
			details TEXT,
			user_agent TEXT,
			metadata TEXT DEFAULT '{}',
			ip_address TEXT,
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
	`
	if err := database.Exec(schema).Error; err != nil {
		t.Fatalf("failed to create sqlite schema: %v", err)
	}

	db.DB = database
	InitJWTSecret()

	// Clear Twilio credentials for dev mode tests
	os.Unsetenv("TWILIO_ACCOUNT_SID")
	os.Unsetenv("TWILIO_AUTH_TOKEN")
	os.Unsetenv("TWILIO_VERIFY_SERVICE_SID")
	os.Unsetenv("TWILIO_PHONE_NUMBER")
	services.SetTwilioHTTPClient(nil)

	app := fiber.New()
	app.Post("/auth/otp/send", SendOTP)
	app.Post("/auth/otp/resend", ResendOTP)
	app.Post("/auth/otp/verify", VerifyOTP)
	app.Post("/auth/twilio/webhook", TwilioWebhookCallback)

	// Protected test endpoint
	app.Get("/protected/admin", JWTMiddleware, RequireAdmin, func(c *fiber.Ctx) error {
		return c.JSON(fiber.Map{"authorized": true, "user_id": c.Locals("user_id")})
	})

	return app
}

func TestSendOTP_ValidationAndDevMode(t *testing.T) {
	app := setupTestApp(t)

	// 1. Missing body
	req := httptest.NewRequest("POST", "/auth/otp/send", bytes.NewBufferString("invalid json"))
	req.Header.Set("Content-Type", "application/json")
	resp, _ := app.Test(req)
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("expected 400 for bad json, got %d", resp.StatusCode)
	}

	// 2. Invalid phone format
	reqBody, _ := json.Marshal(SendOTPRequest{Phone: "12345", Channel: "sms"})
	req = httptest.NewRequest("POST", "/auth/otp/send", bytes.NewBuffer(reqBody))
	req.Header.Set("Content-Type", "application/json")
	resp, _ = app.Test(req)
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("expected 400 for invalid phone, got %d", resp.StatusCode)
	}

	// 3. Unsupported channel
	reqBody, _ = json.Marshal(SendOTPRequest{Phone: "+263771112233", Channel: "carrier_pigeon"})
	req = httptest.NewRequest("POST", "/auth/otp/send", bytes.NewBuffer(reqBody))
	req.Header.Set("Content-Type", "application/json")
	resp, _ = app.Test(req)
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("expected 400 for invalid channel, got %d", resp.StatusCode)
	}

	// 4. Successful Send in Dev Mode
	reqBody, _ = json.Marshal(SendOTPRequest{Phone: "+263771112233", Channel: "sms"})
	req = httptest.NewRequest("POST", "/auth/otp/send", bytes.NewBuffer(reqBody))
	req.Header.Set("Content-Type", "application/json")
	resp, _ = app.Test(req)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 for valid send, got %d", resp.StatusCode)
	}

	var result map[string]interface{}
	json.NewDecoder(resp.Body).Decode(&result)
	if result["status"] != "pending" || result["dev_code"] != "000000" {
		t.Errorf("expected pending status with dev code 000000, got: %v", result)
	}

	// 5. Rate Limiting: Sending immediately again within 30s should return 429
	req = httptest.NewRequest("POST", "/auth/otp/send", bytes.NewBuffer(reqBody))
	req.Header.Set("Content-Type", "application/json")
	resp, _ = app.Test(req)
	if resp.StatusCode != http.StatusTooManyRequests {
		t.Errorf("expected 429 for rate-limited send, got %d", resp.StatusCode)
	}
}

func TestVerifyOTP_FlowAndJWT(t *testing.T) {
	app := setupTestApp(t)
	testPhone := "+263779998877"

	// 1. Send OTP first
	reqBody, _ := json.Marshal(SendOTPRequest{Phone: testPhone, Channel: "sms"})
	req := httptest.NewRequest("POST", "/auth/otp/send", bytes.NewBuffer(reqBody))
	req.Header.Set("Content-Type", "application/json")
	app.Test(req)

	// 2. Verify with wrong code
	verifyBody, _ := json.Marshal(VerifyOTPRequest{Phone: testPhone, Code: "999999"})
	req = httptest.NewRequest("POST", "/auth/otp/verify", bytes.NewBuffer(verifyBody))
	req.Header.Set("Content-Type", "application/json")
	resp, _ := app.Test(req)
	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("expected 401 for wrong code, got %d", resp.StatusCode)
	}

	// 3. Verify with valid dev code (First login: creates new user & tenant)
	verifyBody, _ = json.Marshal(VerifyOTPRequest{
		Phone: testPhone,
		Code:  "000000",
		Email: "testuser@example.com",
	})
	req = httptest.NewRequest("POST", "/auth/otp/verify", bytes.NewBuffer(verifyBody))
	req.Header.Set("Content-Type", "application/json")
	resp, _ = app.Test(req)
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("expected 200 for valid verify, got %d, body: %s", resp.StatusCode, string(body))
	}

	var authResp AuthResponse
	json.NewDecoder(resp.Body).Decode(&authResp)
	if !authResp.IsNewUser {
		t.Error("expected IsNewUser to be true on first login")
	}
	if authResp.Token == "" {
		t.Error("expected JWT token to be generated")
	}
	if authResp.User.Phone != testPhone {
		t.Errorf("expected user phone %s, got %s", testPhone, authResp.User.Phone)
	}
	if authResp.User.Email != "testuser@example.com" {
		t.Errorf("expected user email testuser@example.com, got %s", authResp.User.Email)
	}

	// 4. Test accessing protected endpoint with the returned JWT token
	req = httptest.NewRequest("GET", "/protected/admin", nil)
	req.Header.Set("Authorization", "Bearer "+authResp.Token)
	resp, _ = app.Test(req)
	if resp.StatusCode != http.StatusOK {
		t.Errorf("expected 200 accessing protected route with valid JWT, got %d", resp.StatusCode)
	}

	// 5. Subsequent login with existing user
	// Advance clock slightly for OTP table
	time.Sleep(10 * time.Millisecond)
	verifyBody2, _ := json.Marshal(VerifyOTPRequest{
		Phone: testPhone,
		Code:  "000000",
	})
	req2 := httptest.NewRequest("POST", "/auth/otp/verify", bytes.NewBuffer(verifyBody2))
	req2.Header.Set("Content-Type", "application/json")
	resp2, _ := app.Test(req2)
	if resp2.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 for existing user verify, got %d", resp2.StatusCode)
	}

	var authResp2 AuthResponse
	json.NewDecoder(resp2.Body).Decode(&authResp2)
	if authResp2.IsNewUser {
		t.Error("expected IsNewUser to be false on subsequent login")
	}
	if authResp2.User.ID != authResp.User.ID {
		t.Errorf("expected same user ID %s, got %s", authResp.User.ID, authResp2.User.ID)
	}
}

func TestResendOTP(t *testing.T) {
	app := setupTestApp(t)
	testPhone := "+263774445566"

	// 1. Resend OTP
	reqBody, _ := json.Marshal(ResendOTPRequest{Phone: testPhone, Channel: "call"})
	req := httptest.NewRequest("POST", "/auth/otp/resend", bytes.NewBuffer(reqBody))
	req.Header.Set("Content-Type", "application/json")
	resp, _ := app.Test(req)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 on initial resend, got %d", resp.StatusCode)
	}

	var result map[string]interface{}
	json.NewDecoder(resp.Body).Decode(&result)
	if result["channel"] != "call" {
		t.Errorf("expected channel call, got %v", result["channel"])
	}

	// 2. Cooldown on immediate resend
	req = httptest.NewRequest("POST", "/auth/otp/resend", bytes.NewBuffer(reqBody))
	req.Header.Set("Content-Type", "application/json")
	resp, _ = app.Test(req)
	if resp.StatusCode != http.StatusTooManyRequests {
		t.Errorf("expected 429 for cooldown on resend, got %d", resp.StatusCode)
	}
}

func TestTwilioWebhookCallback(t *testing.T) {
	app := setupTestApp(t)

	formData := "MessageSid=SM123456789&MessageStatus=delivered&To=%2B263771234567"
	req := httptest.NewRequest("POST", "/auth/twilio/webhook", bytes.NewBufferString(formData))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, _ := app.Test(req)

	if resp.StatusCode != http.StatusOK {
		t.Errorf("expected 200 for webhook callback, got %d", resp.StatusCode)
	}

	body, _ := io.ReadAll(resp.Body)
	if string(body) != "<Response></Response>" {
		t.Errorf("expected TwiML empty response, got %s", string(body))
	}
}

func TestJWTMiddleware_UnauthorizedCases(t *testing.T) {
	app := setupTestApp(t)

	// 1. Missing Authorization header
	req := httptest.NewRequest("GET", "/protected/admin", nil)
	resp, _ := app.Test(req)
	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("expected 401 for missing header, got %d", resp.StatusCode)
	}

	// 2. Invalid header format
	req = httptest.NewRequest("GET", "/protected/admin", nil)
	req.Header.Set("Authorization", "Token invalid")
	resp, _ = app.Test(req)
	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("expected 401 for invalid header format, got %d", resp.StatusCode)
	}

	// 3. Forged / invalid signature JWT
	req = httptest.NewRequest("GET", "/protected/admin", nil)
	req.Header.Set("Authorization", "Bearer eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9.eyJ1c2VyX2lkIjoiMTIzIn0.invalidsignature")
	resp, _ = app.Test(req)
	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("expected 401 for invalid JWT, got %d", resp.StatusCode)
	}
}

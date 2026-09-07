package handlers

import (
	"fmt"
	"log"
	"os"
	"strings"
	"time"

	"api-gateway/db"
	"api-gateway/models"
	"api-gateway/services"

	"github.com/gofiber/fiber/v2"
	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
)

var jwtSecret []byte

func InitJWTSecret() {
	secret := os.Getenv("JWT_SECRET")
	if secret == "" {
		secret = "supersecretjwttokenkey123!"
	}
	jwtSecret = []byte(secret)
}

func InitTwilio() {
	services.InitTwilio()
}

type SendOTPRequest struct {
	Phone   string `json:"phone"`
	Channel string `json:"channel"` // "sms" (default), "call", or "whatsapp"
	Locale  string `json:"locale"`   // optional, e.g. "en", "es", "fr"
}

type ResendOTPRequest struct {
	Phone   string `json:"phone"`
	Channel string `json:"channel"` // "sms", "call", or "whatsapp"
	Locale  string `json:"locale"`
}

type VerifyOTPRequest struct {
	Phone      string `json:"phone"`
	Code       string `json:"code"`
	Email      string `json:"email"` // optional email to attach to the account
	DeviceHash string `json:"device_hash,omitempty"`
	DeviceName string `json:"device_name,omitempty"`
	PublicKey  string `json:"public_key,omitempty"`
}

type AuthResponse struct {
	Token        string      `json:"token"`
	User         models.User `json:"user"`
	IsNewUser    bool        `json:"is_new_user"`
	TwilioStatus string      `json:"twilio_status,omitempty"`
	DevCode      string      `json:"dev_code,omitempty"` // only present when Twilio is in dev mode
}

// SendOTP triggers a Twilio Verify OTP to the given phone number with rate-limit checks
func SendOTP(c *fiber.Ctx) error {
	var req SendOTPRequest
	if err := c.BodyParser(&req); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "Invalid request body"})
	}

	phone, err := services.NormalizePhone(req.Phone)
	if err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": err.Error()})
	}

	channel := strings.ToLower(strings.TrimSpace(req.Channel))
	if channel == "" {
		channel = "sms"
	}
	if channel != "sms" && channel != "call" && channel != "whatsapp" {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "channel must be 'sms', 'call', or 'whatsapp'"})
	}

	// Rate limiting: prevent spamming by enforcing a 30-second cooldown between OTP requests for the same number
	var recentVerification models.OTPVerification
	cooldownThreshold := time.Now().Add(-30 * time.Second)
	if err := db.DB.Where("phone = ? AND created_at > ?", phone, cooldownThreshold).Order("created_at desc").First(&recentVerification).Error; err == nil {
		return c.Status(fiber.StatusTooManyRequests).JSON(fiber.Map{
			"error": "Please wait at least 30 seconds before requesting another verification code",
		})
	}

	status, devCode, err := services.SendVerificationCode(phone, channel, req.Locale)
	if err != nil {
		log.Printf("[AUTH] Failed to send OTP to %s via %s: %v", phone, channel, err)
		return c.Status(fiber.StatusBadGateway).JSON(fiber.Map{"error": fmt.Sprintf("Failed to send verification code: %v", err)})
	}

	// Track the verification attempt locally
	verification := models.OTPVerification{
		ID:        uuid.New(),
		Phone:     phone,
		Channel:   channel,
		Status:    status,
		Attempts:  0,
		ExpiresAt: time.Now().Add(10 * time.Minute),
	}
	if err := db.DB.Create(&verification).Error; err != nil {
		log.Printf("[AUTH] Warning: Failed to record OTP verification in DB: %v", err)
	}

	resp := fiber.Map{
		"message": fmt.Sprintf("Verification code dispatched via %s", channel),
		"status":  status,
		"channel": channel,
		"phone":   phone,
	}
	if devCode != "" {
		resp["dev_code"] = devCode
	}

	return c.JSON(resp)
}

// ResendOTP triggers a new verification code or switches channel if previous attempt failed/expired
func ResendOTP(c *fiber.Ctx) error {
	var req ResendOTPRequest
	if err := c.BodyParser(&req); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "Invalid request body"})
	}

	phone, err := services.NormalizePhone(req.Phone)
	if err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": err.Error()})
	}

	channel := strings.ToLower(strings.TrimSpace(req.Channel))
	if channel == "" {
		channel = "sms"
	}
	if channel != "sms" && channel != "call" && channel != "whatsapp" {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "channel must be 'sms', 'call', or 'whatsapp'"})
	}

	// Rate limiting: 30-second cooldown
	var recentVerification models.OTPVerification
	cooldownThreshold := time.Now().Add(-30 * time.Second)
	if err := db.DB.Where("phone = ? AND created_at > ?", phone, cooldownThreshold).Order("created_at desc").First(&recentVerification).Error; err == nil {
		return c.Status(fiber.StatusTooManyRequests).JSON(fiber.Map{
			"error": "Please wait at least 30 seconds before resending a verification code",
		})
	}

	status, devCode, err := services.SendVerificationCode(phone, channel, req.Locale)
	if err != nil {
		log.Printf("[AUTH] Failed to resend OTP to %s via %s: %v", phone, channel, err)
		return c.Status(fiber.StatusBadGateway).JSON(fiber.Map{"error": fmt.Sprintf("Failed to resend verification code: %v", err)})
	}

	verification := models.OTPVerification{
		ID:        uuid.New(),
		Phone:     phone,
		Channel:   channel,
		Status:    status,
		Attempts:  0,
		ExpiresAt: time.Now().Add(10 * time.Minute),
	}
	db.DB.Create(&verification)

	resp := fiber.Map{
		"message": fmt.Sprintf("Verification code resent via %s", channel),
		"status":  status,
		"channel": channel,
		"phone":   phone,
	}
	if devCode != "" {
		resp["dev_code"] = devCode
	}

	return c.JSON(resp)
}

// VerifyOTP checks the code with Twilio Verify, creates the user/tenant on first login, and returns a signed JWT
func VerifyOTP(c *fiber.Ctx) error {
	var req VerifyOTPRequest
	if err := c.BodyParser(&req); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "Invalid request body"})
	}

	phone, err := services.NormalizePhone(req.Phone)
	if err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": err.Error()})
	}
	if strings.TrimSpace(req.Code) == "" {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "Verification code is required"})
	}

	approved, twilioStatus, err := services.CheckVerificationCode(phone, req.Code)
	if err != nil {
		log.Printf("[AUTH] Twilio Verify check error for %s: %v", phone, err)
		return c.Status(fiber.StatusBadGateway).JSON(fiber.Map{"error": fmt.Sprintf("Failed to verify code: %v", err)})
	}

	if !approved {
		// Increment attempts counter on pending verification
		db.DB.Model(&models.OTPVerification{}).
			Where("phone = ? AND status = ?", phone, "pending").
			Update("attempts", db.DB.Raw("attempts + 1"))

		return c.Status(fiber.StatusUnauthorized).JSON(fiber.Map{
			"error":  "Invalid or expired verification code",
			"status": twilioStatus,
		})
	}

	// Find or create user
	isNewUser := false
	var user models.User
	if err := db.DB.Where("phone = ?", phone).First(&user).Error; err != nil {
		// First login: create a tenant + admin user for this phone number
		isNewUser = true

		tenant := models.Tenant{
			ID:   uuid.New(),
			Name: phone,
		}
		if err := db.DB.Create(&tenant).Error; err != nil {
			return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "Failed to create tenant"})
		}

		user = models.User{
			ID:           uuid.New(),
			Phone:        phone,
			Email:        strings.TrimSpace(req.Email),
			PasswordHash: "-",
			Role:         "admin",
			TenantID:     tenant.ID,
		}
		if err := db.DB.Create(&user).Error; err != nil {
			return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "Failed to create user"})
		}

		defaultProject := models.Project{
			ID:       uuid.New(),
			Name:     "Default Project",
			TenantID: tenant.ID,
		}
		db.DB.Create(&defaultProject)
	} else if strings.TrimSpace(req.Email) != "" && user.Email == "" {
		// Attach email if not previously set
		user.Email = strings.TrimSpace(req.Email)
		db.DB.Model(&user).Update("email", user.Email)
	}

	// Mark local verification record(s) approved
	db.DB.Model(&models.OTPVerification{}).
		Where("phone = ? AND status = ?", phone, "pending").
		Updates(map[string]interface{}{"status": "approved"})

	// Device trust: if device_hash provided, register or update device
	if req.DeviceHash != "" {
		deviceName := req.DeviceName
		if deviceName == "" {
			deviceName = "Mobile Device"
		}
		_, _, _ = services.RegisterOrVerifyDevice(user.ID, user.TenantID, req.DeviceHash, deviceName, req.PublicKey, c.IP(), c.Get("User-Agent"))
	} else {
		_ = services.LogSecurityAudit(user.TenantID, user.ID, "login_otp_success", "User authenticated via OTP", c.IP(), c.Get("User-Agent"), map[string]interface{}{
			"is_new_user": isNewUser,
		})
	}

	token, err := GenerateJWT(user.ID, user.TenantID, user.Role)
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "Failed to generate token"})
	}

	return c.JSON(AuthResponse{
		Token:        token,
		User:         user,
		IsNewUser:    isNewUser,
		TwilioStatus: twilioStatus,
	})
}

// TwilioWebhookCallback receives and verifies status callbacks from Twilio
func TwilioWebhookCallback(c *fiber.Ctx) error {
	signature := c.Get("X-Twilio-Signature")
	fullURL := c.BaseURL() + c.OriginalURL()

	// Extract form parameters
	params := make(map[string]string)
	c.Request().PostArgs().VisitAll(func(key, val []byte) {
		params[string(key)] = string(val)
	})

	if services.IsTwilioVerifyConfigured() && signature != "" {
		if !services.ValidateTwilioWebhookSignature(fullURL, params, signature) {
			log.Printf("[TWILIO-WEBHOOK] Invalid Twilio signature from IP %s", c.IP())
			return c.Status(fiber.StatusForbidden).JSON(fiber.Map{"error": "Invalid Twilio signature"})
		}
	}

	messageSid := params["MessageSid"]
	messageStatus := params["MessageStatus"]
	to := params["To"]

	log.Printf("[TWILIO-WEBHOOK] Status Callback received: To=%s | SID=%s | Status=%s", to, messageSid, messageStatus)

	return c.Status(fiber.StatusOK).SendString("<Response></Response>")
}

func GenerateJWT(userID uuid.UUID, tenantID uuid.UUID, role string) (string, error) {
	claims := jwt.MapClaims{
		"user_id":   userID.String(),
		"tenant_id": tenantID.String(),
		"role":      role,
		"exp":       time.Now().AddDate(1, 0, 0).Unix(),
	}

	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	return token.SignedString(jwtSecret)
}

// JWTMiddleware validates the bearer token and injects claims into context locals
func JWTMiddleware(c *fiber.Ctx) error {
	authHeader := c.Get("Authorization")
	if authHeader == "" {
		return c.Status(fiber.StatusUnauthorized).JSON(fiber.Map{"error": "Missing authorization token"})
	}

	parts := strings.Split(authHeader, " ")
	if len(parts) != 2 || strings.ToLower(parts[0]) != "bearer" {
		return c.Status(fiber.StatusUnauthorized).JSON(fiber.Map{"error": "Invalid authorization header format"})
	}

	tokenStr := parts[1]
	token, err := jwt.Parse(tokenStr, func(token *jwt.Token) (interface{}, error) {
		if _, ok := token.Method.(*jwt.SigningMethodHMAC); !ok {
			return nil, jwt.ErrSignatureInvalid
		}
		return jwtSecret, nil
	})

	if err != nil || !token.Valid {
		return c.Status(fiber.StatusUnauthorized).JSON(fiber.Map{"error": "Invalid or expired token"})
	}

	claims, ok := token.Claims.(jwt.MapClaims)
	if !ok {
		return c.Status(fiber.StatusUnauthorized).JSON(fiber.Map{"error": "Failed to parse claims"})
	}

	c.Locals("user_id", claims["user_id"])
	c.Locals("tenant_id", claims["tenant_id"])
	c.Locals("role", claims["role"])

	return c.Next()
}

// OptionalJWTMiddleware checks for a Bearer token if provided, injecting claims, without blocking if absent
func OptionalJWTMiddleware(c *fiber.Ctx) error {
	authHeader := c.Get("Authorization")
	if authHeader == "" {
		return c.Next()
	}

	parts := strings.Split(authHeader, " ")
	if len(parts) != 2 || strings.ToLower(parts[0]) != "bearer" {
		return c.Next()
	}

	tokenStr := parts[1]
	token, err := jwt.Parse(tokenStr, func(token *jwt.Token) (interface{}, error) {
		if _, ok := token.Method.(*jwt.SigningMethodHMAC); !ok {
			return nil, jwt.ErrSignatureInvalid
		}
		return jwtSecret, nil
	})

	if err == nil && token.Valid {
		if claims, ok := token.Claims.(jwt.MapClaims); ok {
			c.Locals("user_id", claims["user_id"])
			c.Locals("tenant_id", claims["tenant_id"])
			c.Locals("role", claims["role"])
		}
	}

	return c.Next()
}

// RequireAdminOrReviewer restricts endpoint access to authenticated users with admin or reviewer privileges
func RequireAdminOrReviewer(c *fiber.Ctx) error {
	role, _ := c.Locals("role").(string)
	if role != "admin" && role != "reviewer" {
		return c.Status(fiber.StatusForbidden).JSON(fiber.Map{
			"error": "Forbidden: Admin or Reviewer access required to check and review documents",
		})
	}
	return c.Next()
}

// RequireAdmin restricts endpoint access strictly to admin users
func RequireAdmin(c *fiber.Ctx) error {
	role, _ := c.Locals("role").(string)
	if role != "admin" {
		return c.Status(fiber.StatusForbidden).JSON(fiber.Map{
			"error": "Forbidden: Admin access required",
		})
	}
	return c.Next()
}

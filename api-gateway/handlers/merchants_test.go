package handlers

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"api-gateway/db"
	"api-gateway/models"

	"github.com/gofiber/fiber/v2"
	"github.com/google/uuid"
)

func TestRequireRoles_PermissionMatrix(t *testing.T) {
	app := setupTestApp(t)

	userID := uuid.New()
	tenantID := uuid.New()

	userToken, _ := GenerateJWT(userID, tenantID, models.RoleUser)
	merchantToken, _ := GenerateJWT(userID, tenantID, models.RoleMerchant)
	moderatorToken, _ := GenerateJWT(userID, tenantID, models.RoleModerator)
	adminToken, _ := GenerateJWT(userID, tenantID, models.RoleAdmin)

	// Protected test routes
	app.Get("/test/merchant-only", JWTMiddleware, RequireRoles(models.RoleMerchant), func(c *fiber.Ctx) error {
		return c.JSON(fiber.Map{"status": "ok_merchant"})
	})
	app.Get("/test/moderator-only", JWTMiddleware, RequireRoles(models.RoleModerator), func(c *fiber.Ctx) error {
		return c.JSON(fiber.Map{"status": "ok_moderator"})
	})

	// 1. Standard user accessing merchant-only -> 403 Forbidden
	req := httptest.NewRequest("GET", "/test/merchant-only", nil)
	req.Header.Set("Authorization", "Bearer "+userToken)
	resp, _ := app.Test(req)
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("expected 403 for user accessing merchant endpoint, got %d", resp.StatusCode)
	}

	// 2. Merchant accessing merchant-only -> 200 OK
	req = httptest.NewRequest("GET", "/test/merchant-only", nil)
	req.Header.Set("Authorization", "Bearer "+merchantToken)
	resp, _ = app.Test(req)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 for merchant accessing merchant endpoint, got %d", resp.StatusCode)
	}

	// 3. Moderator accessing moderator-only -> 200 OK
	req = httptest.NewRequest("GET", "/test/moderator-only", nil)
	req.Header.Set("Authorization", "Bearer "+moderatorToken)
	resp, _ = app.Test(req)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 for moderator accessing moderator endpoint, got %d", resp.StatusCode)
	}

	// 4. Admin accessing merchant-only -> 200 OK (Superadmin override)
	req = httptest.NewRequest("GET", "/test/merchant-only", nil)
	req.Header.Set("Authorization", "Bearer "+adminToken)
	resp, _ = app.Test(req)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 for admin accessing merchant endpoint, got %d", resp.StatusCode)
	}
}

func TestRegisterMerchant_Flow(t *testing.T) {
	app := setupTestApp(t)

	userID := uuid.New()
	tenantID := uuid.New()

	testUser := models.User{
		ID:       userID,
		TenantID: tenantID,
		Phone:    "+263771122334",
		Email:    "merchant_test_unique@bytfin.com",
		Role:     models.RoleUser,
	}
	db.DB.Create(&testUser)

	userToken, _ := GenerateJWT(userID, tenantID, models.RoleUser)

	app.Post("/api/merchants/register", JWTMiddleware, RegisterMerchant)
	app.Get("/api/merchants/profile", JWTMiddleware, RequireRoles(models.RoleMerchant), GetMerchantProfile)

	// 1. Register as Merchant
	body, _ := json.Marshal(RegisterMerchantRequest{
		BusinessName:     "Fresh Harvest Supermarket",
		TaxNumber:        "ZIMRA-889977",
		Category:         "Grocery",
		SettlementBank:   "CBZ Bank",
		SettlementAccNum: "01122334455",
	})

	req := httptest.NewRequest("POST", "/api/merchants/register", bytes.NewBuffer(body))
	req.Header.Set("Authorization", "Bearer "+userToken)
	req.Header.Set("Content-Type", "application/json")
	resp, err := app.Test(req)
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("expected 201 created for merchant registration, got %d", resp.StatusCode)
	}

	var res struct {
		Message string                 `json:"message"`
		Profile models.MerchantProfile `json:"profile"`
	}
	json.NewDecoder(resp.Body).Decode(&res)
	if res.Profile.BusinessName != "Fresh Harvest Supermarket" {
		t.Errorf("expected business name 'Fresh Harvest Supermarket', got %s", res.Profile.BusinessName)
	}
	if res.Profile.StaticQRURI != "zwc:ZIMRA-889977?name=Fresh Harvest Supermarket" {
		t.Errorf("unexpected static QR URI: %s", res.Profile.StaticQRURI)
	}

	// Verify user role was upgraded to 'merchant'
	var updatedUser models.User
	db.DB.Where("id = ?", userID).First(&updatedUser)
	if updatedUser.Role != models.RoleMerchant {
		t.Errorf("expected user role 'merchant', got %s", updatedUser.Role)
	}

	// 2. Fetch merchant profile with new merchant token
	merchantToken, _ := GenerateJWT(userID, tenantID, models.RoleMerchant)
	req = httptest.NewRequest("GET", "/api/merchants/profile", nil)
	req.Header.Set("Authorization", "Bearer "+merchantToken)
	resp, _ = app.Test(req)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 fetching merchant profile, got %d", resp.StatusCode)
	}
}

func TestAssignUserRole_AdminFlow(t *testing.T) {
	app := setupTestApp(t)

	adminID := uuid.New()
	targetUserID := uuid.New()
	tenantID := uuid.New()

	adminUser := models.User{
		ID:       adminID,
		TenantID: tenantID,
		Phone:    "+263770000001",
		Email:    "admin_test_1@bytfin.com",
		Role:     models.RoleAdmin,
	}
	db.DB.Create(&adminUser)

	targetUserRec := models.User{
		ID:       targetUserID,
		TenantID: tenantID,
		Phone:    "+263770000002",
		Email:    "target_user_2@bytfin.com",
		Role:     models.RoleUser,
	}
	db.DB.Create(&targetUserRec)

	adminToken, _ := GenerateJWT(adminID, tenantID, models.RoleAdmin)
	userToken, _ := GenerateJWT(targetUserID, tenantID, models.RoleUser)

	app.Post("/api/admin/roles/assign", JWTMiddleware, RequireAdmin, AssignUserRole)

	// 1. Non-admin trying to assign role -> 403
	body, _ := json.Marshal(AssignRoleRequest{
		UserID: targetUserID,
		Role:   models.RoleModerator,
	})
	req := httptest.NewRequest("POST", "/api/admin/roles/assign", bytes.NewBuffer(body))
	req.Header.Set("Authorization", "Bearer "+userToken)
	req.Header.Set("Content-Type", "application/json")
	resp, _ := app.Test(req)
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("expected 403 for non-admin role assignment, got %d", resp.StatusCode)
	}

	// 2. Admin promoting user to moderator -> 200 OK
	req = httptest.NewRequest("POST", "/api/admin/roles/assign", bytes.NewBuffer(body))
	req.Header.Set("Authorization", "Bearer "+adminToken)
	req.Header.Set("Content-Type", "application/json")
	resp, _ = app.Test(req)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 for admin assigning moderator role, got %d", resp.StatusCode)
	}

	var targetUser models.User
	db.DB.Where("id = ?", targetUserID).First(&targetUser)
	if targetUser.Role != models.RoleModerator {
		t.Errorf("expected role 'moderator', got %s", targetUser.Role)
	}
}

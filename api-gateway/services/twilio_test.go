package services

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha1"
	"encoding/base64"
	"io"
	"net/http"
	"os"
	"strings"
	"testing"
)

type mockRoundTripper func(req *http.Request) (*http.Response, error)

func (m mockRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	return m(req)
}

func TestNormalizePhone(t *testing.T) {
	validCases := []struct {
		input    string
		expected string
	}{
		{"+263771234567", "+263771234567"},
		{" +15551234567 ", "+15551234567"},
		{"+447911123456", "+447911123456"},
		{"+12345678", "+12345678"},
		{"+123456789012345", "+123456789012345"},
	}

	for _, tc := range validCases {
		res, err := NormalizePhone(tc.input)
		if err != nil {
			t.Errorf("expected valid for %q, got error: %v", tc.input, err)
		}
		if res != tc.expected {
			t.Errorf("expected %q, got %q", tc.expected, res)
		}
	}

	invalidCases := []string{
		"",
		"   ",
		"15551234567",       // missing +
		"+1555abc4567",      // letters
		"+123456",           // too short (<8)
		"+1234567890123456", // too long (>15)
		"+",
	}

	for _, input := range invalidCases {
		_, err := NormalizePhone(input)
		if err == nil {
			t.Errorf("expected error for invalid phone %q, got nil", input)
		}
	}
}

func TestTwilioVerify_DevMode(t *testing.T) {
	// Ensure env vars are empty
	os.Setenv("TWILIO_ACCOUNT_SID", "")
	os.Setenv("TWILIO_AUTH_TOKEN", "")
	os.Setenv("TWILIO_VERIFY_SERVICE_SID", "")

	if IsTwilioVerifyConfigured() {
		t.Fatal("expected IsTwilioVerifyConfigured to be false in dev mode")
	}

	// 1. Test Send in dev mode
	status, devCode, err := SendVerificationCode("+263771234567", "sms", "en")
	if err != nil {
		t.Fatalf("unexpected error in dev send: %v", err)
	}
	if status != "pending" || devCode != "000000" {
		t.Errorf("expected status 'pending' and devCode '000000', got %s, %s", status, devCode)
	}

	// Channel validation
	_, _, err = SendVerificationCode("+263771234567", "invalid_channel", "")
	if err == nil {
		t.Error("expected error for invalid channel, got nil")
	}

	// 2. Test Check in dev mode
	approved, st, err := CheckVerificationCode("+263771234567", "000000")
	if err != nil {
		t.Fatalf("unexpected error checking dev code: %v", err)
	}
	if !approved || st != "approved" {
		t.Errorf("expected dev code 000000 to be approved, got approved=%t, status=%s", approved, st)
	}

	// Wrong code in dev mode
	approved, st, err = CheckVerificationCode("+263771234567", "999999")
	if err != nil {
		t.Fatalf("unexpected error checking wrong code: %v", err)
	}
	if approved {
		t.Error("expected wrong code to fail")
	}
}

func TestTwilioVerify_WithMockHTTP(t *testing.T) {
	os.Setenv("TWILIO_ACCOUNT_SID", "ACtest1234567890")
	os.Setenv("TWILIO_AUTH_TOKEN", "test_auth_token")
	os.Setenv("TWILIO_VERIFY_SERVICE_SID", "VAtest1234567890")
	defer func() {
		os.Unsetenv("TWILIO_ACCOUNT_SID")
		os.Unsetenv("TWILIO_AUTH_TOKEN")
		os.Unsetenv("TWILIO_VERIFY_SERVICE_SID")
		SetTwilioHTTPClient(nil)
	}()

	if !IsTwilioVerifyConfigured() {
		t.Fatal("expected IsTwilioVerifyConfigured to be true")
	}

	// Mock Send Success
	mockClient := &http.Client{
		Transport: mockRoundTripper(func(req *http.Request) (*http.Response, error) {
			respJSON := `{"sid": "VE123", "service_sid": "VAtest", "to": "+263771234567", "channel": "sms", "status": "pending", "valid": false}`
			return &http.Response{
				StatusCode: 200,
				Body:       io.NopCloser(bytes.NewBufferString(respJSON)),
				Header:     make(http.Header),
			}, nil
		}),
	}
	SetTwilioHTTPClient(mockClient)

	status, devCode, err := SendVerificationCode("+263771234567", "sms", "")
	if err != nil {
		t.Fatalf("send verification failed: %v", err)
	}
	if status != "pending" || devCode != "" {
		t.Errorf("expected pending, empty devCode, got %s, %s", status, devCode)
	}

	// Mock Check Success
	mockClientCheck := &http.Client{
		Transport: mockRoundTripper(func(req *http.Request) (*http.Response, error) {
			respJSON := `{"sid": "VE123", "service_sid": "VAtest", "to": "+263771234567", "channel": "sms", "status": "approved", "valid": true}`
			return &http.Response{
				StatusCode: 200,
				Body:       io.NopCloser(bytes.NewBufferString(respJSON)),
				Header:     make(http.Header),
			}, nil
		}),
	}
	SetTwilioHTTPClient(mockClientCheck)

	approved, st, err := CheckVerificationCode("+263771234567", "123456")
	if err != nil {
		t.Fatalf("check verification failed: %v", err)
	}
	if !approved || st != "approved" {
		t.Errorf("expected approved=true, status=approved, got %t, %s", approved, st)
	}

	// Mock Twilio Error (e.g. 400 Invalid Parameter)
	mockClientError := &http.Client{
		Transport: mockRoundTripper(func(req *http.Request) (*http.Response, error) {
			errJSON := `{"code": 60200, "message": "Invalid parameter ` + "`To`" + `", "more_info": "https://www.twilio.com/docs/errors/60200", "status": 400}`
			return &http.Response{
				StatusCode: 400,
				Body:       io.NopCloser(bytes.NewBufferString(errJSON)),
				Header:     make(http.Header),
			}, nil
		}),
	}
	SetTwilioHTTPClient(mockClientError)

	_, _, err = SendVerificationCode("+263771234567", "sms", "")
	if err == nil {
		t.Error("expected error from Twilio 400 response, got nil")
	}
}

func TestTwilioMessaging_SendSMS(t *testing.T) {
	// 1. Test Dev Mode (unconfigured)
	os.Unsetenv("TWILIO_ACCOUNT_SID")
	os.Unsetenv("TWILIO_AUTH_TOKEN")
	os.Unsetenv("TWILIO_PHONE_NUMBER")

	mockSID, err := SendSMS("+263771234567", "Test alert message")
	if err != nil {
		t.Fatalf("unexpected dev SendSMS error: %v", err)
	}
	if mockSID == "" {
		t.Error("expected non-empty mock SID in dev mode")
	}

	// 2. Test with mock HTTP client
	os.Setenv("TWILIO_ACCOUNT_SID", "ACtest1234567890")
	os.Setenv("TWILIO_AUTH_TOKEN", "test_auth_token")
	os.Setenv("TWILIO_PHONE_NUMBER", "+15550001111")
	defer func() {
		os.Unsetenv("TWILIO_ACCOUNT_SID")
		os.Unsetenv("TWILIO_AUTH_TOKEN")
		os.Unsetenv("TWILIO_PHONE_NUMBER")
		SetTwilioHTTPClient(nil)
	}()

	mockClientSMS := &http.Client{
		Transport: mockRoundTripper(func(req *http.Request) (*http.Response, error) {
			respJSON := `{"sid": "SM999888777", "status": "sent", "to": "+263771234567", "from": "+15550001111", "body": "Test message"}`
			return &http.Response{
				StatusCode: 201,
				Body:       io.NopCloser(bytes.NewBufferString(respJSON)),
				Header:     make(http.Header),
			}, nil
		}),
	}
	SetTwilioHTTPClient(mockClientSMS)

	sid, err := SendSMS("+263771234567", "Test message")
	if err != nil {
		t.Fatalf("SendSMS failed: %v", err)
	}
	if sid != "SM999888777" {
		t.Errorf("expected SID SM999888777, got %s", sid)
	}
}

func TestValidateTwilioWebhookSignature(t *testing.T) {
	os.Setenv("TWILIO_AUTH_TOKEN", "12345")
	defer os.Unsetenv("TWILIO_AUTH_TOKEN")

	expectedURL := "https://mycompany.com/auth/twilio/webhook"
	params := map[string]string{
		"CallSid": "CA1234567890ABCDE",
		"Caller":  "+12349013030",
		"Digits":  "1234",
		"From":    "+12349013030",
		"To":      "+18005551212",
	}

	// Valid signature computed according to Twilio standard
	// URL + Caller+12349013030 + CallSidCA1234567890ABCDE + Digits1234 + From+12349013030 + To+18005551212
	// with HMAC-SHA1 key "12345"
	// Let's compute expected signature with function:
	// Verify valid signature returns true
	// We can test by computing a valid signature or checking mismatch
	validSignature := generateExpectedSignature(expectedURL, params, "12345")
	if !ValidateTwilioWebhookSignature(expectedURL, params, validSignature) {
		t.Errorf("expected valid signature %s to return true", validSignature)
	}

	// Test invalid signature
	if ValidateTwilioWebhookSignature(expectedURL, params, "invalid_sig_here") {
		t.Error("expected invalid signature to return false")
	}

	// Test empty signature
	if ValidateTwilioWebhookSignature(expectedURL, params, "") {
		t.Error("expected empty signature to return false")
	}

	// Test with known computed signature
	params2 := map[string]string{
		"MessageStatus": "delivered",
		"To":            "+263771234567",
	}
	// Compute expected signature
	sig := generateExpectedSignature(expectedURL, params2, "12345")
	if !ValidateTwilioWebhookSignature(expectedURL, params2, sig) {
		t.Error("expected valid computed signature to return true")
	}
}

func generateExpectedSignature(urlStr string, params map[string]string, token string) string {
	keys := make([]string, 0, len(params))
	for k := range params {
		keys = append(keys, k)
	}
	for i := 0; i < len(keys); i++ {
		for j := i + 1; j < len(keys); j++ {
			if keys[i] > keys[j] {
				keys[i], keys[j] = keys[j], keys[i]
			}
		}
	}
	var b strings.Builder
	b.WriteString(urlStr)
	for _, k := range keys {
		b.WriteString(k)
		b.WriteString(params[k])
	}
	mac := hmac.New(sha1.New, []byte(token))
	mac.Write([]byte(b.String()))
	return base64.StdEncoding.EncodeToString(mac.Sum(nil))
}

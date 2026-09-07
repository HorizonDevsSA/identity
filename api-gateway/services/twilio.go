package services

import (
	"crypto/hmac"
	"crypto/sha1"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"sort"
	"strings"
	"time"
)

var (
	twilioHTTPClient *http.Client = &http.Client{Timeout: 15 * time.Second}
)

// SetTwilioHTTPClient allows overriding the HTTP client for testing/mocking
func SetTwilioHTTPClient(client *http.Client) {
	if client != nil {
		twilioHTTPClient = client
	} else {
		twilioHTTPClient = &http.Client{Timeout: 15 * time.Second}
	}
}

// InitTwilio checks and logs Twilio configuration state on startup
func InitTwilio() {
	if !IsTwilioVerifyConfigured() {
		log.Println("[TWILIO] WARNING: TWILIO_ACCOUNT_SID / TWILIO_AUTH_TOKEN / TWILIO_VERIFY_SERVICE_SID not fully set.")
		log.Println("[TWILIO] Running in DEV MODE: OTP verification code '000000' will be accepted.")
	} else {
		log.Println("[TWILIO] Twilio Verify Service successfully configured.")
	}

	if !IsTwilioMessagingConfigured() {
		log.Println("[TWILIO] Outbound SMS notifications running in DEV MODE (messages will be logged to console).")
	} else {
		log.Println("[TWILIO] Twilio Programmable Messaging configured for outbound SMS notifications.")
	}
}

// IsTwilioVerifyConfigured returns true if all required Twilio Verify credentials exist
func IsTwilioVerifyConfigured() bool {
	return strings.TrimSpace(os.Getenv("TWILIO_ACCOUNT_SID")) != "" &&
		strings.TrimSpace(os.Getenv("TWILIO_AUTH_TOKEN")) != "" &&
		strings.TrimSpace(os.Getenv("TWILIO_VERIFY_SERVICE_SID")) != ""
}

// IsTwilioMessagingConfigured returns true if credentials for sending outbound SMS exist
func IsTwilioMessagingConfigured() bool {
	hasAuth := strings.TrimSpace(os.Getenv("TWILIO_ACCOUNT_SID")) != "" &&
		strings.TrimSpace(os.Getenv("TWILIO_AUTH_TOKEN")) != ""
	hasSender := strings.TrimSpace(os.Getenv("TWILIO_PHONE_NUMBER")) != "" ||
		strings.TrimSpace(os.Getenv("TWILIO_MESSAGING_SERVICE_SID")) != ""
	return hasAuth && hasSender
}

// NormalizePhone validates and standardizes E.164 (+XXXXXXXXXX...) phone formatting
func NormalizePhone(raw string) (string, error) {
	phone := strings.TrimSpace(raw)
	if phone == "" {
		return "", fmt.Errorf("phone is required")
	}
	if !strings.HasPrefix(phone, "+") {
		return "", fmt.Errorf("phone must be in E.164 format with leading +, e.g. +263771234567")
	}
	digits := strings.TrimPrefix(phone, "+")
	for _, r := range digits {
		if r < '0' || r > '9' {
			return "", fmt.Errorf("phone must contain only digits after the leading +")
		}
	}
	if len(digits) < 8 || len(digits) > 15 {
		return "", fmt.Errorf("phone number length is invalid (must be 8-15 digits after +)")
	}
	return phone, nil
}

type twilioGenericResponse struct {
	Sid          string `json:"sid"`
	Status       string `json:"status"`
	To           string `json:"to"`
	Channel      string `json:"channel"`
	Valid        bool   `json:"valid"`
	ErrorCode    *int   `json:"error_code,omitempty"`
	ErrorMessage string `json:"error_message,omitempty"`
	Code         *int   `json:"code,omitempty"`
	Message      string `json:"message,omitempty"`
}

// SendVerificationCode triggers a Twilio Verify OTP code to the given phone number.
// In dev mode, returns "pending" status along with devCode "000000".
func SendVerificationCode(phone, channel, locale string) (status string, devCode string, err error) {
	normPhone, err := NormalizePhone(phone)
	if err != nil {
		return "", "", err
	}

	channel = strings.ToLower(strings.TrimSpace(channel))
	if channel == "" {
		channel = "sms"
	}
	if channel != "sms" && channel != "call" && channel != "whatsapp" {
		return "", "", fmt.Errorf("channel must be 'sms', 'call', or 'whatsapp'")
	}

	if !IsTwilioVerifyConfigured() {
		log.Printf("[TWILIO-DEV] Mock OTP sent to %s via %s (Dev Code: 000000)", normPhone, channel)
		return "pending", "000000", nil
	}

	serviceSID := strings.TrimSpace(os.Getenv("TWILIO_VERIFY_SERVICE_SID"))
	accountSID := strings.TrimSpace(os.Getenv("TWILIO_ACCOUNT_SID"))
	authToken := strings.TrimSpace(os.Getenv("TWILIO_AUTH_TOKEN"))

	endpoint := fmt.Sprintf("https://verify.twilio.com/v2/Services/%s/Verifications", serviceSID)

	data := url.Values{}
	data.Set("To", normPhone)
	data.Set("Channel", channel)
	if locale != "" {
		data.Set("Locale", locale)
	}

	req, err := http.NewRequest(http.MethodPost, endpoint, strings.NewReader(data.Encode()))
	if err != nil {
		return "", "", fmt.Errorf("failed to create Twilio request: %w", err)
	}
	req.SetBasicAuth(accountSID, authToken)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	resp, err := twilioHTTPClient.Do(req)
	if err != nil {
		return "", "", fmt.Errorf("network error calling Twilio Verify: %w", err)
	}
	defer resp.Body.Close()

	respBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", "", fmt.Errorf("failed to read Twilio response body: %w", err)
	}

	var twResp twilioGenericResponse
	if err := json.Unmarshal(respBytes, &twResp); err != nil {
		return "", "", fmt.Errorf("failed to parse Twilio response (status %d): %s", resp.StatusCode, string(respBytes))
	}

	if resp.StatusCode >= 300 {
		errMsg := twResp.Message
		if errMsg == "" {
			errMsg = twResp.ErrorMessage
		}
		if errMsg == "" {
			errMsg = fmt.Sprintf("HTTP %d", resp.StatusCode)
		}
		return "", "", fmt.Errorf("twilio verify error (%d): %s", resp.StatusCode, errMsg)
	}

	return twResp.Status, "", nil
}

// CheckVerificationCode validates a user-provided OTP code against Twilio Verify.
// In dev mode, accepts code "000000".
func CheckVerificationCode(phone, code string) (approved bool, status string, err error) {
	normPhone, err := NormalizePhone(phone)
	if err != nil {
		return false, "", err
	}

	code = strings.TrimSpace(code)
	if code == "" {
		return false, "", fmt.Errorf("verification code cannot be empty")
	}

	if !IsTwilioVerifyConfigured() {
		if code == "000000" {
			log.Printf("[TWILIO-DEV] Dev OTP code 000000 verified for %s", normPhone)
			return true, "approved", nil
		}
		return false, "failed", nil
	}

	serviceSID := strings.TrimSpace(os.Getenv("TWILIO_VERIFY_SERVICE_SID"))
	accountSID := strings.TrimSpace(os.Getenv("TWILIO_ACCOUNT_SID"))
	authToken := strings.TrimSpace(os.Getenv("TWILIO_AUTH_TOKEN"))

	endpoint := fmt.Sprintf("https://verify.twilio.com/v2/Services/%s/VerificationCheck", serviceSID)

	data := url.Values{}
	data.Set("To", normPhone)
	data.Set("Code", code)

	req, err := http.NewRequest(http.MethodPost, endpoint, strings.NewReader(data.Encode()))
	if err != nil {
		return false, "", fmt.Errorf("failed to create Twilio check request: %w", err)
	}
	req.SetBasicAuth(accountSID, authToken)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	resp, err := twilioHTTPClient.Do(req)
	if err != nil {
		return false, "", fmt.Errorf("network error calling Twilio VerificationCheck: %w", err)
	}
	defer resp.Body.Close()

	respBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		return false, "", fmt.Errorf("failed to read Twilio response body: %w", err)
	}

	var twResp twilioGenericResponse
	if err := json.Unmarshal(respBytes, &twResp); err != nil {
		return false, "", fmt.Errorf("failed to parse Twilio verification check response: %s", string(respBytes))
	}

	if resp.StatusCode >= 300 {
		errMsg := twResp.Message
		if errMsg == "" {
			errMsg = twResp.ErrorMessage
		}
		if errMsg == "" {
			errMsg = fmt.Sprintf("HTTP %d", resp.StatusCode)
		}
		return false, twResp.Status, fmt.Errorf("twilio verification check error (%d): %s", resp.StatusCode, errMsg)
	}

	return twResp.Status == "approved", twResp.Status, nil
}

// SendSMS sends an outbound SMS notification using Twilio Programmable Messaging.
// If Twilio messaging is not configured, logs the message and returns a mock SID.
func SendSMS(toPhone, messageBody string) (messageSID string, err error) {
	normPhone, err := NormalizePhone(toPhone)
	if err != nil {
		return "", err
	}

	messageBody = strings.TrimSpace(messageBody)
	if messageBody == "" {
		return "", fmt.Errorf("SMS message body cannot be empty")
	}

	if !IsTwilioMessagingConfigured() {
		mockSID := fmt.Sprintf("SM_mock_%d", time.Now().UnixNano())
		log.Printf("[TWILIO-DEV-SMS] To: %s | MockSID: %s | Message: %q", normPhone, mockSID, messageBody)
		return mockSID, nil
	}

	accountSID := strings.TrimSpace(os.Getenv("TWILIO_ACCOUNT_SID"))
	authToken := strings.TrimSpace(os.Getenv("TWILIO_AUTH_TOKEN"))
	fromPhone := strings.TrimSpace(os.Getenv("TWILIO_PHONE_NUMBER"))
	messagingServiceSID := strings.TrimSpace(os.Getenv("TWILIO_MESSAGING_SERVICE_SID"))

	endpoint := fmt.Sprintf("https://api.twilio.com/2010-04-01/Accounts/%s/Messages.json", accountSID)

	data := url.Values{}
	data.Set("To", normPhone)
	data.Set("Body", messageBody)

	if messagingServiceSID != "" {
		data.Set("MessagingServiceSid", messagingServiceSID)
	} else if fromPhone != "" {
		data.Set("From", fromPhone)
	}

	req, err := http.NewRequest(http.MethodPost, endpoint, strings.NewReader(data.Encode()))
	if err != nil {
		return "", fmt.Errorf("failed to create Twilio SMS request: %w", err)
	}
	req.SetBasicAuth(accountSID, authToken)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	resp, err := twilioHTTPClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("network error sending SMS via Twilio: %w", err)
	}
	defer resp.Body.Close()

	respBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("failed to read Twilio SMS response: %w", err)
	}

	var twResp twilioGenericResponse
	if err := json.Unmarshal(respBytes, &twResp); err != nil {
		return "", fmt.Errorf("failed to parse Twilio SMS response: %s", string(respBytes))
	}

	if resp.StatusCode >= 300 {
		errMsg := twResp.Message
		if errMsg == "" {
			errMsg = twResp.ErrorMessage
		}
		if errMsg == "" {
			errMsg = fmt.Sprintf("HTTP %d", resp.StatusCode)
		}
		return "", fmt.Errorf("twilio SMS error (%d): %s", resp.StatusCode, errMsg)
	}

	log.Printf("[TWILIO-SMS] Outbound SMS dispatched successfully to %s (SID: %s, Status: %s)", normPhone, twResp.Sid, twResp.Status)
	return twResp.Sid, nil
}

// ValidateTwilioWebhookSignature validates the X-Twilio-Signature header on incoming webhooks
func ValidateTwilioWebhookSignature(expectedURL string, params map[string]string, signature string) bool {
	authToken := strings.TrimSpace(os.Getenv("TWILIO_AUTH_TOKEN"))
	if authToken == "" {
		// When no auth token is configured in dev mode, accept webhook
		return true
	}
	if signature == "" {
		return false
	}

	// 1. Sort POST parameters alphabetically by key
	keys := make([]string, 0, len(params))
	for k := range params {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	// 2. Concatenate URL + key1 + val1 + key2 + val2 ...
	var builder strings.Builder
	builder.WriteString(expectedURL)
	for _, k := range keys {
		builder.WriteString(k)
		builder.WriteString(params[k])
	}

	// 3. Compute HMAC-SHA1
	mac := hmac.New(sha1.New, []byte(authToken))
	mac.Write([]byte(builder.String()))
	expectedSignature := base64.StdEncoding.EncodeToString(mac.Sum(nil))

	return hmac.Equal([]byte(expectedSignature), []byte(signature))
}

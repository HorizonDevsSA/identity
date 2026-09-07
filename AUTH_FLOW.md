# Identity API Gateway — Authentication & Authorization Flow

This document describes the full authentication, phone verification, and authorization flow for the **ZWC Identity API Gateway** (`identity/api-gateway`).

---

## Overview

The gateway uses a **Phone-First Passwordless Authentication (Twilio Verify OTP)** paired with a **JWT-based (JSON Web Token) Bearer Token** scheme.

- **Public Authentication Routes:** Phone OTP request (`/auth/otp/send`), OTP resend (`/auth/otp/resend`), OTP verification (`/auth/otp/verify`), and webhook callbacks (`/auth/twilio/webhook`).
- **Open KYC Submissions:** Document uploads (`/api/documents/upload`) and status lookups (`/api/documents/verify-status`) are open submissions. They employ `OptionalJWTMiddleware` to attach authenticated tenant claims when present, automatically falling back to the system default tenant if unauthenticated.
- **Protected Administrative Routes:** Document inspection, human review, and dataset management are protected using `JWTMiddleware` along with role-based guards (`RequireAdmin`, `RequireAdminOrReviewer`).
- **Transactional SMS Alerts:** When document KYC reviews are finalized by an admin/reviewer, automated outbound SMS notifications are dispatched via Twilio Programmable Messaging to alert the user of approval/whitelisting.

---

## Architecture

```
Client / Mobile App
  │
  ├── POST /auth/otp/send                  (public - Twilio Verify OTP trigger)
  ├── POST /auth/otp/resend                (public - Twilio Verify OTP resend)
  ├── POST /auth/otp/verify                (public - OTP Check & JWT issuance)
  ├── POST /auth/twilio/webhook            (public - Twilio signature-validated callback)
  ├── GET  /health                         (public - Health check)
  │
  ├── POST /api/documents/upload           (open KYC submission / optional JWT)
  ├── POST /api/documents/verify-status    (open status check / optional JWT)
  │
  └── /api/*                               (protected administrative routes)
        │
        └── JWTMiddleware ──► Role Guards ──► Handler (ReviewDocument, Datasets, etc.)
```

---

## 1. Request Verification Code (`POST /auth/otp/send`)

Dispatches an OTP verification code to the user's phone via Twilio Verify.

### Request
```http
POST /auth/otp/send
Content-Type: application/json

{
  "phone": "+263771234567",
  "channel": "sms",
  "locale": "en"
}
```
*Channels supported:* `"sms"` (default), `"call"`, `"whatsapp"`.

### Server Logic
1. Normalizes the phone number according to **E.164** standard (`+XXXXXXXXXX...`, 8-15 digits).
2. **Rate Limiting / Abuse Prevention:** Enforces a 30-second cooldown between verification code requests for the same phone number.
3. If Twilio Verify is configured (`TWILIO_ACCOUNT_SID`, `TWILIO_AUTH_TOKEN`, `TWILIO_VERIFY_SERVICE_SID`), calls the Twilio Verify v2 API.
4. **Dev Mode Sandbox:** If Twilio credentials are omitted, automatically returns dev code `"000000"` for seamless local development and automated testing.
5. Logs a pending `OTPVerification` record in PostgreSQL with a 10-minute expiry timestamp.

---

## 2. Resend Verification Code (`POST /auth/otp/resend`)

Allows requesting a fresh verification code or switching channels (e.g., from SMS to voice call if SMS delivery is delayed).

### Request
```http
POST /auth/otp/resend
Content-Type: application/json

{
  "phone": "+263771234567",
  "channel": "call"
}
```

---

## 3. Verify OTP & Issue Token (`POST /auth/otp/verify`)

Validates the OTP code against Twilio Verify v2 and issues a signed JWT token.

### Request
```http
POST /auth/otp/verify
Content-Type: application/json

{
  "phone": "+263771234567",
  "code": "123456",
  "email": "user@example.com"
}
```

### Server Logic
1. Checks the submitted code against Twilio Verify v2 `VerificationCheck` (or validates `000000` in Dev Mode).
2. If the code is invalid:
   - Increments the `attempts` counter on the active `OTPVerification` record.
   - Returns `401 Unauthorized`.
3. If the code is approved:
   - Finds or creates a **Tenant** and **User** record (with role `"admin"` on first registration, or links to existing user).
   - Marks the `OTPVerification` record as `approved`.
   - Generates a signed JWT token valid for 1 year.
   - Returns `200 OK` with token and user object.

### Response
```json
{
  "token": "eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9...",
  "user": {
    "id": "c76f8274-06c8-47fb-a75d-6c17e47f2935",
    "phone": "+263771234567",
    "email": "user@example.com",
    "role": "admin",
    "tenant_id": "4bcfbe08-592b-4fa8-b223-28f80459c3f1",
    "created_at": "2026-09-07T10:00:00Z"
  },
  "is_new_user": true,
  "twilio_status": "approved"
}
```

---

## 4. JWT Token Structure

Tokens are signed with **HMAC-SHA256** (`HS256`) using `JWT_SECRET`.

### Claims
| Claim       | Type   | Description                                    |
|-------------|--------|------------------------------------------------|
| `user_id`   | string | UUID of the authenticated user                 |
| `tenant_id` | string | UUID of the user's organization tenant         |
| `role`      | string | User role (`admin`, `user`, `reviewer`)        |
| `exp`       | int64  | Expiry timestamp (1 year from issue)           |

---

## 5. Webhook Callbacks (`POST /auth/twilio/webhook`)

Receives asynchronous delivery status updates from Twilio for SMS and verification events.

- Validates the `X-Twilio-Signature` header using Twilio's HMAC-SHA1 signature verification protocol against the full webhook URL and sorted request parameters.
- Responds with `200 OK` and empty `<Response></Response>` TwiML.

---

## 6. KYC Review & SMS Notification Flow

When a reviewer or admin verifies a KYC document via `POST /api/documents/:id/review`:
1. The document is marked `verified` and `completed`.
2. Asynchronously registers the user's deterministic wallet address and alias on the ZWC blockchain rail (`x/kyc` and `x/alias`).
3. If an alias phone number is present, dispatches an automated SMS alert via Twilio Programmable Messaging:
   > *"Zimbabwe Coin (ZWC): Your identity verification has been approved! Your wallet address zwc1... is now whitelisted."*

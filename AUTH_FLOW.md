# Identity API Gateway — Authentication & Authorization Flow

This document describes the full authentication and authorization flow for the **ZWC Identity API Gateway** (`identity/api-gateway`).

---

## Overview

The gateway uses a **JWT-based (JSON Web Token) Bearer Token** scheme. All public routes (registration, login, health check) are open. Every other endpoint is protected by a JWT middleware that validates the token and injects the caller's identity into the request context.

---

## Architecture

```
Client
  │
  ├── POST /auth/register   (public)
  ├── POST /auth/login      (public)
  ├── GET  /health          (public)
  │
  └── /api/*                (protected — requires Bearer token)
        │
        └── JWTMiddleware ──► handler (UploadDocument, ReviewDocument, etc.)
```

---

## 1. Registration (`POST /auth/register`)

Registers a new **Tenant** (organisation) and creates the first **Admin User** for that tenant.

### Request
```http
POST /auth/register
Content-Type: application/json

{
  "tenant_name": "ACME Corp",
  "email": "admin@acme.com",
  "password": "securepassword123"
}
```

### Server Logic
1. Validates that `email`, `password`, and `tenant_name` are all present.
2. Checks if a user with the same email already exists. Returns `409 Conflict` if so.
3. Creates a new `Tenant` record in PostgreSQL (UUID primary key).
4. Hashes the password using **bcrypt** (`DefaultCost = 10`).
5. Creates a new `User` record with role `"admin"` linked to the new tenant.
6. Creates a default `Project` for the tenant (`Default Project`).
7. Calls `GenerateJWT` to produce a signed JWT token.
8. Returns `201 Created` with the token and user object.

### Response
```json
{
  "token": "eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9...",
  "user": {
    "id": "a1b2c3d4-...",
    "email": "admin@acme.com",
    "role": "admin",
    "tenant_id": "e5f6g7h8-...",
    "created_at": "2026-08-10T00:00:00Z",
    "updated_at": "2026-08-10T00:00:00Z"
  }
}
```

---

## 2. Login (`POST /auth/login`)

Authenticates an existing user and returns a fresh JWT token.

### Request
```http
POST /auth/login
Content-Type: application/json

{
  "email": "admin@acme.com",
  "password": "securepassword123"
}
```

### Server Logic
1. Looks up the user by email in PostgreSQL.
2. Returns `401 Unauthorized` if the user is not found (generic to prevent enumeration attacks).
3. Compares the supplied password against the stored bcrypt hash using `bcrypt.CompareHashAndPassword`.
4. Returns `401 Unauthorized` if the password does not match.
5. Calls `GenerateJWT` to produce a signed JWT token.
6. Returns `200 OK` with the token and user object.

### Response
```json
{
  "token": "eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9...",
  "user": {
    "id": "a1b2c3d4-...",
    "email": "admin@acme.com",
    "role": "admin",
    "tenant_id": "e5f6g7h8-...",
    "created_at": "2026-08-10T00:00:00Z",
    "updated_at": "2026-08-10T00:00:00Z"
  }
}
```

---

## 3. JWT Token Structure

Tokens are signed using **HMAC-SHA256** (`HS256`) with the `JWT_SECRET` environment variable.

### Claims
| Claim       | Type   | Description                                    |
|-------------|--------|------------------------------------------------|
| `user_id`   | string | UUID of the authenticated user                 |
| `tenant_id` | string | UUID of the user's tenant                      |
| `role`      | string | User role (`admin`, `user`, `reviewer`)        |
| `exp`       | int64  | Expiry timestamp (1 year from time of issue)   |

### Example Decoded Payload
```json
{
  "user_id": "a1b2c3d4-...",
  "tenant_id": "e5f6g7h8-...",
  "role": "admin",
  "exp": 1817913600
}
```

---

## 4. JWT Middleware (Protected Routes)

All routes under `/api/*` require a valid Bearer token. Applied at group level in `main.go`:

```go
api := app.Group("/api", handlers.JWTMiddleware)
```

### Middleware Logic
1. Reads the `Authorization` request header.
2. Returns `401 Unauthorized` if missing.
3. Validates the format: must be `Bearer <token>`.
4. Parses and verifies the JWT signature using `JWT_SECRET`.
5. Returns `401 Unauthorized` if the token is invalid, malformed, or expired.
6. Injects decoded claims into the Fiber request context:
   - `c.Locals("user_id")` — caller's user UUID
   - `c.Locals("tenant_id")` — caller's tenant UUID
   - `c.Locals("role")` — caller's role string
7. Calls `c.Next()` to pass control to the route handler.

### Using the Token in Requests
```http
POST /api/documents/upload
Authorization: Bearer eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9...
Content-Type: multipart/form-data
```

---

## 5. Protected Endpoints

| Method   | Path                                      | Description                                      |
|----------|-------------------------------------------|--------------------------------------------------|
| `POST`   | `/api/documents/upload`                   | Upload identity document for OCR processing      |
| `GET`    | `/api/documents`                          | List all documents for the caller's tenant       |
| `GET`    | `/api/documents/:id`                      | Get a single document record                     |
| `GET`    | `/api/documents/:id/prediction`           | Get OCR prediction result for a document         |
| `POST`   | `/api/documents/verify-status`            | Batch-check verification statuses                |
| `POST`   | `/api/documents/:id/review`               | Submit human review (triggers on-chain KYC)      |
| `GET`    | `/api/documents/:id/extracted-fields`     | Get extracted field values for a document        |
| `GET`    | `/api/reviews`                            | List documents pending manual review             |
| `POST`   | `/api/datasets`                           | Create a training dataset                        |
| `GET`    | `/api/datasets`                           | List training datasets                           |
| `GET`    | `/api/datasets/:id`                       | Get a specific dataset                           |
| `DELETE` | `/api/datasets/:id`                       | Delete a dataset                                 |
| `POST`   | `/api/datasets/:id/images`                | Upload image to a dataset                        |
| `POST`   | `/api/training/start`                     | Start an OCR training job                        |
| `GET`    | `/api/training/jobs`                      | List training jobs                               |
| `GET`    | `/api/models`                             | List trained model versions                      |
| `POST`   | `/api/models/:id/deploy`                  | Deploy a model version                           |
| `POST`   | `/api/schemas`                            | Create an extraction schema                      |
| `GET`    | `/api/schemas`                            | List extraction schemas                          |

---

## 6. Full Registration → Upload → Verification Flow

```
Client                          API Gateway                    PostgreSQL / Blockchain
  │                                   │                               │
  │── POST /auth/register ───────────►│                               │
  │                                   │── INSERT Tenant ─────────────►│
  │                                   │── INSERT User (admin) ────────►│
  │                                   │── INSERT Default Project ─────►│
  │                                   │── GenerateJWT(user_id, ...)    │
  │◄── 201 { token, user } ──────────│                               │
  │                                   │                               │
  │── POST /api/documents/upload ────►│                               │
  │   Authorization: Bearer <token>   │                               │
  │                                   │── JWTMiddleware: verify token  │
  │                                   │── DeriveWalletAddress()        │
  │                                   │   SHA256(id_number + salt)     │
  │                                   │── INSERT Document record ─────►│
  │                                   │── Enqueue OCR job (Redis)      │
  │◄── 202 { id, wallet_address } ───│                               │
  │                                   │                               │
  │── POST /api/documents/:id/review ►│                               │
  │   Authorization: Bearer <token>   │                               │
  │                                   │── JWTMiddleware: verify token  │
  │                                   │── UPDATE Document status ─────►│
  │                                   │── go RegisterUserOnChain()     │
  │                                   │     ├── zwc-chaind set-kyc-status
  │                                   │     └── zwc-chaind set-alias   │
  │◄── 200 { verified: true } ───────│                               │
```

---

## 7. Error Reference

| HTTP Status | Message                                | Cause                                         |
|-------------|----------------------------------------|-----------------------------------------------|
| `400`       | `Invalid request body`                 | Malformed JSON body                           |
| `400`       | `Missing required fields`              | `email`, `password`, or `tenant_name` absent  |
| `401`       | `Missing authorization token`          | No `Authorization` header present             |
| `401`       | `Invalid authorization header format`  | Not in `Bearer <token>` format                |
| `401`       | `Invalid or expired token`             | JWT signature invalid or `exp` has passed     |
| `401`       | `Invalid credentials`                  | Wrong email or password on login              |
| `409`       | `User with this email already exists`  | Duplicate registration attempt                |
| `500`       | `Failed to create tenant/user`         | Database write error                          |

---

## 8. Security Notes

- Passwords are stored as **bcrypt hashes** (never plaintext).
- JWT tokens expire after **1 year** (suitable for internal tooling; reduce for production).
- Set `JWT_SECRET` to a long, random string in production. The default value is insecure.
- All protected handlers scope data by `tenant_id` from the token — cross-tenant data access is impossible.
- Login error messages are intentionally generic to prevent user enumeration.
- The `wallet_address` is **never supplied by the user** — it is derived deterministically server-side from the ID number using `SHA256(id_number + ZWC_MASTER_SALT)` → secp256k1 key → Bech32 `zwc1...` address.

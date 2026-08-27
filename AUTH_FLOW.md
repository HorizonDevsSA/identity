# Identity API Gateway — Authentication & Authorization Flow

This document describes the full authentication and authorization flow for the **ZWC Identity API Gateway** (`identity/api-gateway`).

---

## Overview

The gateway uses a **JWT-based (JSON Web Token) Bearer Token** scheme. 
Following recent updates, the system employs a hybrid approach:
- **Public Routes:** Registration, login, and health checks are fully open.
- **Open Submissions:** Document uploads (`/api/documents/upload`) and status checks (`/api/documents/verify-status`) are conditionally open. They use an `OptionalJWTMiddleware` that injects user claims if a token is present, but falls back to a default system tenant if no token is provided.
- **Protected Administrative Routes:** Document inspection, human review, and dataset management are strictly protected using `JWTMiddleware` along with role-based guards (`RequireAdmin`, `RequireAdminOrReviewer`).

---

## Architecture

```
Client
  │
  ├── POST /auth/register                  (public)
  ├── POST /auth/login                     (public)
  ├── GET  /health                         (public)
  │
  ├── POST /api/documents/upload           (open submission / optional JWT)
  ├── POST /api/documents/verify-status    (open submission / optional JWT)
  │
  └── /api/*                               (protected administrative routes)
        │
        └── JWTMiddleware ──► Role Guards ──► handler (ReviewDocument, etc.)
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

---

## 2. Login (`POST /auth/login`)

Authenticates an existing user and returns a fresh JWT token.

### Server Logic
1. Looks up the user by email in PostgreSQL.
2. Returns `401 Unauthorized` if the user is not found (generic to prevent enumeration attacks).
3. Compares the supplied password against the stored bcrypt hash using `bcrypt.CompareHashAndPassword`.
4. Returns `401 Unauthorized` if the password does not match.
5. Calls `GenerateJWT` to produce a signed JWT token.
6. Returns `200 OK` with the token and user object.

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

---

## 4. Middlewares & Role Guards

### `JWTMiddleware` (Strict Authentication)
Used for all administrative routes.
1. Reads the `Authorization` request header.
2. Returns `401 Unauthorized` if missing, invalid format, or expired.
3. Injects decoded claims into the Fiber request context: `user_id`, `tenant_id`, `role`.

### `OptionalJWTMiddleware` (Flexible Authentication)
Used for open endpoints like `/api/documents/upload`.
1. Reads the `Authorization` request header.
2. If present and valid, injects the claims (so authenticated users tie uploads to their tenant).
3. If absent or invalid, it does **not** block the request, allowing the handler to fall back to a system-wide default tenant.

### Role Guards (`RequireAdmin`, `RequireAdminOrReviewer`)
Applied after `JWTMiddleware` on administrative routes to ensure the caller has the necessary permissions.

---

## 5. Endpoints & Access Control

| Method   | Path                                      | Access Level                                     |
|----------|-------------------------------------------|--------------------------------------------------|
| `POST`   | `/api/documents/upload`                   | Open (Optional JWT)                              |
| `POST`   | `/api/documents/verify-status`            | Open (Optional JWT)                              |
| `GET`    | `/api/documents`                          | Admin / Reviewer (Cross-tenant allowed)          |
| `GET`    | `/api/documents/:id`                      | Admin / Reviewer (Cross-tenant allowed)          |
| `GET`    | `/api/documents/:id/prediction`           | Admin / Reviewer                                 |
| `POST`   | `/api/documents/:id/review`               | Admin / Reviewer (Cross-tenant allowed)          |
| `GET`    | `/api/reviews`                            | Admin / Reviewer (Cross-tenant allowed)          |
| `POST`   | `/api/datasets`                           | Protected (Tenant Scoped)                        |
| `GET`    | `/api/datasets`                           | Protected (Tenant Scoped)                        |

*(Note: Admins and Reviewers bypass the `tenant_id` restrictions when inspecting and reviewing documents, allowing them to manage submissions system-wide.)*

---

## 6. Full Registration → Upload → Verification Flow

```
Client                          API Gateway                    PostgreSQL / Blockchain
  │                                   │                               │
  │── POST /auth/register ───────────►│                               │
  │                                   │── INSERT Tenant ─────────────►│
  │                                   │── INSERT User (admin) ────────►│
  │◄── 201 { token, user } ──────────│                               │
  │                                   │                               │
  │── POST /api/documents/upload ────►│                               │
  │   (No auth required)              │                               │
  │                                   │── OptionalJWTMiddleware        │
  │                                   │── Fallback to Default Tenant   │
  │                                   │── DeriveWalletAddress()        │
  │                                   │   SHA256(id_number + salt)     │
  │                                   │── INSERT Document record ─────►│
  │◄── 202 { id, wallet_address } ───│                               │
  │                                   │                               │
  │── POST /api/documents/:id/review ►│                               │
  │   Authorization: Bearer <token>   │                               │
  │                                   │── JWTMiddleware: verify token  │
  │                                   │── RequireAdminOrReviewer guard │
  │                                   │── UPDATE Document status ─────►│
  │                                   │── go RegisterUserOnChain()     │
  │◄── 200 { verified: true } ───────│                               │
```

---

## 7. Security Notes

- **Tenant Bypassing**: Admins and Reviewers are granted global scope when querying documents, meaning `tenant_id` filters are ignored for them.
- **Global Duplicate Checks**: `isDuplicateVerifiedIdentity` now checks globally across the entire system, preventing the same person from being verified twice under different tenants.
- **Wallet Address Generation**: The `wallet_address` is **never supplied by the user**. It is derived deterministically server-side from the ID number.

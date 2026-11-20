# Tenant and Primary Owner provisioning

This backend slice implements the approved D1–D5 contract. The issue records the decision and acceptance criteria. It also bounds the remaining OPEN-011/016 work for other features. [Issue #2 approval](https://github.com/PTKPP/hi-ve/issues/2#issuecomment-7008397059)

## Runtime

Run the migration checks on disposable PostgreSQL with the command in [DATABASE.md](DATABASE.md). The script also runs the HTTP provisioning acceptance tests against its temporary database and a local provider with signed OIDC fixtures.

The Go command is `go run ./cmd/api` from `backend/`. Apply the repository migrations through 015 to the authorized development/test target first. Configure the following outside the repository.

| Environment | Purpose |
| --- | --- |
| `HIVE_DATABASE_URL` | Scoped application DB role, with the required DML and read-only access to the migration version. Use the migration role separately. Legacy Google writers need INSERT on the neutral registry for the compatibility trigger introduced by 012. |
| `HIVE_RECEIPT_HMAC_KEY` | Stable hex-encoded key of at least 32 random bytes. Keep it available for existing receipts. Rotation needs a compatible verification plan before the old key can retire. |
| `HIVE_ADMIN_ORIGIN`, `HIVE_APP_ORIGIN` | Distinct exact HTTPS UI origins. Browser clients send credentials on API calls. Run UI and API on the same site for SameSite Lax cookies. |
| `HIVE_GOOGLE_CLIENT_ID`, `HIVE_GOOGLE_CLIENT_SECRET`, `HIVE_GOOGLE_REDIRECT_URL` | Direct Google OAuth client and exact HTTPS callback ending in `/api/v1/oidc/google/callback`. |
| `HIVE_SMTP_ADDRESS`, `HIVE_SMTP_USER`, `HIVE_SMTP_PASSWORD`, `HIVE_SMTP_FROM` | Authenticated SMTP over implicit TLS. Configure a relay that accepts the invitation sender. |
| `HIVE_LISTEN` | Local API listen address, default `127.0.0.1:8080`. Terminate HTTPS through the configured frontend network boundary. |

Platform login requires an explicitly provisioned Google principal and active Platform principal. An authorized operator must provision the first principal and grant the two actions through reviewed Identity data setup on the intended environment. Database migration installs only schema. There is no anonymous bootstrap route. App sessions require active tenant membership. Target Cloudflare, OIDC client, SMTP relay, role grants, and browser-origin validation still depend on the deployment environment and OPEN-015.

## API and operating flow

1. From the admin origin, `POST /api/v1/admin/login` returns an authorization URL. Navigate the browser to it. The callback sets the scoped Platform session only for a preprovisioned principal. Auth cookies are opaque, Secure, HttpOnly, host-only, and kept as digests server-side. Session lifetime is eight hours for this slice. Authorization is checked against live DB state. A successful sign-in replaces the browser’s previous session for the matching Platform or tenant context in one transaction. Expired session digests and abandoned OIDC flows are removed in bounded batches. Provisioning audit history is retained separately.
2. Provision through `POST /api/v1/admin/tenants` with the approved JSON and `Idempotency-Key`. Keep the same key and payload when retrying. The service commits pending state before sending the invitation code to the designated contact. The response remains the committed result if SMTP fails. An admin with create permission can use `POST /api/v1/admin/tenants/{tenant_id}/owner-invitation` to rotate the invitation and send it again. Rotation invalidates the previous generation.
3. The invited user submits `{"invitation":"<code>"}` to `POST /api/v1/app/owner-claim` and opens the returned Google authorization URL. On a valid callback they receive the claim ID without a tenant session. The operator verifies organizational ownership with a known independent contact, confirms the exact claim ID through that contact, and records an internal verification reference. Use the method identifier `verified-organization-contact` and a non-secret reference.
4. Activate through `POST /api/v1/admin/tenants/{tenant_id}/activate` using the approved fields and a fresh idempotency key. Platform serializes writes for that tenant. Identity consumes the current verified claim and assigns the Primary Owner. Audit and Platform commit their records in the same transaction. The Owner then signs in through `POST /api/v1/app/tenants/{tenant_id}/login`. `GET /api/v1/app/tenants/{tenant_id}/session` checks that the tenant session and active membership match.
5. The login callback currently returns 204 for a session and JSON for a claim. UI integration can present the result with an approved navigation flow when the frontend is available. Configure access logs and reverse proxies to omit OAuth callback query parameters and sensitive headers. Recovery, Owner transfer, wider entitlement and device review remain outside this slice.

## Scoped sign-out

Send POST to `/api/v1/admin/logout` from the admin origin or `/api/v1/app/tenants/{tenant_id}/logout` from the App origin. The response is an empty 204. A live matching session is revoked with its audit record before its cookie is cleared. Missing, expired, previously revoked and other-context cookies receive 204 with no cookie change.

Logout targets the exact presented session. A separately authenticated callback may issue a new session, and an old logout retry cannot revoke that session. Serialize logout and a new login in the browser so the browser applies cookie responses in order. A persistence failure preserves the session and its cookie for retry.

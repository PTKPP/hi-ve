# Online entitlement and membership lifecycle

## Entitlement

The initial admin allowlist contains only `catalog.products`. Its first consumer in this increment is the disposable acceptance test. Catalog product writes are the proposed first business consumer and need their own contract approval. [Entitlement decision](https://github.com/PTKPP/hi-ve/issues/5#issuecomment-7012754500)

Under `/api/v1/admin/tenants/{tenant_id}/entitlements/{capability_id}`, use GET for the current grant, PUT with `{effective_at,expires_at,source_reference,revision}` to grant, and POST to `/revoke` with `{reason_code,revision}` to revoke. Grant times normalize to UTC and apply on `[effective_at,expires_at)`. The initial finite lease applies to active tenants. Grant source is `manual_platform`, and the caller supplies an opaque reference. The revoke reason is `administrative_revocation`. The three operations each have a dedicated `platform.entitlements.*` permission.

A consuming operation must evaluate its scoped user permission and rollout separately. Use the entitlement read check for ordinary reads, or call `CheckWrite` inside the transaction for writes that need ordering against revoke. Lock only the targeted grant, check server time after locking, perform the authorized write, and commit in the same transaction. A write ordered before revoke may complete. New requests after the revoke commit receive a denial. Keep the lock order explicit when adding a consuming domain. Grace, renewal/replacement of a live grant, pending offline processing, and commercial packages remain under review.

## Membership lifecycle

The active Primary Owner can call `POST /api/v1/app/tenants/{tenant_id}/members/{member_id}/deactivate` or `/reactivate` with `{revision,reason_code}`. The reason is `administrative_action`. The Owner and Platform contexts stay separate. Deactivation deletes the target tenant/member's session digests and custom role assignments in the transaction with the membership transition and its audit. Prior assignments remain in audit. Reactivation restores eligibility for fresh authentication and explicit assignment. [Membership decision](https://github.com/PTKPP/hi-ve/issues/6#issuecomment-7012755045)

For deactivation and assignment, the RBAC revision lock precedes the target membership lock. Issuance and rotation lock their affected memberships in stable principal-ID order before touching sessions. The lock holds through commit. Active requests use live membership and grants.

Migration 012 adds and backfills neutral principal IDs, mirrors inserts from Google, and validates the extra FKs. Existing membership IDs and the Google-only FK are preserved. Before an authorized rollout, grant the legacy app writer INSERT on `hive.identity_principals` for the trigger. Retirement of the old FK and PIN-only onboarding follow their separate compatibility and approval gates.

## Replays

Mutations require an `Idempotency-Key`. Revision zero means absent entitlement or an unchanged initial membership. Each successful transition advances its own revision once. The server rechecks live authority even for replays. An exact replay returns the original response. A different payload with the same key, stale revision or invalid transition returns 409. Use a fresh key and the current revision for a new transition. Receipts contain no credentials or tokens.

# Tenant-wide role and grant management

This slice follows the approved Issue #4 contract. The active Primary Owner manages custom roles and their assignments to existing active members of the same tenant. A member with the explicit tenant-wide `identity.roles.read` action can list role definitions. The backend reads current membership and grants each time. Platform sessions use a separate authority. [Scope approval](https://github.com/PTKPP/hi-ve/issues/4#issuecomment-7010571295)

| Method and path under `/api/v1/app/tenants/{tenant_id}` | Request |
| --- | --- |
| `GET /roles` | Owner or member with `identity.roles.read`. Returns role definitions and tenant RBAC revision. |
| `POST /roles` | Owner. Send `Idempotency-Key` and `{key,name,actions}`. Returns 201 and the original result on an authorized exact replay. Role keys are immutable. |
| `PUT /roles/{role_id}` | Owner. Send `{name,actions,revision}` to replace the role definition. |
| `PUT /members/{member_id}/roles` | Owner. Send `{role_ids,revision}` to replace assignments. Use the member's principal ID within the current tenant. An empty array revokes all custom roles. |

The initial revision is zero. Use the returned revision for subsequent updates. A stale revision returns `409 REVISION_CONFLICT`. Reusing a create key with a different payload returns `409 IDEMPOTENCY_CONFLICT`. Conflicting tenant role keys return `409 STATE_CONFLICT`. Clients should refresh definitions and revision before retrying an update. Use the App origin and session cookie as for existing tenant endpoints.

Identity owns role definitions, actions, assignments, revisions and create receipts. Composite references protect role and member links by tenant. The transaction compares and updates the revision for RBAC mutations only. It acquires scoped session/membership locks for those mutations and writes the before/after audit through the Audit domain in the same transaction. Reads use a consistent snapshot and no authorization cache. Migration 006 creates the Identity tables and migration 007 creates the Audit table. [Migration workflow](DATABASE.md)

Membership deactivation and reactivation require their own contract. A future deactivation workflow must revoke all sessions for the affected membership atomically. Location grants require the Locations contract. Entitlement and rollout remain separate checks for their own slices.

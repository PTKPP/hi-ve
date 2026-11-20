# Locations Foundation

Locations owns the tenant-scoped location records and stable IDs. Phase 03 inventory mappings reuse those records. Identity provides the existing live Primary Owner authorization contract. Devices can consume the Locations lookup after its slice is approved. [Approved LF1–LF4 contract](https://github.com/PTKPP/hi-ve/issues/7#issuecomment-7022822045)

An active tenant Primary Owner can use `GET /api/v1/app/tenants/{tenant_id}/locations` to list `{locations:[...]}` sorted by code. POST to the same path with `Idempotency-Key` and `{code,name,type}`; the response is 201 with `{id,code,name,type,status}`. Type is `outlet` or `warehouse`. New locations start active. Send the App origin and a scoped tenant session cookie.

Code is trimmed and normalized to lowercase ASCII. It is unique across location types and statuses within a tenant. Names can repeat. Replaying the same key and normalized input returns the original result after live authorization. A changed payload with the same key returns `409 IDEMPOTENCY_CONFLICT`. A duplicate code with a new key returns `409 STATE_CONFLICT`. Creation, receipt and audit commit in the same transaction.

`ResolveActiveOutlet` returns the record only for a same-tenant active outlet in an active tenant. Callers enforce Identity permissions separately. Archive, location grants and inventory balance mapping follow their approved contracts.

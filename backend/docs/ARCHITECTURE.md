# Hi-Ve Backend Architecture — Go Screaming Modular Monolith

> **Target placement:** `backend/docs/ARCHITECTURE.md` in the Hi-Ve repository.  
> **Document version:** Architecture Draft 03 · deployment topology revised.
> **Status:** **DESIGN BASELINE / REVIEW REQUIRED**. Approved product and architecture *principles* are distinguished from proposed implementation patterns. No actual Hi-Ve Go repository was inspected for this document; file paths, package names, database layouts, endpoints, event names, and testing commands are **examples, not an as-built description**.  
> **Applies to:** the single Hi-Ve Go backend and its integration boundaries. Frontend, database migrations, deployment, and infrastructure changes require independent authorization.  
> **Authoritative sources:** canonical [PRD](../../PRD.md) (v04, CAP-01–CAP-58), [Functional Specification](../../FUNCTIONAL_SPEC.md) (57 FR: 36 baseline + 21 derived), and [Backend Agent Instructions](../AGENTS.md). The supplied SPK-SPN `AGENTS.md` was used **only** as an example of architecture guardrails; its domains and TypeScript-specific implementation are **not** Hi-Ve requirements.

## 1. Purpose, scope and source-of-truth rules

This document translates Hi-Ve's approved product architecture into reviewable engineering boundaries. It **does not authorize implementation**, choose solutions for OPEN product decisions, claim existing code, or replace acceptance criteria.

**Document authority (when compatible):**

1. Explicit latest **APPROVED** product decisions and supersession rules in canonical `PRD.md`.
2. Compatible **APPROVED** FR/AC in [Functional Specification](../../FUNCTIONAL_SPEC.md). Derived/review-required AC remains review-required; historical `PRDv1.0.md` is provenance already embedded there and need not exist in the active repo.
3. Approved API/schema contracts, if separately established; existing repository source/tests establish *implemented behavior*, not product approval.
4. This architecture document and [Backend Agent Instructions](../AGENTS.md) constrain design and task execution. If a conflict arises, report it rather than silently choosing an implementation.
5. Historical `PRDv*.md`, `PRDv1.0.md`, sketches, and examples never supersede explicit later decisions.

**Terminology:** **APPROVED** = product decision; **DESIGN RECOMMENDATION** = a proposed engineering realization requiring repository review; **OPEN** = not decided; **NOT VERIFIED** = requires inspection/testing against actual Hi-Ve source/target. A requirement can be approved without code being implemented.

**Non-goals:** microservices; product-specific backends/databases; introducing 40 Go packages solely for 40 capability IDs; implementing P1 cash sessions/customer/supplier directory; automated SaaS billing; full accounting; forcing mass-to-volume conversion; deciding HPP costing algorithms or financial valuation without approval.

## 2. Architecture at a glance — APPROVED principles

```text
                         Cloudflare static delivery
        +-------------------+--------------------+---------------------+
        |                   |                    |                     |
  Landing Page         Hi-Ve App             Superadmin App       (browser)
  Pages / static       Workers Static Assets Workers Static Assets
  public content       mobile/tablet-first PWA static, no offline V1
        |                   |                    |
        | (no core API)     +-----------+--------+
        |                               |
        |                      HTTPS API / WSS
        |                               |
        |                     Cloudflare Tunnel
        |                               |
        |                    +--------------------+
        |                    | Go Modular Monolith |
        |                    | container on app VM |
        |                    | app API + admin API |
        |                    +---------+----------+
        |                              |
        |                    +---------+----------+
        |                    |                    |
        |                PostgreSQL            Redis (optional)
        |                on separate DB VM     support/cache only
        +-------------------- (no platform business logic at edge)
```

- **Three frontends** are independent deployments but not three business backends.
- **One Go backend** owns authentication, transaction/inventory logic, entitlement, audit, and realtime API endpoints; PostgreSQL is authoritative.
- **Cloudflare Pages:** Landing Page only. **Cloudflare Workers Static Assets:** Hi-Ve App and Superadmin. V1 operational paths must not execute Pages Functions, Worker script, edge SSR, edge auth, or edge API proxy. Cloudflare static-asset request limits and Worker script CPU/request limits are distinct.
- **Redis** may accelerate/support disposable state, but never becomes the authoritative store for posted sales, stock, entitlements, login audit, or tenant permissions.
- **Offline-first** is constrained to approved core POS cash-sale operations. Repacking, receiving, transfer, adjustment, stock count and administrative mutations are online-only in V1.
- Cloudflare availability does **not** guarantee availability of Go VPS or durability of browser IndexedDB.

**Deployment choices are approved (CAP-17–20, CAP-51/54); the exact hostnames, OAuth redirect URIs, session cookie scopes, CORS allowlist, Cloudflare routing config, and rollback procedures are OPEN-015.**

### 2.1 Development and Proxmox production topology — APPROVED

- **Development:** Build and run the native Go binary via a systemd user unit. Run PostgreSQL 18 in Docker Compose with a persistent named volume and a loopback-only published port. Keep runtime configuration and secrets in local environment files. Automated integration tests use a separate disposable PostgreSQL instance.
- **Production target:** Run the Go backend as a container on the Proxmox application VM. Compose can manage the backend. PostgreSQL runs natively on a separate VM. Connect over a private network through a firewall allowlist and PostgreSQL TLS with certificate and hostname verification. The backend image contains no database service.
- **Migration and recovery:** Both environments use the same PostgreSQL schema, migration files and HTTP contracts. Production separates application and migration roles and runs Tern as an explicit controlled process. Replica startup verifies the schema. Backup and restore evidence, health probes, and a compatible application rollback are release gates. See [deployment procedures](DEPLOYMENT.md). OPEN-015 still covers validation of the actual hosts, ingress, credentials, and integrations.

## 3. Screaming Modular Monolith — APPROVED principle

### 3.1 What the code structure must communicate

A developer or agent opening the backend should quickly find business capabilities such as **Inventory, Sales, Identity, Devices and Platform**, rather than beginning at generic `controllers/`, `models/` and `services/` folders. Keep one executable/service boundary but explicit internal ownership boundaries.

Core rules:

- Group by business capability and use case. Transport, persistence, policies and DTOs stay close to the owning capability when this fits the verified repository conventions.
- HTTP handlers parse/validate request shape and invoke use cases; handlers are **not** stock-ledger engines, permission policy engines or transaction coordinators.
- A module's state changes are performed through its **owned use cases**. Other modules cannot directly update the owner's tables or import private repositories.
- Cross-domain calls use deliberately exposed contracts/interfaces, with clear transaction behavior and no circular package imports.
- Reports consume authorized read-only queries/projections; they do not issue authoritative inventory or sales mutations.
- Product menus (`POS`, `Retail`, `Warehouse`) are **presentation/entitlement mappings**, not alternate business engines.
- Keep shared platform/technical helpers small and purposeful. Do not use an unbounded generic `common`, `util`, or service locator as an escape hatch.

### 3.2 Example Go package organization — DESIGN RECOMMENDATION, NOT A MIGRATION PLAN

```text
backend/                                # conceptual only; inspect repository first
  AGENTS.md
  docs/
    ARCHITECTURE.md                    # this document
  cmd/
    api/                                # entrypoint + composition only
  internal/
    platform/                           # tenancy, entitlements, administration
    identity/                           # authentication, users, permissions, offline_access
    devices/                            # management, operators
    settings/
    audit/
    catalog/
    locations/
    inventory/                          # ledger, balance, receiving, repacking, ...
    sales/                              # transaction posting, payments, returns, ...
    reports/
    offline/                            # intake/sync/reconciliation coordination
    realtime/
    bootstrap/                          # composition root/wiring, no domain policy
    transport/                          # shared router/middleware only where justified
    support/                            # minimal technical primitives, not a business domain
```

**Do not create these directories from this diagram.** Existing, verified source structure may require different names or grouping. Domain names here represent **12 logical responsibilities**, not an instruction to force precisely 12 root packages; **40 submodules are canonical capability IDs, not 40 services/packages/tables**. The package convention, DI approach, router, ORM/query layer, Go/runtime version, and migration tool remain **NOT VERIFIED**.

### 3.3 Boundary testing and static checks — DESIGN RECOMMENDATION

After inspecting real code, CI should fail on forbidden imports across private domain internals, circular dependencies, unauthorized writes across domain ownership, and misplaced business policy in adapters when mechanically testable. Define import allowlists based on **actual package graph**, not on hypothetical paths above. A production build passing alone is insufficient evidence of sound boundaries.

## 4. Canonical domain ownership — APPROVED principles

| Domain (logical) | Owns authoritative behavior and data | Boundary / important constraints | Related examples |
|---|---|---|---|
| **Platform** | Tenant lifecycle, Primary Owner provisioning coordination, entitlement lifecycle, Superadmin administration workflows | Platform identity is separate from tenant actor; entitlement is not subscription billing | `platform.tenancy`, `platform.entitlements`, `platform.administration` |
| **Identity** | OIDC account linking/session, users, custom roles, permissions/location scope, PIN and offline authorization policy | Backend is authoritative; device assignment cannot increase user grants | `identity.authentication`, `identity.users`, `identity.permissions`, `identity.offline_access` |
| **Devices** | Pairing, installation/device registration, status/revoke, eligible outlet assignments, approved operator assignments, operational context | One tenant per device; **one active outlet per operational session**, even if several are assigned | `devices.management`, `devices.operators` |
| **Settings** | Tenant operational preferences; contracts for approved global config where needed | Platform-wide secrets/policies are not tenant-configurable secrets | `settings.tenant` |
| **Audit** | Durable security/activity trail, login attempt history and ingestion | Seven-day login history is distinct from critical audit-retention policy | `audit.activity` |
| **Catalog** | Product/SKU, variant/category/barcode, unit definitions and selling-price rules | Batch stock movements are Inventory-owned; cost access is confidential | `catalog.products`, `catalog.units`, `catalog.pricing` |
| **Locations** | Outlet/warehouse master, physical location relationships and status | Authorization to locations is **Identity-owned**, not a second access engine | `locations.management` |
| **Inventory** | Available/non-saleable/in-transit balances, **immutable stock ledger**, batch lineage, receiving, repacking, transfer, adjustments, stock count | Only owner of authoritative stock posting. Do not count transfer/repacking as sales revenue | All 8 `inventory.*` submodules |
| **Sales** | Cart/checkout, sale state, payment status, sale history, return, void and receipt behavior | Sales owns sale lifecycle; **Inventory posts stock effects** through contract | All 7 `sales.*` submodules |
| **Reports** | Authorized read models and calculations over confirmed operational data | No direct business transaction writes; confidential fields filtered by permission | All 4 `reports.*` submodules |
| **Offline** | Server-side sync intake, idempotent reconciliation and resolution protocol for queued operations | Browser IndexedDB resides in App; backend remains authoritative for postings | `offline.storage`, `offline.sync`, `offline.reconciliation` |
| **Realtime** | Delivery of post-commit changes, authorized notifications, connection status | Notification failure cannot roll back a committed sale; no sensitive payload leakage | `realtime.events`, `realtime.notifications`, `realtime.connection_status` |

**Ownership nuance:** `offline.storage` describes a cross-tier capability; IndexedDB implementation is owned by the Hi-Ve App, while the backend owns acceptance, reconciliation and durable receipt of synchronized operations. Do not relocate browser storage responsibilities to Go.

**Unfinalized choices:** actual aggregates/table names, location-to-stock bucket schema, event schemas, report materialization strategy, and HPP valuation methods are **not defined by this table**.

## 5. Dependency rules and cross-domain contracts

### 5.1 Allowed interactions (conceptual)

```text
Superadmin API -> Platform -> [Identity / Settings / Audit contracts]
Tenant App API -> [Identity authorization + Platform entitlement]
               -> [Devices operational context + Locations scope]
               -> owning domain use case (Sales, Inventory, Catalog, ...)
Sales posting -> Inventory posting interface (same backend, controlled transaction)
Inventory/Sales -> committed records -> post-commit event publication
Reports -> read-only authorized projections of domain-owned records
Offline sync -> owning Sales use case -> Inventory contract; no parallel stock engine
Realtime -> post-commit event feed -> only authorized recipients
Audit -> durable security/operation events; no dependence on realtime availability
```

This is a **responsibility diagram**, not a fixed compile-time import DAG. Actual direction of Go package imports should be designed to avoid cycles using appropriately placed consumer-facing interfaces and a composition root.

### 5.2 Contracts must state

Every cross-domain public operation must define: **owner and use case; input/context; tenant and location scope; required entitlement/permission; idempotency identity; transaction boundary; state transition; errors; output; audit/event effects; test references.** If a contract is not approved/verified, label it `SPEC-ONLY` rather than inventing endpoint or method names.

Go interface example (illustrative only):

```go
// Illustrative contract only. Neither type nor method is an approved API.
type StockPosting interface {
    PostSaleStock(ctx context.Context, tx UnitOfWork, cmd SaleStockCommand) (StockPostingResult, error)
}
```

`UnitOfWork` is merely one possible pattern; the real choice between transaction orchestration, transaction context, and persistence adapters requires repository review. **Passing arbitrary database handles between domains should not become an uncontrolled bypass of ownership.**

### 5.3 Synchronous vs asynchronous boundary

- **Synchronous, within a controlled PostgreSQL transaction:** authoritative effects that must succeed or fail together, such as confirmed sale + ledger/balance reduction, repacking input/output, and transfer state transition.
- **Post-commit asynchronous delivery:** dashboard notifications, realtime fan-out, analytics projections, and non-authoritative alerts. Delivery failure must be observable/retryable but must not create duplicate business effects.
- **DESIGN RECOMMENDATION:** evaluate a transactional outbox when reliable post-commit delivery is needed. Do not declare an outbox table/queue mandatory or completed before its event delivery semantics, recovery requirements and operational budget have been reviewed.

## 6. Transaction, stock and money invariants — APPROVED principles

### 6.1 PostgreSQL authoritative transaction rules

- **Posting is atomic:** confirmed sales with stock consequences cannot leave a committed sale but half-written stock mutation, or the reverse, for standard online execution.
- **Idempotency** is enforced server-side at the relevant business operation scope, including receipt of offline retries. Repeated identical request => same authoritative business result; reusing an identifier with incompatible payload must be rejected or reconciled explicitly (exact error contract pending).
- **Concurrency:** competing decrements cannot consume the same final stock twice online. Evaluate locking/conditional updates/serialization once tables and transaction isolation are designed. Do not rely on UI checks alone.
- **Ledger/history:** posted stock movement is immutable; corrections use traceable authorized reversal/adjustment; posted sale/void/return preserves original document and audit reference.
- **Decimal-safe values:** amount and quantity cannot use unguarded floating point for money or stock reconciliation. Use PostgreSQL precision and Go decimal-safe representation as established by approved data design; no library/version chosen here.
- **Unit conversion V1:** `kg <-> g` and `L <-> mL` are deterministic same-dimension conversions; **no automatic kg-to-mL conversion**. For honey repacking, capture measured input and actual output, not invented density.
- **Cost/HPP:** storage of precise costs and their authorization is approved; inventory costing basis, sale cost snapshots and valuation calculation are **OPEN-002**, not author-selected defaults.

### 6.2 Sales checkout (conceptual, no endpoint specified)

1. Validate authenticated tenant/operator/device/outlet context, active grants, feature entitlement and sale permissions.
2. Validate SKU, applicable outlet price and accepted payment mode/state. For online posting, resolve authoritative stock availability according to Inventory policy.
3. In one controlled business transaction, persist Sales' confirmed transaction and invoke Inventory's **owned stock posting**, with atomic idempotency/ledger effects.
4. Commit authoritative records. Audit and durable event intent must be handled according to the approved atomicity plan; emit externally visible realtime only **after commit**.
5. Return a stable acknowledgement/status. Duplicate retries must not create duplicate sales, ledger lines or revenue events.

**Offline exception:** device stores a **pending** cash sale locally; this is **not** a server-confirmed transaction, and the backend must later route it through the same authoritative Sales/Inventory validation path with exception handling. Preserve the record for manual reconciliation if server stock/authorization differs; do not silently drop it or mark it as confirmed.

### 6.3 Inventory workflows (conceptual)

| Workflow | Authoritative rule | Failure/retry rule |
|---|---|---|
| Receiving | Draft does not increase stock; authorized post records batch/source and ledger | One posting per document; correct posted errors through controlled document |
| Repacking | One source batch and one output SKU in V1; decrement actual input and increment actual output atomically at same location; trace batch lineage | No partial input/output balances; track measured loss/difference |
| Transfer normal | Source decrease, in-transit accounting, destination receipt; no double saleable availability | Partial receipts and discrepancy resolution remain represented |
| Transfer cepat | Direct authorized move under approved constraints | Ledger on both locations; no sales revenue |
| Stock adjustment | Reason/actor/scope/approval per final policy, traceable ledger | Never direct overwrite of ledger history |
| Stock count | Snapshot, physical count, difference review, authorized posting | Counting alone does not directly mutate authoritative balance |
| Return/void | Sales owns lifecycle and reference to original; Inventory owns stock reversals and saleable/damaged classification | No cumulative over-return or double posting |

**OPEN-003:** approval presets are not final. **OPEN-007/008:** final treatment of pending offline transactions under suspended tenants, revoked devices or expired entitlements is not settled; preserve evidence and route to explicit reconciliation rather than inventing success/failure semantics.

## 7. Authentication, authorization and device context

### 7.1 Separate principals and sessions — APPROVED

- **Platform/Superadmin principal** is not a tenant Owner principal. Use separate session/authorization contexts and API protection. Platform admins do not automatically inherit tenant transactional access.
- **Primary Owner and Superadmin:** direct Google OAuth 2.0 / OIDC Authorization Code + PKCE, explicit provisioning and account link via stable `issuer + sub`; email alone does not grant authorization. Backend-managed sessions and correct cookie/CSRF handling.
- **No Auth0**, no Owner password-authentication implementation in V1, and no additional mandatory Hi-Ve Superadmin MFA in V1 unless separately approved. External identity-provider controls do not replace backend authorization.
- **Tenant staff V1:** Owner-defined individual six-digit PIN, selected user profile on activated device; staff personal-device login without activation is out of V1.

### 7.2 Policy evaluation — APPROVED

The server authorizes each applicable request using the intersection of:

```text
Authenticated principal + active tenant/owner/operator
  ∩ canonical capability entitlement (product/submodule/feature as applicable)
  ∩ granular permission/action
  ∩ allowed location and data scope
  ∩ active device + assigned operator + exactly one active outlet (device context)
  ∩ current resource and lifecycle state
  -> ALLOW or DENY (default DENY)
```

Do not trust client-provided tenant ID, operator ID, outlet ID, permission, feature flag or role claims without matching live server authority. Authorization is enforced for **reads, writes, individual response fields, exports, reports and realtime subscriptions**. UI hiding and CORS/Origin are not access controls. Approved entitlement may be shared across product presentations, but role/location constraints still apply.

### 7.3 Device activation and offline limitations — APPROVED principle, parameters OPEN

- A device generates a **short-lived, single-use online pairing request**; Owner approves using their authenticated personal device. The shared cashier device must not receive/retain the Owner's Google token.
- Device registration is associated with **one tenant** and one or more allowed outlets, while an operational session has **one active outlet**. Operator selection is the intersection of device/outlet assignment and the operator's actual permissions.
- Switching outlet requires online validation and safe resolution of pending transactions; offline activity remains in last authorized outlet context.
- PIN plaintext is not stored. Server PIN verifier and the device's separate hardened offline verifier must not be conflated; PIN brute-force attacks on stolen local verifier remain a risk with six digits.
- Login lockout baseline: **five failed attempts -> fifteen-minute temporary lock** as product policy, recognizing that offline browser compromise can bypass local counters.
- **OPEN-001:** exact offline authorization TTL (72 h is an *unapproved pilot hypothesis*), key derivation specifics, storage protections and recovery after stale revocation.
- **OPEN-013:** storage eviction, lost device, unresolved pending cash sales and reactivation recovery. PWA installation is not hardware-rooted identity.

### 7.4 Sensitive data — APPROVED

- Treat HPP, purchase costs, margin, stock valuation and relevant supplier terms as **Confidential**, recoveries/secrets/verifier material as **Restricted**.
- **Do not return unauthorized fields** from API, reports, export, websocket events, logs, notifications or IndexedDB cache. Masking only in UI is insufficient.
- HPP **does not require special per-column/application-level encryption in V1**. Use database least privilege, TLS, protected backups, secret management and audit. This does **not** protect HPP from every privileged direct database reader; document the accepted residual risk.
- Security audit logs cannot record raw PIN, token, OAuth secrets, verifier, or sensitive financial values beyond what a specifically authorized audit use case needs.

## 8. Offline sync, audit ingestion and realtime

### 8.1 The two queues must not be confused

| Queue/data | Owner | Server record | Retention/deletion rule |
|---|---|---|---|
| Offline **sale** pending | PWA IndexedDB until server decision | Sales transaction or reconciliation exception in PostgreSQL | Preserve until acknowledged and safely resolved; never purge due to 7-day login audit policy |
| Offline **login attempt** pending | Temporary PWA IndexedDB queue | **7-day operational login history in PostgreSQL** | Delete local queue item **only after durable server acknowledgement** |

- Each item needs a stable event/operation identifier and idempotent receipt. Server distinguishes client timestamp from authoritative receive timestamp.
- Failed network retry must never be interpreted as success; queue capacity/full/eviction must surface as a risk/status in the client.
- Critical audit events (permission, device revoke, Owner recovery, posting decisions) have a **separate retention policy, OPEN-006**; seven days applies to operational login history only.
- Untrusted offline device audit events cannot be treated as tamper-proof evidence.

### 8.2 Reconciliation rules

The backend does **not** bypass current tenant/device/entitlement checks solely because an offline sale was locally recorded. Where policies conflict (suspended tenant, changed grants, shortage), retain submitted evidence, prevent double posting, and classify the exception under the **eventual approved reconciliation policy**. Do not invent silent accept/reject rules while OPEN-007/008 remain open.

### 8.3 Realtime is downstream of business commit

- Events representing confirmed changes are only delivered after successful commit. Consider durable outbox + delivery worker as a **DESIGN RECOMMENDATION** if at-least-once delivery and recovery are required.
- Consumers must be tolerant of duplicate delivery; event IDs and authorization checks must prevent duplicate notification effects and unauthorized field leakage.
- WebSocket authentication and context switching must enforce server authorization. Separate connectivity states: internet, backend/API, websocket, sync pending, and sync exception.
- Push notifications depend on browser/device capability and user consent; realtime dashboard is primary while Owner App is active.

## 9. API, database and integration boundaries

### 9.1 APIs — APPROVED separation, contract details OPEN

- Expose **separately authorized App and Admin API surfaces** within one Go backend. Example prefixes such as `/api/v1/app/*` and `/api/v1/admin/*` are **illustrative, not approved endpoint contracts**.
- Public Landing Page does not require privileged operational API endpoints.
- Enforce input validation, permission/entitlement checks, tenant-scoped data access, rate limiting and safe error handling as appropriate; precise parameters belong to `SECURITY.md` / approved API contract.
- Consider versioned error/response envelopes, pagination, filtering and request idempotency headers in API contract design; do not invent established formats.
- Browser requests are untrusted even when originating from expected Cloudflare domains. Exact CORS origins, secure cookies, CSRF, OAuth redirect handling, and websocket protection must be verified per OPEN-015.

### 9.2 PostgreSQL — Modular Database Architecture + Evolutionary Database Design

**Status:** PostgreSQL as authoritative system of record and logical domain ownership are **APPROVED principles** (PRD). **Modular Database Architecture + Evolutionary Database Design** are the database engineering guardrails for future changes. This section does **not** approve physical schemas per domain, database-per-module, RLS, particular table names or any DDL execution; those remain **DESIGN OPTIONS / NOT VERIFIED** until reviewed against the actual repository.

#### 9.2.1 Logical ownership and extension boundaries

- Every authoritative table/dataset has **one owning domain** responsible for schema evolution, write rules and invariants. New capabilities extend the owning domain or add an independently owned table/module; they must not bypass another owner's repository or mutate its tables directly.
- Cross-domain changes use explicit owned use-case contracts (see §5); authorized reads use controlled query contracts or read models/projections. Read-model duplication is permissible only when labeled **non-authoritative**, with defined refresh/consistency behavior; do not create competing writable copies of stock, SKU or sales truth.
- POS, Retail and Warehouse are product/entitlement views over shared domain capabilities, **not** reasons to clone a tenant's Catalog, Inventory or Identity tables. Physical PostgreSQL schema-per-domain is optional; logical ownership is mandatory regardless of physical organization.
- Cross-domain references must use stable identities and an explicitly reviewed consistency/constraint strategy. Do not introduce circular ownership, unreviewed shared mutable tables, or assume foreign keys must always be prohibited across module boundaries.

#### 9.2.2 Tenant isolation and transactional invariants

- Every tenant-scoped read/write must be restricted using **server-validated tenant and authorized location context**; never trust tenant identifiers supplied by the browser without authorization. Model tenant-scoped uniqueness and referential integrity in constraints/indexes where appropriate. Platform-global data is not automatically tenant-scoped.
- Critical mutations that must succeed together (e.g. confirmed Sales posting and Inventory ledger/balance effects) remain within a **controlled atomic PostgreSQL transaction** through owner interfaces. Idempotency and concurrency protection must prevent double posting, lost updates and overselling. Follow §6 for stock, money and immutable history semantics.
- Protect confidential financial fields in authorized queries, projections, logs, exports and caches; retain decimal-safe amounts and traceable reversal/adjustment rather than rewriting posted stock movements. DB permissions, limited credentials, protected backups and restore tests are defense in depth.
- PostgreSQL **RLS** may be evaluated as additional isolation, but is not an alternative to backend authorization; connection/role context and pooling behavior must be verified before adoption.

#### 9.2.3 Backward-compatible schema evolution

- Database changes **MUST use versioned, reviewed migrations** with an explicit owning domain. Prefer additive changes; do not combine schema removal/rename or incompatible type changes with the same deployment that first introduces replacements.
- Use **Expand → Migrate → Contract** for breaking changes: (1) add compatible structure, (2) deploy compatible readers/writers and backfill/validate existing data, (3) retire old structure only after dependent versions/consumers have migrated and removal is separately approved. Dual-read/write, if needed, requires a reconciliation plan and finite compatibility window.
- Migration/backfill logic must preserve tenant boundaries, unique/idempotent operation identities, history and referential integrity. For large tables, assess lock duration, index creation strategy, query plans, batching, retryability and operational load before execution.
- Deleting data, rewriting immutable postings, or running destructive DDL is **not** an implicit consequence of approval to add a feature. Persistent/production migrations need separate target-specific authorization and recovery/roll-forward procedures (not an assumed safe automatic down-migration).

#### 9.2.4 Verification and enforcement

- For a database-affecting change, review the table owner, affected cross-module contracts, old/new application compatibility, tenant/field security, concurrency/idempotency, constraints/indexes, migration deployment order, recovery and performance impact.
- Test migrations on a **disposable** database from a representative previous schema as well as a fresh install; validate repeatable backfill behavior and relevant negative tenant/duplicate/rollback cases. Do not claim CI checks, migration commands or a verified baseline without inspecting and running them.
- **DESIGN RECOMMENDATION:** Once actual tooling is known, add CI guards for migration consistency, domain-owned data access, cross-module boundary violations and representative upgrade tests. Avoid imposing fictitious scripts or a universal per-task audit report.

### 9.3 External integrations and resilience

- Google OIDC: provider adapter and account-linking checks belong to Identity. Provider error cannot be interpreted as implicit access grant.
- WhatsApp contact verification: provider is **OPEN-009**; keep interface replaceable and do not claim OTP verification is mandatory until decided.
- Cloudflare Tunnel is the network publication boundary for Go VPS. Tunnel/CORS/OIDC/WebSocket configuration must be validated in actual environment, not inferred from this drawing.
- Redis failure may degrade non-authoritative cache/realtime support but must not cause silently inconsistent stock/payment posting.

## 10. Product capability and entitlement relationship

- A **Product** is a commercial presentation/entitlement bundle. A **Module/Submodule/Feature** identifies capabilities; **canonical IDs are domain-centric**.
- Shared capability can be included by POS, Retail or Warehouse without creating duplicate backend engines or charging the same tenant twice for the same canonical entitlement.
- Capability registry, tenant entitlement, per-actor permission, and feature rollout flag are four separate checks. A feature being visible or flagged on does not imply permission.
- POS needs minimum Catalog/Locations/Inventory capability without full Warehouse subscription; Warehouse works without POS subscription; Retail may include Cashier through Sales entitlement.
- 3 P1 candidates (`sales.cash_sessions`, `customers.directory`, `partners.suppliers`) are **not among the 40 P0 conceptual submodules** and must not be added to pilot implementation without authorization.
- Prices, product tier matrix, quotas and exact per-feature entitlement mapping are **OPEN-010 / OPEN-011**.

## 11. Observability, quality gates and release safety

### 11.1 Minimum evidence for architecture review

For **material architecture changes, cross-domain writes, or security-sensitive source changes**, capture the relevant evidence below. For a small, isolated change, use only the checks relevant to its impact; do not require a separate dossier or ceremonial plan for every edit:

1. CAP/FR/AC and approval state; concrete affected business capability.
2. Verified source baseline (`git status`, dependencies, target Go version/tools, route/tests/migrations currently present), without exposing secrets.
3. Module owner, allowed cross-module dependencies and transaction boundary.
4. Security review: tenant leak, data-field leak, impersonation, stale offline grant, replay/idempotency and privilege escalation.
5. Test plan including negative authorization, concurrency and rollback, offline/reconnect, audit and notification effects.
6. Exact verification results, including checks that are BLOCKED or not executed.

### 11.2 Required types of tests (commands NOT VERIFIED)

| Test category | Important assertions |
|---|---|
| Domain/use case | State transitions, eligibility, approvals, reversals, tenant/capability scope |
| Concurrency/integration | Double-sell/double-post prevention; transaction rollback, in-transit and batch correctness |
| API/contract | Authentication context, input/error/pagination contracts, rate/permission boundaries |
| Security negative | Cross-tenant access denied; HPP omitted for cashier; CSRF/session guard; Admin API isolation |
| Offline | PIN bounded offline; queue retry/idempotency, storage loss, shortage and suspended tenant cases |
| Realtime/audit | Event only after commit, reconnect/dedupe; login audit queue ack then removal, 7-day server retention |
| Architecture | Forbidden imports, dependency cycles, one owner per write, static-only deployment checks |
| Database evolution | Versioned migration consistency, prior-schema upgrade/compatibility, tenant isolation, constraints, backfill idempotency and lock/performance risk (on disposable DB) |
| User acceptance | PRD baseline UAT-01–10 and `FUNCTIONAL_SPEC.md` UAT-HV-01–11 where applicable; UAT-HV-11 recovery remains BLOCKED by OPEN-004 |

**Do not claim PASS unless run against the correct verified source and disposable test environment.** PRD's performance targets are *pilot goals*, not measured results. Backup and restore must be exercised before production readiness.

### 11.3 Task boundaries and delivery references

Follow [Backend Agent Instructions](../AGENTS.md) for scoped execution, verification and high-impact actions. **This architecture document does not impose a fixed approval ceremony or a universal patch/ZIP delivery format**; authorization and deliverables are governed by the **applicable project-level rules and the actual user task**. Do not assume this paragraph overrides stricter project instructions. No commit, push, execution of persistent migrations, or deployment without authorization specific to that action/target. **This document alone authorizes no source, database or infrastructure changes.**

## 12. Open architecture decisions and required follow-up documents

| Tracker | Still unresolved; must not be chosen silently | Dependency |
|---|---|---|
| `OPEN-001` | Offline authorization TTL, KDF/lockout effectiveness and device credential persistence | `SECURITY.md`, offline design |
| `OPEN-002` | Costing/HPP method, valuation, historical snapshots | `DATABASE.md`, inventory/business rules |
| `OPEN-003` | Approval presets and sensitive price-change approvals | `FUNCTIONAL_SPEC.md`, `SECURITY.md` |
| `OPEN-004` | Owner recovery evidence and Superadmin break-glass | `SECURITY.md` |
| `OPEN-006` | Retention of critical audit and clock-skew/loss handling | `SECURITY.md`, `DATABASE.md` |
| `OPEN-007` | Suspended/revoked tenant/device handling of pending offline sales | offline reconciliation design |
| `OPEN-008` | Entitlement expiry/grace and pending-operation policy | Platform + Offline design |
| `OPEN-009` | WhatsApp contact verification requirement/provider | integration adapter |
| `OPEN-010` | Product tier/price/quota matrix | commercial product planning |
| `OPEN-011` | Final feature IDs/permission actions, API and schema contracts | `API_CONTRACT.md`, `DATABASE.md` |
| `OPEN-012` | Final numerical NFR/product KPIs based on pilot tests | `TESTING.md` |
| `OPEN-013` | PWA storage loss, lost-device recovery, pending sale recovery | offline & operational runbook |
| `OPEN-014` | Whether bottles, caps and labels are inventory SKUs, and any auxiliary material costing | inventory/costing business review |
| `OPEN-015` | Domains, cookies/CSRF/OIDC redirect, Cloudflare & Tunnel config | deployment/security design |
| `OPEN-016` | Full FR/AC review and supersession/traceability closure | `FUNCTIONAL_SPEC.md` review |

`OPEN-005` (hosting provider allocation) is **CLOSED**: Landing on Pages; Hi-Ve App and Superadmin on Workers Static Assets.

**Suggested documentation order after architecture:** `SECURITY.md` (sessions, tenant/field authorization, device/PIN), `DATABASE.md` (domain ownership, numeric precision, stock ledger/transaction boundaries), `API_CONTRACT.md` (App/Admin separation, operation/response/error contracts), `TESTING.md` (FR/AC/UAT traceability, CI/quality gates), and optional `OPERATIONS.md` (Cloudflare/VPS/backup/recovery). Suggested filenames are not assumed to exist.

## 13. Architecture decision and verification log

| Item | Status | Evidence or next action |
|---|---|---|
| One Go Screaming Modular Monolith | **APPROVED** | PRD CAP-18, `backend/AGENTS.md` |
| Twelve logical domains / forty canonical submodules | **APPROVED at registry level** | PRD §07.7; not one Go package per submodule |
| One PostgreSQL authoritative data layer | **APPROVED** | PRD §04/07 |
| Logical database ownership and single authoritative data source | **APPROVED principle** | PRD §04/05/07; §9.2.1 |
| Modular Database Architecture + Evolutionary Database Design | **ENGINEERING GUARDRAIL (documented 19 Nov 2026)** | §9.2; versioned, compatible migrations; no approval of DDL |
| Product-specific UI over shared domain logic | **APPROVED** | CAP-01–05, CAP-27, CAP-43–46 |
| Inventory sole stock posting owner | **APPROVED principle** | PRD §07.8; FR-STK / FR-REP / FR-TRF |
| Sales business lifecycle + Inventory stock contract | **APPROVED principle** | PRD §07.8; FR-POS / FR-RET |
| Separated platform and tenant auth, Google OIDC | **APPROVED** | CAP-24/33/34 |
| Multi-outlet device with one active operational outlet | **APPROVED** | CAP-35–39 |
| PostgreSQL 7-day operational login history | **APPROVED** | FR-HV-AUD-001 |
| Sensitive data field authorization; no special HPP column encryption | **APPROVED** | CAP-55–58 |
| Landing Pages; App/Admin Workers Static Assets | **APPROVED** | PRD §06.4 |
| Exact package tree, sync/outbox implementation, DB isolation technique, library versions | **DESIGN RECOMMENDATION / NOT VERIFIED** | Inspect actual repository and obtain scoped technical approval |
| OPEN product policies listed above | **OPEN** | Resolve through approved product/technical decision before implementing affected behavior |

---

**Usage instruction for AI coding agents:** Read this file alongside [PRD](../../PRD.md), [Functional Specification](../../FUNCTIONAL_SPEC.md), and [Backend Agent Instructions](../AGENTS.md), then inspect the real source and relevant approved contracts. Trace each requested change to applicable FR/AC, verify the owning domain, and **stop rather than inventing a solution for OPEN policy or missing baseline**. Nothing in this file authorizes file edits, schema migrations, deploys, or new versions.

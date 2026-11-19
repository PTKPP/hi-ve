# Hi-Ve Backend — Agent Instructions

> **Location:** `backend/AGENTS.md` · **Scope:** `backend/**` only.  
> **Status:** Repository guidance (proposed until checked against the actual Hi-Ve Go source).  
> **Authority:** [PRD](../PRD.md) for approved product decisions; [Functional Specification](../FUNCTIONAL_SPEC.md) for relevant FR/AC; [Backend Architecture](docs/ARCHITECTURE.md) for architecture and ownership. Existing source and tests describe implemented behavior, not necessarily all approved requirements.  
> **Important:** This is **not** an implementation request or evidence that the Hi-Ve backend repository has been inspected. Do not assume package names, API routes, test commands, migrations or versions from examples.

## 1. Working in this repository

- **Follow the user's actual task.** A review, audit, diagnosis or planning request is read-only. An explicit implementation request authorizes only its stated scope; do not require a second ceremonial plan or approval for the same scope.
- Before making changes, inspect the relevant current source, `git status`, dependencies and existing tests. Preserve unrelated local changes. Never reset, stash, overwrite or discard someone else's work to simplify a task.
- Make the smallest coherent change that fulfills the request. Do not broaden scope into other frontends, backend domains, API contracts, schema/migrations, Docker, Cloudflare, deployments, environment settings, documentation or versions without authorization.
- **High-impact actions require specific approval**, independently of permission to edit code: commit, push, open/merge PRs, deploy, modify production infrastructure, alter real secrets, run migrations against persistent databases, reset data or seed/provision real accounts.
- If the request depends on missing source, incompatible requirements or an unresolved product decision, explain the blocker and propose a minimal clarification. Do not invent approvals or silently change business rules.
- Do not expose secrets, OAuth material, PINs/verifiers, access tokens, private URLs, sensitive business data or production records through logs, tests, reports or generated artifacts.

## 2. Requirements and contracts: use what is relevant

- Treat the **latest approved decision** in `../PRD.md` as product authority. Use `../FUNCTIONAL_SPEC.md` for applicable requirement IDs and acceptance criteria; respect supersession rules and OPEN / REVIEW REQUIRED markers.
- Inspect existing API/schema contracts **if they exist and the task affects their interface**. Published contracts must not be silently broken. If an endpoint or contract has not been specified, do not create or claim one merely to match an example in documentation.
- Trace the **affected use case** sufficiently to understand its inputs, caller/tenant scope, owning domain, state/database effects and tests. Full CAP → FR → AC → API → database traceability is useful for new cross-domain behavior or contract changes, **not a mandatory report for every small edit**.
- Distinguish **APPROVED**, **OPEN**, **SPEC-ONLY**, **IMPLEMENTED (verified)** and **NOT VERIFIED** when relevant. Existing implementation is not automatically the approved future behavior; a desired requirement is not automatically implemented.
- When approved requirements, published contracts, implementation or tests disagree, report the concrete conflict rather than weakening tests, changing a contract without authorization or guessing which behavior to implement.
- Public Go interfaces across domains are **internal architectural contracts**; do not confuse them with an approved HTTP/OpenAPI contract that may not yet exist.

## 3. Go Screaming Modular Monolith

- Keep **one deployable Go backend**, organized around business capabilities and use cases, not primarily routes, controllers, ORM models or generic technology-layer folders.
- Preserve logical domain ownership across **Platform, Identity, Devices, Settings, Audit, Catalog, Locations, Inventory, Sales, Reports, Offline and Realtime**. The **40 canonical submodules** are capability IDs, **not an instruction to create 40 Go packages**.
- Follow the *actual verified repository structure*. Proposed `internal/<domain>/` layouts are examples, not automatic migration tasks. Do not split or rename modules merely for style.
- Keep transport handlers thin; validation, state transitions and transaction orchestration belong to the owning domain use case. Use explicit public cross-domain interfaces; do not access another domain's private repositories, create dependency cycles or duplicate the same business engine.
- **Inventory** owns stock balances, immutable stock movement ledger, batch traceability, receiving, repacking, transfer and inventory reconciliation. **Sales** owns checkout, transactions, payments, returns, voids and receipt lifecycle; its stock effects go through Inventory's owned interface. **Reports** reads/projections; **Realtime** publishes committed events; **Offline** coordinates sync/reconciliation without becoming a parallel stock authority.
- **Platform** owns tenant/entitlement lifecycle; **Identity** owns users, authentication, PIN and authorization; **Devices** owns activation, multi-outlet assignment and device/operator context; **Audit** owns audit event records.
- PostgreSQL is authoritative for transactions, stock, users, entitlements and audit. Redis, if used, is supporting infrastructure, not the durable source of business truth.

### 3.1 Database architecture guardrails

- **MUST follow Modular Database Architecture + Evolutionary Database Design** in [Backend Architecture §9.2](docs/ARCHITECTURE.md): one authoritative PostgreSQL data layer, logical ownership by business domain, and safe incremental schema evolution. This does **not** mandate a database or physical PostgreSQL schema per module/product.
- **MUST preserve one owning domain per table/dataset.** Only the owning module mutates its authoritative data; other modules use approved use-case contracts. Cross-domain reads use controlled interfaces or authorized read models, not private repositories; no duplicated source of truth for POS, Retail or Warehouse.
- **MUST protect tenant-scoped data and transaction invariants** across queries, indexes/constraints and cross-domain operations. Apply backend-derived tenant/location context; keep critical Sales–Inventory writes atomic, concurrency-safe and idempotent.
- **MUST use reviewed, versioned, backward-compatible migrations.** For breaking changes use **Expand → Migrate → Contract**, with compatibility and backfill verification before retiring the old representation. Do not silently drop/rename columns, rewrite posted history, or introduce unmanaged schema changes.
- **MUST assess migration safety** for data volume, locks, index/query performance, referential integrity, backfill retries, deployment ordering and recovery. Test relevant upgrade/compatibility paths on disposable data. Physical schemas, PostgreSQL RLS and migration tooling remain design choices until verified/approved; never run persistent/production migrations without specific authorization.

## 4. Domain and security invariants

- Enforce tenant isolation, authenticated actor/session, entitlement, granular permissions, location scope and device/operator context on the **backend**, as applicable. Deny by default; hiding a menu or configuring CORS is not authorization.
- Superadmin and Primary Owner use **direct Google OIDC** with explicit provisioning/account linking and separate platform/tenant authorization contexts. Google authentication alone is not authorization; do not introduce Auth0 or a new login mechanism without an approved requirement.
- Staff V1 use an **Owner-assigned individual six-digit PIN** on Owner-activated shared devices. Devices belong to one tenant, may allow multiple outlets, but have **one active outlet per operational session**. Operator assignment never expands actual permissions.
- Offline PIN use requires previously authorized, limited local verification; plaintext PIN must never be stored. **Offline authorization TTL and crypto parameters remain OPEN**, not automatically 72 hours. Device revocation takes effect on the server first; plan for offline delay.
- Offline-first in V1 covers approved **core cash-sales** only. Posting receiving, repacking, transfer, adjustment and stock count is online-only. Do not lose, double-post or silently discard an offline sale because of retries, shortages, revocation or connectivity failure.
- Business writes affecting sale and stock must be atomic, concurrency-safe and idempotent. Keep posted transaction/movement history; rectify through controlled reversal or adjustment, not silent edits/deletes.
- Transit stock is not saleable. Partial transfer receipts and discrepancies must remain traceable. V1 repacking uses **one source batch and one output SKU** with actual measured input/output.
- Use decimal-safe money/quantity calculations. Standard units **kg↔g** and **L↔mL** are allowed; do not infer mass↔volume factors. Do not choose an HPP/costing method while the relevant PRD decision is OPEN.
- Treat HPP, margin, supplier cost and stock valuation as **Confidential**. Authorize fields and minimize API, export, report, realtime and offline-cache payloads. V1 does **not** require special HPP field/application encryption, but still needs proper database access, secrets, secure transport and backups.
- Operational login history is retained **seven days in PostgreSQL**. When offline, IndexedDB is only a temporary event queue: delete a queued event after acknowledged server persistence. Security-critical audit follows its separate retention policy, which is still OPEN.
- Emit realtime business events only after successful transaction commit. Keep server-confirmed and local-pending sales distinguishable.

## 5. Frontend and infrastructure boundaries

- Landing is static on **Cloudflare Pages**; Hi-Ve App PWA and Superadmin are on **Workers Static Assets**. Backend Go runs on VPS, exposed through the authorized HTTPS/WSS and Cloudflare Tunnel configuration.
- Business APIs, sessions, permissions and stock logic run in Go, **not** in Pages Functions, Workers script or edge middleware in V1. Static assets must not be confused with billable/executable Worker requests.
- Inspect actual deployment configuration before suggesting routing, cookie, CSRF, OIDC, CORS or WebSocket changes. Do not infer origin URL, credentials or security posture from this file.
- An available static frontend does not imply an available backend; IndexedDB is not guaranteed to survive browser data eviction or device loss.

## 6. Verification and task completion

- Validate the **affected behavior**, choosing the smallest relevant tests first, then broader checks when impact justifies them. Inspect the actual Go tooling, CI scripts and environment rather than inventing command names.
- For sensitive changes, cover tenant/field authorization, device and operator scope, posting idempotency, stock concurrency, rollback, offline sync/reconciliation, audit redaction and event-after-commit as applicable.
- Run database/destructive tests only on a verified disposable test environment; never on a persistent/shared/production target without explicit approval.
- Keep changes reviewable. Report **what changed, where, which checks actually ran (PASS/FAIL/BLOCKED), remaining risks, and any required migration/deployment steps**. Never claim tests passed without running them.
- **Delivery format is task-specific**, not a universal coding instruction. Provide patches, archives, PRs or other artifacts when requested or required by the project's applicable delivery agreement. This file alone does not require producing a patch and full-source ZIP for every task; it also does not override any higher-level delivery obligations.

## 7. Read only the documentation needed for the task

- [PRD](../PRD.md) — approved decisions, scope, supersession and OPEN tracker.
- [Functional Specification](../FUNCTIONAL_SPEC.md) — FR/AC, capability ownership and UAT references; derived/review-required ACs are not automatically approved.
- [Backend Architecture](docs/ARCHITECTURE.md) — conceptual domain ownership, dependency/transaction boundaries and remaining architecture decisions. Examples are not evidence of an implemented Go structure.
- Actual source, Go tooling, migrations, tests and CI — verify what exists before proposing commands or modifications.

**This document governs behavior within `backend/**`; it does not grant permission to modify or deploy the application.**

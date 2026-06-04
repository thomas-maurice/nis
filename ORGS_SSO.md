# Organizations + OIDC SSO — Implementation Plan

Status: **DESIGN — awaiting approval.** Pre-reviewed by the Plan review agent (2026-06-03); review findings folded in below and tagged `[review:Bn/Sn/Nn]`.

This document is the forward-planning spec for two intertwined features:

1. **Organizations** — a top-level tenant ("super entity") that owns operators, and transitively their clusters/accounts/users. Adds a new `org-admin` role.
2. **OIDC SSO login** (OpenID Connect only, e.g. Authentik) — per-org SSO so a user can log into their own org's NIS view via their IdP.

It is written to be split into delegatable agent sub-tasks (see **§7 Chunked plan**). Opus directs/integrates; Sonnet sub-agents implement per-chunk.

---

## 1. Goals & non-goals

**Goals**
- Organization is the new mandatory root of the identity tree: `Organization → Operator → Account → User` (+ `Organization → Operator → Cluster`).
- New role `org-admin`, scoped to one organization. Can: configure that org's SSO, CRUD local (username/password) api_users within the org, create/revoke **service-account API tokens** (`nis_pat_`) scoped to the org, manage the org's group→role mappings, and manage everything beneath the org (operators read; accounts/users/clusters/templates/webhooks/backups CRUD within the org).
- **Both local management-plane credential types remain creatable per-org:** (a) username/password `api_users` (break-glass + interactive), and (b) long-lived service-account API tokens (P8 `nis_pat_`, for CI/automation). An org-admin manages both within their own org; the role-ceiling and org-match guards apply equally to both.
- Platform `admin` is unchanged in spirit: super-admin **above** all orgs. Creates/deletes orgs, break-glass, sees everything. Platform admins are org-less (`organization_id = NULL`).
- OIDC-only SSO. Group-mapping is authoritative: the user's role+scope is recomputed from IdP group claims on every login.
- Local username/password login stays as break-glass for every role.

**Non-goals (v1)**
- SAML. (OIDC only.)
- Organization as a `pkg/manifest` kind. **Deferred to v1.1** `[review:B4]` — keeps `example/manifests/*.yaml` + `examples_test.go` green. Manifest-applied operators land in the **default org** (see §5.4).
- Per-org S3/KMS routing, org-level billing, org-level quotas.
- httpOnly-cookie session delivery (v1 keeps the existing localStorage + Bearer model; see §4.6).
- Manual role management of **OIDC-sourced** users (their role is governed by mappings; see §3.3).

---

## 2. Locked decisions

| # | Decision |
|---|----------|
| D1 | Orgs are **mandatory root**. A migration creates one `default` org and backfills all existing operators + api_users into it. New operators must reference an org. |
| D2 | Platform `admin` = super-admin above orgs (org-less). `org-admin` scoped to one org. Local login stays as break-glass. |
| D3 | OIDC role/scope assignment is **group-mapping authoritative + default fallback**. Role recomputed from IdP groups each login. Org-admin manages the mappings, not individual OIDC users. OIDC mappings can **never** assign platform `admin`. |
| D4 | Login routing: user **types an org slug** (no dropdown, no org-list discovery endpoint) to avoid leaking org names. Generic errors + rate limiting blunt slug-existence enumeration. |
| D5 | `?org=<slug>` **triggers the OIDC flow immediately** — both the SPA route `/login?org=foo` (auto-redirect, no click) and the direct deep link `/auth/oidc/start?org=foo`. |
| D6 | Approved new Go deps: `github.com/coreos/go-oidc/v3` + `golang.org/x/oauth2`. No new npm deps. |
| D7 | `organization_id` is a stored column **only** on `operators`, `api_users`, and `api_tokens` (the three management-plane roots). `operators` is the tenancy anchor for all identity-tree data (accounts/users/clusters/templates/webhooks/backups derive their org via the operator they hang off — mirrors account-admin scope today). `api_users` + `api_tokens` carry it directly because a credential can be org-scoped without pointing at any single operator. `[review:SC1 — confirmed sound; api_tokens added for per-org service accounts]` |

---

## 3. Architecture

### 3.1 Data model

New tables:

- **`organizations`** — `id` (TEXT PK), `name` (unique), `slug` (unique; `[a-z0-9-]`, used for SSO routing), `description`, `created_at`, `updated_at`.
- **`organization_sso_configs`** — 1:1 with org. `organization_id` (FK CASCADE, unique), `enabled` (bool), `issuer_url`, `client_id`, `encrypted_client_secret` (storage ref via the existing ChaCha20-Poly1305 encryptor — `encrypted:<key_id>:<base64>`, same as operator seeds), `scopes` (default `openid profile email groups`), `group_claim` (default `groups`), `default_role` (nullable TEXT; NULL ⇒ deny login when no group matches), `created_at`, `updated_at`.
- **`organization_sso_role_mappings`** — `id` (TEXT PK), `organization_id` (FK CASCADE), `group_value` (TEXT), `role` (TEXT; one of `org-admin`/`operator-admin`/`account-admin` — **never** `admin`), `scope_operator_id` (nullable; required for the mapped operator-admin), `scope_account_id` (nullable; required for account-admin), `priority` (int; lower = evaluated first, **first match wins**), `created_at`. Unique `(organization_id, group_value)`.
- **`oidc_login_states`** — `state` (TEXT PK, high-entropy), `organization_id`, `nonce`, `pkce_verifier`, `redirect_after` (nullable), `created_at`, `expires_at`. Durable (not in-memory) so the flow survives multi-replica + restarts. Swept by a new recurring job (§3.4).

Altered tables:

- **`operators`** — add `organization_id` (FK → organizations.id). **NOT NULL after backfill** `[review:B3 — see §5.1 for the SQLite hazard]`. FK on-delete: `RESTRICT` at the org level is enforced in the service layer (deleting an org with operators fails with a friendly preflight); the GORM constraint can be `CASCADE` to keep delete semantics simple, but the service refuses org-delete-with-operators — match the existing operator/cluster pattern (`CanDeleteOperator` fails if clusters exist).
- **`api_users`** — add:
  - `organization_id` (FK → organizations.id, **nullable**: platform admin = NULL; org-scoped roles set it).
  - `auth_source` (TEXT NOT NULL DEFAULT `'local'`; `'local'` | `'oidc'`).
  - `external_subject` (TEXT nullable; the OIDC `sub`).
  - `email` (TEXT nullable).
  - **Partial unique index** `(organization_id, external_subject) WHERE auth_source='oidc'` `[review:S3]` — must be hand-added to **both** dialect migration files and registered in `tools/atlas/loader.go manualExtras` (atlas-provider-gorm cannot emit partial indexes from struct tags). A plain composite unique is insufficient: NULLs are distinct on both SQLite and Postgres, so it would not prevent duplicate OIDC rows.
  - **CHECK** (via GORM struct tag so atlas emits it): `auth_source <> 'oidc' OR (external_subject IS NOT NULL AND organization_id IS NOT NULL)`.
- **`api_tokens`** — add `organization_id` (FK → organizations.id, **nullable**: platform-admin tokens = NULL; org-scoped service-account tokens set it). Set at create time from `CanCreateAPIToken`'s org-ceiling check; the existing `created_by_user_id` FK stays `ON DELETE SET NULL`, so the token's org must be stored on the row rather than derived from its creator (offboarding the creator must not silently de-scope or orphan the token).

New role constant: `entities.RoleOrgAdmin = "org-admin"`.

Role hierarchy (for ceiling checks; **new** — only api_tokens have a `roleRank` today): `admin (3) > org-admin (2) > operator-admin (1) > account-admin (0)`.

### 3.2 Entities

- New: `entities.Organization`, `entities.OrganizationSSOConfig`, `entities.SSORoleMapping`, `entities.OIDCLoginState`.
- `entities.Operator` gains `OrganizationID uuid.UUID`.
- `entities.APIUser` gains `OrganizationID *uuid.UUID`, `AuthSource string`, `ExternalSubject *string`, `Email *string`.
- `entities.APIUserRole` gains `RoleOrgAdmin`; `IsValid()` + a new `RoleRank()` (or a `roleRank` map in `permission_service.go`).

### 3.3 Authz model

Two-layer model is unchanged in shape (coarse `authz.RolePolicy` + `Procedures`; fine `PermissionService.Can*`; SQL scope via `authz.Scope`). Additions:

- **`authz.Scope`** gains `ScopeOrganizationID *uuid.UUID`. `ScopeFromAPIUser` sets it for `org-admin`.
- **Repo scope branches `[review:S4 — enumerate ALL repos]`.** Every repo's `ListPage` **and** `Search` gains an `IsOrgAdmin` case. Checklist (a missing branch is NOT caught by any lint):
  - `operator_repo` → `WHERE organization_id = ?`
  - `account_repo` → `WHERE operator_id IN (SELECT id FROM operators WHERE organization_id = ?)`
  - `cluster_repo` → same subquery as accounts
  - `template_repo`, `webhook_subscription_repo`, `operator_backup_repo`, `operator_age_recipient_repo` → same subquery (direct `operator_id`)
  - `api_token_repo` → org subquery **AND** the existing per-caller "only my tokens" rule (the two must AND, not replace)
  - `user_repo` → nested EXISTS through `accounts → operators` (same depth operator-admin already pays)
  - `scoped_signing_key_repo` → nested EXISTS through `accounts → operators` (**reviewer flagged this was missing from the first draft's table**)
  - `api_user_repo` → `WHERE organization_id = ?` for the org-scoped user-management listing
- **`PermissionService`** gains `ownsOrganization(ctx, apiUser, orgID)`:
  - `admin` → true
  - `org-admin` → `apiUser.OrganizationID == orgID`
  - lower roles → their operator/account's org == orgID (read paths only)
  - `ownsOperator` / `ownsAccount` are extended so an org-admin owning the parent org passes.
  - New `Can*` for the `organization` and `sso` resources (`CanCreateOrganization` = admin only; `CanReadOrganization`; `CanUpdateOrganization` = admin or org-admin-owns; `CanDeleteOrganization` = admin only; `CanManageSSO` / `CanReadSSO` = admin or org-admin-owns).
  - api_user `Can*` (new — see §3.3.1).
  - **api_token (P8) org-scoping `[service accounts]`.** `CanCreateAPIToken` already enforces a `roleRank` ceiling + `ownsOperator`/`ownsAccount` for the token-creator today; extend it so an org-admin may mint tokens whose scope (`organization_id`, and any operator/account scope) falls **within their own org**, and may not mint a token whose role rank ≥ their own. The new `ScopeOrganizationID` carries through to the token row so a service-account token authenticates as an org-scoped synthetic APIUser (middleware already sets `authctx.Actor{Type: ActorTypeAPIToken}`; the synthetic user simply gains the org id). `CanRead/DeleteAPIToken` keep the existing per-caller "only my tokens" rule **AND** add the org-subquery narrowing (the two AND, per §3.3 `api_token_repo`). Chained-token guard is unchanged: a token cannot mint another token.
- **Registry** (`internal/application/authz/registry.go`): new `organization` and `sso` resource constants; new `RolePolicy` grants. org-admin grant set: `organization` read/update (own), `sso` create/read/update/delete (own), `api_user` create/read/update/delete (own org), `operator` read, `account`/`user`/`cluster`/`scoped_key`/`template`/`webhook`/`backup` CRUD (own org), `search` read, `export` read. `Procedures` rows for the new RPCs are added **with their handlers, atomically** `[review:SC3]`.

#### 3.3.1 api_user management rewrite `[review:B1 — blocker]`

`AuthService.CreateAPIUser`, `GetAPIUser`, `GetAPIUserByUsername`, `ListAPIUsers`, `UpdateAPIUserPassword`, `UpdateAPIUserPermissions`, `DeleteAPIUser` currently begin with `if requestingUser.Role != RoleAdmin { deny }`. There is **no role-ceiling logic** for api_users today. This is a from-scratch rewrite of the most security-sensitive surface, hence its own chunk (§7 chunk 5):

- Replace the inline admin gate with `PermissionService.Can{Create,Read,Update,Delete}APIUser`.
- **Role ceiling:** a caller may not create/modify a user whose role rank ≥ the caller's (org-admin cannot mint admin or another org-admin above themselves; enforce via `RoleRank`).
- **Org match:** org-admin can only manage api_users with the same `organization_id`; cannot set a user's org to another org; cannot create platform (org-less) admins.
- **OIDC-row guard `[review:N3]`:** `UpdateAPIUserPermissions` (and password) on a row with `auth_source='oidc'` returns `CodeFailedPrecondition` — its role is governed by mappings and any manual edit would be clobbered on next login. Org-admins manage OIDC users via mappings; manual CRUD is for `local` rows only.

### 3.4 OIDC login flow (auth-code + PKCE, S256)

HTTP handlers (**not** Connect-RPC), placed in `internal/infrastructure/oidc/` or an `*_http.go` file — **never** in `grpc/handlers/` and never with a Connect-shaped signature, so the AST lints don't misfire `[review:N1]`:

- **`GET /auth/oidc/start`** — query `org=<slug>` (required), optional `redirect=<ui-path>`.
  1. Resolve slug → org → enabled SSO config. On miss/disabled: **generic 400 + jittered delay** to blunt timing/enumeration `[review:S5]` (a valid slug does IdP discovery → network round-trip; an invalid slug fails fast on the DB — normalize the latency).
  2. go-oidc discovery on `issuer_url` (cached); build `oauth2.Config`; generate `state`, `nonce`, PKCE verifier/challenge.
  3. Persist `oidc_login_states` row (TTL ~10 min).
  4. 302 to the IdP authorization endpoint.
- **`GET /auth/oidc/callback`** — query `code`, `state`.
  1. Load + delete the `oidc_login_states` row by `state`; reject if missing/expired.
  2. Exchange `code` for tokens with the PKCE verifier.
  3. Verify the ID token: signature via JWKS, `iss == issuer_url`, `aud` contains `client_id`, `exp`, and `nonce` match. (go-oidc `IDTokenVerifier`.)
  4. Extract `sub`, `email`, `preferred_username`, and the `group_claim` array.
  5. Map groups → (role, scope) via the org's ordered mappings; first match wins; else `default_role`; else **deny** (generic error).
  6. **JIT find-or-create** the api_user by `(organization_id, external_subject)`; sync `role`, `email`, `username`, scope fields; `auth_source='oidc'`. Shared helper with §3.3.1.
  7. Mint a NIS session JWT via a **new `AuthService.IssueSessionForUser(user)`** (refactor `generateToken` to be callable without a password).
  8. 302 to the SPA callback route with the NIS JWT in the **URL fragment** (`#token=...`); the SPA stores it exactly like a password login.

**Verifier is behind an interface** so unit tests inject a fake (no live IdP in CI) `[review]`. One optional Dex / `oidc-server-mock` testcontainer e2e exercises the full redirect round-trip (marked optional; not a CI gate).

**Audit `[review:B2]`:** the callback has no `authctx` user when it provisions. Emit `api_user.sso_provisioned` (JIT create) and `api_user.sso_login` via `events.EmitSystem`, carrying `{organization_id, subject, email, username, role, groups}` in the payload (subject identified explicitly in payload since there is no actor row to attribute). No new `ActorType` is required — subsequent authenticated requests carry the NIS session JWT and resolve to `ActorTypeUser` as normal.

### 3.5 Config

- New `server.public_url` (env `SERVER_PUBLIC_URL`) — the externally-reachable base URL used to build the OIDC `redirect_uri` (`<public_url>/auth/oidc/callback`). Document in README + `config.example.yaml`.
- New `oidc.state_ttl_seconds` (default 600) and `oidc.state_sweep_interval_seconds` (default 900).

---

## 4. Security considerations

1. **PKCE (S256) + state (CSRF) + nonce (replay)** are all mandatory. ID-token validation covers `iss`/`aud`/`exp`/signature.
2. **Client secret at rest** is encrypted via the existing encryptor (storage ref), never returned by any read RPC (redacted like other secrets; mirror the running-config redaction list).
3. **No privilege escalation via OIDC:** mappings are constrained to `org-admin`/`operator-admin`/`account-admin`; assigning platform `admin` is rejected at mapping-write time and again at login-map time.
4. **Slug enumeration `[review:S5]`:** no org list is ever exposed; `/auth/oidc/start` returns a uniform generic error with jittered timing for unknown/disabled slugs. Residual existence-leak is acknowledged and acceptable for a tenant-admin tool (not consumer SaaS).
5. **Token-in-fragment `[review:S1]`:** consistent with the existing localStorage+Bearer model (same XSS class). The SPA callback route MUST call `history.replaceState` to strip the fragment immediately. Referer/history exposure documented. (httpOnly cookie is the v1.1 hardening path.)
6. **Break-glass:** local admin login always works even if every org's SSO is misconfigured.
7. **Rate-limit** `/auth/oidc/start` and `/auth/oidc/callback`.

---

## 5. Known hazards & migration notes

### 5.1 The NOT-NULL backfill on `operators.organization_id` `[review:B3]`
SQLite cannot `ADD COLUMN ... NOT NULL` without a constant default, and cannot `ALTER COLUMN SET NOT NULL` — the only path is the 12-step table rebuild (`CREATE new / INSERT SELECT / DROP / RENAME`). The data backfill (create `default` org, set every operator + org-scoped api_user to it) is **not** something `atlas-diff` generates — it is a hand-edit to **both** dialect files. Plan:
1. Create `organizations` (+ SSO tables).
2. `INSERT` the `default` org (fixed UUID or deterministic).
3. Add `operators.organization_id` nullable + `api_users.organization_id` nullable + the other api_users columns + `api_tokens.organization_id` nullable.
4. `UPDATE operators SET organization_id = <default>`; `UPDATE api_users SET organization_id = <default> WHERE role <> 'admin'` (platform admins stay NULL); `UPDATE api_tokens SET organization_id = <default> WHERE created_by_user_id IN (SELECT id FROM api_users WHERE role <> 'admin')` (platform-admin-minted tokens stay NULL).
5. Set `operators.organization_id` NOT NULL — Postgres `ALTER ... SET NOT NULL`; SQLite table-rebuild (Atlas emits it; verify).
6. Register any hand-added partial index / CHECK DDL in `tools/atlas/loader.go manualExtras` so the next `atlas-diff` doesn't drop it.

This is the single trickiest migration; it gets a **dedicated round-trip migration test** in chunk 1 (apply on a seeded pre-migration DB for both dialects, assert backfill + NOT NULL hold).

**Lower-risk alternative (call if the rebuild proves painful):** keep `operators.organization_id` nullable with an app-layer invariant (`CreateOperator` always sets it) + a deferred NOT-NULL migration in a later release. Decision deferred to chunk-1 implementer with a note back to Opus.

### 5.2 Chunk ordering vs the build-gate lints `[review:SC3]`
`handler_authz_lint_test.go` fails the build if a handler method lacks a `Procedures` entry **and** if a `Procedures` entry lacks a handler. Therefore:
- **Chunk 2 adds NO new RPCs** — only `authz.Scope`, repo scope branches, `PermissionService` helpers, registry resource constants, and `RolePolicy` grants (grants without `Procedures` rows compile green).
- **Chunk 3 adds the `Procedures` rows and their handlers in the same commit** (atomic).

### 5.3 server.go wiring `[review:N2]`
- Add `/auth/oidc/start` + `/auth/oidc/callback` to the SPA path allowlist (currently hard-lists `/nis.v1*`, `/livez`, `/healthz`, `/readyz`, `/metrics`) or the SPA swallows them.
- Register them on `mux` unconditionally (so they work with `--enable-ui=false`; note e2e can't exercise the SPA-callback redirect leg).
- Add the new `OrganizationService` (and SSO service, if separate) to the static reflector list — the canonical "RPC works but client can't see it" footgun.

### 5.4 Manifest `[review:B4]`
Organization is **not** a manifest kind in v1. Manifest-applied operators inherit the **default org** (or a configurable default-org slug) at apply time. `example/manifests/` + `examples_test.go` are untouched. Adding the `Organization` kind + an `organization` operator-metadata field is a tracked v1.1 follow-up (will require updating all example files in the same change).

---

## 6. Doc-triangle updates (required, per repo rules)
- **README.md** — Organizations concept, org-admin role, SSO setup (Authentik walkthrough), `server.public_url` config, new env vars, `nisctl org`/`org sso` commands, login-with-SSO UX.
- **`.claude/skills/nis-dev/SKILL.md`** — new architecture subsections (org tenancy model, OIDC flow, the api_user authz rewrite, the backfill migration hazard, the repo-scope checklist).
- **CLAUDE.md** — if a new agent rule emerges (e.g. "new repo `ListPage`/`Search` must add an org-admin branch — not lint-caught").
- **SECURITY.md** — updated role/permission matrix (4 roles), SSO threat model.

---

## 7. Chunked implementation plan (agent sub-tasks)

Each chunk is independently reviewable, keeps the build green, and ends with `make build-all` + the relevant tests passing. Chunks are ordered by dependency. `make test-e2e` is mandatory after chunks touching services/grpc/nats/persistence/cmd (1–5).

### Chunk 1 — Org data model, migration, backfill, entities, repo
**Depends on:** none.
**Scope:** `organizations`, `organization_sso_configs`, `organization_sso_role_mappings`, `oidc_login_states` tables; `operators.organization_id`; `api_users` new columns + partial unique index + CHECK; `api_tokens.organization_id`; the default-org backfill migration (§5.1) for **both** dialects; `manualExtras` registration; entities (`Organization`, `OrganizationSSOConfig`, `SSORoleMapping`, `OIDCLoginState`) + entity field additions; `OrganizationRepository` (CRUD + GetBySlug) and SSO-config/mapping repos; `RoleOrgAdmin` constant + `RoleRank`.
**Out of scope:** any authz/permission logic, any RPC, any UI.
**Acceptance:** `make atlas-diff` clean on both dialects; `make atlas-lint` green; dedicated migration round-trip test (seeded pre-migration DB → migrate → assert backfill + NOT NULL) passes on sqlite **and** postgres; `make build-server` + `make test` green.

### Chunk 2 — Authz plumbing (no new RPCs) `[review:SC3]`
**Depends on:** 1.
**Scope:** `authz.Scope.ScopeOrganizationID` + `ScopeFromAPIUser`; the org-admin branch in **every** repo `ListPage` + `Search` (full checklist in §3.3); `PermissionService.ownsOrganization` + extended `ownsOperator`/`ownsAccount` + new `Can*` for `organization`/`sso`/`api_user`; registry `organization`/`sso` resource constants + `RolePolicy` grants for org-admin. **No `Procedures` rows, no handler methods.**
**Acceptance:** unit tests for scope SQL per repo (org-admin sees only own org's rows; cross-org returns empty); `registry_test.go` deny-rules extended; `make build-all` + `make test` green (lint stays green because no new procedures/handlers).

### Chunk 3 — OrganizationService RPC + SSO config RPC + nisctl + proto + events
**Depends on:** 2.
**Scope:** `proto/nis/v1/organization.proto` (CRUD + SSO config get/set + mapping list/set); `buf generate`; handlers (atomic with their `Procedures` rows `[review:SC3]`); client_secret encryption + redaction; new event types (`organization.created/updated/deleted`, `organization.sso.configured`, `organization.sso.mapping_updated`) wired through `events.EmitTx` (satisfies `handler_emit_lint`); `nisctl org create|list|get|update|delete` + `nisctl org sso set|get` + `nisctl org sso mapping add|list|remove`; `OperatorService.CreateOperator` + `nisctl operator create` gain `--org`; add services to the reflector list.
**Acceptance:** `handler_authz_lint` + `handler_emit_lint` green; `make build-all` + `make test` + `make test-e2e` green; e2e covering org CRUD + cross-org isolation.

### Chunk 4 — OIDC login backend
**Depends on:** 1 (states table), 3 (SSO config rows to read).
**Scope:** `go.mod` add `go-oidc/v3` + `x/oauth2`; `internal/infrastructure/oidc/` (provider+JWKS cache, verifier interface); `internal/application/services/sso_service.go` (start/callback orchestration, claim→role mapping, JIT helper shared with chunk 5, `IssueSessionForUser`); HTTP handlers `/auth/oidc/start` + `/auth/oidc/callback` (outside `grpc/handlers/`, non-Connect-shaped `[review:N1]`); `oidc_login_states` sweep job (`oidc.state_sweep`, recurring, registered before `runner.Run` + startup `EnsureScheduled` priming `[review:S2]`); `server.public_url` config + redirect_uri build; server.go mux + allowlist (§5.3); `AuthService.IssueSessionForUser`; audit events via `EmitSystem` `[review:B2]`.
**Acceptance:** unit tests for claim→role mapping + JIT (fake verifier, no live IdP); states sweep test; `make build-all` + `make test` + `make test-e2e` green; optional Dex e2e behind a build flag.

### Chunk 5 — Org-scoped api_user + service-account token management rewrite `[review:B1]`
**Depends on:** 2 (Can*APIUser, extended CanCreateAPIToken), 4 (shared JIT helper).
**Scope:**
- **api_users:** rewrite `AuthService.CreateAPIUser`/`Get*`/`List*`/`Update*`/`Delete*` to use `PermissionService.Can*APIUser` (replacing the inline `RoleAdmin` gate); role-ceiling via `RoleRank`; org-match enforcement; OIDC-row manual-edit guard (`CodeFailedPrecondition`) `[review:N3]`; org-scoped `ListAPIUsers`; `nisctl api-user create --org/--role org-admin`; `nis user create` bootstrap gains `--org`.
- **Service-account API tokens (per-org):** extend `CanCreateAPIToken` with the org-ceiling + org-match (§3.3), persist `organization_id` on the new token row, and AND the org-subquery into `api_token_repo` `ListPage`/`Search` alongside the existing per-caller rule. The middleware synthetic-APIUser gains the org id so an org-scoped token authenticates within its org. `nisctl apiuser`/token create gains `--org` (admin) and is implicitly own-org for an org-admin caller.
**Acceptance:** `handler_authz_lint` green; e2e: org-admin can CRUD local users **and** create/revoke service-account tokens in own org, cannot touch other orgs (users or tokens), cannot mint admin/org-admin above ceiling, cannot edit oidc-row roles; a token scoped to org A cannot read/act on org B; `make test-e2e` green.

### Chunk 6 — UI
**Depends on:** 3, 4, 5.
**Scope:** platform-admin Orgs list/detail/create views; org-admin SSO config page + role-mapping editor + org-scoped user management (local users **and** service-account API tokens, reusing the existing token-create/reveal-once UI scoped to the org); LoginView "Sign in with SSO" slug entry → navigate to `/auth/oidc/start?org=<slug>`; `/login?org=foo` auto-triggers the flow `[review:D5]`; `/login/callback` route that reads `#token=`, stores it, and calls `history.replaceState` to strip the fragment `[review:S1]`; auth store + nav for `org-admin`; role-gated nav visibility. **No new npm deps.**
**Acceptance:** manual browser walkthrough of password login (regression), SSO login (against dev Authentik/Dex), org CRUD, SSO config, user mgmt; existing UI flows unbroken.

### Chunk 7 — e2e hardening + docs
**Depends on:** all.
**Scope:** consolidate org-isolation + SSO e2e; backfill migration test (if not already in chunk 1); doc-triangle updates (§6); SECURITY.md role matrix.
**Acceptance:** full `make test` + `make test-e2e` green; docs reviewed.

---

## 8. Open items for Opus to confirm during build
- §5.1: NOT-NULL-now vs nullable+deferred for `operators.organization_id` (chunk-1 implementer reports back).
- Whether the SSO config + mappings live on `OrganizationService` or a separate `SSOConfigService` (affects proto/reflector wiring; default: same service to limit surface).
- Dev IdP for the optional full-flow e2e: Dex vs `oidc-server-mock` testcontainer (default: Dex, widely used, OIDC-conformant).

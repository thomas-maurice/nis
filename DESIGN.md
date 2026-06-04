# NIS Design Notes

Consolidated design and decision ledger. Two parts:

- **Part I — Improvement Proposals**: the `A#` / `P#` / `C#` / `E#` decision IDs referenced
  throughout the code and `SKILL.md` (e.g. `DESIGN.md A1`, `P1`).
- **Part II — Organizations + OIDC SSO**: the `§` section anchors referenced from code
  (e.g. `§4.4`, `§5.1`, `security consideration S1`).

Code comments and docs cite this file by those IDs and section numbers — they are
preserved verbatim below from the former `PROPOSALS.md` and `ORGS_SSO.md`.

---

# Part I — Improvement Proposals


Generated 2026-05-13 by three review agents (Product / Architecture / Code Review).

**Legend** — Effort: S (≤1 day), M (1–5 days), L (>5 days). Risk: Low / Med / High.

---

## Decisions

| ID  | Status | Notes |
| --- | ------ | ----- |
| P1  | **Done 2026-05-25** | Audit-log diffs, filter UX, and emit-coverage lint. Diff capture via `events.DiffBuilder` wired into 13 update sites; `events.diff` column in migration `00008_event_diff.sql`. `EventFilter` gains `actor_type`, `actor_id`, `search_q`. Lint test (`handler_emit_lint_test.go`) walks `authz.Procedures` and asserts every Create/Update/Delete mutation reaches `events.EmitTx`/`EmitSystem`; 15 previously unaudited mutations were caught and fixed. New event types: `api_token.deleted`, `api_user.*`, `webhook.subscription.*`. |
| P2  | **Done 2026-05-18** | Per-operator JWT expiry, revocation, and optional auto-renew. New `user_jwt_revocations` table; `UserRevocationService`; `JWTExpirySweeper` (four phases: prune, expiring-soon alert, auto-renew, expired alert). Default policy is all-zero (no expiry) for back-compat. Expired credentials are never auto-renewed — operator must call `RegenerateUserCredentials` explicitly. |
| P3  | **Done 2026-05-22** | SSK rotation. `RotateScopedSigningKey` replaces NKey material in one tx, re-mints all active dependent users, regenerates the account JWT, and returns per-cluster push outcomes inline. Critical fix: revocation timestamp is backdated by 1s to prevent born-revoked JWTs (jwt v2 `iat >= revoked_at`). Plain-signer SSKs refused. |
| P4  | **Done 2026-05-14** | Webhook subscriptions on the A6 event substrate. Operator-scoped CRUD, HMAC-signed HTTP POST, exponential backoff, dead-letter after 5 attempts. `pkg/webhooks` public SDK for receiver verification. Wildcard event-type filter restricted to admin. |
| P5  | **Done 2026-05-18** | Bulk manifest apply (`pkg/manifest/`). kubectl-style Parse → Validate → Plan → Apply/Delete for all five NIS kinds. Reserved names enforced; cluster updates and user scopedKey reassignment blocked in v1. `nisctl apply/dump/delete`; `example/manifests/` CI-gated by `examples_test.go`. |
| P6  | **Done 2026-05-19** | Permission templates (versioned, operator-scoped) plus A13-lite auto-sync. SSKs can be created from or pinned to a template; updates bump `latest_version`, pinned SSKs never auto-cascade. A13-lite: every account/SSK mutation pushes the regenerated account JWT to attached clusters after the tx commits (best-effort, superseded by A13-full). |
| P7  |        |       |
| P8  | **Done 2026-05-18** | Service-account API tokens (`nis_pat_<64hex>`). Stored as `sha256(token)`; returned once. Auth middleware detects the prefix and sets both a synthetic `APIUser` and `authctx.Actor` so audit events record the token, not the minting user. `last_used_at` coalescing flusher; chained-token minting blocked. |
| P9  | **Done 2026-05-19** | Sync drift dashboard. `GetClusterDriftStatus` compares NIS DB vs resolver per account (five statuses: IN_SYNC, DB_AHEAD, OUT_OF_BAND, MISSING_ON_RESOLVER, UNREACHABLE + ORPHAN_ON_RESOLVER). `ReconcileAccountOnCluster` pushes one account to one cluster; cross-operator guard enforced. `AccountService.DeleteAccount` now sends `$SYS.REQ.CLAIMS.DELETE` to all cluster resolvers post-commit. |
| P10 | **Done 2026-05-19** | Live per-cluster JetStream usage via `$SYS.REQ.ACCOUNT.<pubkey>.JSZ`. `NOT_ACTIVATED` status added for JS-enabled accounts that haven't connected yet. No DB writes; UI polls on demand. |
| P11 | **Done 2026-05-19** | Global search across all five identity-tree kinds in one RPC. SQL-level `authz.Scope` enforcement (A20); LIKE queries with `escapeLikeParam`; JSON-escape workaround for scoped-key permission columns. Query: 2–128 chars, limit capped at 100/kind. |
| P12 | **Done 2026-05-20** | Per-operator scheduled S3 backups on the A2 jobs substrate. Two handlers: `backup.sweep` (recurring) and `backup.execute` (per-operator). Format is YAML in `SecretsPlaintext` mode inside an age envelope (changed 2026-05-25 — see P15). `LeaseDuration=15min`. MinIO in docker-compose and `make run` for dev. |
| P13 | **Done 2026-05-19** | Active JWT revocations panel on `AccountDetailView.vue`. New RPC `ListAccountJWTRevocations` exposes active (`pruned_at IS NULL`) revocation rows joined with the user row, surfacing `UserStillFlagged` (user row flagged vs. JWT still in the NATS Revocations map). Badge renamed "Revoked users". |
| P14 | **Done 2026-05-21** | Retention sweep for pruned `user_jwt_revocations` rows. Handler `revocations.retention_sweep` hard-deletes rows with `pruned_at < now - retention_days` (default 90d); batch-capped at 500 via portable `WHERE id IN (SELECT id ... LIMIT N)`. Active rows (`pruned_at IS NULL`) never touched. |
| P15 | **Done 2026-05-25** | Mandatory age encryption for scheduled-backup artifacts. New `operator_age_recipients` table; `BackupService.RunBackup` fails loud when zero recipients are configured (`ErrNoBackupRecipients`). S3 objects suffixed `.age`; sha256 over ciphertext. `nisctl backup keygen`, `backup decrypt`, `restore --identity FILE`. Server never sees private key material. New Go dep: `filippo.io/age`. |
| A1  | **Done 2026-05-13** | `factory.WithTx` transaction abstraction. `CreateOperator`, `DeleteOperator`, `CreateAccount`, and SSK mutations all run inside a single GORM tx. `ExportService.ImportFromNSC` also tx-wrapped. NATS pushes stay outside the tx. |
| A2  | **Done 2026-05-20** | Durable jobs substrate (`jobs` table, TEXT PK, partial unique index for dedup). Driver-aware `ClaimDue`; recurring schedules via per-tick watchdog; panic-safe handler invocation; detached write context post-handler. Admin `JobService` RPC (List/Get/Retry/Cancel). First handlers: `events.retention_sweep`, `jobs.retention_sweep`. |
| A3  |        |       |
| A4  |        |       |
| A5  | **Done 2026-05-23** | Closed cross-tenant leak in `AccountHandler` (Update/UpdateJetStreamLimits/Delete/PushAccountJWT missing `permService.Can*`). Added `handler_authz_lint_test.go` AST guardrail — new handler without registry entry fails build. Spawned A17–A20 as follow-ups. |
| A6  | **Done 2026-05-14** | Append-only `events` table. `EmitTx` (in-tx) and `EmitSystem` (background). `EventService.ListEvents/GetEvent` (admin-only). `EventsRetentionWorker` (daily, 30d default). `authctx` leaf package breaks the import cycle. |
| A7  | **Done 2026-05-22** | Keyset cursor pagination (`ORDER BY created_at DESC, id DESC`) across all list RPCs. `authz.Scope` as mandatory SQL-level parameter on every `ListPage`. Per-resource filters and name-like. UI prev/next with cursor stack. `nisctl` list defaults to fetch-all-pages. |
| A8  | **Done 2026-05-13** | Prometheus `/metrics`, OTel tracing (opt-in), `/livez`/`/readyz`/`/healthz`. otelconnect + otelhttp middleware. Domain gauges via 60s refresh. |
| A9  | **Done 2026-05-13** | Viper flag-default trap fixed via `applyFlagOverrides`. Dead `internal/config` package removed. Config precedence is now flag > env > file > default. |
| A10 |        |       |
| A11 |        |       |
| A12 | **Partial Done 2026-05-13** | NSC import path extracted to `import_nsc.go`; YAML export/import support added; `regenerate_ids` removed (import is a faithful restore). Structural type extraction (`Exporter`/`Importer`/`NSCImporter`) still deferred. |
| A13 | **Done 2026-05-22** | A13-full: every account/SSK/template mutation enqueues per-cluster `cluster.account.push` jobs inside the mutation tx. Staleness-race fix: handler re-reads JWT post-push and enqueues a hash-keyed follow-up on drift. Stay-synchronous carve-outs: `RotateScopedSigningKey`, `SyncCluster`, `ReconcileAccountOnCluster`. |
| A14 | **Done 2026-05-21** | `JWTExpirySweeper` migrated onto the A2 substrate as `jwt.expiry_sweep`. `Sweeper.Run` removed; substrate is the only schedule driver. `OperatorHandler.RunJWTExpirySweep` stays synchronous so the admin RPC can return `SweepResult` counts inline. |
| A15 | **Done 2026-05-21** | Cluster health migrated to `cluster.health.sweep` (fan-out) + `cluster.health_check` (per-cluster, `MaxAttempts=1`). The "blocked on A3" claim in the original filing was wrong — the substrate's partial unique index provides per-row leader election. Eager enqueue on `CreateCluster`. |
| A16 | **Done 2026-05-20** | `WebhookDeliveryWorker` ported to `webhook.deliver` jobs, enqueued in the same tx as the `webhook_deliveries` row. Permanent failures signal `ErrPermanentJobFailure`. `EnqueueCatchUpDeliveries` runs at startup for orphan rows. |
| A17 | **Done 2026-05-23** | Single source of truth in `authz.Procedures` + `authz.RolePolicy`. Casbin retired (`github.com/casbin/casbin/v2` removed). `extractAction` heuristic gone. Lint test iterates `authz.Procedures` directly. Bundled fix: `ValidateToken` is now truly public. |
| A18 | **Done 2026-05-23** | `SyncCluster` and `ReconcileAccountOnCluster` migrated from `CanUpdateOperator` (admin-only) to `CanSyncCluster` (admin OR operator-admin scoped to own operator). Both RPCs changed in lockstep. |
| A19 | **Done 2026-05-23** | Shared `requireAdmin(ctx)` helper in `handlers/util.go` applied to every `KindRoleOnly` handler. Lint test now requires the call site — adding a `KindRoleOnly` handler without it fails the build. |
| A20 | **Done 2026-05-23** | `PermissionService.Filter*` helpers deleted. All five identity-tree repo `Search` methods take `authz.Scope` as a mandatory parameter; SQL-level narrowing mirrors `ListPage`. Closed a latent under-return bug where a LIMIT-20 hit on foreign-tenant rows returned zero in-tenant results. |
| C1  | **Done 2026-05-13** | Dead cmd stubs removed. |
| C2  | **Done 2026-05-13** | Stale top-level docs removed. |
| C3  | **Done 2026-05-13** | `repoErrToConnect` and `authedUser` helpers in `handlers/util.go`; applied across every handler. |
| C4  | **Done 2026-05-13** | `errors.Is` mechanical replace across services + handlers. |
| C5  | **Done 2026-05-13** | `openManagedCluster` helper extracted; 4 cluster-service methods collapsed. |
| C6  | **Done 2026-05-13** | `ListAllClusters` removed; caller redirected to `ListClusters`. |
| C7  | **Done 2026-05-13** | `fmt.Printf`/`Println` replaced with structured logger across services and server. |
| C8  | **Deferred** | Full SQL-repo genericization. Focused refactor of its own; risk is medium on the persistence layer. |
| C9  | **Done 2026-05-13** | `PermissionService` rewritten around `requireRole`/`ownsOperator`/`ownsAccount` helpers; ~330 LOC from 517. |
| C10 | **Done 2026-05-13** | Archive helpers extracted to `archive.go`; NSC import path extracted to `import_nsc.go`. See A12. |
| C11 | **Done 2026-05-13** | GORM `Update` methods use `.Select("*").Omit("CreatedAt")` to persist zero-value field updates. |
| C12 | **Done 2026-05-13** | Casbin model + policy embedded via `//go:embed`; fixes the outside-repo-root RBAC break. (Casbin later retired in A17.) |
| C13 | **Done 2026-05-13** | Unused `operatorID` params on admin-only permission checks cleaned up. Bundled with C9. |
| C14 | **Done 2026-05-13** | `.golangci.yml` added with `errorlint` + `bodyclose`; 3 real issues fixed. Other linters deferred. |
| C15 | **Done 2026-05-21** | Mechanical rename `SKK` → `SSK` across Go, nisctl, UI, and docs. Proto field names untouched. |

---

### E1 — Scoped signing keys not trusted by NATS (FIXED 2026-05-13)

`GenerateAccountJWT` was not populating `claims.SigningKeys`, so NATS rejected every user JWT signed by a scoped key. Fixed: account JWT now calls `claims.SigningKeys.AddScopedSigner(scope)` per key; user JWT calls `claims.SetScoped(true)`. `CreateAccount` generates the default scoped key before signing the first account JWT. Three e2e sub-tests guard the regression surface.

### UI1 — Broken permission textarea (FIXED 2026-05-13)

`SigningKeysView.vue` computed setter was running `split('\n').filter(...)` on every keystroke, silently swallowing newlines. Fix: setters now `split('\n')` only (no filter); trim+filter happens once at submit time. Placeholder strings fixed to render real newlines.

---

## Product proposals (P)

### P1. Audit log of all mutations — M

Every write through the service layer appends to an `audit_log` table (who, when, action, resource, before/after JSON). UI gets a filterable timeline. Best built on top of A6.

### P2. JWT expiration & auto-renewal policy — M

NIS-signed NATS JWTs currently have no `exp`. Adds per-operator default expiry, overridable per entity. Background job auto-renews or fires an alert. UI badge for "expiring in N days."

### P3. NKey rotation workflow — L

"Rotate key" button orchestrating new key generation, re-signing all dependent JWTs, and pushing to resolver with progress tracking and rollback. Scope: SSKs only (operator/account NKey rotation is out of scope — see SKILL.md §2 for the reasons).

### P4. Webhook notifications — S

Per-event-type HTTP POST webhooks (HMAC-signed), retries with backoff, dead-letter. Wires NIS into Slack/PagerDuty/SIEM. Dovetails with A6.

### P5. Bulk operations via YAML manifest — M

`nisctl apply -f team.yaml` declaratively describing operators/accounts/users/scoped keys/clusters, with a diff plan before applying. Reproducible environments.

### P6. Permission templates / account profiles — M

Named, versioned templates attachable to SSKs. Updating a template can re-sign dependents in bulk with explicit version pinning.

### P7. OIDC / SAML SSO login — L

OIDC config (issuer/client_id/secret), redirect flow, claim-to-role mapping. Local users still allowed for break-glass.

### P8. Service-account API tokens — S

Long-lived opaque tokens with per-token scope, revocation, last-use timestamp. `nisctl token create --name ci-runner --role operator-admin --operator demo`.

### P9. Sync drift dashboard — M

Per-cluster, per-account view comparing DB JWT hash vs resolver JWT hash with "drifted/synced" indicator and reconcile button. Needs A2/A3.

### P10. Live JetStream usage vs limits — M

Uses NIS cluster credentials to query `$SYS.REQ.ACCOUNT.<pubkey>.JSZ` per account and shows usage bars in the UI.

### P11. Global search across identity tree — S

Top-bar search across names, public keys, permission subjects, descriptions. DB LIKE queries initially; can grow into FTS.

### P12. Scheduled backup — Done (2026-05-20)

Per-operator scheduled backups to any S3-compatible object store on the A2 jobs substrate. Two job handlers: `backup.sweep` + `backup.execute`. Backup format is YAML in `SecretsPlaintext` mode inside an age envelope (P15). MinIO wired in docker-compose and `make run` for dev.

Deferred to v1.1: per-org S3 routing; per-operator scheduling; restore-from-backup-ID RPC.

### P13. Surface NATS-side revocations honestly in the UI — S

"Revocations" badge on AccountDetailView counts `revoked_at != null` (currently-flagged users), which diverges from the active entries in the account JWT's `Revocations` map once `RegenerateUserJWT` is called. Proposal: rename the badge + add an "Active JWT revocations" panel sourced from a new `ListAccountJWTRevocations` RPC showing what NATS actually enforces.

### P14. Retention sweep for pruned user-JWT revocations — S

`user_jwt_revocations` rows are soft-deleted, never hard-deleted. Unbounded growth on long-lived accounts with high revoke churn. New `RevocationsRetentionWorker` sweeping `pruned_at < now - retention_days` (default 90d); new `DeletePrunedBefore` repo method; metric `nis_user_jwt_revocations_purged_total`.

### P15. Encrypt scheduled-backup artifacts at rest — Done 2026-05-25

Shipped with `filippo.io/age` (option 2 from the design space). Per-operator recipient pubkey lists (`operator_age_recipients`); mandatory encryption before S3 upload; fail-loud when zero recipients configured. `nisctl backup keygen` / `backup decrypt` for local key management. Server never holds private key material. See the P15 row in the decisions table.

---

## Architecture proposals (A)

### A1. Unit-of-Work / transaction abstraction — M / Med

`OperatorService.CreateOperator` does ~6 writes; if write 4 fails, the first 3 are committed. Adds `repoFactory.WithTx(ctx, func(scopedRepos) error)`. NATS pushes stay outside the tx.

### A2. Cluster sync as a background job — L / Med

`SyncCluster` blocks an h2c stream for tens of seconds and has no resume if it crashes. Adds a `jobs` table; RPC enqueues + returns a job ID; worker claims and runs. Foundation for webhooks, drift scans, and all recurring work.

### A3. Leader-elected health-check scheduler — M / Med

60s health-check loop runs in-process. With 2 replicas both check the same clusters and race. A3 is no longer load-bearing for any filed work — A15 uses the substrate's partial unique index for per-row leader election — but remains open for non-substrate paths.

### A4. Envelope encryption + KMS — L / High

Single 32-byte key protects every seed. Lose it → all data bricked. Per-row DEK wrapped by a KEK in Vault/AWS KMS/GCP KMS. Biggest production security win on this list.

### A5. Close the cross-tenant leak in the handler authz layer — Done 2026-05-23

`AccountHandler` mutations were calling `authedUser` but not `permService.Can*`. Leak fixed; `handler_authz_lint_test.go` AST guardrail added. Spawned A17–A20.

### A6. Audit / domain-event substrate — M / Low

No "something happened" stream today. Adds `events` table + `EventPublisher`; services call `events.Publish` on every change. Foundation for P1, P4, P9.

### A7. Filter + cursor pagination — M / Low

`ListOptions` is offset-only. Past 1000 entities you silently lose data. Swaps to cursor pagination; filtering moves into SQL WHERE; tenant scope enforced at the repo level.

### A8. Prometheus metrics + OTel tracing — S / Low

Logging exists but no metrics, no tracing. One Connect interceptor emitting histograms, `/metrics`, OTel SDK.

### A9. Unify config + fix viper flag trap — S / Low

Two `DatabaseConfig` shapes; `--db-driver`/`--db-dsn` don't override `config.yaml`. Collapse to one Config struct with explicit `cmd.Flags().Changed()` overrides.

### A10. Code-generate GORM models + mappers — L / Med

360 LOC of hand-rolled `Entity ↔ Model` converters. Every schema change requires editing four files; drift is silent. Generate from tagged entity structs, or migrate to entgo/sqlc/bun.

### A11. First-class tenant_id in data model — L / High

Multi-tenancy today is `operator_id + role-based filtering in app code`. If a handler forgets `permService.Filter*`, you have a cross-tenant leak. Adds `tenant_id` to every table; repository layer auto-injects `WHERE tenant_id = $1`.

### A12. Decompose ExportService (1204 LOC) — M / Low

One file owns JSON export, JSON import, NSC dir import, zip/tar codec, cluster syncing. Split into `Exporter`, `Importer`, `NSCImporter`, `ArchiveCodec`. Versioned file format header. Same change as C10.

### A13. Auto-sync clusters on mutation — Done 2026-05-22

Shipped as A13-full: every mutation that changes account-JWT-relevant state enqueues per-cluster `cluster.account.push` jobs inside the tx. A13-lite (in-process post-commit push shipped with P6) superseded. Stay-synchronous carve-outs: `RotateScopedSigningKey`, `SyncCluster`, `ReconcileAccountOnCluster`.

### A14. Migrate JWTExpirySweeper onto job substrate — Done 2026-05-21

Shipped as `jwt.expiry_sweep` handler. `Sweeper.Run` goroutine removed entirely.

### A15. Migrate cluster health to per-cluster jobs — Done 2026-05-21

Shipped as `cluster.health.sweep` (fan-out) + `cluster.health_check` (per-cluster). The "blocked on A3" claim was wrong — the substrate provides per-row leader election via the partial unique index.

### A16. Port WebhookDeliveryWorker onto generic substrate — Done 2026-05-20

Shipped as `webhook.deliver` jobs, enqueued in the same tx as `webhook_deliveries` rows.

### A17. De-duplicate the parallel authz tables — Done 2026-05-23

Single `authz.Procedures` + `authz.RolePolicy` replaces `casbin_policy.csv` + `extractAction` + the lint `want` map. Casbin retired.

### A18. Rationalise SyncCluster / ReconcileAccountOnCluster permissions — Done 2026-05-23

Both RPCs moved from `CanUpdateOperator` (admin-only) to `CanSyncCluster` (admin OR operator-admin scoped to own operator).

### A19. Standardise casbinOnly handlers on `requireAdmin` — Done 2026-05-23

Shared `requireAdmin(ctx)` helper applied to every `KindRoleOnly` handler; lint test enforces the call site.

### A20. Retire `PermissionService.Filter*` — Done 2026-05-23

Three `Filter*` helpers deleted; all five repo `Search` methods now take `authz.Scope` as a mandatory parameter with SQL-level narrowing.

---

## Code-review cleanups (C)

### C1. Delete dead command stubs — S / Low

`cmd/fix-cluster-creds/`, `cmd/test-nats-connection/`, `cmd/test-old-user/`, `test-nats.go`.

### C2. Delete stale top-level docs — S / Low

IMPLEMENTATION.md, IMPROVEMENT.md, IMPROVEMENTS_IMPLEM.md, PROGRESS.md, STATUS.md, UI_IMPLEMENTATION.md.

### C3. Centralize handler boilerplate — M / Low

~43 repeat-preamble sites. Add `repoErrToConnect(err)` and `authedUser(ctx)` helpers.

### C4. Replace `err == ErrNotFound` with `errors.Is` — S / Low

Services wrap errors with `%w`; equality checks silently fail on wrapped errors.

### C5. Extract `withClusterClient` helper — S / Low

4 methods in `cluster_service.go` repeat: fetch cluster → decrypt creds → dial NATS → defer close.

### C6. Remove `ListAllClusters` — S / Low

Literal duplicate of `ListClusters`. One caller.

### C7. Replace `fmt.Printf` with slog — S / Low

~10 sites in services and `grpc/server.go` bypass the structured logger.

### C8. Genericize SQL repos — L / Med

**Deferred.** 6 repo files ~150–200 LOC each, byte-identical apart from types. `gormRepo[E, M]` generic could cut ~600 LOC. Medium risk on persistence layer; needs its own review-agent pass.

### C9. Refactor PermissionService with composable helpers — M / Med

517 lines of near-identical role switches. Add `ownsOperator`, `ownsAccount`, `requireRole`; rewrite Can* as 2–5 line compositions.

### C10. Split export_service.go — M / Low

Same as A12.

### C11. Fix GORM Update zero-value skipping — S / Med

GORM `Updates(&model)` silently skips zero values. Fix with `Select("*")`.

### C12. Resolve dead Casbin config keys — S / Low

Embed model + policy via `//go:embed`. (Casbin later retired in A17.)

### C13. Drop unused operatorID params on admin-only permission checks — S / Low

`CanUpdateOperator(apiUser, operatorID)` doesn't use `operatorID`. Remove or validate.

### C14. Enable errorlint, unparam, dupl, gocyclo in golangci-lint — M / Low

Defer until other Cs settle.

### C15. Rename SKK → SSK across the codebase — Done 2026-05-21

Mechanical rename across Go, nisctl, UI, and docs. Proto field names untouched.

---

## Dependencies & suggested sequencing

- **Audit + events:** A6 → P1, P4.
- **Job substrate:** A2 → A3, P9.
- **Encryption hardening:** A4 → P3.
- **Authz cleanup:** C12 → A5 → C9 → C13.
- **Tidying batch (safe before e2e tests):** C1, C2, C4, C5, C6, C7, C12, C13.
- **Refactor batch (after e2e tests are green):** C3, C8, C9, C10, C11, C14.
- **A12 and C10 are the same change.**


---

# Part II — Organizations + OIDC SSO


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

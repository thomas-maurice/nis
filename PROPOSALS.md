# NIS Improvement Proposals

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

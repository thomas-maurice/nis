# NIS Improvement Proposals

Generated 2026-05-13 by three review agents (Product / Architecture / Code Review).
Status: awaiting per-item approval.

**Legend** — Effort: S (≤1 day), M (1–5 days), L (>5 days). Risk: Low / Med / High.

---

## Decisions

Mark each proposal `Yes` / `No` / `Defer`. Notes welcome.

| ID  | Status | Notes |
| --- | ------ | ----- |
| P1  |        |       |
| P2  |        |       |
| P3  |        |       |
| P4  |        |       |
| P5  |        |       |
| P6  |        |       |
| P7  |        |       |
| P8  |        |       |
| P9  |        |       |
| P10 |        |       |
| P11 |        |       |
| P12 |        |       |
| A1  |        |       |
| A2  |        |       |
| A3  |        |       |
| A4  |        |       |
| A5  |        |       |
| A6  |        |       |
| A7  |        |       |
| A8  |        |       |
| A9  |        |       |
| A10 |        |       |
| A11 |        |       |
| A12 |        |       |
| C1  | **Done 2026-05-13** | Dead cmds + `test-nats.go` removed. |
| C2  | **Done 2026-05-13** | Stale top-level docs removed (IMPLEMENTATION/IMPROVEMENT/IMPROVEMENTS_IMPLEM/PROGRESS/STATUS/UI_IMPLEMENTATION.md). |
| C3  |        | Deferred until e2e suite is green on master (it is now). |
| C4  | **Done 2026-05-13** | `errors.Is` mechanical replace across services + handlers; `errors` imports added by goimports. |
| C5  | **Done 2026-05-13** | Introduced `openManagedCluster` helper; 4 cluster-service methods collapsed to a few lines each. |
| C6  | **Done 2026-05-13** | `ListAllClusters` removed; caller redirected to `ListClusters`. |
| C7  | **Done 2026-05-13** | `fmt.Printf`/`Println` removed from services, grpc/server.go, cmd/nis/serve.go; replaced with `logging.LogFromContext` / `logging.GetLogger`. |
| C8  |        | **Refactor-class — needs review-agent gate + e2e green first.** |
| C9  |        | **Security-sensitive refactor — needs review-agent gate + e2e first.** |
| C10 |        | Same as A12. **Refactor — needs e2e first.** |
| C11 |        | Behavior change — needs e2e first. |
| C12 | **Done 2026-05-13** | Casbin model + policy embedded via `//go:embed` in `internal/application/services/casbin_embed.go`; `initCasbin` is now a 2-line passthrough. Side benefit: fixes the "running the binary outside the repo root breaks RBAC" gotcha. |
| C13 |        | Deferred to land with C9 — the param has semantic intent that the C9 PermissionService refactor needs to settle. |
| C14 |        | Surfaces unknown work — deferred. |

### Discovered while building the e2e suite (2026-05-13)

**E1. Scoped signing keys are not added to the account JWT's `signing_keys` list.** `AccountService.CreateAccount` (account_service.go:104–116) signs the account JWT *before* creating its default scoped key, and there is no flow that re-signs the account JWT when a scoped key is added later. NATS therefore rejects any user JWT signed by a scoped key as `Authorization Violation`. The e2e suite originally tested `pub_deny` enforcement against a scoped-key-signed user and surfaced this immediately. Severity: high — this is a featured part of NIS that doesn't actually work end-to-end. Fix surface: `JWTService.GenerateAccountJWT` should load the account's scoped signing keys and populate `claims.SigningKeys`, and `ScopedSigningKeyService.CreateScopedSigningKey` / `UpdatePermissions` / `Delete` must trigger account-JWT re-signing. Worth folding into A6 (event substrate) so any scoped-key mutation emits an `AccountSigningKeysChanged` event that the account service subscribes to.

---

## Product proposals (P)

### P1. Audit log of all mutations — M

If a credential gets weird permissions or a user disappears, today there's no record of who or when. Every write through the service layer would also append to an `audit_log` table with (who, when, action, resource, before/after JSON). UI gets a filterable timeline. Best built on top of A6 (event substrate) — otherwise it's sprinkled `audit.Log(...)` calls in 50 handlers.

### P2. JWT expiration & auto-renewal policy — M

NIS-signed NATS JWTs currently have no `exp` (or it's far-future), so a leaked `.creds` file works forever. This adds a per-operator default (e.g. user creds = 90d, account creds = 1y), overridable per entity. A background job finds JWTs expiring soon and either auto-renews or fires an alert. UI badge for "expiring in N days."

### P3. NKey rotation workflow — L

To rotate a suspected-compromised account signing key today you manually create a new key, re-sign every dependent JWT, push to resolver, and pray. This adds a "Rotate key" button orchestrating the whole flow with progress tracking and rollback. Significantly safer once A4 (envelope encryption) is in place — botched rotation can otherwise corrupt encrypted blobs.

### P4. Webhook notifications — S

Cluster unhealthy / sync failed / cred expiring → currently only visible if someone opens the UI. This adds per-event-type HTTP POST webhooks (HMAC-signed), retries with backoff, dead-letter for failed deliveries. Wires NIS into Slack/PagerDuty/SIEM. Dovetails with A6 — A6 emits events, P4 subscribes.

### P5. Bulk operations via YAML manifest — M

Creating 50 users today is a bash loop. This adds `nisctl apply -f team.yaml` declaratively describing operators/accounts/users/scoped keys/clusters, with a diff plan (like `terraform plan`) before applying. Reproducible environments.

### P6. Permission templates / account profiles — M

Today every user is built from raw pub/sub allow/deny lists. Common roles ("ServiceReader", "MetricsWriter") get re-typed each time and drift across users. This adds named, versioned templates attachable when creating a user; updating a template can re-sign dependents in bulk. Cascade semantics need thought — version pinning so an update doesn't surprise production.

### P7. OIDC / SAML SSO login — L

Local username+password only today; most orgs with central IdP won't adopt without SSO. This adds OIDC config (issuer/client_id/secret), redirect flow, claim-to-role mapping (e.g. `groups: nis-admins → admin`). Local users still allowed for break-glass. High-leverage for enterprise adoption.

### P8. Service-account API tokens — S

`nisctl` and CI today authenticate as a real user with the same TTL JWT as a person. This adds long-lived opaque tokens with per-token scope, revocation, last-use timestamp, separate code path in auth middleware. `nisctl token create --name ci-runner --role operator-admin --operator demo`.

### P9. Sync drift dashboard — M

After `cluster sync`, you have no idea if the resolver has since been touched out-of-band. This adds a per-cluster, per-account view comparing DB JWT hash vs resolver JWT hash with a "drifted/synced" indicator and a reconcile button. Needs periodic background scans — fits A2/A3.

### P10. Live JetStream usage vs limits — M

JetStream limits are set but unused capacity is invisible. This uses NIS's cluster credentials to query `$JS.API.ACCOUNT.INFO` per account and shows usage bars in the UI. Turns blind quota config into informed sizing.

### P11. Global search across identity tree — S

Today's search is per-list. To find "which scoped key allows pub on `metrics.>`", you click through every account → every key. This adds a top-bar search across names, public keys, permission subjects, descriptions. Small (DB LIKE queries) initially; can grow into FTS.

### P12. Scheduled backup + restore-verify — M

`nisctl export operator` exists but backups are someone's homework. This adds config-driven cron, encrypted blob to S3-compatible storage, retention policy, and a `verify` command that boots a shadow NIS in a tmpdir and imports the backup to confirm it works. Backup encryption key must be separate from data key.

---

## Architecture proposals (A)

### A1. Unit-of-Work / transaction abstraction — M / Med

`OperatorService.CreateOperator` does ~6 writes (operator → $SYS account → system user → scoped key). If write 4 fails, the first 3 are committed and you have a half-created operator the API often can't clean up. This adds `repoFactory.WithTx(ctx, func(scopedRepos) error { … })` — all writes inside use the same GORM tx; auto rollback on error. NATS pushes stay outside the tx (you can't roll back a network call).

### A2. Cluster sync as a background job — L / Med

`SyncCluster` does decrypt + dial NATS + push N JWTs serially inside the RPC. For a few hundred accounts it blocks an h2c stream for tens of seconds, and if it crashes mid-way there's no resume. This adds a `jobs` table; the RPC enqueues + returns a job ID; a worker claims and runs. Same substrate enables retries and observability for sync, webhooks, drift scans.

### A3. Leader-elected health-check scheduler — M / Med

The 60s health-check loop runs in-process. With 2 replicas (as README suggests), both check the same clusters, both write rows, race. This adds a leader lease via `SELECT … FOR UPDATE SKIP LOCKED` (Postgres). Or, more elegantly, jobize each cluster check (A2) so any replica can pick one up. SQLite single-replica is fine as-is.

### A4. Envelope encryption + KMS — L / High

A single 32-byte key in process memory protects every seed. Lose the key → all encrypted data bricked permanently. This restructures encryption so each row has a per-row data-encryption-key (DEK), wrapped by a key-encryption-key (KEK) stored in Vault Transit / AWS KMS / GCP KMS. NIS holds DEKs only transiently; KEK never leaves the KMS. Significant crypto surface — must be done carefully. Biggest production security win.

### A5. Embed Casbin + consolidate authz — M / Med

Authorization is split. Casbin (middleware) only sees `(role, resource, action)` extracted by parsing procedure names — it can't enforce "operator-admin for op X can't touch op Y". That check lives in `PermissionService`, manually invoked in every handler; forget to call it = silent cross-tenant leak. Also Casbin config files load by relative path, so running the binary outside the repo root breaks RBAC. This picks one model: either embed Casbin and extend it with ABAC matchers, or drop Casbin and standardize on `PermissionService` from a single interceptor.

### A6. Audit / domain-event substrate — M / Low

No "something happened" stream today; every state change is silent. This adds an `events` table + `EventPublisher` interface; services call `events.Publish(ctx, AccountCreated{...})` on every change; outbox dispatcher forwards to subscribers. Foundation for P1 (audit log), P4 (webhooks), P9 (drift). Building these without it = wiring into every handler.

### A7. Filter + cursor pagination — M / Low

`ListOptions` is `Limit/Offset` only. `FilterAccounts/FilterUsers` fetch up to 1000 rows and filter in Go (including RBAC scope). Past 1000 entities you silently lose data. This swaps to `Filter+Sort+Cursor`; filtering moves into SQL `WHERE`; tenant scope enforced at the repo level (handlers can't forget). Touches every list endpoint.

### A8. Prometheus metrics + OTel tracing — S / Low

Logging exists but no metrics, no tracing. This adds one Connect interceptor emitting histograms, `/metrics`, OTel SDK at startup. `/healthz` becomes real (DB ping + encryptor + NATS reachability). Cheapest production-observability win on this list.

### A9. Unify config + fix viper flag trap — S / Low

Two `DatabaseConfig` shapes in code; `--db-driver`/`--db-dsn` flags don't override `config.yaml` due to viper BindPFlag default behavior — a footgun bad enough to have its own section in the skill. This collapses to one Config struct, single DSN field, explicit `cmd.Flags().Changed(...)` for overrides, documented precedence: env > flag > file > default.

### A10. Code-generate GORM models + mappers — L / Med

360 LOC of hand-rolled `Entity ↔ Model` converters. Every schema change requires editing entity + model + mapper + migration; drift is silent until a bug. Generate from tagged entity structs, or migrate to entgo/sqlc/bun (bigger). Lower urgency than the rest.

### A11. First-class tenant_id in data model — L / High

Multi-tenancy today is "operator_id + role-based filtering in app code". If a handler forgets `permService.Filter*`, you have a cross-tenant data leak. This adds `tenant_id` to every table; repository layer auto-injects `WHERE tenant_id = $1` from a ctx-scoped JWT claim. Postgres RLS can layer on later. Worth it if you target multi-org SaaS; not if every install is single-tenant.

### A12. Decompose ExportService (1204 LOC) — M / Low

One file owns JSON export, JSON import, NSC dir import, zip/tar/gz/bz2 codec, cluster syncing as a side effect. Hard to test, hard to reason about. Split into `Exporter`, `Importer`, `NSCImporter`, `ArchiveCodec`, each independently testable. Versioned file format header. Same change as C10.

---

## Code-review cleanups (C)

### C1. Delete dead command stubs — S / Low

`cmd/fix-cluster-creds/`, `cmd/test-nats-connection/`, `cmd/test-old-user/`, and `test-nats.go` at repo root. Compile, aren't referenced by any Make target. Delete.

### C2. Delete stale top-level docs — S / Low

IMPLEMENTATION.md (52KB), IMPROVEMENT.md, IMPROVEMENTS_IMPLEM.md, PROGRESS.md, STATUS.md, UI_IMPLEMENTATION.md — point-in-time snapshots that rotted. Some are even in `.gitignore` yet tracked. Skill explicitly says "outdated docs are worse than missing docs."

### C3. Centralize handler boilerplate — M / Low

Every gRPC handler method repeats the same 4-line preamble; ~43 sites of `if err == ErrNotFound { return CodeNotFound }`. Add `repoErrToConnect(err)` and `authedUser(ctx)` helpers; handlers become 3 lines of orchestration. Touches every handler — must be exactly equivalent.

### C4. Replace `err == ErrNotFound` with `errors.Is` — S / Low

Services wrap errors with `%w`; the equality check silently fails on wrapped errors, so some NotFounds leak as `Internal`. Mechanical fix; enable `errorlint` to prevent regression.

### C5. Extract `withClusterClient` helper — S / Low

4 methods in `cluster_service.go` (lines 319, 451, 481, 536) repeat: fetch cluster → decrypt creds → dial NATS → defer close → do thing. One helper, four methods become 3-5 lines each.

### C6. Remove `ListAllClusters` — S / Low

Literal duplicate of `ListClusters`. One caller. Inline + delete.

### C7. Replace `fmt.Printf` with slog — S / Low

~10 sites in services and `grpc/server.go` use `fmt.Printf("Warning: ...")` bypassing the structured logger. Wrecks log aggregation.

### C8. Genericize SQL repos — L / Med

6 repo files, ~150–200 LOC each, byte-identical apart from Entity/Model types and `ListBy<Parent>`. Make `gormRepo[E, M]` generic with the common 5 methods; per-repo files keep custom queries only. Cuts ~600 LOC. **Refactor-class — needs e2e tests landed first.**

### C9. Refactor PermissionService with composable helpers — M / Med

517 lines of near-identical role switches per (resource × verb). Add `ownsOperator`, `ownsAccount`, `requireRole`; rewrite Can* as 2–5 line compositions. Cuts ~250 LOC. **Security-sensitive — needs e2e tests landed first.**

### C10. Split export_service.go — M / Low

Same as A12.

### C11. Fix GORM Update zero-value skipping — S / Med (behaviour change)

GORM's `Updates(&model)` with a struct silently skips zero values. Clearing a description to `""` is a no-op. Fix with `Select("*")` or explicit field map. Subtle behavior change — clears that were silently dropped will start happening; may reveal latent caller bugs. **Needs e2e tests first.**

### C12. Resolve dead Casbin config keys — S / Low

`config.casbin_model_path` exists in config struct but `serve.go` hardcodes the path. `//go:embed` the model + policy and drop config keys. Fixes A5's "outside-repo-root breaks RBAC" problem incidentally.

### C13. Drop unused operatorID params on admin-only permission checks — S / Low

`CanUpdateOperator(apiUser, operatorID)` doesn't use `operatorID` — caller thinks they're getting a scope check, they aren't. Either remove the param or actually validate. Bundle with C9.

### C14. Enable errorlint, unparam, dupl, gocyclo in golangci-lint — M / Low

Linter passes today but the enabled set is minimal. Surfaces unknown amount of additional work that may overlap with proposals here. **Defer until other Cs settle.**

---

## Dependencies & suggested sequencing

- **Audit + events:** A6 → P1, P4. A6 first; P1/P4 become straightforward subscribers.
- **Job substrate:** A2 → A3, P9. Job queue first; health-check leader election and drift scans both ride on it.
- **Encryption hardening:** A4 → P3.
- **Authz cleanup:** C12 (embed) → A5 (consolidate) → C9 (refactor) → C13. Each step makes the next easier.
- **Tidying batch (safe before e2e tests):** C1, C2, C4, C5, C6, C7, C12, C13. All small, low-risk.
- **Refactor batch (after e2e tests are green):** C3, C8, C9, C10, C11, C14.
- **A12 and C10 are the same change.** Pick one ID.

---

## Status of this round (2026-05-13)

- **Tidying batch implemented and landed:** C1, C2, C4, C5, C6, C7, C12. C13 was pulled out because the unused `operatorID` params carry semantic intent that the C9 PermissionService refactor needs to settle properly; it will land in that batch.
- **E2E test harness implemented and passing:** `tests/e2e/e2e_test.go` (build tag `e2e`), `make test-e2e`, CI job added between `lint`/`test` and `build`. Three sub-tests cover: authorized connection with a fresh user's creds, rejection of unauthenticated connections, and account-level subject isolation. CLAUDE.md gained rule 6 making `make test-e2e` mandatory after non-trivial server-side changes; the skill's §6 documents the suite.
- **Discovered gap E1** (scoped-signing-key trust, above) is now visible and recorded. A focused fix should be its own proposal; tagging here for triage.
- All other proposals (P1–P12, A1–A12, C3, C8–C11, C14) still awaiting decision in the table above. The refactor-class Cs (C3, C8, C9, C10, C11, C14) are unblocked now that e2e is green.

### Files touched in this round

```
deleted   cmd/fix-cluster-creds/, cmd/test-nats-connection/, cmd/test-old-user/, test-nats.go
deleted   IMPLEMENTATION.md, IMPROVEMENT.md, IMPROVEMENTS_IMPLEM.md, PROGRESS.md, STATUS.md, UI_IMPLEMENTATION.md
new       internal/application/services/casbin_embed.go        — embedded RBAC model+policy
new       tests/e2e/e2e_test.go                                — e2e harness + suite (build tag e2e)
new       PROPOSALS.md                                         — this file
edit      internal/application/services/cluster_service.go     — C5/C6/C7
edit      internal/application/services/account_service.go     — C7
edit      internal/application/services/export_service.go      — C7
edit      internal/application/services/{auth,scoped_signing_key,operator,user}_service.go — C4
edit      internal/interfaces/grpc/handlers/*_handler.go        — C4
edit      internal/interfaces/grpc/server.go                   — C7
edit      cmd/nis/commands/serve.go                            — C7 + C12 (initCasbin)
edit      Makefile                                             — test-e2e target + .PHONY
edit      .github/workflows/build.yml                          — new e2e job between [test,lint] and build
edit      CLAUDE.md                                            — rule 6 (mandatory e2e), Test section update
edit      README.md                                            — make test-e2e doc
edit      .claude/skills/nis-dev/SKILL.md                      — §6 e2e suite documentation
```

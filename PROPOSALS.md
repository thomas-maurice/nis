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
| P4  | **Done 2026-05-14** | Webhook subscriptions implemented end-to-end on top of A6: operator-scoped CRUD (`WebhookService`), per-operator RBAC enforced in `PermissionService` (admin can manage any; operator-admin only own), wildcard `["*"]` event-type filter restricted to admin at the handler. Secret is generated server-side (32 random bytes hex-encoded), encrypted via the existing `Encryptor`, returned ONCE on create. Background `WebhookDeliveryWorker` polls `webhook_deliveries.ClaimDue`, POSTs with HMAC-SHA256 via the public `pkg/webhooks` SDK, exponential backoff (10s base × 2, ±20% jitter, capped 10min), dead-letter after 5 attempts. Auto-disables the sub on `Decrypt` failure with `disabled_reason='secret_unreadable'`. Headers: `X-NIS-Event`, `X-NIS-Delivery` (per-attempt UUID), `X-NIS-Subscription`, `X-NIS-Timestamp`, `X-NIS-Signature` (`sha256=<hex>` of `timestamp.body`). `TestWebhookSubscription` synthesizes a `webhook.test` event scoped to the sub and returns its delivery ID. UI: `WebhooksView` (list + create modal with one-time secret reveal), `WebhookDetailView` (edit, Send Test, deliveries panel with status chips). nisctl: `webhook create|list|get|update|delete|test|deliveries`. Tunables: `webhooks.poll_interval_seconds`, `webhooks.delivery_timeout_seconds`, `webhooks.max_attempts`, `webhooks.backoff_base_seconds`, `webhooks.backoff_cap_seconds`, `webhooks.shutdown_timeout_seconds`, `webhooks.succeeded_retention_days`. Metrics: `nis_webhook_deliveries_total{status}`, `nis_webhook_delivery_duration_seconds`. e2e coverage in `tests/e2e/webhooks_test.go` — 10 scenarios including HMAC validation via the public SDK, filter match/mismatch, wildcard, disabled, retry-then-success, dead-letter, test-subscription, operator-admin own-op-only RBAC, wildcard-admin-only, secret-encrypted-at-rest. Deferred to v1.1: per-operator RBAC on event listing, manual `RetryDelivery` for dead-lettered rows, startup catch-up for events emitted while the worker was down. |
| P5  |        |       |
| P6  |        |       |
| P7  |        |       |
| P8  | **Done 2026-05-18** | Service-account API tokens landed end-to-end. New `api_tokens` table (migration 00003; FK `created_by_user_id` is `ON DELETE SET NULL` so offboarding the creator doesn't kill CI tokens). Plaintext format is `nis_pat_<64 hex>`, returned ONCE in `CreateAPITokenResponse.plaintext`; only `sha256(token)` is stored. Auth middleware (`internal/interfaces/grpc/middleware/auth.go::resolveCaller`) detects `nis_pat_`-prefixed bearers and calls `APITokenService.Authenticate` instead of the JWT path; on success it sets BOTH a synthetic APIUser (so `PermissionService.Can*` keeps working) AND `authctx.SetActor({Type: ActorTypeAPIToken, ID: token.ID})` (so the audit log records `actor_type='api_token'`, not the api_user who minted the token). New `authctx.SetActor`/`GetActor` + `entities.ActorTypeAPIToken` plumb that distinction through `events.actorFromContext`. `last_used_at` is updated by a coalescing background flusher (`middleware.APITokenLastUsedFlusher`, default 30s interval) so per-request DB writes don't serialize against burst CI traffic; graceful drain on shutdown. Privilege escalation guard in `PermissionService.CanCreateAPIToken`: role ceiling + scope ownership; an operator-admin cannot mint admin tokens or tokens for another operator. Chained-privilege block in the handler: a token-authed call cannot mint another token. `RevokeAPIToken` is a soft-delete (sets `revoked_at`); `DeleteAPIToken` removes the row. New events `api_token.created` / `api_token.revoked`. New metrics `nis_api_token_authentications_total{status}` plus `nis_auth_rejections_total{reason="invalid_api_token"}`. nisctl: `token create|list|get|revoke|delete` with `--expires-in`; `NIS_TOKEN` env var picked up by `cmd/nisctl/commands/root.go` with precedence `--token > NIS_TOKEN > stored session`. UI: `ApiTokensView.vue` (list + create modal with one-time plaintext reveal + revoke/delete per row + "show revoked" toggle). Casbin: new `apitoken` resource with create/read/delete for all three roles; `revoke` maps to `delete` via an `extractAction` HasPrefix rule. Login is unaffected — JWT path unchanged, and tokens cannot be used to mint a JWT. Tunables: `api_tokens.last_used_flush_interval_seconds` (default 30s). Doc triangle (README/SKILL/SECURITY) all updated in the same change. Test coverage: 9 unit tests (api_token_service_test.go) + 2 flusher tests (api_token_lastused_test.go) + 12 e2e scenarios (api_tokens_test.go) covering happy path, expired/revoked/invalid/never-expires, login-rejects-token, token-cannot-mint-token, operator-admin scope enforcement, list-scoped-to-own, audit attribution, deleted-creator-preserves-token, NIS_TOKEN env-var path. **Deferred to v1.1:** SSO/OIDC integration (P7) will plug in by extending `resolveCaller` with another branch and using the same `authctx.Actor` shape. |
| P9  |        |       |
| P10 |        |       |
| P11 |        |       |
| P12 |        |       |
| A1  | **Done 2026-05-13** | Repository factory gained `WithTx(ctx, fn func(tx RepositoryFactory) error) error` (GORM-backed). `OperatorService.CreateOperator` (4 writes incl. nested `$SYS` account create), `OperatorService.DeleteOperator` (operator → accounts → users cascade), `AccountService.CreateAccount` + Update + UpdateJetStreamLimits, and `ScopedSigningKeyService.Create`/Update/Delete (mutation + `regenerateAccountJWT`) all run inside a single tx so a partial failure rolls the whole tree back. Manual-rollback hacks removed (`account_service.go` 130-136, `scoped_signing_key_service.go` 149-153). Live `sqlRepositoryFactory.Connect` now sets `SetMaxOpenConns(1)` for SQLite so longer write txs don't deadlock with concurrent reads. NATS pushes stay outside the tx; the existing 60s cluster-sync loop reconciles any DB-ahead-of-resolver window. Regression coverage: 4 new tests in `internal/infrastructure/persistence/withtx_test.go` (commit/rollback/panic/intra-tx-read), plus 3 service-layer partial-failure tests in `internal/application/services/partial_failure_test.go` (CreateOperator, CreateScopedSigningKey, ImportFromNSC) backed by a fault-injecting encryptor that confirms no orphan rows survive. All existing unit + integration + e2e suites green. **2026-05-13 follow-up:** `ExportService.ImportFromNSC` is now also tx-wrapped — added `SetSystemAccountTx`/`CreateUserTx`/`UpdateClusterCredentialsTx` so its inner service calls participate in the import-level tx; archive extraction stays OUTSIDE the tx (filesystem state isn't rolled back; `defer os.RemoveAll` handles it). |
| A2  |        |       |
| A3  |        |       |
| A4  |        |       |
| A5  |        |       |
| A6  | **Done 2026-05-14** | Durable event substrate landed. New `events` table (TEXT PK, append-only) — same migration file works on SQLite + Postgres, verified end-to-end with up/down/re-up + FK CASCADE smoke test on both engines. `internal/application/events` package exposes `EmitTx(ctx, tx, Event)` (in-tx emit + same-tx webhook fanout) and `EmitSystem(ctx, factory, Event)` (background, forces `actor_type='system'`, opens its own short tx). Service-layer wiring (Chunk D) covers operator/account/user/scoped_key/cluster create-update-delete and cluster sync/health transitions. Actor extraction via `internal/infrastructure/authctx` — a leaf package extracted to break the `services → events → middleware → services` cycle (middleware re-exports `UserContextKey` as a `var` for back-compat). `EventService.ListEvents` + `GetEvent` RPCs (admin-only via Casbin policy `p, admin, event, read`); filter supports types/operator/resource/since/until with base64-encoded cursor pagination. nisctl: `event list|get`. UI: `EventsView` admin-only with filter bar + cursor pagination + JSON detail modal. `EventsRetentionWorker` sweeps daily — default 30d events / 7d succeeded webhook deliveries; dead-letter rows are never auto-deleted. Metrics: `nis_events_emitted_total{type}`. **Known limitation:** when an operator is deleted, the SQL FK CASCADE removes child accounts/users without emitting per-child `*.deleted` events — only the trigger `operator.deleted` is recorded. Audit-cascade is a v1.1 follow-up. e2e coverage in `tests/e2e/events_test.go` (7 scenarios). |
| A7  |        |       |
| A8  | **Done 2026-05-13** | Prometheus `/metrics`, OTel tracing (off by default), `/livez` + `/readyz` (strict) + `/healthz` (back-compat). otelconnect interceptor + otelhttp middleware. Domain gauges via 60s refresh. Six new instrumentation sites. Full doc-triangle update incl. README OTel walkthrough. |
| A9  | **Done 2026-05-13** | Viper flag-default trap fixed via `applyFlagOverrides` (writes to viper only when flag explicitly passed). Dead `internal/config` package + `sql.NewDB(config.DatabaseConfig)` removed; tests migrated to `sql.NewDB(driver, dsn)`. `config.example.yaml` rewritten to match the live shape (was documenting `database.path`/etc., none of which the binary reads). Doc triangle updated; precedence is now flag > env > file > default. Regression test in `cmd/nis/commands/viper_overrides_test.go`. |
| A10 |        |       |
| A11 |        |       |
| A13 |        | Added 2026-05-18 — auto-sync on mutation; depends on A2 job substrate. |
| A12 | **Partial Done 2026-05-13** | NSC import path (`ImportFromNSC` + 6 helpers, ~590 LOC) extracted to `import_nsc.go`. `export_service.go` down from 1089 to 493 LOC. Pure file split — methods stay on `*ExportService`, no API change, no behavior change. e2e green. Structural extraction into separate `Exporter` / `Importer` / `NSCImporter` types (needing new constructors and dependency wiring) deferred — that's a real design call. **2026-05-13 follow-up:** YAML support added end-to-end — `ExportOperatorYAML`/`ImportOperatorYAML` service methods, format-aware `ExportOperatorBytes`/`ImportOperatorBytes` with auto-detection by sniffing the first non-whitespace byte, proto `format` field on `ExportOperatorRequest/Response`, gRPC handler dispatch, `nisctl export operator --format yaml\|json` (default yaml). All 5 `Exported*` structs got `yaml:` tags alongside `json:` to keep field names identical across encodings (yaml.v3 defaults to lowercased Go names otherwise). Round-trip tests cover the JSON regression path, the YAML happy path, auto-detect, tricky `$SYS`-prefixed names, and back-compat default-to-JSON. **2026-05-14 API simplification:** removed `regenerate_ids` from `ImportOperatorRequest` (reserved in proto), from all `ImportOperator*` service methods, and from `nisctl export import`. The flag only swapped UUIDs while leaving NKey public keys untouched — useless for cloning (pubkey collides with source) and useless for migration (NKeys carry over identity). Import is now strictly a faithful restore: same UUIDs, same NKeys, same JWTs, end state byte-identical to what was exported. A future "duplicate operator" feature, if it's ever needed, will be a separate operation that also mints fresh NKeys and re-signs the tree. The structural type extraction remains the only A12 item still outstanding. |
| C1  | **Done 2026-05-13** | Dead cmds + `test-nats.go` removed. |
| C2  | **Done 2026-05-13** | Stale top-level docs removed (IMPLEMENTATION/IMPROVEMENT/IMPROVEMENTS_IMPLEM/PROGRESS/STATUS/UI_IMPLEMENTATION.md). |
| C3  | **Done 2026-05-13** | `repoErrToConnect(err)` and `authedUser(ctx)` helpers in `handlers/util.go`; applied across every handler via perl + goimports cleanup. `errorlint` (C14) prevents regressions. |
| C4  | **Done 2026-05-13** | `errors.Is` mechanical replace across services + handlers; `errors` imports added by goimports. |
| C5  | **Done 2026-05-13** | Introduced `openManagedCluster` helper; 4 cluster-service methods collapsed to a few lines each. |
| C6  | **Done 2026-05-13** | `ListAllClusters` removed; caller redirected to `ListClusters`. |
| C7  | **Done 2026-05-13** | `fmt.Printf`/`Println` removed from services, grpc/server.go, cmd/nis/serve.go; replaced with `logging.LogFromContext` / `logging.GetLogger`. |
| C8  |        | **Deferred.** Full SQL-repo genericization is a focused refactor of its own. The agent-cited ~600 LOC savings overstate the win once per-resource methods (GetByName, ListBy<Parent>, GetByPublicKey) are excluded, while the risk on persistence layer is medium and is best done with its own review-agent pass. Leaving as a stand-alone follow-up. |
| C9  | **Done 2026-05-13** | `PermissionService` rewritten around three helpers: `requireRole`, `ownsOperator`, `ownsAccount`. Can* methods are now 2–5 line compositions; the 517-line file is down to ~330 LOC with materially clearer scope semantics. RBAC isolation tests + integration tests still green. |
| C10 | **Done 2026-05-13** | Archive helpers extracted to `archive.go` in first sub-round; NSC import path extracted to `import_nsc.go` later in the day (see A12). `export_service.go` now 493 LOC (was 1207). Further structural extraction into named types is the deferred part of A12, not C10. |
| C11 | **Done 2026-05-13** | All six GORM repo `Update` methods now use `.Select("*").Omit("CreatedAt").Updates(model)` so zero-value field updates (e.g. clearing a description) actually persist. CreatedAt remains immutable. Caught no regressions in the test suite. |
| C12 | **Done 2026-05-13** | Casbin model + policy embedded via `//go:embed` in `internal/application/services/casbin_embed.go`; `initCasbin` is now a 2-line passthrough. Side benefit: fixes the "running the binary outside the repo root breaks RBAC" gotcha. **Bonus 2026-05-13:** both files now have thorough explanatory comments suitable for newcomers — see the files. |
| C13 | **Done 2026-05-13** | Bundled with C9. Admin-only Can*Operator / Can*Cluster methods now route through `requireRole(RoleAdmin)`; the `operatorID` params on `CanUpdateOperator`/`CanDeleteOperator` are explicitly retained for future operator-admin-self-update semantics (commented). |
| C14 | **Done 2026-05-13** | `.golangci.yml` added enabling `errorlint` (catches sentinel-error comparisons + bad type assertions on errors) and `bodyclose`. Surfaced 3 real issues (config viper ConfigFileNotFoundError, two test-only `err.(*connect.Error)` assertions), all fixed with `errors.As`. Lint runs clean. Other linters from the proposal (`unparam`, `dupl`, `gocyclo`, `gosec`) deferred — each surfaces its own backlog and should be a follow-up. |

### E1 — Scoped signing keys not trusted by NATS  (FIXED 2026-05-13)

Originally discovered while building the e2e suite: `JWTService.GenerateAccountJWT` did not populate `claims.SigningKeys`, so NATS rejected every user JWT signed by a scoped key as "Authorization Violation". Fixed across four files:

1. **`jwt_service.go`** — `GenerateAccountJWT` now takes `scopedKeys []*entities.ScopedSigningKey` and calls `claims.SigningKeys.AddScopedSigner(scope)` per key, embedding the pub/sub allow/deny lists and response permission as a `UserScope` template.
2. **`jwt_service.go`** — `GenerateUserJWT` now calls `claims.SetScoped(true)` for scoped users, zeroing `UserPermissionLimits` (NATS's `UserScope.ValidateScopedSigner` requires `HasEmptyPermissions()`; `NewUserClaims` pre-fills `NatsLimits` with `NoLimit` sentinels so silent failures were guaranteed until `SetScoped` was called).
3. **`account_service.go`** — `CreateAccount` now generates the default scoped key in memory *before* signing the account JWT, so the very first JWT pushed to the resolver already trusts the default signer. `UpdateAccount` / `UpdateJetStreamLimits` fetch the account's scoped keys and pass them when regenerating.
4. **`scoped_signing_key_service.go`** — gained `operatorRepo` and `jwtService` dependencies and a `regenerateAccountJWT(accountID)` helper. Every mutating method (`Create`, `Update`, `Delete`) calls it after persisting, so the next `SyncCluster` push carries the up-to-date signing-keys map. `Create` rolls back its just-persisted key if regeneration fails.
5. **`scoped_key_handler.go`** — `UpdatePermissions` (previously a `CodeUnimplemented` stub) now delegates to `UpdateScopedSigningKey` so callers can actually update template permissions over the wire.

Regression net: three new e2e sub-tests cover this surface and run in CI:

- `ScopedKey_PubDenyEnforced` — user signed by a scoped key with `pub_deny=["secret.>"]` is permitted on `public.>` and denied on `secret.>`.
- `SyncAfterMutation_NewScopeTakesEffect` — start permissive, mutate the scope to add `pub_deny`, sync, and confirm the new template applies on next connect.
- `SyncAfterMutation_DeleteRevokesAccess` — delete a scoped key, sync, and confirm previously-issued user creds signed by that key are rejected by NATS.

### UI1 — URGENT: Broken UI to setup permissions, impossible to use newlines  (FIXED 2026-05-13)

Root cause: `SigningKeysView.vue` bound each pub/sub allow/deny textarea via a computed v-model whose **setter** ran `value.split('\n').filter(s => s.trim() !== '')` on every keystroke. Pressing Enter momentarily set the textarea value to `"foo\n"`; the setter split that into `["foo", ""]`, filtered the trailing empty element, leaving `["foo"]`; the getter re-joined to `"foo"` and Vue wrote that back to the DOM — silently swallowing the newline before the user could type the next line.

Fix in `ui/src/views/SigningKeysView.vue`:

1. Setters no longer filter — they just `value.split('\n')`. Empty lines survive while typing, so Enter behaves normally.
2. `handleSubmit` now does the trim+filter once, at submit time, before sending the array to `CreateScopedSigningKey` (`map(trim).filter(!=='')`).
3. Bonus: placeholders changed from `placeholder="events.>\ndata.>"` (HTML attribute, shows literal `\n`) to `:placeholder="`events.>\ndata.>`"` (JS template literal with a real newline) so the example renders as the two-line hint it was always meant to be.

Note: the proposal mentioned "creating a user" but users don't have permission textareas — permissions live entirely on scoped signing keys; users inherit them via the assigned scoped key. Only `SigningKeysView.vue` needed the fix.

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

### A13. Auto-sync clusters on mutation — M / Med

Today, account/user/scoped-key changes update the DB but never reach the NATS resolver until a human runs `nisctl cluster sync` or hits the Sync button. The 60s background loop is health-only (reachability check; no JWT push) — confirmed in `cluster_service.go` (`CheckClusterHealth` line 593, `CheckAllClustersHealth` line 647) and `cmd/nis/commands/serve.go:324`. Drift between DB and NATS is the default state, not the exception.

**Proposal.** When a service mutation changes account-JWT-relevant state (account create/update/delete, scoped-signing-key CRUD that triggers `regenerateAccountJWT`, user delete on a revocation path, JetStream limit updates), enqueue a per-cluster sync. Debounce so a burst of mutations from one workflow collapses into one push round. Cross-reference with A2 (jobs table) — this is the natural first consumer of that substrate: the mutation emits a `cluster.sync_requested` event/job; the worker debounces and runs `SyncCluster` against each cluster attached to the operator. Errors are retried with backoff and exposed via the existing webhook substrate (A6/P4) as `cluster.sync_failed`.

**Design notes / open questions.**
- *Scope per cluster, not per account.* Pushing one account JWT to one cluster is the natural unit, but the existing `SyncCluster` does N pushes inside one connection — keep that as the worker's unit of work to amortize the TLS+auth round-trip. Debounce key = `cluster.id`.
- *Per-operator → per-cluster fan-out.* A mutation knows its operator; the worker resolves attached clusters at run time, not enqueue time, so newly-attached clusters catch up automatically.
- *Prune semantics.* Auto-sync should NOT prune by default (a misconfigured DB row should not silently remove accounts from NATS). Prune stays an explicit user/CLI option.
- *Audit emit.* Each automatic sync should emit `cluster.sync_completed` (or `.sync_failed`) with `trigger='auto'` distinguishing from manual syncs so the audit log is honest about who acted.
- *Opt-out.* Per-cluster `auto_sync_enabled` bool (default true) so an operator running NATS through change-control can keep manual sync. Surface in UI + nisctl.
- *Depends on A2* for the job substrate; without it, doing this in-goroutine repeats the same single-replica drift problem A3 raised.

**Discovered 2026-05-18** while investigating "do syncs happen on their own?" — answer was no, and the SKILL claimed otherwise (now corrected). Prior wording in A1's notes ("the existing 60s cluster-sync loop reconciles any DB-ahead-of-resolver window") was likewise wrong and load-bearing for the multi-write-atomicity story; A13 closes that gap properly instead of patching docs forever.

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

All cleanup proposals except **C8** are now landed and the CI gate is in place. Specifically:

- **Tidying batch (C1, C2, C4, C5, C6, C7, C12)** — landed in the first sub-round.
- **E1 fix** — scoped signing keys now appear in the account JWT's `signing_keys`, account JWT is re-signed on every scoped-key mutation, user JWTs use `SetScoped(true)` per NATS's validator. Three dedicated e2e sub-tests guard the regression surface.
- **Cleanup batch (C3, C9, C10 partial, C11, C13, C14)** — landed.
- **C8** is the one outstanding C. It's a real refactor (genericize SQL repos) whose value is mostly aesthetic and whose risk is medium because it touches persistence; left for a focused follow-up PR where it can get its own review-agent gate.
- **CI**: `e2e` job added to `.github/workflows/build.yml`, gating the `build` job behind `[test, lint, e2e]`. Job does `docker info` upfront and pre-pulls the NATS image. `make test-e2e` is the local equivalent.
- **Casbin docs**: both `casbin_model.conf` and `casbin_policy.csv` now carry detailed header + per-section comments explaining the request/policy/role/matcher format and what each policy row authorises. Readable cold.
- All other proposals (P1–P12, A1–A12) still awaiting decision in the table above.

### E2E coverage today

Split across per-scenario files under `tests/e2e/`, all passing locally and in CI. Shared harness lives in `harness_test.go`; each test boots its own NIS (and NATS, when needed) so failures localise:

- `lifecycle_test.go` — operator/account/user/scoped-key CRUD + `GetUserCredentials` shape.
- `nats_live_test.go` — `AuthorizedConnection`, `UnauthorizedConnectionRejected`, `ScopedKeyPubDenyEnforced` (E1), `SyncAfterMutation_NewScopeTakesEffect`, `SyncAfterMutation_DeleteRevokesAccess`, `AccountIsolation_CrossAccountSubjectsDoNotLeak`.
- `export_test.go` — `YAMLEncoding` (regression for yaml struct-tag fix), `JSONEncoding`, `PlaintextSecretsShape`, `DefaultsToJSONWhenFormatUnset`.
- `import_backup_test.go` — `RefusesExistingWithoutOverwrite`, `OverwritePreservesClusters`, `FullRoundTrip` (A1 cascade), `PlaintextSecretsRoundTrip` (DR scenario).
- `import_nsc_test.go` — `MinimalArchive` (tx-wrapped NSC import path).
- `delete_test.go` — `OperatorBlockedByAttachedCluster` (FK-leak regression), `OperatorCascadesAccountsAndUsers`, `AccountCascadesUsersAndKeys`, `UserIsScoped`.
- `observability_test.go` — `/livez`/`/healthz`/`/readyz`/`/metrics` all 200 plus `rpc_server_duration_*` and `nis_operators_total` series.

### Files touched across the full round

```
deleted   cmd/fix-cluster-creds/, cmd/test-nats-connection/, cmd/test-old-user/, test-nats.go
deleted   IMPLEMENTATION.md, IMPROVEMENT.md, IMPROVEMENTS_IMPLEM.md, PROGRESS.md, STATUS.md, UI_IMPLEMENTATION.md
new       internal/application/services/casbin_embed.go         — embedded RBAC model+policy + loader
new       internal/application/services/archive.go              — zip/tar.gz/tar.bz2 extraction helpers (C10)
new       internal/interfaces/grpc/handlers/util.go             — repoErrToConnect + authedUser (C3)
new       tests/e2e/                                            — split per-scenario suite (harness_test.go + lifecycle/nats_live/export/import_backup/import_nsc/delete/observability)
new       .golangci.yml                                         — errorlint + bodyclose config (C14)
new       PROPOSALS.md                                          — this file
edit      internal/application/services/jwt_service.go          — E1: SigningKeys + SetScoped(true)
edit      internal/application/services/account_service.go      — E1 + C7
edit      internal/application/services/scoped_signing_key_service.go — E1 (regenerateAccountJWT)
edit      internal/application/services/cluster_service.go      — C5/C6/C7
edit      internal/application/services/export_service.go       — C7 + C10 partial
edit      internal/application/services/permission_service.go   — full C9 rewrite
edit      internal/application/services/casbin_model.conf       — extensive comments
edit      internal/application/services/casbin_policy.csv       — section headers + per-row notes
edit      internal/application/services/{auth,operator,user}_service.go — C4
edit      internal/interfaces/grpc/handlers/*_handler.go         — C3 + C4 + UpdatePermissions impl
edit      internal/interfaces/grpc/server.go                    — C7
edit      internal/infrastructure/persistence/sql/*_repo.go     — C11 (Select("*").Omit("CreatedAt"))
edit      internal/config/config.go                             — errorlint fix (errors.As)
edit      internal/interfaces/grpc/handlers/auth_handler_test.go — errorlint fix
edit      cmd/nis/commands/serve.go                             — C7 + C12 (initCasbin → services.NewCasbinEnforcer)
edit      Makefile                                              — test-e2e target + .PHONY
edit      .github/workflows/build.yml                           — e2e job between [test,lint] and build
edit      CLAUDE.md                                             — rule 6 (mandatory e2e), Test section update
edit      README.md                                             — make test-e2e doc
edit      .claude/skills/nis-dev/SKILL.md                       — §6 e2e suite documentation
```

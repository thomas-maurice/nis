# Improvements Implementation Guide

This document provides step-by-step instructions for sub-agents to implement improvements from `IMPROVEMENT.md`. Each task includes agent instructions, acceptance criteria, and checkpoint tracking.

---

## Baseline Stats (Before Improvements)

Captured: 2026-02-18

| Metric | Value |
|--------|-------|
| Source files (excl. generated) | 83 |
| Test files | 12 |
| Packages with tests | 5 of 27 |
| Packages passing | 5 (all pass) |
| Coverage: encryption | 92.2% |
| Coverage: persistence/sql | 38.5% |
| Coverage: nats | 35.0% |
| Coverage: application/services | 24.0% |
| Coverage: grpc/handlers | 0% |
| Coverage: config | 0% |
| Coverage: interfaces/http | 0% |
| Coverage: middleware | 0% |
| CI/CD runs tests | No |
| CI/CD runs lint | No |
| Semantic versioning | No |

---

## Post-Implementation Stats

_To be filled after all improvements are complete._

| Metric | Before | After | Delta |
|--------|--------|-------|-------|
| Test files | 12 | | |
| Packages with tests | 5 | | |
| Coverage: application/services | 24.0% | | |
| Coverage: grpc/handlers | 0% | | |
| Coverage: config | 0% | | |
| CI/CD runs tests | No | | |
| CI/CD runs lint | No | | |
| Semantic versioning | No | | |

---

**Checkpointing Rules:**
- Before starting a task, mark it `IN PROGRESS` in the checkpoint table below
- After completing a task, mark it `DONE` and note the date
- If a task is blocked, mark it `BLOCKED` with a reason
- Agents must read this file before starting and update it when done

---

## Checkpoint Tracker

| ID | Task | Status | Date | Notes |
|----|------|--------|------|-------|
| T1 | CI/CD: Add test execution to GitHub Actions | TODO | | |
| T2 | CI/CD: Add linting and security scanning | TODO | | |
| T3 | CI/CD: Add Dependabot configuration | TODO | | |
| T4 | Testing: Configuration validation tests | TODO | | |
| T5 | Testing: gRPC handler tests (auth) | TODO | | |
| T6 | Testing: gRPC handler tests (operator) | TODO | | |
| T7 | Testing: gRPC handler tests (account) | TODO | | |
| T8 | Testing: gRPC handler tests (user) | TODO | | |
| T9 | Testing: gRPC handler tests (cluster) | TODO | | |
| T10 | Testing: gRPC handler tests (scoped_key) | TODO | | |
| T11 | Testing: gRPC handler tests (export) | TODO | | |
| T12 | Testing: Account service tests | TODO | | |
| T13 | Testing: Export service tests | TODO | | |
| T14 | Testing: Scoped signing key service tests | TODO | | |
| T15 | Testing: HTTP/Middleware tests | TODO | | |
| T16 | Testing: Integration test expansion | TODO | | |
| T17 | Build: Semantic versioning in Makefile | TODO | | |
| T18 | Build: GoReleaser configuration | TODO | | |
| T19 | Config: Fix auto-migrate production default | TODO | | |
| T20 | Config: Remove hardcoded secrets from examples | TODO | | |
| T21 | Docs: OPERATIONS.md | TODO | | |
| T22 | Docs: SECURITY.md | TODO | | |
| T23 | Docs: DEPLOYMENT.md | TODO | | |
| T24 | Docs: README "Why NIS?" section | TODO | | |
| T25 | Docs: Architecture diagrams (ARCHITECTURE.md) | TODO | | |
| T26 | Cleanup: Remove stale files, update .gitignore | TODO | | |

---

## Task Instructions

### T1: Add Test Execution to GitHub Actions

**File:** `.github/workflows/build.yml`

**Agent Instructions:**
1. Read `.github/workflows/build.yml`
2. Add a new job `test` that runs BEFORE the `build` job
3. The test job should:
   - Check out the code
   - Set up Go 1.25
   - Run `go test -v -race -coverprofile=coverage.out ./...`
   - Upload coverage artifact
4. Make the `build` job depend on `test` via `needs: test`
5. Run the workflow file through a YAML linter mentally to ensure validity

**Acceptance Criteria:**
- [ ] `test` job exists and runs `go test -race ./...`
- [ ] `build` job has `needs: test`
- [ ] Coverage file is uploaded as artifact
- [ ] YAML is valid

---

### T2: Add Linting and Security Scanning to CI/CD

**File:** `.github/workflows/build.yml`

**Agent Instructions:**
1. Read `.github/workflows/build.yml` (after T1 changes)
2. Add a `lint` job that runs `golangci-lint` using `golangci/golangci-lint-action@v6`
3. Add a `security` step in the `build` job that runs Trivy on the built image:
   ```yaml
   - name: Scan Docker image
     if: github.event_name != 'pull_request'
     uses: aquasecurity/trivy-action@master
     with:
       image-ref: ${{ env.IMAGE_NAME }}:latest
       severity: CRITICAL,HIGH
       exit-code: 1
   ```
4. Make `build` depend on both `test` and `lint`

**Acceptance Criteria:**
- [ ] `lint` job runs golangci-lint
- [ ] Trivy scan runs on the Docker image
- [ ] YAML is valid

---

### T3: Add Dependabot Configuration

**File:** `.github/dependabot.yml` (new file)

**Agent Instructions:**
1. Create `.github/dependabot.yml` with updates for:
   - `gomod` (Go modules) - weekly schedule
   - `docker` (Dockerfile base images) - weekly schedule
   - `github-actions` (action versions) - weekly schedule
2. Set `open-pull-requests-limit: 10`

**Acceptance Criteria:**
- [ ] File exists at `.github/dependabot.yml`
- [ ] Covers gomod, docker, and github-actions ecosystems

---

### T4: Configuration Validation Tests

**File:** `internal/config/config_test.go` (new file)

**Agent Instructions:**
1. Read `internal/config/config.go` to understand the `Config` struct and `Validate()` method
2. Create `internal/config/config_test.go` with table-driven tests covering:
   - Valid config passes validation
   - Invalid database driver rejected
   - Missing SQLite path rejected
   - Missing PostgreSQL host/dbname rejected
   - Empty encryption keys rejected
   - Missing current_key_id rejected
   - current_key_id not found in keys rejected
   - Encryption key missing ID rejected
   - Encryption key missing key value rejected
   - Missing signing_key_path rejected
   - Zero/negative token_expiry rejected
3. Also test `Load()` with a temp config file
4. Use `testify/assert` for assertions (already a project dependency)
5. Run `go test ./internal/config/` to verify all tests pass

**Acceptance Criteria:**
- [ ] All `Validate()` branches have at least one test
- [ ] `Load()` tested with valid and invalid config files
- [ ] All tests pass

---

### T5-T11: gRPC Handler Tests

Each handler test follows the same pattern. Instructions below use auth_handler as the example; repeat the pattern for each handler.

**General Pattern:**

1. Read the handler file (e.g., `internal/interfaces/grpc/handlers/auth_handler.go`)
2. Read existing test patterns from `internal/application/services/auth_service_test.go` for style reference
3. Read `internal/application/services/` to understand the service interfaces the handlers depend on
4. Create a test file (e.g., `internal/interfaces/grpc/handlers/auth_handler_test.go`)
5. For each handler, set up:
   - An in-memory SQLite database (see `internal/infrastructure/persistence/sql/db_test.go` for pattern)
   - Real service instances wired with real repositories (the project uses real deps in tests, not mocks)
   - A ConnectRPC test server or call handler methods directly
6. Test each RPC method's happy path and key error paths
7. Run `go test ./internal/interfaces/grpc/handlers/` to verify

**T5: Auth Handler Tests**
- File: `internal/interfaces/grpc/handlers/auth_handler_test.go`
- Methods to test: Login, ValidateToken, CreateAPIUser, ListAPIUsers, UpdateAPIUser, DeleteAPIUser, UpdatePassword, GetCurrentUser
- Key scenarios: valid login, invalid password, expired token, permission denied

**T6: Operator Handler Tests**
- File: `internal/interfaces/grpc/handlers/operator_handler_test.go`
- Methods to test: CreateOperator, GetOperator, ListOperators, UpdateOperator, DeleteOperator
- Key scenarios: create with valid data, duplicate name, get nonexistent, cascade delete

**T7: Account Handler Tests**
- File: `internal/interfaces/grpc/handlers/account_handler_test.go`
- Methods to test: CreateAccount, GetAccount, ListAccounts, UpdateAccount, DeleteAccount
- Key scenarios: create under operator, JetStream limits, delete with users

**T8: User Handler Tests**
- File: `internal/interfaces/grpc/handlers/user_handler_test.go`
- Methods to test: CreateUser, GetUser, ListUsers, UpdateUser, DeleteUser, GetUserCredentials
- Key scenarios: create under account, permission scoping, credential generation

**T9: Cluster Handler Tests**
- File: `internal/interfaces/grpc/handlers/cluster_handler_test.go`
- Methods to test: CreateCluster, GetCluster, ListClusters, UpdateCluster, DeleteCluster, SyncCluster, GetClusterStatus
- Key scenarios: create with URLs, health check status, sync without NATS (graceful error)

**T10: Scoped Key Handler Tests**
- File: `internal/interfaces/grpc/handlers/scoped_key_handler_test.go`
- Methods to test: CreateScopedSigningKey, GetScopedSigningKey, ListScopedSigningKeys, UpdateScopedSigningKey, DeleteScopedSigningKey
- Key scenarios: create with permissions template, apply to user

**T11: Export Handler Tests**
- File: `internal/interfaces/grpc/handlers/export_handler_test.go`
- Methods to test: ExportOperator, ImportOperator (if present)
- Key scenarios: export full operator tree, import and verify entities created

**Acceptance Criteria (each):**
- [ ] Test file created with at least happy-path + 2 error-path tests per method
- [ ] Tests pass with `go test ./internal/interfaces/grpc/handlers/`
- [ ] No test relies on external services (NATS, PostgreSQL)

---

### T12: Account Service Tests

**File:** `internal/application/services/account_service_test.go` (new file)

**Agent Instructions:**
1. Read `internal/application/services/account_service.go`
2. Read existing test patterns from `internal/application/services/operator_service_test.go`
3. Create test suite with:
   - Create account under operator (happy path)
   - Create account with JetStream limits
   - Create account under nonexistent operator (error)
   - Get account by ID
   - List accounts with pagination
   - Update account name/settings
   - Delete account (verify cascade to users)
4. Run `go test ./internal/application/services/` to verify

**Acceptance Criteria:**
- [ ] All CRUD operations tested
- [ ] JetStream limit configuration tested
- [ ] Cascade delete verified
- [ ] Tests pass

---

### T13: Export Service Tests

**File:** `internal/application/services/export_service_test.go` (new file)

**Agent Instructions:**
1. Read `internal/application/services/export_service.go`
2. Create tests covering:
   - Export operator with accounts and users
   - Export format validation (check output structure)
   - Import from exported data
   - Import with conflicts (duplicate names)
3. Run tests to verify

**Acceptance Criteria:**
- [ ] Export produces valid output
- [ ] Round-trip (export → import) preserves data
- [ ] Error cases tested

---

### T14: Scoped Signing Key Service Tests

**File:** `internal/application/services/scoped_signing_key_service_test.go` (new file)

**Agent Instructions:**
1. Read `internal/application/services/scoped_signing_key_service.go`
2. Create tests covering:
   - Create scoped key with permission template
   - Get/List/Update/Delete operations
   - Permission template validation
   - Key association with account
3. Run tests to verify

**Acceptance Criteria:**
- [ ] All CRUD operations tested
- [ ] Permission template handling verified
- [ ] Tests pass

---

### T15: HTTP/Middleware Tests

**Files:** `internal/interfaces/http/ui.go`, `internal/interfaces/grpc/middleware/`

**Agent Instructions:**
1. Read all files in `internal/interfaces/http/` and `internal/interfaces/grpc/middleware/`
2. Create `internal/interfaces/http/ui_test.go`:
   - Test UI handler serves embedded files
   - Test SPA fallback (unknown routes return index.html)
3. Create middleware test files:
   - Test auth middleware rejects unauthenticated requests
   - Test auth middleware passes valid tokens
   - Test logging middleware captures request info
4. Run tests to verify

**Acceptance Criteria:**
- [ ] UI serving tested
- [ ] Auth middleware tested for accept/reject
- [ ] Tests pass

---

### T16: Integration Test Expansion

**File:** `internal/integration/` (new test files)

**Agent Instructions:**
1. Read `internal/integration/rbac_isolation_test.go` for existing pattern
2. Create `internal/integration/lifecycle_test.go`:
   - Full lifecycle: create operator → create account → create user → get credentials → delete operator (cascade)
3. Create `internal/integration/export_import_test.go`:
   - Export operator with full tree → import into fresh database → verify all entities
4. Run `go test ./internal/integration/` to verify

**Acceptance Criteria:**
- [ ] Full entity lifecycle tested end-to-end
- [ ] Export/import round-trip tested
- [ ] Tests pass

---

### T17: Semantic Versioning in Makefile

**File:** `Makefile`

**Agent Instructions:**
1. Read the current `Makefile`
2. Add a `VERSION` variable: `VERSION ?= $(shell git describe --tags --always --dirty)`
3. Add `-ldflags="-X main.version=$(VERSION)"` to `build-server` and `build-cli` targets
4. Read `cmd/nis/main.go` and `cmd/nisctl/main.go`
5. Add a `version` variable at package level: `var version = "dev"`
6. If using Cobra, register a `version` command that prints the version
7. Verify with `make build-server && ./bin/nis version`

**Acceptance Criteria:**
- [ ] `./bin/nis version` prints the version
- [ ] `./bin/nisctl version` prints the version
- [ ] Version derived from git tags

---

### T18: GoReleaser Configuration

**File:** `.goreleaser.yml` (new file)

**Agent Instructions:**
1. Create `.goreleaser.yml` configuring:
   - Builds for `cmd/nis` and `cmd/nisctl`
   - Targets: linux/amd64, linux/arm64, darwin/amd64, darwin/arm64
   - Archive format: tar.gz (Linux), zip (macOS/Windows)
   - Changelog from git commits
   - Docker image build and push
2. Add a `release` target to the Makefile: `goreleaser release --clean`
3. Optionally add a `.github/workflows/release.yml` that triggers on tag push

**Acceptance Criteria:**
- [ ] `.goreleaser.yml` exists and is valid
- [ ] Builds both binaries for multiple platforms
- [ ] Release workflow triggers on version tags

---

### T19: Fix Auto-Migrate Production Default

**Files:** `config.example.yaml`, `ansible/defaults/main.yml`

**Agent Instructions:**
1. Read `config.example.yaml` - check if `auto_migrate` is set
2. Read `ansible/defaults/main.yml` - check the default value
3. Ensure production examples default to `auto_migrate: false`
4. Add a comment in config.example.yaml: `# Set to true only for development. Use explicit migrations in production.`
5. If the serve command has a flag, ensure the flag description mentions the production risk

**Acceptance Criteria:**
- [ ] Production config examples default to `auto_migrate: false`
- [ ] Warning comment added
- [ ] Ansible defaults updated if applicable

---

### T20: Remove Hardcoded Secrets from Examples

**Files:** `config.yaml`, `config.example.yaml`, `docker-compose.yml`, `ansible/defaults/main.yml`

**Agent Instructions:**
1. Read each file and identify hardcoded secrets (encryption keys, JWT secrets, passwords)
2. Replace with placeholder values like `CHANGE_ME_GENERATE_WITH_openssl_rand_base64_32`
3. Add Makefile targets:
   ```makefile
   generate-jwt-secret:
   	@openssl rand -base64 32
   generate-encryption-key:
   	@openssl rand -base64 32
   ```
4. Add comments pointing to the generation commands
5. If `config.yaml` (not example) is tracked in git, add it to `.gitignore`

**Acceptance Criteria:**
- [ ] No real secrets in example configs
- [ ] Makefile has key generation helpers
- [ ] Comments explain how to generate proper values

---

### T21: Create OPERATIONS.md

**File:** `OPERATIONS.md` (new file)

**Agent Instructions:**
1. Read `config.example.yaml` to understand encryption key rotation config
2. Read `IMPLEMENTATION.md` for architecture context
3. Read the Dockerfile and docker-compose.yml for deployment context
4. Write `OPERATIONS.md` covering:
   - **Encryption Key Rotation**: Step-by-step procedure (add new key, update current_key_id, restart, optional re-encryption)
   - **Backup & Restore**: PostgreSQL pg_dump/pg_restore commands, SQLite file copy, volume backup for Docker
   - **Monitoring**: Health check endpoint, suggested Prometheus metrics, alerting rules
   - **Database Migrations**: How to run manually, rollback procedure, pre-migration backup
   - **Troubleshooting**: Common errors and solutions (port in use, NATS unreachable, encryption key mismatch, migration failures)
5. Keep it practical with actual commands, not theoretical

**Acceptance Criteria:**
- [ ] Key rotation documented with step-by-step commands
- [ ] Backup/restore procedures for both SQLite and PostgreSQL
- [ ] Monitoring guidance included
- [ ] Migration procedures documented

---

### T22: Create SECURITY.md

**File:** `SECURITY.md` (new file)

**Agent Instructions:**
1. Read encryption implementation: `internal/infrastructure/encryption/`
2. Read auth service: `internal/application/services/auth_service.go`
3. Read Casbin config files in `config/`
4. Write `SECURITY.md` covering:
   - **Security Model**: What is protected, trust boundaries
   - **Encryption**: ChaCha20-Poly1305 at rest, key management best practices
   - **Authentication**: JWT-based API auth, bcrypt password hashing, token expiry
   - **Authorization**: Casbin RBAC model, role definitions, permission matrix
   - **Secrets Management**: How to handle encryption keys, JWT signing keys, database credentials
   - **Network Security**: TLS recommendations, reverse proxy setup
   - **Vulnerability Reporting**: Contact information and process

**Acceptance Criteria:**
- [ ] Threat model documented
- [ ] Encryption and auth mechanisms explained
- [ ] RBAC permission matrix included
- [ ] Practical secrets management guidance

---

### T23: Create DEPLOYMENT.md

**File:** `DEPLOYMENT.md` (new file)

**Agent Instructions:**
1. Read `Dockerfile`, `docker-compose.yml`, `ansible/` directory
2. Read `QUICKSTART.md` for what's already documented
3. Write `DEPLOYMENT.md` covering:
   - **Production Checklist**: Pre-deployment verification steps
   - **Docker Deployment**: Production docker-compose with PostgreSQL, TLS termination
   - **Systemd Deployment**: Running as a systemd service (bare metal)
   - **Reverse Proxy**: Nginx/Caddy config for TLS termination
   - **Environment Variables**: Complete reference of all env vars
   - **Scaling**: Considerations for multi-instance (shared DB, stateless server)
   - **Ansible Deployment**: Reference to ansible/ directory with instructions

**Acceptance Criteria:**
- [ ] Production checklist is actionable
- [ ] At least Docker and systemd deployment paths documented
- [ ] Reverse proxy example included
- [ ] Environment variable reference complete

---

### T24: README "Why NIS?" Section

**File:** `README.md`

**Agent Instructions:**
1. Read current `README.md`
2. Add a "Why NIS?" section after the introduction, covering:
   - Problem: `nsc` is CLI-only, manual, no centralized management, no UI
   - Solution: NIS provides API-driven management, web UI, encrypted key storage, cluster sync
   - Comparison table: NIS vs nsc (features like API access, UI, key encryption, cluster push, multi-user)
   - Use cases: SaaS multi-tenancy, microservices credential management, dev team self-service
3. Keep it concise (under 60 lines)

**Acceptance Criteria:**
- [ ] "Why NIS?" section added
- [ ] Comparison table with nsc included
- [ ] Use cases listed

---

### T25: Architecture Diagrams

**File:** `ARCHITECTURE.md` (new file)

**Agent Instructions:**
1. Read `IMPLEMENTATION.md` for architecture details
2. Read key source directories to understand component relationships
3. Create `ARCHITECTURE.md` with Mermaid diagrams:
   - **System Overview**: Components (Server, CLI, UI, DB, NATS) and their connections
   - **JWT Hierarchy**: Operator → Account → User with signing relationships
   - **Cluster Sync Flow**: Sequence diagram showing JWT push to NATS
   - **Request Flow**: HTTP request → middleware → handler → service → repository → DB
   - **Hexagonal Architecture**: Ports and adapters diagram
4. Add brief text explanations alongside each diagram

**Acceptance Criteria:**
- [ ] At least 3 Mermaid diagrams
- [ ] JWT hierarchy clearly shown
- [ ] Request flow documented
- [ ] Renders correctly in GitHub markdown

---

### T26: Repository Cleanup

**Agent Instructions:**
1. Check for stale files:
   - `docker-compose.yml.bak` - delete if exists
   - Any `.db` files tracked in git - add to `.gitignore`
   - Any `config.yaml` with real secrets - add to `.gitignore`
2. Read `.gitignore` and add missing entries:
   - `*.db` (SQLite databases)
   - `config.yaml` (if it contains secrets; keep `config.example.yaml`)
   - `.env` (environment files)
   - `bin/` (if not already ignored)
3. Verify no binary files are tracked: `git ls-files | grep -E '\.(db|exe|bin)$'`

**Acceptance Criteria:**
- [ ] Stale files removed
- [ ] `.gitignore` updated
- [ ] No secrets or binaries tracked in git

---

## Execution Order

Recommended order for minimal conflicts between agents:

**Wave 1 (independent, can run in parallel):**
- T4 (config tests)
- T17 (versioning in Makefile)
- T19 (auto-migrate default)
- T20 (remove hardcoded secrets)
- T26 (cleanup)

**Wave 2 (independent, can run in parallel):**
- T1 (CI/CD tests)
- T2 (CI/CD lint/scan)
- T3 (Dependabot)
- T12 (account service tests)
- T13 (export service tests)
- T14 (scoped key service tests)

**Wave 3 (handler tests, can run in parallel):**
- T5 through T11 (all gRPC handler tests)
- T15 (HTTP/middleware tests)

**Wave 4 (depends on handler tests existing):**
- T16 (integration test expansion)
- T18 (GoReleaser - depends on T17)

**Wave 5 (documentation, can run in parallel):**
- T21 (OPERATIONS.md)
- T22 (SECURITY.md)
- T23 (DEPLOYMENT.md)
- T24 (README update)
- T25 (ARCHITECTURE.md)

---

## Agent Context Loading

Before starting any task, agents must:

1. **Read this file** to check the checkpoint table and find their task
2. **Use claude-mem MCP tools** to search for relevant context:
   ```
   search(query="<task-related keywords>", project="natsidentityservice")
   ```
3. **Read CLAUDE.md** for build/test/run commands
4. **Read the specific files** listed in their task instructions
5. **Run existing tests** to ensure they pass before making changes:
   ```
   go test ./...
   ```
6. **Update the checkpoint table** in this file when starting and completing work

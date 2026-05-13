.PHONY: generate build build-ui build-server build-cli build-all test lint clean migrate-up migrate-down run run-demo run-stop run-clean run-status run-logs run-logs-nats run-logs-pg serve-local run-dev install-cli install-ui generate-key generate-jwt-secret generate-encryption-key docker-build docker-run docker-stop docker-logs help

# Version from git tags (override with: make build-all VERSION=1.2.3)
VERSION ?= $(shell git describe --tags --always --dirty)
LDFLAGS := -s -w -X main.version=$(VERSION)

# Generate protobuf code
generate:
	buf generate

# Build the Vue UI
build-ui:
	@echo "Building Vue UI..."
	cd ui && npm install && npm run build
	@echo "Copying UI build to Go embed directory..."
	rm -rf internal/interfaces/http/ui/dist
	mkdir -p internal/interfaces/http/ui/dist
	cp -r ui/dist/* internal/interfaces/http/ui/dist/
	@echo "UI build complete!"

# Build server binary (with UI embedded)
build-server: generate build-ui
	@echo "Building Go server with embedded UI..."
	go build -ldflags="$(LDFLAGS)" -o bin/nis ./cmd/nis
	@echo "Server binary created at bin/nis"

# Build server binary (legacy target, now includes UI)
build: build-server

# Build CLI client
build-cli: generate
	go build -ldflags="$(LDFLAGS)" -o bin/nisctl ./cmd/nisctl

# Build both binaries with UI
build-all: build-server build-cli

# Run all tests
test:
	go test -v -race -coverprofile=coverage.out ./...

# Run linter
lint:
	golangci-lint run

# Install UI dependencies
install-ui:
	@echo "Installing UI dependencies..."
	cd ui && npm install

# Clean build artifacts
clean:
	@echo "Cleaning build artifacts..."
	rm -rf bin/ gen/ coverage.out
	rm -rf ui/dist/
	rm -rf internal/interfaces/http/ui/dist/
	@echo "Clean complete!"

# Run database migrations (up)
migrate-up:
	go run ./cmd/nis migrate up

# Run database migrations (down)
migrate-down:
	go run ./cmd/nis migrate down

# ---------------------------------------------------------------------------
# Dev stack: `make run` brings up a full local NIS stack with Postgres + NATS
# in Docker and the NIS server on the host. State lives under ./.run/ (gitignored).
# Lifecycle: make run | run-demo | run-status | run-logs | run-stop | run-clean
# ---------------------------------------------------------------------------

RUN_DIR             ?= .run
RUN_PG_CONTAINER    ?= nis-dev-postgres
RUN_NATS_CONTAINER  ?= nis-dev-nats
RUN_PG_PORT         ?= 5432
RUN_PG_USER         ?= nis
RUN_PG_PASS         ?= nis-dev-password
RUN_PG_DB           ?= nis
RUN_PG_VOLUME       ?= nis_dev_pg_data
RUN_PG_DSN          ?= host=localhost port=$(RUN_PG_PORT) user=$(RUN_PG_USER) password=$(RUN_PG_PASS) dbname=$(RUN_PG_DB) sslmode=disable
RUN_JWT_SECRET      ?= dev-jwt-secret-min-32-bytes-min-32-bytes
RUN_ENC_KEY         ?= 01234567890123456789012345678901
RUN_NIS_PID         := $(RUN_DIR)/nis.pid
RUN_NIS_LOG         := $(RUN_DIR)/nis.log
RUN_NATS_CONF       := $(RUN_DIR)/nats-server.conf
RUN_NATS_RESOLVER   := $(RUN_DIR)/nats-resolver
RUN_NATS_JETSTREAM  := $(RUN_DIR)/nats-jetstream
RUN_DEMO_CREDS      := $(RUN_DIR)/app-user.creds

# Spin up the full dev stack: Postgres + NATS (open mode) + NIS on host + admin user.
# NATS runs WITHOUT JWT auth here — run `make run-demo` to bootstrap JWT + a demo identity tree.
run: build-all
	@mkdir -p $(RUN_DIR)
	@echo "==> Starting Postgres ($(RUN_PG_CONTAINER))..."
	@if docker ps --format '{{.Names}}' | grep -q "^$(RUN_PG_CONTAINER)$$"; then \
		echo "    already running"; \
	elif docker ps -a --format '{{.Names}}' | grep -q "^$(RUN_PG_CONTAINER)$$"; then \
		docker start $(RUN_PG_CONTAINER) >/dev/null; \
	else \
		docker run -d --name $(RUN_PG_CONTAINER) \
			-e POSTGRES_USER=$(RUN_PG_USER) \
			-e POSTGRES_PASSWORD=$(RUN_PG_PASS) \
			-e POSTGRES_DB=$(RUN_PG_DB) \
			-p $(RUN_PG_PORT):5432 \
			-v $(RUN_PG_VOLUME):/var/lib/postgresql/data \
			postgres:16-alpine >/dev/null; \
	fi
	@echo "==> Waiting for Postgres..."
	@until docker exec $(RUN_PG_CONTAINER) pg_isready -U $(RUN_PG_USER) -d $(RUN_PG_DB) >/dev/null 2>&1; do sleep 1; done
	@echo "==> Starting NATS ($(RUN_NATS_CONTAINER), open mode — no JWT yet)..."
	@if docker ps --format '{{.Names}}' | grep -q "^$(RUN_NATS_CONTAINER)$$"; then \
		echo "    already running"; \
	elif docker ps -a --format '{{.Names}}' | grep -q "^$(RUN_NATS_CONTAINER)$$"; then \
		docker start $(RUN_NATS_CONTAINER) >/dev/null; \
	else \
		docker run -d --name $(RUN_NATS_CONTAINER) \
			-p 4222:4222 -p 8222:8222 \
			nats:2.10-alpine -js -m 8222 >/dev/null; \
	fi
	@echo "==> Waiting for NATS monitoring..."
	@until curl -sf http://localhost:8222/healthz >/dev/null 2>&1; do sleep 1; done
	@echo "==> Starting NIS server (Postgres backend, background)..."
	@if [ -f $(RUN_NIS_PID) ] && kill -0 $$(cat $(RUN_NIS_PID)) 2>/dev/null; then \
		echo "    already running (pid $$(cat $(RUN_NIS_PID))) — run 'make run-stop' to restart"; \
	else \
		DATABASE_DRIVER=postgres \
		DATABASE_DSN="$(RUN_PG_DSN)" \
		DATABASE_AUTO_MIGRATE=true \
		AUTH_JWT_SECRET="$(RUN_JWT_SECRET)" \
		ENCRYPTION_KEY="$(RUN_ENC_KEY)" \
		./bin/nis serve > $(RUN_NIS_LOG) 2>&1 & echo $$! > $(RUN_NIS_PID); \
	fi
	@echo "==> Waiting for NIS /healthz..."
	@until curl -sf http://localhost:8080/healthz >/dev/null 2>&1; do sleep 1; done
	@echo "==> Creating admin user (idempotent)..."
	@DATABASE_DRIVER=postgres DATABASE_DSN="$(RUN_PG_DSN)" \
		./bin/nis user create admin --password admin123 --role admin >/dev/null 2>&1 \
		|| echo "    (admin already exists)"
	@echo ""
	@echo "✓ NIS dev stack ready"
	@echo "  UI / API:  http://localhost:8080   (login: admin / admin123)"
	@echo "  Postgres:  localhost:$(RUN_PG_PORT)  user=$(RUN_PG_USER) db=$(RUN_PG_DB)"
	@echo "  NATS:      localhost:4222 (monitoring :8222)   JWT auth: DISABLED"
	@echo "  Logs:      $(RUN_NIS_LOG)   (tail with: make run-logs)"
	@echo "  Status:    make run-status"
	@echo "  Stop:      make run-stop    (clean wipe: make run-clean)"
	@echo "  Bootstrap JWT auth + demo identity tree: make run-demo"

# Full JWT bootstrap on top of `make run`: operator, JWT-enabled NATS, demo account/user/cluster, creds dump.
run-demo: run
	@echo "==> nisctl login..."
	@./bin/nisctl login http://localhost:8080 -u admin -p admin123 >/dev/null
	@echo "==> Creating demo-operator (idempotent)..."
	@./bin/nisctl operator create demo-operator >/dev/null 2>&1 || echo "    (operator already exists)"
	@./bin/nisctl operator generate-include demo-operator > $(RUN_NATS_CONF)
	@echo "==> Restarting NATS with JWT config..."
	@docker rm -f $(RUN_NATS_CONTAINER) >/dev/null 2>&1 || true
	@mkdir -p $(RUN_NATS_RESOLVER) $(RUN_NATS_JETSTREAM)
	@docker run -d --name $(RUN_NATS_CONTAINER) \
		-p 4222:4222 -p 8222:8222 \
		-v $(abspath $(RUN_NATS_RESOLVER)):/resolver \
		-v $(abspath $(RUN_NATS_JETSTREAM)):/data/jetstream \
		-v $(abspath $(RUN_NATS_CONF)):/nats-server.conf:ro \
		nats:2.10-alpine -c /nats-server.conf -m 8222 >/dev/null
	@until curl -sf http://localhost:8222/healthz >/dev/null 2>&1; do sleep 1; done
	@echo "==> Registering demo-cluster, app-account, app-user (idempotent)..."
	@./bin/nisctl cluster create demo-cluster --operator demo-operator --urls nats://localhost:4222 >/dev/null 2>&1 || echo "    (cluster already exists)"
	@./bin/nisctl account create app-account --operator demo-operator >/dev/null 2>&1 || echo "    (account already exists)"
	@./bin/nisctl user create app-user --operator demo-operator --account app-account >/dev/null 2>&1 || echo "    (user already exists)"
	@./bin/nisctl cluster sync demo-cluster
	@./bin/nisctl user creds app-user --operator demo-operator --account app-account > $(RUN_DEMO_CREDS)
	@echo ""
	@echo "✓ Demo identity tree provisioned"
	@echo "  Operator:  demo-operator"
	@echo "  Account:   app-account"
	@echo "  User:      app-user"
	@echo "  Cluster:   demo-cluster   (becomes healthy within ~60s)"
	@echo "  Creds:     $(RUN_DEMO_CREDS)"
	@echo ""
	@echo "Verify NATS auth round-trip:"
	@echo "  nats --creds=$(RUN_DEMO_CREDS) --server=nats://localhost:4222 rtt"

# Stop the dev stack (keeps Postgres volume so data survives).
run-stop:
	@if [ -f $(RUN_NIS_PID) ]; then \
		kill $$(cat $(RUN_NIS_PID)) 2>/dev/null || true; \
		rm -f $(RUN_NIS_PID); \
		echo "NIS server stopped"; \
	else \
		echo "NIS server not running"; \
	fi
	@docker rm -f $(RUN_NATS_CONTAINER) $(RUN_PG_CONTAINER) >/dev/null 2>&1 || true
	@echo "Containers removed (Postgres volume $(RUN_PG_VOLUME) preserved)"

# Full wipe: stop + remove Postgres volume + clear ./.run/.
run-clean: run-stop
	@docker volume rm $(RUN_PG_VOLUME) >/dev/null 2>&1 || true
	@rm -rf $(RUN_DIR)
	@echo "Volume $(RUN_PG_VOLUME) removed, $(RUN_DIR) cleared"

run-status:
	@if [ -f $(RUN_NIS_PID) ] && kill -0 $$(cat $(RUN_NIS_PID)) 2>/dev/null; then \
		echo "NIS:      running (pid $$(cat $(RUN_NIS_PID))) — http://localhost:8080"; \
	else \
		echo "NIS:      not running"; \
	fi
	@docker ps --filter name=$(RUN_PG_CONTAINER) --format 'Postgres: {{.Status}}' | grep -q . || echo "Postgres: not running"
	@docker ps --filter name=$(RUN_PG_CONTAINER) --format 'Postgres: {{.Status}}'
	@docker ps --filter name=$(RUN_NATS_CONTAINER) --format 'NATS:     {{.Status}}' | grep -q . || echo "NATS:     not running"
	@docker ps --filter name=$(RUN_NATS_CONTAINER) --format 'NATS:     {{.Status}}'

run-logs:
	@tail -f $(RUN_NIS_LOG)

run-logs-nats:
	@docker logs -f $(RUN_NATS_CONTAINER)

run-logs-pg:
	@docker logs -f $(RUN_PG_CONTAINER)

# Legacy: simple host-only server with SQLite and hardcoded dev secrets (no Docker).
serve-local:
	./bin/nis serve \
		--jwt-secret "$${NIS_JWT_SECRET:-development-secret-key-32-bytes-long!!}" \
		--encryption-key "$${NIS_ENCRYPTION_KEY:-01234567890123456789012345678901}" \
		--enable-ui

# Run in development mode (instructions)
run-dev:
	@echo "Development mode setup:"
	@echo "  1. Terminal 1: cd ui && npm run dev"
	@echo "  2. Terminal 2: make run"
	@echo ""
	@echo "UI will be at http://localhost:5173"
	@echo "API will be at http://localhost:8080"

# Install CLI to $GOPATH/bin
install-cli:
	go install ./cmd/nisctl

# Generate a new encryption key (helper)
generate-key:
	@openssl rand -base64 32

# Generate a JWT secret (minimum 32 bytes)
generate-jwt-secret:
	@openssl rand -base64 32

# Generate an encryption key (32 bytes)
generate-encryption-key:
	@openssl rand -base64 32

# Docker targets
# Default Docker image settings (can be overridden with environment variables)
DOCKER_IMAGE ?= nats-identity-service
DOCKER_TAG ?= latest
DOCKER_CONTAINER_NAME ?= nis

# Build Docker image
docker-build:
	@echo "Building Docker image $(DOCKER_IMAGE):$(DOCKER_TAG)..."
	docker build -t $(DOCKER_IMAGE):$(DOCKER_TAG) .
	@echo "Docker image built successfully!"

# Run Docker container
docker-run:
	@echo "Starting Docker container $(DOCKER_CONTAINER_NAME)..."
	docker run -d \
		--name $(DOCKER_CONTAINER_NAME) \
		-p 8080:8080 \
		-v $(PWD)/data:/data \
		-e JWT_SECRET="$${NIS_JWT_SECRET:-CHANGE_ME_GENERATE_WITH_openssl_rand_base64_32}" \
		-e ENCRYPTION_KEY="$${NIS_ENCRYPTION_KEY:-CHANGE_ME_GENERATE_WITH_openssl_rand_base64_32}" \
		-e ENCRYPTION_KEY_ID="default" \
		$(DOCKER_IMAGE):$(DOCKER_TAG) \
		./nis serve --address :8080 --jwt-secret "$${JWT_SECRET}" --encryption-key "$${ENCRYPTION_KEY}" --encryption-key-id "$${ENCRYPTION_KEY_ID}"
	@echo "Container started! Access at http://localhost:8080"
	@echo "View logs with: make docker-logs"

# Stop and remove Docker container
docker-stop:
	@echo "Stopping Docker container $(DOCKER_CONTAINER_NAME)..."
	docker stop $(DOCKER_CONTAINER_NAME) || true
	docker rm $(DOCKER_CONTAINER_NAME) || true
	@echo "Container stopped and removed!"

# Show Docker container logs
docker-logs:
	docker logs -f $(DOCKER_CONTAINER_NAME)

# Help target
help:
	@echo "NIS Makefile targets:"
	@echo "  make generate     - Generate protobuf code"
	@echo "  make build-ui     - Build Vue UI"
	@echo "  make build-server - Build Go server with embedded UI"
	@echo "  make build        - Alias for build-server"
	@echo "  make build-cli    - Build nisctl CLI tool"
	@echo "  make build-all    - Build server and CLI"
	@echo "  make install-ui   - Install UI npm dependencies"
	@echo "  make clean        - Remove build artifacts"
	@echo "  make test         - Run Go tests"
	@echo "  make lint         - Run linter"
	@echo "  make migrate-up   - Run database migrations"
	@echo "  make migrate-down - Rollback database migrations"
	@echo "  make run          - Run the server (requires build)"
	@echo "  make run-dev      - Show development mode instructions"
	@echo "  make install-cli  - Install nisctl to GOPATH/bin"
	@echo "  make generate-key            - Generate encryption key"
	@echo "  make generate-jwt-secret     - Generate a JWT secret"
	@echo "  make generate-encryption-key - Generate an encryption key"
	@echo "  make docker-build - Build Docker image"
	@echo "  make docker-run   - Run Docker container"
	@echo "  make docker-stop  - Stop and remove Docker container"
	@echo "  make docker-logs  - Show Docker container logs"
	@echo "  make help         - Show this help message"

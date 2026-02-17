.PHONY: generate build build-ui build-server build-cli build-all test lint clean migrate-up migrate-down run run-dev install-cli install-ui generate-key generate-jwt-secret generate-encryption-key docker-build docker-run docker-stop docker-logs help

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

# Run the server (requires build first)
run:
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

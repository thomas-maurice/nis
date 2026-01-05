.PHONY: generate build build-ui build-server build-cli build-all test lint clean migrate-up migrate-down run run-dev install-cli install-ui generate-key help

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
	go build -o bin/nis ./cmd/nis
	@echo "Server binary created at bin/nis"

# Build server binary (legacy target, now includes UI)
build: build-server

# Build CLI client
build-cli: generate
	go build -o bin/nisctl ./cmd/nisctl

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
		--jwt-secret "development-secret-key-32-bytes-long!!" \
		--encryption-key "01234567890123456789012345678901" \
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
	@echo "  make generate-key - Generate encryption key"
	@echo "  make help         - Show this help message"

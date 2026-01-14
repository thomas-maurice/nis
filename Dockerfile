# UI Builder stage
FROM node:22-alpine AS ui-builder
WORKDIR /ui
COPY ui/package*.json ./
RUN npm ci
COPY ui/ ./
RUN npm run build

# Go Builder stage
FROM golang:1.25-alpine AS builder

# Install build dependencies
RUN apk add --no-cache git make gcc musl-dev

WORKDIR /build

# Copy go mod files and download dependencies
COPY go.mod go.sum ./
RUN go mod download

# Copy source code
COPY . .

# Copy UI build from ui-builder stage
COPY --from=ui-builder /ui/dist ./internal/interfaces/http/ui/dist

# Build the applications with CGO enabled for SQLite
RUN CGO_ENABLED=1 GOOS=linux go build -a -ldflags="-s -w" -o nis ./cmd/nis
RUN CGO_ENABLED=1 GOOS=linux go build -a -ldflags="-s -w" -o nisctl ./cmd/nisctl

# Runtime stage
FROM alpine:3.21

# Install runtime dependencies
RUN apk --no-cache add ca-certificates sqlite-libs libgcc libstdc++ wget

WORKDIR /app

# Copy binaries and required files from builder
COPY --from=builder /build/nis .
COPY --from=builder /build/nisctl .
COPY --from=builder /build/migrations ./migrations
COPY --from=builder /build/internal/application/services/casbin_model.conf ./internal/application/services/
COPY --from=builder /build/internal/application/services/casbin_policy.csv ./internal/application/services/

# Create data directory
RUN mkdir -p /data

# Expose HTTP/gRPC port
EXPOSE 8080

# Health check
HEALTHCHECK --interval=30s --timeout=3s --start-period=5s --retries=3 \
  CMD wget --no-verbose --tries=1 --spider http://localhost:8080/healthz || exit 1

# Run as non-root user
RUN addgroup -g 1000 nis && \
    adduser -D -u 1000 -G nis nis && \
    chown -R nis:nis /app /data

USER nis

# Run the application
CMD ["./nis", "serve", "--address", ":8080"]

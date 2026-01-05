# UI Builder stage
FROM node:18-alpine AS ui-builder
WORKDIR /ui
COPY ui/package*.json ./
RUN npm ci
COPY ui/ ./
RUN npm run build

# Go Builder stage
FROM golang:1.25-alpine AS builder

# Install build dependencies including gcc for CGO
RUN apk add --no-cache git make gcc musl-dev

WORKDIR /build

# Copy go mod files
COPY go.mod go.sum ./
RUN go mod download

# Copy source code
COPY . .

# Debug: List what was copied
RUN ls -la /build
RUN ls -la /build/cmd || echo "cmd directory not found"

# Copy UI build from ui-builder stage
COPY --from=ui-builder /ui/dist ./internal/interfaces/http/ui/dist

# Build the applications with CGO enabled for SQLite
RUN CGO_ENABLED=1 GOOS=linux go build -a -o nis ./cmd/nis
RUN CGO_ENABLED=1 GOOS=linux go build -a -o nisctl ./cmd/nisctl

# Runtime stage
FROM alpine:latest

RUN apk --no-cache add ca-certificates sqlite libgcc

WORKDIR /app

# Copy binaries, migrations, and casbin files from builder
COPY --from=builder /build/nis .
COPY --from=builder /build/nisctl .
COPY --from=builder /build/migrations ./migrations
COPY --from=builder /build/internal/application/services/casbin_model.conf ./internal/application/services/
COPY --from=builder /build/internal/application/services/casbin_policy.csv ./internal/application/services/

# Create data directory for SQLite
RUN mkdir -p /data

# Expose gRPC port
EXPOSE 8080

# Set environment variables (can be overridden)
ENV DB_DSN=/data/nis.db
ENV NATS_URL=nats://nats:4222

# Run the application
CMD ["./nis", "serve", "--address", ":8080"]

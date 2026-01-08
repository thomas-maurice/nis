// Package logging provides structured logging using Charm's log library with slog compatibility.
//
// The logger can be configured via the LOG_FORMAT environment variable:
//   - "json" - JSON format for production/log aggregation
//   - "text" or unset - Interactive colorful format for terminals (default)
//
// Usage in handlers:
//
//	func (h *Handler) SomeMethod(ctx context.Context, req *Request) error {
//	    logger := logging.LogFromContext(ctx)
//	    logger.Info("Processing request", "user_id", req.UserID)
//	    // ... do work ...
//	    logger.Info("Request completed", "duration_ms", elapsed)
//	    return nil
//	}
//
// The RequestLoggingMiddleware automatically injects a logger enriched with request metadata
// (method, path, IP, user agent) into the context, so handlers can use LogFromContext(ctx)
// to get a logger that will automatically include this information in all log entries.
package logging

import (
	"context"
	"io"
	"log/slog"
	"os"

	"github.com/charmbracelet/log"
)

type contextKey string

const loggerKey contextKey = "logger"

// GetLogger returns a configured slog.Logger instance
// Uses Charm logger with format based on LOG_FORMAT env var (text or json)
func GetLogger() *slog.Logger {
	logFormat := os.Getenv("LOG_FORMAT")

	var handler slog.Handler
	opts := &log.Options{
		ReportTimestamp: true,
		ReportCaller:    true,
	}

	var writer io.Writer = os.Stderr

	if logFormat == "json" {
		// JSON format for production/non-interactive environments
		charmLogger := log.NewWithOptions(writer, *opts)
		charmLogger.SetFormatter(log.JSONFormatter)
		handler = charmLogger
	} else {
		// Text format for interactive terminals (default)
		charmLogger := log.NewWithOptions(writer, *opts)
		charmLogger.SetFormatter(log.TextFormatter)
		handler = charmLogger
	}

	return slog.New(handler)
}

// LogFromContext retrieves a logger from the context
// Falls back to GetLogger() if no logger is in the context
func LogFromContext(ctx context.Context) *slog.Logger {
	if logger, ok := ctx.Value(loggerKey).(*slog.Logger); ok && logger != nil {
		return logger
	}
	return GetLogger()
}

// WithLogger adds a logger to the context
func WithLogger(ctx context.Context, logger *slog.Logger) context.Context {
	return context.WithValue(ctx, loggerKey, logger)
}

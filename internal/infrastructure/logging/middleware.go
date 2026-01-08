package logging

import (
	"net/http"
	"time"
)

// responseWriter wraps http.ResponseWriter to capture status code
type responseWriter struct {
	http.ResponseWriter
	statusCode int
	written    int64
}

func (rw *responseWriter) WriteHeader(statusCode int) {
	rw.statusCode = statusCode
	rw.ResponseWriter.WriteHeader(statusCode)
}

func (rw *responseWriter) Write(b []byte) (int, error) {
	n, err := rw.ResponseWriter.Write(b)
	rw.written += int64(n)
	return n, err
}

// RequestLoggingMiddleware is an HTTP middleware that logs requests like Gin does
// It logs: method, path, status, latency, client IP, and user agent
// It also adds a logger to the request context enriched with request metadata
func RequestLoggingMiddleware(next http.Handler) http.Handler {
	baseLogger := GetLogger()

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()

		// Get client IP
		clientIP := r.RemoteAddr
		if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
			clientIP = xff
		}

		// Create a request-specific logger with request metadata
		requestLogger := baseLogger.With(
			"method", r.Method,
			"path", r.URL.Path,
			"ip", clientIP,
			"user_agent", r.UserAgent(),
		)

		// Add logger to request context
		ctx := WithLogger(r.Context(), requestLogger)
		r = r.WithContext(ctx)

		// Wrap the response writer to capture status code
		wrapped := &responseWriter{
			ResponseWriter: w,
			statusCode:     http.StatusOK, // default if WriteHeader is never called
		}

		// Process request
		next.ServeHTTP(wrapped, r)

		// Calculate latency
		latency := time.Since(start)

		// Log the request with final status and timing
		requestLogger.Info("HTTP Request",
			"status", wrapped.statusCode,
			"latency", latency.String(),
			"latency_ms", latency.Milliseconds(),
			"bytes", wrapped.written,
		)
	})
}

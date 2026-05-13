package metrics

import (
	"net/http"
	"strings"
	"time"
)

// pathClass collapses raw HTTP paths into a small fixed set of buckets so that
// the path_class label can be safely used in metrics without exploding cardinality.
// RPC paths are excluded from HTTP-level recording entirely — otelconnect emits
// rpc.server.* metrics for those.
func pathClass(p string) string {
	switch {
	case strings.HasPrefix(p, "/nis.v1."):
		return "rpc"
	case p == "/metrics":
		return "metrics"
	case p == "/healthz":
		return "healthz"
	case p == "/livez":
		return "livez"
	case p == "/readyz":
		return "readyz"
	case p == "/" || strings.HasPrefix(p, "/assets/"):
		return "ui"
	default:
		return "other"
	}
}

// HTTPMiddleware records request duration into nis_http_server_duration_seconds.
// It deliberately does NOT record RPC paths (otelconnect handles those) and
// scrape/probe paths (avoid noise from polling).
func (r *Recorder) HTTPMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		start := time.Now()
		cls := pathClass(req.URL.Path)

		// Skip self-monitoring traffic.
		if cls == "rpc" || cls == "metrics" || cls == "healthz" || cls == "livez" || cls == "readyz" {
			next.ServeHTTP(w, req)
			return
		}

		wrapped := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
		next.ServeHTTP(wrapped, req)

		r.recordHTTPDuration(req.Context(), time.Since(start).Seconds(), cls, req.Method, wrapped.status)
	})
}

type statusRecorder struct {
	http.ResponseWriter
	status int
}

func (s *statusRecorder) WriteHeader(code int) {
	s.status = code
	s.ResponseWriter.WriteHeader(code)
}

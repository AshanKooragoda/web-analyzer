package web

import (
	"crypto/rand"
	"log/slog"
	"net/http"
	"runtime/debug"
	"strconv"
	"time"
)

// statusRecorder captures the status code written by a handler.
type statusRecorder struct {
	http.ResponseWriter
	status int
}

func (r *statusRecorder) WriteHeader(code int) {
	r.status = code
	r.ResponseWriter.WriteHeader(code)
}

func (r *statusRecorder) Unwrap() http.ResponseWriter { return r.ResponseWriter }

// logRequests logs every request and records request metrics.
func (h *Handler) logRequests(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		rec := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
		requestID := rand.Text()[:12]
		w.Header().Set("X-Request-ID", requestID)

		next.ServeHTTP(rec, r)

		// ServeMux sets r.Pattern; using it as the label keeps cardinality low.
		route := r.Pattern
		if route == "" {
			route = "unmatched"
		}
		elapsed := time.Since(start)
		h.metrics.Requests.WithLabelValues(r.Method, route, strconv.Itoa(rec.status)).Inc()
		h.metrics.RequestDuration.WithLabelValues(r.Method, route).Observe(elapsed.Seconds())

		h.logger.InfoContext(r.Context(), "request",
			slog.String("request_id", requestID),
			slog.String("method", r.Method),
			slog.String("path", r.URL.Path),
			slog.Int("status", rec.status),
			slog.Int64("duration_ms", elapsed.Milliseconds()),
			slog.String("remote_addr", r.RemoteAddr),
		)
	})
}

// recoverPanics turns a panic in a handler into a 500 response.
func (h *Handler) recoverPanics(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if err := recover(); err != nil {
				if err == http.ErrAbortHandler {
					panic(err)
				}
				h.logger.ErrorContext(r.Context(), "panic recovered",
					slog.Any("error", err),
					slog.String("stack", string(debug.Stack())),
				)
				http.Error(w, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)
			}
		}()
		next.ServeHTTP(w, r)
	})
}

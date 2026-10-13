// Package web serves the HTML user interface for the analyzer.
package web

import (
	"bytes"
	"context"
	"embed"
	"errors"
	"fmt"
	"html/template"
	"log/slog"
	"net/http"
	"time"

	"web-analyzer/internal/analyzer"
	"web-analyzer/internal/metrics"
)

//go:embed templates/*.html
var templateFS embed.FS

// maxFormBytes limits the size of the submitted form.
const maxFormBytes = 8 << 10

// Analyzer is what the handlers need from the analyzer package.
type Analyzer interface {
	Analyze(ctx context.Context, rawURL string) (*analyzer.Result, error)
}

// Handler serves the web UI.
type Handler struct {
	analyzer Analyzer
	metrics  *metrics.Metrics
	logger   *slog.Logger
	tmpl     *template.Template
}

// pageData is passed to the index template. Result and Error are mutually
// exclusive; both are nil when the empty form is shown.
type pageData struct {
	URL    string
	Result *analyzer.Result
	Error  *errorView
}

type errorView struct {
	StatusCode int
	StatusText string
	Message    string
}

// NewHandler parses the templates and returns a ready to use Handler.
func NewHandler(a Analyzer, m *metrics.Metrics, logger *slog.Logger) (*Handler, error) {
	tmpl, err := template.New("").Funcs(template.FuncMap{
		"round": func(d time.Duration) time.Duration { return d.Round(time.Millisecond) },
	}).ParseFS(templateFS, "templates/*.html")
	if err != nil {
		return nil, fmt.Errorf("parse templates: %w", err)
	}
	return &Handler{analyzer: a, metrics: m, logger: logger, tmpl: tmpl}, nil
}

// Routes returns the application's HTTP handler, including middleware.
// metricsHandler serves /metrics.
func (h *Handler) Routes(metricsHandler http.Handler) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /{$}", h.index)
	mux.HandleFunc("POST /analyze", h.analyze)
	mux.HandleFunc("GET /healthz", h.health)
	mux.Handle("GET /metrics", metricsHandler)

	return h.logRequests(h.recoverPanics(mux))
}

func (h *Handler) index(w http.ResponseWriter, r *http.Request) {
	h.render(w, r, http.StatusOK, pageData{})
}

func (h *Handler) health(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	_, _ = w.Write([]byte("ok"))
}

func (h *Handler) analyze(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, maxFormBytes)
	if err := r.ParseForm(); err != nil {
		h.render(w, r, http.StatusBadRequest, pageData{Error: &errorView{
			StatusCode: http.StatusBadRequest,
			StatusText: http.StatusText(http.StatusBadRequest),
			Message:    "The form could not be read.",
		}})
		return
	}
	rawURL := r.PostForm.Get("url")

	start := time.Now()
	res, err := h.analyzer.Analyze(r.Context(), rawURL)
	h.metrics.AnalysisDuration.Observe(time.Since(start).Seconds())

	if err != nil {
		status, view, outcome := h.describeError(r, rawURL, err)
		h.metrics.Analyses.WithLabelValues(outcome).Inc()
		h.render(w, r, status, pageData{URL: rawURL, Error: view})
		return
	}

	h.metrics.Analyses.WithLabelValues("success").Inc()
	h.metrics.LinksChecked.Add(float64(res.CheckedLinks))
	h.metrics.LinksBroken.Add(float64(len(res.InaccessibleLinks)))
	h.logger.InfoContext(r.Context(), "page analyzed",
		slog.String("url", res.URL),
		slog.Int("internal_links", res.InternalLinks),
		slog.Int("external_links", res.ExternalLinks),
		slog.Int("inaccessible_links", len(res.InaccessibleLinks)),
		slog.Int64("duration_ms", res.Duration.Milliseconds()),
	)
	h.render(w, r, http.StatusOK, pageData{URL: rawURL, Result: res})
}

// describeError maps an analyzer error to the response status, the message
// shown to the user and the outcome label used for metrics.
func (h *Handler) describeError(r *http.Request, rawURL string, err error) (int, *errorView, string) {
	var fetchErr *analyzer.FetchError

	switch {
	case errors.Is(err, analyzer.ErrInvalidURL):
		return http.StatusBadRequest, &errorView{
			StatusCode: http.StatusBadRequest,
			StatusText: http.StatusText(http.StatusBadRequest),
			Message:    err.Error(),
		}, "invalid_url"

	case errors.As(err, &fetchErr):
		h.logger.WarnContext(r.Context(), "target page unreachable",
			slog.String("url", rawURL), slog.Any("error", err))
		// The status shown is the target's; the response itself is a gateway error.
		status := http.StatusBadGateway
		if fetchErr.StatusCode == http.StatusGatewayTimeout {
			status = http.StatusGatewayTimeout
		}
		msg := fetchErr.Message
		if fetchErr.Err != nil {
			msg += " (" + fetchErr.Err.Error() + ")"
		}
		return status, &errorView{
			StatusCode: fetchErr.StatusCode,
			StatusText: http.StatusText(fetchErr.StatusCode),
			Message:    msg,
		}, "fetch_error"

	default:
		h.logger.ErrorContext(r.Context(), "analysis failed",
			slog.String("url", rawURL), slog.Any("error", err))
		return http.StatusInternalServerError, &errorView{
			StatusCode: http.StatusInternalServerError,
			StatusText: http.StatusText(http.StatusInternalServerError),
			Message:    "The page could not be analyzed because of an unexpected error.",
		}, "error"
	}
}

// render executes the template into a buffer first so that a template error
// never results in a half written page.
func (h *Handler) render(w http.ResponseWriter, r *http.Request, status int, data pageData) {
	var buf bytes.Buffer
	if err := h.tmpl.ExecuteTemplate(&buf, "index.html", data); err != nil {
		h.logger.ErrorContext(r.Context(), "render template", slog.Any("error", err))
		http.Error(w, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(status)
	_, _ = buf.WriteTo(w)
}

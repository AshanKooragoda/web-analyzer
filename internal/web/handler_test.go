package web

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"

	"web-analyzer/internal/analyzer"
	"web-analyzer/internal/metrics"
)

// fakeAnalyzer returns a canned result so handler tests need no network.
type fakeAnalyzer func(ctx context.Context, rawURL string) (*analyzer.Result, error)

func (f fakeAnalyzer) Analyze(ctx context.Context, rawURL string) (*analyzer.Result, error) {
	return f(ctx, rawURL)
}

func newTestServer(t *testing.T, a Analyzer) (http.Handler, *metrics.Metrics) {
	t.Helper()
	m := metrics.New(prometheus.NewRegistry())
	h, err := NewHandler(a, m, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	metricsHandler := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, "metrics here")
	})
	return h.Routes(metricsHandler), m
}

func postURL(srv http.Handler, target string) *httptest.ResponseRecorder {
	form := url.Values{"url": {target}}
	req := httptest.NewRequest(http.MethodPost, "/analyze", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)
	return rec
}

func TestIndex(t *testing.T) {
	srv, _ := newTestServer(t, nil)
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	body := rec.Body.String()
	for _, want := range []string{`<form method="post" action="/analyze">`, `name="url"`, "Analyze"} {
		if !strings.Contains(body, want) {
			t.Errorf("body missing %q", want)
		}
	}
	if strings.Contains(body, "<script") {
		t.Error("page should not contain JavaScript")
	}
	if rec.Header().Get("X-Request-ID") == "" {
		t.Error("missing X-Request-ID header")
	}
}

func TestAnalyzeSuccess(t *testing.T) {
	var gotURL string
	srv, m := newTestServer(t, fakeAnalyzer(func(_ context.Context, rawURL string) (*analyzer.Result, error) {
		gotURL = rawURL
		return &analyzer.Result{
			URL:           "https://example.com",
			HTMLVersion:   "HTML5",
			Title:         "Example <Domain>",
			Headings:      []analyzer.HeadingCount{{Level: "h1", Count: 2}},
			InternalLinks: 4,
			ExternalLinks: 7,
			CheckedLinks:  9,
			InaccessibleLinks: []analyzer.BrokenLink{
				{URL: "https://example.com/gone", StatusCode: 404, Reason: "Not Found"},
				{URL: "https://down.test", Reason: "connection refused"},
			},
			UncheckedLinks: 1,
			HasLoginForm:   true,
			Duration:       1234567 * time.Microsecond,
		}, nil
	}))

	rec := postURL(srv, "example.com")

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if gotURL != "example.com" {
		t.Errorf("analyzer got %q, want the submitted value", gotURL)
	}
	body := rec.Body.String()
	for _, want := range []string{
		"HTML5",
		"Example &lt;Domain&gt;", // escaped by html/template
		"<b>2</b><span>h1</span>",
		"Internal links</th><td class=\"num\">4",
		"External links</th><td class=\"num\">7",
		"Inaccessible links</th><td class=\"num\">2",
		"Not checked (time limit reached)",
		"https://example.com/gone",
		"connection refused",
		`<span class="yes">Yes</span>`,
		"1.235s",
		`value="example.com"`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("body missing %q", want)
		}
	}
	if got := testutil.ToFloat64(m.Analyses.WithLabelValues("success")); got != 1 {
		t.Errorf("analyses_total{success} = %v, want 1", got)
	}
	if got := testutil.ToFloat64(m.LinksBroken); got != 2 {
		t.Errorf("links_inaccessible_total = %v, want 2", got)
	}
}

func TestAnalyzeErrors(t *testing.T) {
	tests := []struct {
		name        string
		err         error
		wantStatus  int
		wantInBody  []string
		wantOutcome string
	}{
		{
			name:        "invalid url",
			err:         errors.Join(analyzer.ErrInvalidURL, errors.New("bad input")),
			wantStatus:  http.StatusBadRequest,
			wantInBody:  []string{"Error 400", "bad input"},
			wantOutcome: "invalid_url",
		},
		{
			name:        "target returned 404",
			err:         &analyzer.FetchError{StatusCode: http.StatusNotFound, Message: "example.com responded with 404 Not Found"},
			wantStatus:  http.StatusBadGateway,
			wantInBody:  []string{"Error 404", "Not Found", "example.com responded with 404 Not Found"},
			wantOutcome: "fetch_error",
		},
		{
			name:        "timeout",
			err:         &analyzer.FetchError{StatusCode: http.StatusGatewayTimeout, Message: "slow.test did not respond", Err: context.DeadlineExceeded},
			wantStatus:  http.StatusGatewayTimeout,
			wantInBody:  []string{"Error 504", "Gateway Timeout", "did not respond", "context deadline exceeded"},
			wantOutcome: "fetch_error",
		},
		{
			name:        "unexpected",
			err:         errors.New("disk on fire"),
			wantStatus:  http.StatusInternalServerError,
			wantInBody:  []string{"Error 500", "unexpected error"},
			wantOutcome: "error",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			srv, m := newTestServer(t, fakeAnalyzer(func(context.Context, string) (*analyzer.Result, error) {
				return nil, tt.err
			}))

			rec := postURL(srv, "https://example.com")

			if rec.Code != tt.wantStatus {
				t.Errorf("status = %d, want %d", rec.Code, tt.wantStatus)
			}
			body := rec.Body.String()
			for _, want := range tt.wantInBody {
				if !strings.Contains(body, want) {
					t.Errorf("body missing %q", want)
				}
			}
			if tt.name == "unexpected" && strings.Contains(body, "disk on fire") {
				t.Error("internal error details leaked to the user")
			}
			if got := testutil.ToFloat64(m.Analyses.WithLabelValues(tt.wantOutcome)); got != 1 {
				t.Errorf("analyses_total{%s} = %v, want 1", tt.wantOutcome, got)
			}
		})
	}
}

func TestAnalyzeRejectsOversizedForm(t *testing.T) {
	srv, _ := newTestServer(t, fakeAnalyzer(func(context.Context, string) (*analyzer.Result, error) {
		t.Fatal("analyzer should not be called")
		return nil, nil
	}))
	rec := postURL(srv, strings.Repeat("a", maxFormBytes+1))
	if rec.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", rec.Code)
	}
}

func TestRoutes(t *testing.T) {
	srv, m := newTestServer(t, nil)
	tests := []struct {
		method, path string
		wantStatus   int
		wantBody     string
	}{
		{http.MethodGet, "/healthz", http.StatusOK, "ok"},
		{http.MethodGet, "/metrics", http.StatusOK, "metrics here"},
		{http.MethodGet, "/nope", http.StatusNotFound, ""},
		{http.MethodGet, "/analyze", http.StatusMethodNotAllowed, ""},
	}
	for _, tt := range tests {
		t.Run(tt.method+" "+tt.path, func(t *testing.T) {
			rec := httptest.NewRecorder()
			srv.ServeHTTP(rec, httptest.NewRequest(tt.method, tt.path, nil))
			if rec.Code != tt.wantStatus {
				t.Errorf("status = %d, want %d", rec.Code, tt.wantStatus)
			}
			if !strings.Contains(rec.Body.String(), tt.wantBody) {
				t.Errorf("body = %q, want it to contain %q", rec.Body.String(), tt.wantBody)
			}
		})
	}
	if got := testutil.ToFloat64(m.Requests.WithLabelValues("GET", "GET /healthz", "200")); got != 1 {
		t.Errorf("http_requests_total for /healthz = %v, want 1", got)
	}
}

func TestRecoverPanics(t *testing.T) {
	srv, m := newTestServer(t, fakeAnalyzer(func(context.Context, string) (*analyzer.Result, error) {
		panic("something broke")
	}))

	rec := postURL(srv, "https://example.com")

	if rec.Code != http.StatusInternalServerError {
		t.Errorf("status = %d, want 500", rec.Code)
	}
	if got := testutil.ToFloat64(m.Requests.WithLabelValues("POST", "POST /analyze", "500")); got != 1 {
		t.Errorf("panic was not recorded as a 500 request, got %v", got)
	}
}

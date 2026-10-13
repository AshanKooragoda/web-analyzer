package analyzer

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"testing"
	"time"
)

const samplePage = `<!DOCTYPE html>
<html>
<head><title>Sample Page</title></head>
<body>
  <h1>Welcome</h1>
  <h2>News</h2><h2>Events</h2>
  <a href="/about">About</a>
  <a href="/missing">Broken</a>
  <a href="https://golang.org">Go</a>
  <a href="mailto:me@example.com">Mail</a>
  <form action="/login"><input name="user"><input type="password" name="pass"></form>
</body>
</html>`

func TestAnalyze(t *testing.T) {
	client := doerFunc(func(req *http.Request) (*http.Response, error) {
		switch req.URL.String() {
		case "https://example.com":
			if req.Method != http.MethodGet {
				t.Errorf("page fetched with %s, want GET", req.Method)
			}
			return htmlResponse(req, samplePage), nil
		case "https://example.com/about", "https://golang.org":
			return response(req, http.StatusOK, "", ""), nil
		case "https://example.com/missing":
			return response(req, http.StatusNotFound, "", ""), nil
		}
		t.Errorf("unexpected request %s %s", req.Method, req.URL)
		return nil, errors.New("unexpected request")
	})

	res, err := New(client, Config{}).Analyze(context.Background(), "example.com")
	if err != nil {
		t.Fatalf("Analyze: %v", err)
	}

	if res.URL != "https://example.com" {
		t.Errorf("URL = %q", res.URL)
	}
	if res.HTMLVersion != "HTML5" {
		t.Errorf("HTMLVersion = %q, want HTML5", res.HTMLVersion)
	}
	if res.Title != "Sample Page" {
		t.Errorf("Title = %q", res.Title)
	}
	wantHeadings := []HeadingCount{{"h1", 1}, {"h2", 2}, {"h3", 0}, {"h4", 0}, {"h5", 0}, {"h6", 0}}
	if !slices.Equal(res.Headings, wantHeadings) {
		t.Errorf("Headings = %v, want %v", res.Headings, wantHeadings)
	}
	if res.InternalLinks != 2 || res.ExternalLinks != 1 {
		t.Errorf("internal, external = %d, %d; want 2, 1", res.InternalLinks, res.ExternalLinks)
	}
	if res.CheckedLinks != 3 || res.UncheckedLinks != 0 {
		t.Errorf("checked, unchecked = %d, %d; want 3, 0", res.CheckedLinks, res.UncheckedLinks)
	}
	if len(res.InaccessibleLinks) != 1 || res.InaccessibleLinks[0].URL != "https://example.com/missing" {
		t.Errorf("InaccessibleLinks = %+v", res.InaccessibleLinks)
	}
	if !res.HasLoginForm {
		t.Error("HasLoginForm = false, want true")
	}
}

func TestAnalyzeUsesFinalURLAfterRedirect(t *testing.T) {
	client := doerFunc(func(req *http.Request) (*http.Response, error) {
		if req.URL.Host == "short.test" {
			// Simulate http.Client having followed a redirect.
			final, _ := url.Parse("https://www.target.test/home")
			redirected := req.Clone(req.Context())
			redirected.URL = final
			return htmlResponse(redirected, `<a href="/page">x</a><a href="https://short.test/y">y</a>`), nil
		}
		return response(req, http.StatusOK, "", ""), nil
	})

	res, err := New(client, Config{}).Analyze(context.Background(), "https://short.test/abc")
	if err != nil {
		t.Fatal(err)
	}
	if res.URL != "https://www.target.test/home" {
		t.Errorf("URL = %q, want final URL", res.URL)
	}
	if res.InternalLinks != 1 || res.ExternalLinks != 1 {
		t.Errorf("internal, external = %d, %d; want 1, 1", res.InternalLinks, res.ExternalLinks)
	}
}

func TestAnalyzeDecodesCharset(t *testing.T) {
	client := doerFunc(func(req *http.Request) (*http.Response, error) {
		// "Café" encoded as ISO-8859-1.
		return response(req, http.StatusOK, "text/html; charset=iso-8859-1", "<title>Caf\xe9</title>"), nil
	})
	res, err := New(client, Config{}).Analyze(context.Background(), "https://example.com")
	if err != nil {
		t.Fatal(err)
	}
	if res.Title != "Café" {
		t.Errorf("Title = %q, want Café", res.Title)
	}
}

func TestAnalyzeErrors(t *testing.T) {
	tests := []struct {
		name       string
		input      string
		respond    func(*http.Request) (*http.Response, error)
		wantStatus int    // expected FetchError status, 0 for ErrInvalidURL
		wantMsg    string // substring of the user facing message
	}{
		{
			name:  "invalid url",
			input: "ftp://example.com",
		},
		{
			name:  "not found",
			input: "https://example.com/nope",
			respond: func(req *http.Request) (*http.Response, error) {
				return response(req, http.StatusNotFound, "text/html", "not here"), nil
			},
			wantStatus: http.StatusNotFound,
			wantMsg:    "example.com responded with 404 Not Found",
		},
		{
			name:  "server error",
			input: "https://example.com",
			respond: func(req *http.Request) (*http.Response, error) {
				return response(req, http.StatusServiceUnavailable, "", ""), nil
			},
			wantStatus: http.StatusServiceUnavailable,
			wantMsg:    "503 Service Unavailable",
		},
		{
			name:  "dns failure",
			input: "https://no-such-host.test",
			respond: func(*http.Request) (*http.Response, error) {
				return nil, &url.Error{Op: "Get", URL: "https://no-such-host.test", Err: &net.DNSError{Err: "no such host", Name: "no-such-host.test", IsNotFound: true}}
			},
			wantStatus: http.StatusBadGateway,
			wantMsg:    "could not be resolved",
		},
		{
			name:  "timeout",
			input: "https://slow.test",
			respond: func(*http.Request) (*http.Response, error) {
				return nil, &url.Error{Op: "Get", URL: "https://slow.test", Err: context.DeadlineExceeded}
			},
			wantStatus: http.StatusGatewayTimeout,
			wantMsg:    "did not respond within",
		},
		{
			name:  "connection refused",
			input: "https://closed.test",
			respond: func(*http.Request) (*http.Response, error) {
				return nil, &url.Error{Op: "Get", URL: "https://closed.test", Err: errors.New("connection refused")}
			},
			wantStatus: http.StatusBadGateway,
			wantMsg:    "could not connect to closed.test",
		},
		{
			name:  "not html",
			input: "https://example.com/data.json",
			respond: func(req *http.Request) (*http.Response, error) {
				return response(req, http.StatusOK, "application/json", "{}"), nil
			},
			wantStatus: http.StatusUnsupportedMediaType,
			wantMsg:    "not an HTML page",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			client := doerFunc(func(req *http.Request) (*http.Response, error) {
				if tt.respond == nil {
					t.Fatal("no request expected")
				}
				return tt.respond(req)
			})
			_, err := New(client, Config{FetchTimeout: time.Second}).Analyze(context.Background(), tt.input)

			if tt.wantStatus == 0 {
				if !errors.Is(err, ErrInvalidURL) {
					t.Fatalf("error = %v, want ErrInvalidURL", err)
				}
				return
			}
			var fe *FetchError
			if !errors.As(err, &fe) {
				t.Fatalf("error = %v, want *FetchError", err)
			}
			if fe.StatusCode != tt.wantStatus {
				t.Errorf("StatusCode = %d, want %d", fe.StatusCode, tt.wantStatus)
			}
			if !strings.Contains(fe.Error(), tt.wantMsg) {
				t.Errorf("error %q does not contain %q", fe.Error(), tt.wantMsg)
			}
		})
	}
}

func TestFetchErrorUnwrap(t *testing.T) {
	cause := errors.New("boom")
	err := &FetchError{StatusCode: http.StatusBadGateway, Message: "failed", Err: cause}
	if !errors.Is(err, cause) {
		t.Error("errors.Is(FetchError, cause) = false, want true")
	}
	if got, want := err.Error(), "502 Bad Gateway: failed: boom"; got != want {
		t.Errorf("Error() = %q, want %q", got, want)
	}
}

func TestIsHTML(t *testing.T) {
	tests := map[string]bool{
		"":                         true,
		"text/html":                true,
		"TEXT/HTML; charset=UTF-8": true,
		"application/xhtml+xml":    true,
		"application/json":         false,
		"image/png":                false,
		"text/plain":               false,
		"text/html;;; broken":      true,
	}
	for ct, want := range tests {
		if got := isHTML(ct); got != want {
			t.Errorf("isHTML(%q) = %v, want %v", ct, got, want)
		}
	}
}

func TestConfigDefaults(t *testing.T) {
	cfg := Config{}.withDefaults()
	if cfg.FetchTimeout <= 0 || cfg.LinkTimeout <= 0 || cfg.LinkCheckBudget <= 0 || cfg.Workers <= 0 || cfg.MaxBodyBytes <= 0 {
		t.Errorf("withDefaults left zero values: %+v", cfg)
	}
	custom := Config{Workers: 3}.withDefaults()
	if custom.Workers != 3 {
		t.Errorf("Workers = %d, want 3 to be kept", custom.Workers)
	}
}

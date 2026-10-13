# Plan: Web Page Analyzer (Go)

## 1. Goal

A Go web application with a form where the user enters a URL. The server fetches the page and shows:

- HTML version
- Page title
- Heading counts per level (h1–h6)
- Internal / external link counts, and how many links are inaccessible
- Whether the page contains a login form

If the URL is unreachable, show an error with the HTTP status code and a useful description.

Deliverables: a git repo, a README (build/deploy steps, assumptions, improvements), Docker + Makefile, and unit tests with ≥70% coverage.

## 2. Open points to keep in mind

- The checklist says "Avoid using AI generated code in the application", but the team has confirmed AI assistance is allowed for this assignment. The author must still understand and be able to explain every part of the code.
- Checklist item 1.3 ("focus more on architectural complexity rather than idiomatic Go") reads as reversed. Go with **simple, idiomatic Go** and avoid over-engineering.

## 3. Assumptions / decisions (copy to README)

| Topic | Decision |
|---|---|
| HTML version | Read the `<!DOCTYPE>`. `<!DOCTYPE html>` → HTML5. Map public IDs to HTML 4.01 Strict/Transitional/Frameset and XHTML 1.0/1.1. No doctype → "Unknown (quirks mode)". |
| Title | Text of the first `<title>` in `<head>`, trimmed. Empty → "(no title)". |
| Headings | Count `h1`–`h6` elements anywhere in the document. |
| Internal vs external | Resolve each `href` against the page URL (or the `<base href>` if present). Same host → internal. Different host → external. |
| Ignored links | `mailto:`, `tel:`, `javascript:`, `data:`, empty `href` and fragment-only (`#x`) links. |
| Inaccessible link | Check each unique link with a HEAD request, falling back to GET on 405/501. Count it as inaccessible on status ≥ 400, a network error or a timeout. |
| Login form | A `<form>` containing `<input type="password">`. |
| URL validation | `net/url` (no regex). Scheme must be `http`/`https` and a host must be present. Add `https://` if the scheme is missing. |
| Unreachable target | Upstream 4xx/5xx → show that status and the status text. DNS/connection error → 502 Bad Gateway. Timeout → 504 Gateway Timeout. Always include the underlying cause. |
| Limits | Fetch timeout 15s, link-check timeout 5s per link, max body 10 MB, at most ~20 concurrent link checks. |

## 4. Tech stack

- **Language:** Go 1.27, modules for dependency management
- **HTTP:** standard `net/http` with Go 1.22+ `ServeMux` patterns (no framework)
- **HTML parsing:** `golang.org/x/net/html`
- **Templates:** `html/template` + `embed` (server-rendered, **no JavaScript**)
- **Logging:** `log/slog` (JSON handler)
- **Metrics:** `github.com/prometheus/client_golang`
- **Profiling:** `net/http/pprof` on a separate admin port
- **Testing:** `testing`, `net/http/httptest`, table-driven tests
- **DevOps:** multi-stage Dockerfile, Makefile, `golangci-lint`

## 5. Project layout

```
.
├── cmd/server/main.go          # config, wiring, graceful shutdown
├── internal/
│   ├── analyzer/
│   │   ├── analyzer.go         # Analyze(ctx, url) (*Result, error)
│   │   ├── parse.go            # single-pass DOM walk: version, title, headings, links, login form
│   │   ├── links.go            # concurrent link checker (worker pool)
│   │   ├── errors.go           # FetchError{StatusCode, Message}
│   │   └── *_test.go
│   ├── web/
│   │   ├── handler.go          # GET /, POST /analyze
│   │   ├── middleware.go       # request logging, panic recovery, metrics
│   │   ├── templates/          # index.html, result.html, error.html
│   │   └── *_test.go
│   └── metrics/metrics.go      # Prometheus collectors
├── Dockerfile
├── Makefile
├── README.md
├── go.mod / go.sum
└── .gitignore
```

## 6. Endpoints

| Method | Path | Port | Purpose |
|---|---|---|---|
| GET | `/` | 8080 | Form page |
| POST | `/analyze` | 8080 | Run analysis, render result or error page |
| GET | `/healthz` | 8080 | Liveness check |
| GET | `/metrics` | 8080 | Prometheus metrics |
| GET | `/debug/pprof/` | 6060 | pprof (admin port only) |

## 7. Key design details

### Analyzer
- Depends on an `HTTPDoer` interface (`Do(*http.Request) (*http.Response, error)`) instead of a concrete client, so tests can inject fakes.
- Flow: validate URL → fetch (with context timeout) → check status → `html.Parse(io.LimitReader(...))` → walk the tree once → check links concurrently → return `Result`.
- Use the **final URL after redirects** (`resp.Request.URL`) as the base for internal/external classification.

### Concurrent link checker
- Deduplicate URLs first.
- Worker pool: N goroutines read from a `jobs` channel and write to a `results` channel.
- `sync.WaitGroup` waits for the workers; a separate goroutine closes `results` after `wg.Wait()`.
- Respect `ctx.Done()` so a cancelled request stops all workers.
- Drain the response body (`io.Copy(io.Discard, ...)`) and close it to allow connection reuse.

### Error handling
- Wrap errors with `fmt.Errorf("...: %w", err)`.
- Map errors to user-facing messages in one place in the web layer (`errors.As` on `*FetchError`, `context.DeadlineExceeded`, `*net.DNSError`).
- Never expose stack traces. Log full details with `slog`.

### Observability
- Metrics: `http_requests_total{path,code}`, `analysis_duration_seconds` (histogram), `link_checks_total{result}`.
- Logging middleware records method, path, status, duration and a request ID.

## 8. Implementation steps

1. **Setup:** `git init`, `go mod init`, `.gitignore`, a minimal `main.go` with `slog` and graceful shutdown (`signal.NotifyContext`, `srv.Shutdown`). *Commit.*
2. **Parsing:** implement and unit-test HTML version, title, headings, login form and link extraction against inline HTML fixtures (table-driven). *Commit.*
3. **URL validation and link classification:** `net/url` based, with table-driven tests (relative, protocol-relative, `<base>`, `www.` vs bare host). *Commit.*
4. **Fetcher and analyzer:** `HTTPDoer` interface and `FetchError`, tested with `httptest.Server` (local, no internet) for 200/404/500/timeout. *Commit.*
5. **Link checker:** worker pool with WaitGroup + channels, tested with a fake `RoundTripper` that returns mixed statuses, plus a cancellation test. *Commit.*
6. **Web layer:** handlers, embedded templates, minimal CSS, tested with `httptest.NewRecorder` (form renders, invalid URL → 400, success → result, upstream 404 → error page). *Commit.*
7. **Middleware and metrics:** logging, recovery, Prometheus, pprof on the admin port. *Commit.*
8. **DevOps:** multi-stage Dockerfile (`golang:1.27` → `distroless/static`), Makefile targets. *Commit.*
9. **Quality pass:** `go vet`, `golangci-lint`, `go test -race -cover ./...`, and confirm coverage ≥ 70%. *Commit.*
10. **Docs:** README and screenshots. *Commit.*

## 9. Makefile targets

```
make run          # go run ./cmd/server
make build        # go build -o bin/server ./cmd/server
make test         # go test -race ./...
make cover        # coverage report (HTML)
make lint         # go vet + golangci-lint
make docker-build # docker build -t web-analyzer .
make docker-run   # docker run -p 8080:8080 web-analyzer
```

## 10. Testing strategy

- **Unit tests** (no real network): HTML fixtures as strings, `httptest.Server` bound to localhost, fake `RoundTripper` for the link checker.
- **Coverage target:** ≥ 70% (`go test -coverprofile`).
- **Integration tests** (document them; optionally implement): use a `//go:build integration` tag that analyzes real sites (e.g. `https://example.com`) and runs with `go test -tags=integration ./...`.

## 11. README outline (per checklist)

1. Project overview
2. Prerequisites (Go 1.27, Docker, Make)
3. Technologies used (BE, FE, DevOps, validation)
4. External dependencies and how to install them (`go mod download`)
5. Setup and run instructions (local, Make, Docker)
6. URLs and endpoints (app, metrics, pprof, health), plus a short API spec for `POST /analyze`
7. Usage and main features (analysis, logging, error handling, metrics)
8. Assumptions and decisions (section 3 above)
9. Testing (unit, coverage, integration)
10. Challenges and how they were solved
11. Possible improvements
12. Screenshots (optional)

## 12. Possible improvements (for README)

- Cache results per URL with a TTL
- Async analysis (job queue + polling) for pages with many links
- Headless browser rendering for JavaScript-heavy pages
- SSRF protection (block private/loopback IPs)
- Per-host rate limiting and `robots.txt` respect
- JSON API alongside the HTML UI
- OpenTelemetry tracing
- Smarter login detection (e.g. OAuth buttons, multi-step forms)

## 13. Checklist mapping

| Checklist item | Covered by |
|---|---|
| Idiomatic Go, no Java style | Small packages, interfaces at the consumer, no getters/setters, no deep hierarchies |
| Simple working UI, minimal JS | `html/template`, no JavaScript |
| slog / logrus | `log/slog` |
| Standard URL validation | `net/url` |
| pprof, Prometheus | §7 Observability |
| WaitGroups, channels, concurrency | §7 Link checker |
| Error handling | §7 Error handling |
| Dependency management | Go modules, few dependencies |
| Docker / Makefile | §8 step 8, §9 |
| ≥70% unit tests, no real HTTP | §10 |
| README contents | §11 |

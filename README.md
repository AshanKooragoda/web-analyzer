# Web Page Analyzer

A small Go web application that analyzes a web page. You enter a URL and the app reports:

- the **HTML version** of the document
- the **page title**
- how many **headings** of each level (h1–h6) it has
- how many **internal** and **external** links it has, and how many of them are **inaccessible**
- whether the page contains a **login form**

If the page can't be reached, the app shows the HTTP status code and a description of what went wrong.

### Documentation

| Document | Read it to understand |
|---|---|
| [docs/architecture.md](docs/architecture.md) | How the code is organized: layers, packages, interfaces, startup/shutdown, middleware, concurrency model, testing seams, and where to make changes |
| [docs/how-analysis-works.md](docs/how-analysis-works.md) | What happens to a URL step by step, from validation through fetching, parsing, link classification and concurrent link checking to error handling, with diagrams |

---

## Contents

1. [Prerequisites](#prerequisites)
2. [Quick start](#quick-start)
3. [Technologies used](#technologies-used)
4. [URLs and API](#urls-and-api)
5. [Project structure](#project-structure)
6. [How it works](#how-it-works)
7. [Assumptions and decisions](#assumptions-and-decisions)
8. [Testing](#testing)
9. [Configuration](#configuration)
10. [Challenges and solutions](#challenges-and-solutions)
11. [Possible improvements](#possible-improvements)

---

## Prerequisites

| Tool | Version | Needed for |
|---|---|---|
| [Go](https://go.dev/dl/) | 1.27 or newer | building and running locally |
| [GNU Make](https://www.gnu.org/software/make/) | any | the shortcut commands (optional) |
| [Docker](https://docs.docker.com/get-docker/) | 20.10 or newer | running in a container (optional) |
| [golangci-lint](https://golangci-lint.run/welcome/install/) | any | `make lint` (optional; skipped if missing) |

### External dependencies

Go modules manage all dependencies. They are listed in `go.mod` and downloaded automatically by `go build`, `go test` or `go mod download`.

| Module | Purpose |
|---|---|
| [`golang.org/x/net/html`](https://pkg.go.dev/golang.org/x/net/html) | HTML5-compliant parser (from the Go project) |
| [`golang.org/x/net/html/charset`](https://pkg.go.dev/golang.org/x/net/html/charset) | Decodes pages that are not UTF-8 |
| [`github.com/prometheus/client_golang`](https://github.com/prometheus/client_golang) | Prometheus metrics |

Everything else comes from the standard library: `net/http`, `html/template`, `log/slog`, `net/url`, `net/http/pprof`, `embed` and `sync`.

## Quick start

### Option 1: Make

```bash
make run              # starts on http://localhost:8080
```

### Option 2: Plain Go

```bash
go mod download
go run ./cmd/server
```

### Option 3: Docker

```bash
make docker-run       # or:
docker build -t web-analyzer .
docker run --rm -p 8080:8080 web-analyzer
```

Then open **http://localhost:8080**, type a URL (for example `go.dev`) and press **Analyze**.

### All Make targets

```
make help             # list targets
make run              # run locally
make build            # build bin/server
make test             # unit tests with the race detector
make test-integration # tests against real websites (needs internet)
make cover            # coverage summary + coverage.html
make lint             # go vet + golangci-lint
make docker-build     # build the image
make docker-run       # build and run the image
make clean            # remove build output
```

## Technologies used

| Area | Technology |
|---|---|
| **Backend** | Go 1.27, standard library `net/http` with the Go 1.22+ `ServeMux` routing patterns (no web framework) |
| **Frontend** | Server-rendered `html/template` page with inline CSS. **No JavaScript.** It supports light and dark mode and works on mobile. |
| **HTML parsing** | `golang.org/x/net/html` |
| **Field validation** | Browser-side: the `required` attribute. Server-side: `net/url` parsing that checks the scheme (`http`/`https`) and the host. No regular expressions are used. |
| **Logging** | `log/slog` with structured JSON output |
| **Metrics** | Prometheus (`client_golang`) |
| **Profiling** | `net/http/pprof` on a separate admin port |
| **Testing** | `testing`, `net/http/httptest`, `prometheus/testutil`, table-driven tests |
| **DevOps** | Multi-stage Dockerfile (`golang:1.27-alpine` → `distroless/static:nonroot`, about 26 MB) and a Makefile |

## URLs and API

| Method | URL | Description |
|---|---|---|
| `GET` | http://localhost:8080/ | The form page |
| `POST` | http://localhost:8080/analyze | Analyzes a page and renders the result or error page |
| `GET` | http://localhost:8080/healthz | Liveness probe; returns `ok` |
| `GET` | http://localhost:8080/metrics | Prometheus metrics |
| `GET` | http://localhost:6060/debug/pprof/ | pprof profiles (admin port, bound to `localhost` by default) |

### `POST /analyze`

**Request:** `Content-Type: application/x-www-form-urlencoded`

| Field | Required | Example | Notes |
|---|---|---|---|
| `url` | yes | `https://go.dev` or `go.dev` | If the scheme is missing, `https://` is added. |

**Response:** an HTML page.

| Status | When |
|---|---|
| `200 OK` | The page was analyzed. |
| `400 Bad Request` | The URL is empty or invalid, or the scheme is not http/https. |
| `502 Bad Gateway` | The target page returned an error (the page shows the target's own status, e.g. 404), DNS failed, the connection failed, or the content is not HTML. |
| `504 Gateway Timeout` | The target didn't respond within 15 seconds. |
| `500 Internal Server Error` | Unexpected failure. Details are logged, not shown to the user. |

Example with curl:

```bash
curl -s -X POST -d 'url=https://go.dev' http://localhost:8080/analyze
```

### Metrics exposed

| Metric | Type | Labels |
|---|---|---|
| `http_requests_total` | counter | `method`, `route`, `code` |
| `http_request_duration_seconds` | histogram | `method`, `route` |
| `analyses_total` | counter | `outcome` (`success`, `invalid_url`, `fetch_error`, `error`) |
| `analysis_duration_seconds` | histogram | - |
| `links_checked_total` | counter | - |
| `links_inaccessible_total` | counter | - |

The Go runtime and process collectors are also registered.

## Project structure

```
.
├── cmd/server/            # entry point: config, wiring, graceful shutdown
│   ├── main.go
│   └── config.go          # reads settings from environment variables
├── internal/
│   ├── analyzer/          # core logic, no knowledge of the web UI
│   │   ├── analyzer.go    # Analyze(): fetch → parse → classify links → check links
│   │   ├── parse.go       # one pass over the HTML tree
│   │   ├── links.go       # internal/external classification
│   │   ├── linkcheck.go   # concurrent link checker (worker pool)
│   │   ├── url.go         # user input validation
│   │   └── errors.go      # ErrInvalidURL, FetchError
│   ├── web/               # HTTP handlers, middleware, templates
│   │   ├── handler.go
│   │   ├── middleware.go  # request logging, metrics, panic recovery
│   │   └── templates/index.html
│   └── metrics/           # Prometheus collectors
├── Dockerfile
├── Makefile
└── go.mod
```

Dependencies point in one direction: `cmd/server` → `web` → `analyzer`. The analyzer depends only on a small `HTTPDoer` interface, not on `*http.Client`. The web layer depends on an `Analyzer` interface. Both interfaces are defined where they are used, so each layer can be tested with a fake.

## How it works

1. **Validation.** `NormalizeURL` trims the input, adds `https://` if no scheme was given, and parses it with `net/url`. It rejects anything that isn't http/https or has no host.
2. **Fetch.** A GET request with a 15 s timeout, sent through the context. Redirects are followed, and the final URL becomes the base for resolving relative links. Responses with status ≥ 400 become a `FetchError` that carries the target's status code. Network errors are mapped to 502 or 504 with a readable cause. Non-HTML content types are rejected.
3. **Parse.** The body is limited to 10 MB, decoded to UTF-8 based on its charset, and parsed with `x/net/html`. A single recursive walk over the tree collects the DOCTYPE, title, heading counts, `<a href>` values, `<base href>` and password fields inside forms.
4. **Classify links.** Each href is resolved against the page (or `<base>`) URL. Links on the same host count as internal, all others as external. Links are deduplicated before checking.
5. **Check links concurrently.** A pool of 20 worker goroutines reads URLs from a `jobs` channel and sends results to a `results` channel. A `sync.WaitGroup` tracks the workers, and a separate goroutine closes `results` once they have all finished. Each check sends HEAD first and retries with GET if HEAD returns a status ≥ 400. Each link has a 5 s timeout, and all checks together share a 30 s budget. Links that weren't checked before the budget ran out are reported as "not checked", not as broken.
6. **Render.** The template is rendered into a buffer first, so a template error can never send a half-written page.

### Cross-cutting concerns

- **Error handling.** Errors are wrapped with `%w` and classified in one place (`describeError`) with `errors.Is` and `errors.As`. Users see a clear message. Internal details are only logged.
- **Logging.** Every request is logged as JSON with a request ID (also returned in the `X-Request-ID` header), method, path, status and duration. Each analysis is logged with its link counts.
- **Resilience.** Panic recovery middleware, server read/write/idle timeouts, a cap on form size, a cap on page size, and graceful shutdown on SIGINT/SIGTERM.
- **Authentication.** None. The app is a public tool and the assignment doesn't ask for authentication.

## Assumptions and decisions

| Topic | Decision |
|---|---|
| **HTML version** | Taken from the `<!DOCTYPE>`. `<!DOCTYPE html>` (or `about:legacy-compat`) means **HTML5**. Known public identifiers map to HTML 2.0, 3.2, 4.0, 4.01 (Strict/Transitional/Frameset), XHTML 1.0 (Strict/Transitional/Frameset) and XHTML 1.1. No doctype is reported as "Unknown (quirks mode)". |
| **Title** | The first `<title>` element, with whitespace collapsed. A `<title>` inside inline SVG is ignored. |
| **Headings** | All `h1`–`h6` elements in the document, all six levels always shown. |
| **Which links count** | `<a href>` elements only. `mailto:`, `tel:`, `javascript:`, `data:`, empty and fragment-only (`#top`) hrefs are ignored because they aren't links to other web resources. |
| **Internal vs external** | Internal means the same host as the analyzed page after redirects, ignoring case and a leading `www.`. Subdomains (e.g. `blog.example.com`) count as **external**. Ports and schemes are ignored. |
| **Link counts** | Internal and external counts include every occurrence. If a page links to `/about` three times, it counts three times, because the question is how many links the document contains. |
| **Inaccessible links** | Each *distinct* URL is checked once. A link is inaccessible if it returns status ≥ 400 on both HEAD and GET, or the request fails (DNS, connection, TLS, 5 s timeout). Redirects are followed. Some sites (e.g. Medium, Stack Overflow) return 403 to automated clients. Those links are reported as inaccessible because the app can't reach them. |
| **Login form** | A `<form>` that contains an `<input type="password">`. A password field outside any form is not counted. |
| **Error status codes** | If the target returns an error status, the page shows **that** status (e.g. 404 Not Found) with a description. For failures without an HTTP status, it shows 502 (DNS or connection failure) or 504 (timeout). The response from this app itself is 502/504, because this app is acting as a gateway. |
| **Input without scheme** | `example.com` is treated as `https://example.com`. |
| **Non-HTML URLs** | Rejected with 415 Unsupported Media Type shown on the page. A missing `Content-Type` header is accepted. |
| **No JavaScript** | The checklist asks to avoid unnecessary JavaScript, so the UI is a plain HTML form and the page waits while the analysis runs. |
| **pprof** | Served on a separate port bound to `localhost`, so it isn't exposed publicly. The Docker image doesn't publish it. |

## Testing

```bash
make test     # unit tests with the race detector
make cover    # coverage report
```

- **Coverage:** about **93%** of statements across all packages (`make cover`). By package: analyzer 97%, web 92%.
- **No real HTTP in unit tests.** The analyzer is tested with a fake `HTTPDoer` (`doerFunc`) that returns canned responses and errors. The web layer is tested with a fake `Analyzer` and `httptest.NewRecorder`. The only sockets opened are on `127.0.0.1:0`, in the test that checks the server shuts down gracefully.
- **What's covered:** table-driven tests for DOCTYPE detection, title edge cases, headings, login forms, URL validation, link classification (relative, protocol-relative, `<base>`, `www.`, subdomains, duplicates, ignored schemes), link checking (HEAD→GET retry, network errors, per-link timeout vs overall budget, a check that no more than the configured number of workers run at once), redirects, charset decoding, every error path in the handler, panic recovery, metrics recording and config loading.

### Integration tests

`internal/analyzer/integration_test.go` has the build tag `integration`, so normal test runs skip it. It analyzes real sites (`https://example.com`, and a 404 page on `go.dev`) to check that the real HTTP client, TLS, redirects and parser work together.

```bash
make test-integration     # go test -race -tags=integration ./...
```

These tests need internet access and depend on third-party sites, so they're kept out of the default test run.

## Configuration

Settings are read from environment variables:

| Variable | Default | Description |
|---|---|---|
| `ADDR` | `:8080` | Public listen address |
| `ADMIN_ADDR` | `localhost:6060` | pprof listen address |
| `LOG_LEVEL` | `info` | `debug`, `info`, `warn` or `error` |

Analyzer limits are set in `analyzer.Config`: 15 s fetch timeout, 5 s per link, 30 s budget for all link checks, 20 workers and a 10 MB page limit.

## Challenges and solutions

| Challenge | Approach |
|---|---|
| **Pages with hundreds of links** made the analysis slow. | A bounded worker pool (20 goroutines) with channels and a WaitGroup. Links are deduplicated first. There's a per-link timeout plus an overall time budget, and links that run out of time are reported as "not checked" so they aren't mistaken for broken links. |
| **False "broken" links.** Many servers reject HEAD (405, 403, 404) but serve GET fine. | HEAD is tried first because it's cheap. Any status ≥ 400 is confirmed with a GET before the link is counted as inaccessible. |
| **Telling a broken link apart from one that ran out of time.** | Both fail with a context error. After the request, the checker looks at whether the overall budget context is done. If it is, the link is marked as skipped; otherwise it's a real timeout. |
| **Relative links resolved incorrectly** after redirects or with a `<base>` tag. | The final URL after redirects is the base, and `<base href>` overrides how links are resolved. Whether a link is internal is still decided against the page's own host. |
| **Non-UTF-8 pages** showed garbled titles. | The body goes through `charset.NewReader`, which uses the `Content-Type` header and `<meta charset>`. |
| **Testing without the network.** | The analyzer depends on a one-method `HTTPDoer` interface, so tests swap in a function that returns canned responses. |
| **Unbounded metric labels.** | Request metrics use the matched route pattern (`r.Pattern`, e.g. `POST /analyze`) as the label, never the raw path, so the number of label values stays small. |
| **One server failing at startup** left the other running. | `run` always shuts down both servers before returning. A test caught this. |

## Possible improvements

- **Asynchronous analysis.** Submit a job, then poll or stream results. The page could then show progress for sites with thousands of links. That would need a little JavaScript, or server-sent events.
- **Caching.** Cache results per URL with a short TTL, and cache link check results across analyses.
- **SSRF protection.** Block requests to private, loopback and link-local IP ranges with a custom `net.Dialer.Control`, before exposing the app publicly.
- **Rate limiting.** Per client IP on `/analyze`, and per target host during link checks, to be polite to servers.
- **JavaScript-rendered pages.** Single-page apps build their content in the browser. A headless browser (e.g. chromedp) would make those analyses accurate.
- **Better login detection.** Detect OAuth/SSO buttons, multi-step login flows and password fields created by scripts.
- **JSON API.** Return the same `Result` as JSON when `Accept: application/json` is requested, with an OpenAPI spec.
- **Tracing.** OpenTelemetry spans for fetch, parse and each link check.
- **CI pipeline.** GitHub Actions running lint, tests and coverage, and pushing the Docker image.
- **Configurable limits.** Expose timeouts and the worker count as environment variables.
- **Show more detail.** List the internal and external links themselves, and the redirect chain.

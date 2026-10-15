# How a URL gets analyzed

This document follows one request from the moment the user presses **Analyze** until the result page is rendered. Diagrams use [Mermaid](https://mermaid.js.org/), which GitHub and most IDEs render automatically.

## Contents

1. [Components](#1-components)
2. [Request lifecycle](#2-request-lifecycle)
3. [Analysis pipeline](#3-analysis-pipeline)
4. [Step 1: URL validation](#4-step-1-url-validation)
5. [Step 2: Fetching the page](#5-step-2-fetching-the-page)
6. [Step 3: Parsing the HTML](#6-step-3-parsing-the-html)
7. [Step 4: Classifying links](#7-step-4-classifying-links)
8. [Step 5: Checking links concurrently](#8-step-5-checking-links-concurrently)
9. [Step 6: Building the result](#9-step-6-building-the-result)
10. [Error handling](#10-error-handling)
11. [Timeouts and limits](#11-timeouts-and-limits)

---

## 1. Components

```mermaid
flowchart LR
    Browser(["Browser"])

    subgraph server["cmd/server"]
        Main["main.go<br/>config, wiring,<br/>graceful shutdown"]
    end

    subgraph web["internal/web"]
        MW["Middleware<br/>logging, metrics,<br/>panic recovery"]
        Handler["Handler<br/>GET /  ·  POST /analyze"]
        Tmpl["templates/index.html"]
    end

    subgraph analyzer["internal/analyzer"]
        Analyze["Analyzer.Analyze"]
        URL["url.go<br/>NormalizeURL"]
        Parse["parse.go<br/>parsePage"]
        Links["links.go<br/>classifyLinks"]
        Check["linkcheck.go<br/>linkChecker"]
    end

    Metrics["internal/metrics<br/>Prometheus collectors"]
    Internet(("Target website<br/>and linked sites"))

    Browser -- "POST /analyze" --> MW --> Handler
    Handler -- "Analyzer interface" --> Analyze
    Analyze --> URL
    Analyze --> Parse
    Analyze --> Links
    Analyze --> Check
    Analyze -- "HTTPDoer interface" --> Internet
    Check -- "HTTPDoer interface" --> Internet
    Handler --> Tmpl --> Browser
    MW --> Metrics
    Handler --> Metrics
    Main --> MW
```

For a fuller picture of the code structure, see [Code architecture](architecture.md).

Dependencies only point inward: `cmd/server` → `web` → `analyzer`. The analyzer never imports anything from the web layer. Two small interfaces separate the layers:

| Interface | Defined in | Real implementation | Test implementation |
|---|---|---|---|
| `Analyzer` | `internal/web/handler.go` | `*analyzer.Analyzer` | `fakeAnalyzer` (returns canned results) |
| `HTTPDoer` | `internal/analyzer/analyzer.go` | `*http.Client` | `doerFunc` (returns canned responses, no network) |

---

## 2. Request lifecycle

```mermaid
sequenceDiagram
    autonumber
    actor User
    participant MW as Middleware
    participant H as web.Handler
    participant A as analyzer.Analyzer
    participant T as Target site
    participant L as Linked sites

    User->>MW: POST /analyze  (url=go.dev)
    MW->>MW: assign request ID, start timer
    MW->>H: analyze()
    H->>H: limit form to 8 KB, read "url" field
    H->>A: Analyze(ctx, "go.dev")
    A->>A: NormalizeURL → https://go.dev
    A->>T: GET https://go.dev  (15 s timeout)
    T-->>A: 200 OK, text/html
    A->>A: decode charset, parse HTML (one tree walk)
    A->>A: classify links, deduplicate
    par 20 workers
        A->>L: HEAD link 1 (GET if HEAD fails)
        L-->>A: status
    and
        A->>L: HEAD link 2 …
        L-->>A: status
    end
    A-->>H: *Result
    H->>H: record metrics, log "page analyzed"
    H->>H: render template into buffer
    H-->>MW: 200 + HTML
    MW->>MW: log request, record status and duration
    MW-->>User: result page
```

---

## 3. Analysis pipeline

`Analyzer.Analyze` (in `internal/analyzer/analyzer.go`) runs six steps in order. Any of the first three can stop the pipeline with an error.

```mermaid
flowchart TD
    In[/"Raw input from form<br/>e.g. '  go.dev  '"/]
    S1["1 · NormalizeURL<br/>validate and complete URL"]
    S2["2 · fetch<br/>GET page, check status and type"]
    S3["3 · parsePage<br/>single walk over the HTML tree"]
    S4["4 · classifyLinks<br/>internal / external, deduplicate"]
    S5["5 · linkChecker.check<br/>concurrent HEAD/GET"]
    S6["6 · build Result"]
    Out[/"*Result"/]

    E1[["ErrInvalidURL → 400"]]
    E2[["FetchError → 502 / 504"]]
    E3[["parse error → 500"]]

    In --> S1
    S1 -- ok --> S2
    S1 -- invalid --> E1
    S2 -- ok --> S3
    S2 -- unreachable,<br/>status ≥ 400,<br/>not HTML --> E2
    S3 -- ok --> S4
    S3 -- error --> E3
    S4 --> S5 --> S6 --> Out
```

---

## 4. Step 1: URL validation

**Code:** `NormalizeURL` in `internal/analyzer/url.go`

Validation uses the standard library's `net/url` parser instead of a regular expression.

```mermaid
flowchart TD
    A[/"raw input"/] --> B["Trim whitespace"]
    B --> C{"Empty?"}
    C -- yes --> X1[["ErrInvalidURL<br/>'please enter a URL'"]]
    C -- no --> D{"Contains '://'?"}
    D -- no --> E["Prepend 'https://'"]
    D -- yes --> F
    E --> F["url.Parse"]
    F --> G{"Parse error?"}
    G -- yes --> X2[["ErrInvalidURL<br/>'not a valid URL'"]]
    G -- no --> H{"Scheme is<br/>http or https?"}
    H -- no --> X3[["ErrInvalidURL<br/>'only http and https…'"]]
    H -- yes --> I{"Has a host?"}
    I -- no --> X4[["ErrInvalidURL<br/>'has no host'"]]
    I -- yes --> OK[/"*url.URL"/]
```

| Input | Result |
|---|---|
| `go.dev` | `https://go.dev` |
| `  http://example.com/path?q=1 ` | `http://example.com/path?q=1` |
| `ftp://example.com` | error: only http and https are supported |
| `https://` | error: no host |
| `http://exa mple.com` | error: not a valid URL |

---

## 5. Step 2: Fetching the page

**Code:** `Analyzer.fetch` in `internal/analyzer/analyzer.go`

```mermaid
flowchart TD
    A["Build GET request<br/>User-Agent, Accept: text/html<br/>context with 15 s timeout"] --> B["client.Do(req)<br/>(follows redirects)"]
    B --> C{"Transport error?"}
    C -- "timeout" --> X1[["FetchError 504<br/>'did not respond within 15s'"]]
    C -- "DNS failure" --> X2[["FetchError 502<br/>'host could not be resolved'"]]
    C -- "other (refused, TLS…)" --> X3[["FetchError 502<br/>'could not connect'"]]
    C -- no --> D{"Status ≥ 400?"}
    D -- yes --> X4[["FetchError with the<br/>target's own status<br/>e.g. 404 Not Found"]]
    D -- no --> E{"Content-Type is HTML?<br/>(text/html, xhtml, or missing)"}
    E -- no --> X5[["FetchError 415<br/>'not an HTML page'"]]
    E -- yes --> F["Limit body to 10 MB"]
    F --> G["charset.NewReader<br/>convert to UTF-8"]
    G --> H["parsePage"]
    H --> I["Take final URL<br/>from resp.Request.URL"]
    I --> OK[/"final URL + page facts"/]
```

The **final URL after redirects** matters. If `http://go.dev` redirects to `https://go.dev/`, relative links like `/doc` must be resolved against the URL the content actually came from.

---

## 6. Step 3: Parsing the HTML

**Code:** `parsePage` and `page.walk` in `internal/analyzer/parse.go`

`golang.org/x/net/html` turns the page into a tree. A single recursive walk visits every node once and collects everything at the same time.

### Example

```html
<!DOCTYPE html>
<html>
  <head><title>Sample</title></head>
  <body>
    <h1>Welcome</h1>
    <a href="/about">About</a>
    <form>
      <input name="user">
      <input type="password">
    </form>
  </body>
</html>
```

```mermaid
flowchart TD
    Doc["Document"] --> DT["DOCTYPE html<br/>→ htmlVersion = HTML5"]
    Doc --> Html["html"]
    Html --> Head["head"]
    Html --> Body["body"]
    Head --> Title["title<br/>→ title = 'Sample'"]
    Body --> H1["h1<br/>→ headings[0]++"]
    Body --> A["a href='/about'<br/>→ hrefs += '/about'"]
    Body --> Form["form<br/>→ inForm = true<br/>for its children"]
    Form --> In1["input name='user'"]
    Form --> In2["input type='password'<br/>inForm is true<br/>→ hasLoginForm = true"]

    classDef hit fill:#dbeafe,stroke:#2563eb,color:#1e3a8a
    class DT,Title,H1,A,Form,In2 hit
```

### What each node type contributes

| Node | Rule | Field set |
|---|---|---|
| `<!DOCTYPE …>` | Public identifier matched against a known list (see below) | `htmlVersion` |
| `<title>` | First one only, skipped inside `<svg>`, whitespace collapsed | `title` |
| `<h1>` … `<h6>` | Counter for that level is incremented | `headings[0..5]` |
| `<a href>` | Raw `href` value appended | `hrefs` |
| `<base href>` | First one only | `baseHref` |
| `<form>` | Sets `inForm = true` while walking its descendants | – |
| `<input type="password">` | Only counts when `inForm` is true | `hasLoginForm` |

### HTML version detection

```mermaid
flowchart TD
    A["DOCTYPE node"] --> B{"Name is 'html'?"}
    B -- no --> U1["Unknown (name)"]
    B -- yes --> C{"No public ID, and system ID<br/>empty or 'about:legacy-compat'?"}
    C -- yes --> H5["HTML5"]
    C -- no --> D{"Public ID contains a<br/>known identifier?"}
    D -- yes --> K["Matching version<br/>e.g. HTML 4.01 Strict,<br/>XHTML 1.0 Transitional, XHTML 1.1"]
    D -- no --> U2["Unknown (public ID)"]
    N["No DOCTYPE in the document"] --> Q["Unknown (no DOCTYPE, quirks mode)"]
```

---

## 7. Step 4: Classifying links

**Code:** `classifyLinks` and `sameSite` in `internal/analyzer/links.go`

Every `href` is resolved to an absolute URL and then labelled internal or external.

```mermaid
flowchart TD
    A[/"href"/] --> B{"Empty, or starts with '#'?"}
    B -- yes --> Skip[["Ignored"]]
    B -- no --> C["Resolve against base<br/>(base href tag if present,<br/>otherwise final page URL)"]
    C --> D{"Parses, scheme is<br/>http/https, has host?"}
    D -- no --> Skip2[["Ignored<br/>mailto:, tel:, javascript:,<br/>data:, malformed"]]
    D -- yes --> E["Drop #fragment"]
    E --> F{"Same host as page?<br/>(case-insensitive,<br/>leading 'www.' ignored)"}
    F -- yes --> I["internal++"]
    F -- no --> X["external++"]
    I --> G{"Seen before?"}
    X --> G
    G -- no --> U["Add to unique list<br/>(will be checked)"]
    G -- yes --> Done["Counted only"]
```

### Examples (page is `https://www.example.com/blog/post`)

| href | Resolved | Class |
|---|---|---|
| `/about` | `https://www.example.com/about` | internal |
| `contact` | `https://www.example.com/blog/contact` | internal |
| `https://example.com/a` | `https://example.com/a` | internal (`www.` ignored) |
| `//www.example.com/y` | `https://www.example.com/y` | internal |
| `https://blog.example.com/` | same | **external** (subdomain) |
| `https://golang.org` | same | external |
| `#top`, `mailto:x@y.z`, `javascript:void(0)` | – | ignored |

Counts include **every occurrence**: three links to `/about` count as three internal links. The list of URLs to check is **deduplicated**, so `/about` is only requested once.

---

## 8. Step 5: Checking links concurrently

**Code:** `linkChecker.check` and `checkOne` in `internal/analyzer/linkcheck.go`

A page can have hundreds of links. Checking them one after another could take minutes, so they're checked by a fixed-size pool of goroutines.

### Worker pool

```mermaid
flowchart LR
    subgraph producer["Producer goroutine"]
        P["for each unique URL:<br/>jobs ← url<br/>then close(jobs)"]
    end

    Jobs[["jobs channel<br/>(unbuffered)"]]

    subgraph pool["Worker pool: min(20, len(urls)) goroutines, tracked by sync.WaitGroup"]
        W1["worker 1<br/>for u := range jobs"]
        W2["worker 2"]
        W3["…"]
        W20["worker 20"]
    end

    Results[["results channel"]]

    subgraph closer["Closer goroutine"]
        C["wg.Wait()<br/>close(results)"]
    end

    subgraph collector["Caller (collector)"]
        R["for r := range results:<br/>count broken / skipped<br/>then sort broken by URL"]
    end

    P --> Jobs
    Jobs --> W1 & W2 & W3 & W20
    W1 & W2 & W3 & W20 --> Results
    Results --> R
    C -. "closes when all<br/>workers are done" .-> Results
```

How the channels shut down:

1. The **producer** sends every URL, then closes `jobs`.
2. Each **worker** leaves its `range jobs` loop once `jobs` is closed and empty, then signals the `WaitGroup`.
3. The **closer** goroutine waits for all workers, then closes `results`.
4. The **collector** leaves its `range results` loop and returns.

Nothing is left blocked and no goroutines leak. Because only the closer closes `results`, no worker can ever send on a closed channel.

### Checking a single link

```mermaid
flowchart TD
    A[/"url"/] --> B{"Overall budget<br/>already expired?"}
    B -- yes --> SK[["skipped"]]
    B -- no --> C["HEAD url<br/>(5 s timeout)"]
    C --> D{"Status ≥ 400?"}
    D -- yes --> E["Retry with GET<br/>(many servers reject HEAD)"]
    D -- no --> F
    E --> F{"Request error?"}
    F -- "yes, and overall<br/>budget expired" --> SK
    F -- "yes, otherwise" --> BR1[["broken<br/>status 0 + error text"]]
    F -- no --> G{"Status ≥ 400?"}
    G -- yes --> BR2[["broken<br/>status code + text"]]
    G -- no --> OK[["accessible"]]
```

### Two kinds of timeout

There are two separate timeouts, and the checker has to tell them apart:

```mermaid
gantt
    title Link check timing (example)
    dateFormat  s
    axisFormat  %Ss

    section Overall budget
    LinkCheckBudget (30 s)        :active, budget, 0, 30s

    section Worker 1
    link A ok                     :done, a, 0, 1s
    link B slow → per-link timeout:crit, b, 1, 5s
    link C ok                     :done, c, 6, 1s

    section Worker 2
    link D ok                     :done, d, 0, 2s
    link E still running at 30 s  :e, 28, 4s
```

| Situation | Who cancelled the context | Reported as |
|---|---|---|
| Link B took more than 5 s | the per-link timeout | **inaccessible** (the site really is too slow) |
| Link E was cut off at 30 s | the overall budget | **not checked** (no fault of the site) |

After a failed request, `checkOne` looks at the budget context (`ctx.Err()`). If the budget has expired, the link is marked as skipped; otherwise the failure belongs to the link.

---

## 9. Step 6: Building the result

```mermaid
classDiagram
    class Result {
        URL string
        HTMLVersion string
        Title string
        Headings []HeadingCount
        InternalLinks int
        ExternalLinks int
        CheckedLinks int
        UncheckedLinks int
        InaccessibleLinks []BrokenLink
        HasLoginForm bool
        Duration time.Duration
    }
    class HeadingCount {
        Level string
        Count int
    }
    class BrokenLink {
        URL string
        StatusCode int
        Reason string
    }
    Result "1" *-- "6" HeadingCount : h1 to h6
    Result "1" *-- "0..*" BrokenLink
```

- `CheckedLinks` = unique links − skipped links
- `Headings` always has all six levels in order, so the template can show zeros.
- `InaccessibleLinks` is sorted by URL, because workers finish in random order.

The handler renders `Result` with `html/template`, which escapes all page content (a title like `<script>` is shown as text, never run).

---

## 10. Error handling

Errors are created in the analyzer and turned into user messages in exactly one place: `Handler.describeError` in `internal/web/handler.go`.

```mermaid
flowchart TD
    E[/"error from Analyze"/] --> A{"errors.Is(err, ErrInvalidURL)?"}
    A -- yes --> R400["Response 400<br/>shows the validation message<br/>metric outcome = invalid_url"]
    A -- no --> B{"errors.As(err, *FetchError)?"}
    B -- yes --> C{"FetchError.StatusCode<br/>== 504?"}
    C -- yes --> R504["Response 504<br/>shows 504 Gateway Timeout"]
    C -- no --> R502["Response 502<br/>shows the target's status<br/>e.g. 404 Not Found"]
    R504 & R502 --> M1["log WARN, outcome = fetch_error"]
    B -- no --> R500["Response 500<br/>generic message only<br/>details logged at ERROR<br/>outcome = error"]
```

**Why the page shows 404 but the response code is 502:** the 404 came from the *target* site, and the user needs to see it. This app itself didn't fail with "not found", though. It acted as a gateway to a server that returned an error, and 502 Bad Gateway says that.

A panic anywhere in a handler is caught by the `recoverPanics` middleware, logged with its stack trace, and returned as a 500. Because that middleware sits inside `logRequests`, the 500 still appears in the request log and metrics.

---

## 11. Timeouts and limits

```mermaid
flowchart LR
    subgraph srv["http.Server (cmd/server/main.go)"]
        RH["ReadHeaderTimeout 5 s"]
        RT["ReadTimeout 10 s"]
        WT["WriteTimeout 90 s"]
    end
    subgraph h["Handler"]
        FB["Form body ≤ 8 KB"]
    end
    subgraph an["analyzer.Config defaults"]
        FT["FetchTimeout 15 s"]
        MB["MaxBodyBytes 10 MB"]
        LT["LinkTimeout 5 s / link"]
        LB["LinkCheckBudget 30 s total"]
        WK["Workers 20"]
    end
    srv --> h --> an
```

Worst case for one analysis is about 15 s (fetch) + 30 s (links) = **45 s**. That's why the server's `WriteTimeout` is 90 s, which leaves room to render and send the page.

| Limit | Value | Protects against |
|---|---|---|
| Form body | 8 KB | huge POST bodies |
| Fetch timeout | 15 s | target that never answers |
| Page size | 10 MB | huge pages using too much memory |
| Per-link timeout | 5 s | one slow link holding up a worker |
| Link budget | 30 s | pages with thousands of links |
| Workers | 20 | flooding target servers or exhausting sockets |

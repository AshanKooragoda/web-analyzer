// Package analyzer fetches a web page and reports facts about its HTML:
// version, title, headings, links and whether it contains a login form.
package analyzer

import (
	"context"
	"fmt"
	"io"
	"mime"
	"net/http"
	"net/url"
	"strings"
	"time"

	"golang.org/x/net/html/charset"
)

const userAgent = "web-analyzer/1.0"

// HTTPDoer is the part of *http.Client the analyzer needs. Tests replace it
// with a fake so no real network requests are made.
type HTTPDoer interface {
	Do(*http.Request) (*http.Response, error)
}

// Config controls timeouts and limits. Zero values are replaced by defaults.
type Config struct {
	FetchTimeout    time.Duration // time allowed to download the page
	LinkTimeout     time.Duration // time allowed for each link check
	LinkCheckBudget time.Duration // total time allowed for all link checks
	Workers         int           // concurrent link checks
	MaxBodyBytes    int64         // largest page that will be parsed
}

func (c Config) withDefaults() Config {
	if c.FetchTimeout <= 0 {
		c.FetchTimeout = 15 * time.Second
	}
	if c.LinkTimeout <= 0 {
		c.LinkTimeout = 5 * time.Second
	}
	if c.LinkCheckBudget <= 0 {
		c.LinkCheckBudget = 30 * time.Second
	}
	if c.Workers <= 0 {
		c.Workers = 20
	}
	if c.MaxBodyBytes <= 0 {
		c.MaxBodyBytes = 10 << 20
	}
	return c
}

// HeadingCount is the number of headings of one level, e.g. "h2".
type HeadingCount struct {
	Level string
	Count int
}

// Result is the outcome of analyzing a page.
type Result struct {
	URL               string // final URL after redirects
	HTMLVersion       string
	Title             string
	Headings          []HeadingCount // always h1 to h6, in order
	InternalLinks     int
	ExternalLinks     int
	CheckedLinks      int // distinct links that were checked
	UncheckedLinks    int // distinct links skipped because the time budget ran out
	InaccessibleLinks []BrokenLink
	HasLoginForm      bool
	Duration          time.Duration
}

// Analyzer analyzes web pages. It is safe for concurrent use.
type Analyzer struct {
	client  HTTPDoer
	cfg     Config
	checker *linkChecker
}

// New returns an Analyzer that uses client for all outgoing requests.
func New(client HTTPDoer, cfg Config) *Analyzer {
	cfg = cfg.withDefaults()
	return &Analyzer{
		client: client,
		cfg:    cfg,
		checker: &linkChecker{
			client:  client,
			workers: cfg.Workers,
			timeout: cfg.LinkTimeout,
		},
	}
}

// Analyze fetches rawURL and analyzes it. It returns an error wrapping
// ErrInvalidURL for bad input, or a *FetchError if the page is unreachable.
func (a *Analyzer) Analyze(ctx context.Context, rawURL string) (*Result, error) {
	start := time.Now()

	target, err := NormalizeURL(rawURL)
	if err != nil {
		return nil, err
	}

	finalURL, p, err := a.fetch(ctx, target)
	if err != nil {
		return nil, err
	}

	links := classifyLinks(finalURL, p.baseHref, p.hrefs)

	checkCtx, cancel := context.WithTimeout(ctx, a.cfg.LinkCheckBudget)
	defer cancel()
	broken, skipped := a.checker.check(checkCtx, links.unique)

	res := &Result{
		URL:               finalURL.String(),
		HTMLVersion:       p.htmlVersion,
		Title:             p.title,
		Headings:          make([]HeadingCount, len(p.headings)),
		InternalLinks:     links.internal,
		ExternalLinks:     links.external,
		CheckedLinks:      len(links.unique) - skipped,
		UncheckedLinks:    skipped,
		InaccessibleLinks: broken,
		HasLoginForm:      p.hasLoginForm,
		Duration:          time.Since(start),
	}
	for i, n := range p.headings {
		res.Headings[i] = HeadingCount{Level: fmt.Sprintf("h%d", i+1), Count: n}
	}
	return res, nil
}

// fetch downloads and parses the page. It returns the URL after redirects,
// which is the base for resolving relative links.
func (a *Analyzer) fetch(ctx context.Context, target *url.URL) (*url.URL, *page, error) {
	ctx, cancel := context.WithTimeout(ctx, a.cfg.FetchTimeout)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, target.String(), nil)
	if err != nil {
		return nil, nil, fmt.Errorf("%w: %v", ErrInvalidURL, err)
	}
	req.Header.Set("User-Agent", userAgent)
	req.Header.Set("Accept", "text/html,application/xhtml+xml;q=0.9,*/*;q=0.8")

	resp, err := a.client.Do(req)
	if err != nil {
		return nil, nil, transportError(target, a.cfg.FetchTimeout, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode >= http.StatusBadRequest {
		return nil, nil, &FetchError{
			StatusCode: resp.StatusCode,
			Message:    fmt.Sprintf("%s responded with %d %s", target.Host, resp.StatusCode, http.StatusText(resp.StatusCode)),
		}
	}

	contentType := resp.Header.Get("Content-Type")
	if !isHTML(contentType) {
		return nil, nil, &FetchError{
			StatusCode: http.StatusUnsupportedMediaType,
			Message:    fmt.Sprintf("the URL points to %q content, not an HTML page", contentType),
		}
	}

	body, err := charset.NewReader(io.LimitReader(resp.Body, a.cfg.MaxBodyBytes), contentType)
	if err != nil {
		return nil, nil, fmt.Errorf("decode page: %w", err)
	}
	p, err := parsePage(body)
	if err != nil {
		return nil, nil, err
	}

	finalURL := target
	if resp.Request != nil && resp.Request.URL != nil {
		finalURL = resp.Request.URL
	}
	return finalURL, p, nil
}

// isHTML reports whether a Content-Type header describes an HTML document.
// A missing header is accepted because many servers omit it.
func isHTML(contentType string) bool {
	if contentType == "" {
		return true
	}
	mediaType, _, err := mime.ParseMediaType(contentType)
	if err != nil {
		return strings.Contains(strings.ToLower(contentType), "html")
	}
	return mediaType == "text/html" || mediaType == "application/xhtml+xml"
}

package analyzer

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"slices"
	"strings"
	"sync"
	"time"
)

// BrokenLink is a link that could not be reached.
type BrokenLink struct {
	URL string
	// StatusCode is zero when the request failed before a response arrived.
	StatusCode int
	Reason     string
}

type linkResult struct {
	url     string
	broken  *BrokenLink
	skipped bool
}

// linkChecker checks links concurrently with a fixed size worker pool.
type linkChecker struct {
	client  HTTPDoer
	workers int
	timeout time.Duration
}

// check reports which urls are inaccessible. Links that were not checked
// because ctx ended first are counted in skipped rather than reported broken.
func (c *linkChecker) check(ctx context.Context, urls []string) (broken []BrokenLink, skipped int) {
	if len(urls) == 0 {
		return nil, 0
	}

	jobs := make(chan string)
	results := make(chan linkResult)

	var wg sync.WaitGroup
	for range min(c.workers, len(urls)) {
		wg.Go(func() {
			for u := range jobs {
				results <- c.checkOne(ctx, u)
			}
		})
	}

	go func() {
		defer close(jobs)
		for _, u := range urls {
			jobs <- u
		}
	}()

	go func() {
		wg.Wait()
		close(results)
	}()

	for r := range results {
		switch {
		case r.skipped:
			skipped++
		case r.broken != nil:
			broken = append(broken, *r.broken)
		}
	}
	// Workers finish in any order; sort for a stable report.
	slices.SortFunc(broken, func(a, b BrokenLink) int { return strings.Compare(a.URL, b.URL) })
	return broken, skipped
}

func (c *linkChecker) checkOne(ctx context.Context, u string) linkResult {
	if ctx.Err() != nil {
		return linkResult{url: u, skipped: true}
	}

	status, err := c.request(ctx, http.MethodHead, u)
	// Many servers reject or mishandle HEAD, so confirm failures with GET.
	if err == nil && status >= http.StatusBadRequest {
		status, err = c.request(ctx, http.MethodGet, u)
	}

	switch {
	case err != nil && ctx.Err() != nil:
		// The overall budget ran out, not this link's own timeout.
		return linkResult{url: u, skipped: true}
	case err != nil:
		return linkResult{url: u, broken: &BrokenLink{URL: u, Reason: err.Error()}}
	case status >= http.StatusBadRequest:
		return linkResult{url: u, broken: &BrokenLink{URL: u, StatusCode: status, Reason: http.StatusText(status)}}
	default:
		return linkResult{url: u}
	}
}

func (c *linkChecker) request(ctx context.Context, method, u string) (int, error) {
	ctx, cancel := context.WithTimeout(ctx, c.timeout)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, method, u, nil)
	if err != nil {
		return 0, fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("User-Agent", userAgent)

	resp, err := c.client.Do(req)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()
	// Drain a little of the body so the connection can be reused.
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 64<<10))
	return resp.StatusCode, nil
}

package analyzer

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"sync/atomic"
	"testing"
	"time"
)

func TestLinkCheckerReportsBrokenLinks(t *testing.T) {
	var calls atomic.Int32
	client := doerFunc(func(req *http.Request) (*http.Response, error) {
		calls.Add(1)
		switch req.URL.Path {
		case "/ok":
			return response(req, http.StatusOK, "", ""), nil
		case "/redirected":
			return response(req, http.StatusMovedPermanently, "", ""), nil
		case "/missing":
			return response(req, http.StatusNotFound, "", ""), nil
		case "/head-not-allowed":
			if req.Method == http.MethodHead {
				return response(req, http.StatusMethodNotAllowed, "", ""), nil
			}
			return response(req, http.StatusOK, "", ""), nil
		case "/down":
			return nil, errors.New("connection refused")
		}
		return nil, fmt.Errorf("unexpected request %s", req.URL)
	})
	c := &linkChecker{client: client, workers: 3, timeout: time.Second}

	broken, skipped := c.check(context.Background(), []string{
		"https://a.test/ok",
		"https://a.test/redirected",
		"https://a.test/missing",
		"https://a.test/head-not-allowed",
		"https://a.test/down",
	})

	if skipped != 0 {
		t.Errorf("skipped = %d, want 0", skipped)
	}
	want := []BrokenLink{
		{URL: "https://a.test/down", Reason: "connection refused"},
		{URL: "https://a.test/missing", StatusCode: http.StatusNotFound, Reason: "Not Found"},
	}
	if !slices.Equal(broken, want) {
		t.Errorf("broken = %+v, want %+v", broken, want)
	}
	// HEAD for each link, plus a GET retry for the two that failed with a status.
	if got := calls.Load(); got != 7 {
		t.Errorf("requests made = %d, want 7", got)
	}
}

func TestLinkCheckerEmpty(t *testing.T) {
	c := &linkChecker{client: doerFunc(func(*http.Request) (*http.Response, error) {
		t.Fatal("no request expected")
		return nil, nil
	}), workers: 2, timeout: time.Second}

	broken, skipped := c.check(context.Background(), nil)
	if broken != nil || skipped != 0 {
		t.Errorf("check(nil) = %v, %d; want nil, 0", broken, skipped)
	}
}

func TestLinkCheckerLimitsConcurrency(t *testing.T) {
	const workers = 4
	var inFlight, peak atomic.Int32
	client := doerFunc(func(req *http.Request) (*http.Response, error) {
		n := inFlight.Add(1)
		defer inFlight.Add(-1)
		for {
			p := peak.Load()
			if n <= p || peak.CompareAndSwap(p, n) {
				break
			}
		}
		time.Sleep(5 * time.Millisecond)
		return response(req, http.StatusOK, "", ""), nil
	})
	c := &linkChecker{client: client, workers: workers, timeout: time.Second}

	urls := make([]string, 40)
	for i := range urls {
		urls[i] = fmt.Sprintf("https://a.test/%d", i)
	}
	broken, _ := c.check(context.Background(), urls)

	if len(broken) != 0 {
		t.Errorf("broken = %v, want none", broken)
	}
	if got := peak.Load(); got > workers || got < 2 {
		t.Errorf("peak concurrency = %d, want between 2 and %d", got, workers)
	}
}

func TestLinkCheckerSkipsWhenBudgetExpires(t *testing.T) {
	client := doerFunc(func(req *http.Request) (*http.Response, error) {
		<-req.Context().Done()
		return nil, req.Context().Err()
	})
	c := &linkChecker{client: client, workers: 2, timeout: time.Minute}

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	broken, skipped := c.check(ctx, []string{"https://a.test/1", "https://a.test/2", "https://a.test/3"})

	if len(broken) != 0 {
		t.Errorf("broken = %v, want none (links were not really checked)", broken)
	}
	if skipped != 3 {
		t.Errorf("skipped = %d, want 3", skipped)
	}
}

func TestLinkCheckerPerLinkTimeoutIsBroken(t *testing.T) {
	client := doerFunc(func(req *http.Request) (*http.Response, error) {
		<-req.Context().Done()
		return nil, req.Context().Err()
	})
	c := &linkChecker{client: client, workers: 1, timeout: 10 * time.Millisecond}

	broken, skipped := c.check(context.Background(), []string{"https://slow.test/"})

	if skipped != 0 || len(broken) != 1 {
		t.Fatalf("broken, skipped = %v, %d; want 1 broken link", broken, skipped)
	}
	if broken[0].StatusCode != 0 {
		t.Errorf("StatusCode = %d, want 0 for a timeout", broken[0].StatusCode)
	}
}

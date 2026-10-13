//go:build integration

package analyzer

import (
	"context"
	"errors"
	"net/http"
	"testing"
	"time"
)

// TestAnalyzeLiveSite talks to the real internet, so it only runs with
// `go test -tags=integration ./...` (or `make test-integration`).
func TestAnalyzeLiveSite(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()

	res, err := New(http.DefaultClient, Config{}).Analyze(ctx, "https://example.com")
	if err != nil {
		t.Fatalf("Analyze: %v", err)
	}
	if res.HTMLVersion != "HTML5" {
		t.Errorf("HTMLVersion = %q, want HTML5", res.HTMLVersion)
	}
	if res.Title == "" {
		t.Error("Title is empty")
	}
}

func TestAnalyzeLiveNotFound(t *testing.T) {
	_, err := New(http.DefaultClient, Config{}).Analyze(context.Background(), "https://go.dev/this-page-does-not-exist")
	var fe *FetchError
	if !errors.As(err, &fe) || fe.StatusCode != http.StatusNotFound {
		t.Fatalf("error = %v, want FetchError with 404", err)
	}
}
